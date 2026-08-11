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
	checkSuccess = "success"
	checkFailure = "failure"
	checkPending = "pending"
)

type pullRequest struct {
	Number            int               `json:"number"`
	State             string            `json:"state"`
	Draft             bool              `json:"isDraft"`
	HeadRefName       string            `json:"headRefName"`
	StatusCheckRollup []checkRollupItem `json:"statusCheckRollup"`
	Check             string            `json:"-"`
}

type checkRollupItem struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

type pullRequestMemoryEntry struct {
	expires time.Time
	values  map[string]pullRequest
}

var pullRequestMemory struct {
	sync.Mutex
	values map[string]pullRequestMemoryEntry
}

func listPullRequests(repo string) (map[string]pullRequest, bool) {
	pullRequestMemory.Lock()
	entry, ok := pullRequestMemory.values[repo]
	pullRequestMemory.Unlock()
	if ok && time.Now().Before(entry.expires) {
		return entry.values, true
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// ponytail: cap PR lookup; add pagination if repositories with 100+ PRs miss active branches.
	fields := "number,state,isDraft,headRefName,statusCheckRollup"
	cmd := boundedCommand(ctx, "gh", "pr", "list", "--state", "all", "--limit", "100", "--json", fields)
	cmd.Dir = repo
	output, err := cmd.Output()
	if err != nil {
		cmd = boundedCommand(ctx, "gh", "pr", "list", "--state", "all", "--limit", "100", "--json", "number,state,isDraft,headRefName")
		cmd.Dir = repo
		output, err = cmd.Output()
	}
	if err != nil {
		return nil, false
	}

	values := parsePullRequests(output)
	pullRequestMemory.Lock()
	if pullRequestMemory.values == nil {
		pullRequestMemory.values = map[string]pullRequestMemoryEntry{}
	}
	pullRequestMemory.values[repo] = pullRequestMemoryEntry{expires: time.Now().Add(time.Minute), values: values}
	pullRequestMemory.Unlock()
	return values, true
}

func parsePullRequests(output []byte) map[string]pullRequest {
	var listed []pullRequest
	if json.Unmarshal(output, &listed) != nil {
		return map[string]pullRequest{}
	}
	result := make(map[string]pullRequest, len(listed))
	for _, pr := range listed {
		if pr.HeadRefName != "" {
			pr.Check = aggregateCheckState(pr.StatusCheckRollup)
			if _, exists := result[pr.HeadRefName]; !exists {
				result[pr.HeadRefName] = pr
			}
		}
	}
	return result
}

func aggregateCheckState(checks []checkRollupItem) string {
	result := ""
	for _, check := range checks {
		status := strings.ToUpper(check.Status)
		if check.State != "" {
			status = strings.ToUpper(check.State)
		}
		conclusion := strings.ToUpper(check.Conclusion)
		switch {
		case conclusion == "FAILURE" || conclusion == "CANCELLED" || conclusion == "TIMED_OUT" || conclusion == "STARTUP_FAILURE" || conclusion == "ACTION_REQUIRED" || status == "FAILURE" || status == "ERROR":
			return checkFailure
		case status == "IN_PROGRESS" || status == "QUEUED" || status == "PENDING" || status == "REQUESTED" || status == "WAITING":
			result = checkPending
		case result == "" && (conclusion == "SUCCESS" || conclusion == "NEUTRAL" || conclusion == "SKIPPED" || status == "SUCCESS"):
			result = checkSuccess
		}
	}
	return result
}

type cachedPRStatus struct {
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
		items[path] = item{kind: "worktree", target: path, cwd: path, prLoaded: true, prNumber: status.Number, prState: status.State, prDraft: status.Draft, prCheck: status.Check}
	}
	return items
}

func savePRStatusCache(items map[string]item) error {
	cached := make(map[string]cachedPRStatus, len(items))
	for path, item := range items {
		if item.prLoaded && item.prNumber != 0 {
			cached[path] = cachedPRStatus{Number: item.prNumber, State: item.prState, Draft: item.prDraft, Check: item.prCheck}
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
