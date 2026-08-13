package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	checkSuccess            = "success"
	checkFailure            = "failure"
	checkPending            = "pending"
	pullRequestRetryPeriod  = 30 * time.Second
	pullRequestQueryTimeout = 3 * time.Second
)

type pullRequest struct {
	Number              int                   `json:"number"`
	State               string                `json:"state"`
	Draft               bool                  `json:"isDraft"`
	HeadRefName         string                `json:"headRefName"`
	HeadRefOID          string                `json:"headRefOid"`
	HeadRepository      pullRequestRepository `json:"headRepository"`
	HeadRepositoryOwner struct {
		Login string `json:"login"`
	} `json:"headRepositoryOwner"`
	IsCrossRepository *bool             `json:"isCrossRepository"`
	StatusCheckRollup []checkRollupItem `json:"statusCheckRollup"`
	URL               string            `json:"url"`
	Check             string            `json:"-"`
	FailedChecks      []string          `json:"-"`
	IdentityAvailable bool              `json:"-"`
}

type pullRequestRepository struct {
	Name          string `json:"name"`
	NameWithOwner string `json:"nameWithOwner"`
}

type checkRollupItem struct {
	Name       string `json:"name"`
	Context    string `json:"context"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

type pullRequestMemoryEntry struct {
	expires time.Time
	values  map[string][]pullRequest
}

var pullRequestMemory struct {
	sync.Mutex
	values map[string]pullRequestMemoryEntry
}

func listPullRequests(repo string) (map[string][]pullRequest, bool) {
	pullRequestMemory.Lock()
	entry, ok := pullRequestMemory.values[repo]
	pullRequestMemory.Unlock()
	if ok && time.Now().Before(entry.expires) {
		return entry.values, entry.values != nil
	}

	run := func(fields string) ([]byte, error, bool) {
		ctx, cancel := context.WithTimeout(context.Background(), pullRequestQueryTimeout)
		defer cancel()
		cmd := boundedCommand(ctx, "gh", "pr", "list", "--state", "all", "--limit", "100", "--json", fields)
		cmd.Dir = repo
		output, err := cmd.Output()
		return output, err, ctx.Err() == context.DeadlineExceeded
	}
	// ponytail: cap PR lookup; add pagination if repositories with 100+ PRs miss active branches.
	baseFields := "number,state,isDraft,headRefName,headRefOid,headRepository,headRepositoryOwner,isCrossRepository"
	fields := baseFields + ",url,statusCheckRollup"
	output, err, timedOut := run(fields)
	identityAvailable := true
	if err != nil && !timedOut {
		output, err, _ = run(baseFields + ",statusCheckRollup")
	}
	if err != nil {
		output, err, _ = run(baseFields)
	}
	if err != nil {
		identityAvailable = false
		output, err, _ = run("number,state,isDraft,headRefName")
	}

	pullRequestMemory.Lock()
	if pullRequestMemory.values == nil {
		pullRequestMemory.values = map[string]pullRequestMemoryEntry{}
	}
	if err != nil {
		pullRequestMemory.values[repo] = pullRequestMemoryEntry{expires: time.Now().Add(pullRequestRetryPeriod)}
		pullRequestMemory.Unlock()
		return nil, false
	}

	values := parsePullRequests(output)
	for branch := range values {
		for index := range values[branch] {
			values[branch][index].IdentityAvailable = identityAvailable
		}
	}
	pullRequestMemory.values[repo] = pullRequestMemoryEntry{expires: time.Now().Add(time.Minute), values: values}
	pullRequestMemory.Unlock()
	return values, true
}

func parsePullRequests(output []byte) map[string][]pullRequest {
	var listed []pullRequest
	if json.Unmarshal(output, &listed) != nil {
		return map[string][]pullRequest{}
	}
	result := make(map[string][]pullRequest, len(listed))
	for _, pr := range listed {
		if pr.HeadRefName == "" {
			continue
		}
		pr.Check = aggregateCheckState(pr.StatusCheckRollup)
		pr.FailedChecks = failedCheckNames(pr.StatusCheckRollup)
		result[pr.HeadRefName] = append(result[pr.HeadRefName], pr)
	}
	return result
}

func pullRequestForBranch(dir, branch string, candidates []pullRequest) (pullRequest, bool) {
	if branch == "" || len(candidates) == 0 {
		return pullRequest{}, false
	}
	identityAvailable := false
	for _, candidate := range candidates {
		if candidate.IdentityAvailable || candidate.HeadRefOID != "" || candidate.IsCrossRepository != nil || candidate.headRepository() != "" {
			identityAvailable = true
			break
		}
	}
	if !identityAvailable {
		return preferredPullRequest(candidates)
	}

	upstreamRepository := branchUpstreamRepository(dir, branch)
	eligible := make([]pullRequest, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.IsCrossRepository != nil && !*candidate.IsCrossRepository {
			eligible = append(eligible, candidate)
		} else if upstreamRepository != "" && strings.EqualFold(candidate.headRepository(), upstreamRepository) {
			eligible = append(eligible, candidate)
		}
	}
	if preferred, ok := preferredPullRequest(eligible); ok && strings.EqualFold(preferred.State, "OPEN") {
		return preferred, true
	}
	if oid, err := gitOutput(dir, "rev-parse", "refs/heads/"+branch); err == nil {
		oid = strings.TrimSpace(oid)
		matches := make([]pullRequest, 0, len(eligible))
		for _, candidate := range eligible {
			if candidate.HeadRefOID != "" && candidate.HeadRefOID == oid {
				matches = append(matches, candidate)
			}
		}
		if preferred, ok := preferredPullRequest(matches); ok {
			return preferred, true
		}
	}
	return preferredPullRequest(eligible)
}

func preferredPullRequest(candidates []pullRequest) (pullRequest, bool) {
	var open []pullRequest
	for _, candidate := range candidates {
		if strings.EqualFold(candidate.State, "OPEN") {
			open = append(open, candidate)
		}
	}
	if len(open) == 1 {
		return open[0], true
	}
	if len(candidates) == 1 {
		return candidates[0], true
	}
	return pullRequest{}, false
}

func (pr pullRequest) headRepository() string {
	if pr.HeadRepository.NameWithOwner != "" {
		return strings.TrimSuffix(pr.HeadRepository.NameWithOwner, ".git")
	}
	if pr.HeadRepositoryOwner.Login != "" && pr.HeadRepository.Name != "" {
		return pr.HeadRepositoryOwner.Login + "/" + strings.TrimSuffix(pr.HeadRepository.Name, ".git")
	}
	return ""
}

func branchUpstreamRepository(dir, branch string) string {
	remote, err := gitOutput(dir, "config", "--get", "branch."+branch+".remote")
	if err != nil {
		return ""
	}
	remote = strings.TrimSpace(remote)
	if remote == "" || remote == "." {
		return ""
	}
	remoteURL, err := gitOutput(dir, "remote", "get-url", remote)
	if err != nil {
		return ""
	}
	return githubRepository(strings.TrimSpace(remoteURL))
}

func githubRepository(remote string) string {
	remote = strings.TrimSuffix(strings.TrimSuffix(remote, "/"), ".git")
	lower := strings.ToLower(remote)
	for _, prefix := range []string{"git@github.com:", "ssh://git@github.com/", "https://github.com/", "http://github.com/", "git://github.com/"} {
		if strings.HasPrefix(lower, prefix) {
			return remote[len(prefix):]
		}
	}
	return ""
}

func aggregateCheckState(checks []checkRollupItem) string {
	result := ""
	for _, check := range checks {
		if failedCheck(check) {
			return checkFailure
		}
		status := strings.ToUpper(check.Status)
		if check.State != "" {
			status = strings.ToUpper(check.State)
		}
		conclusion := strings.ToUpper(check.Conclusion)
		switch {
		case status == "IN_PROGRESS" || status == "QUEUED" || status == "PENDING" || status == "REQUESTED" || status == "WAITING":
			result = checkPending
		case result == "" && (conclusion == "SUCCESS" || conclusion == "NEUTRAL" || conclusion == "SKIPPED" || status == "SUCCESS"):
			result = checkSuccess
		}
	}
	return result
}

func failedCheck(check checkRollupItem) bool {
	status := strings.ToUpper(check.Status)
	if check.State != "" {
		status = strings.ToUpper(check.State)
	}
	switch strings.ToUpper(check.Conclusion) {
	case "FAILURE", "CANCELLED", "TIMED_OUT", "STARTUP_FAILURE", "ACTION_REQUIRED":
		return true
	}
	return status == "FAILURE" || status == "ERROR"
}

func failedCheckNames(checks []checkRollupItem) []string {
	failed := make([]string, 0, len(checks))
	for _, check := range checks {
		name := strings.TrimSpace(check.Name)
		if name == "" {
			name = strings.TrimSpace(check.Context)
		}
		if failedCheck(check) && name != "" {
			failed = append(failed, name)
		}
	}
	return failed
}

type cachedPRStatus struct {
	Branch string `json:"branch"`
	Number int    `json:"number"`
	State  string `json:"state"`
	Draft  bool   `json:"draft,omitempty"`
	Check  string `json:"check,omitempty"`
}

func prStatusCachePath() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "jumpmux", "pr_status_cache.json"), nil
}

func loadPRStatusCache() map[string]item {
	path, err := prStatusCachePath()
	if err != nil {
		return map[string]item{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]item{}
	}
	var cached map[string]cachedPRStatus
	if json.Unmarshal(data, &cached) != nil {
		return map[string]item{}
	}
	items := make(map[string]item, len(cached))
	for path, status := range cached {
		items[path] = item{kind: "worktree", target: path, cwd: path, branch: status.Branch, prLoaded: true, prNumber: status.Number, prState: status.State, prDraft: status.Draft, prCheck: status.Check}
	}
	return items
}

func savePRStatusCache(items map[string]item) error {
	cached := make(map[string]cachedPRStatus, len(items))
	for path, item := range items {
		if item.prLoaded && item.prNumber != 0 && item.branch != "" {
			cached[path] = cachedPRStatus{Branch: item.branch, Number: item.prNumber, State: item.prState, Draft: item.prDraft, Check: item.prCheck}
		}
	}
	data, err := json.Marshal(cached)
	if err != nil {
		return err
	}
	path, err := prStatusCachePath()
	if err != nil {
		return err
	}
	return atomicWrite(path, append(data, '\n'), 0o600)
}
