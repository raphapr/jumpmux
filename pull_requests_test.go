package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyPRCacheInfersBranchFromGitCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path := "/repo"
	if err := saveGitStatusCache(map[string]item{path: {cwd: path, branch: "feature", gitLoaded: true}}); err != nil {
		t.Fatal(err)
	}
	cachePath, err := prStatusCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(cachePath, []byte(`{"/repo":{"number":23,"state":"OPEN"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	model := newDashboardForLaunch(path, "", false)
	if got := model.agentGit[path]; got.branch != "feature" || got.prNumber != 23 {
		t.Fatalf("legacy PR cache was not migrated: %#v", got)
	}
	if got := model.prCache[path].branch; got != "feature" {
		t.Fatalf("migrated cache branch = %q", got)
	}
	if err := savePRStatusCache(model.prCache); err != nil {
		t.Fatal(err)
	}
	if got := loadPRStatusCache()[path].branch; got != "feature" {
		t.Fatalf("saved migrated cache branch = %q", got)
	}
}

func TestPullRequestSelectionAvoidsForeignFork(t *testing.T) {
	for remote, want := range map[string]string{
		"git@github.com:owner/repo.git":       "owner/repo",
		"https://github.com/owner/repo.git":   "owner/repo",
		"ssh://git@github.com/owner/repo.git": "owner/repo",
		"https://example.com/owner/repo.git":  "",
	} {
		if got := githubRepository(remote); got != want {
			t.Fatalf("githubRepository(%q) = %q, want %q", remote, got, want)
		}
	}
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "feature"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.com"}, {"commit", "--allow-empty", "-qm", "base"}} {
		if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	oidOutput, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"remote", "add", "fork", "git@github.com:owner/fork.git"}, {"config", "branch.feature.remote", "fork"}} {
		if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	foreign, local := true, false
	matchingOID := strings.TrimSpace(string(oidOutput))
	candidates := []pullRequest{
		{Number: 1, State: "OPEN", HeadRefOID: matchingOID, HeadRepository: pullRequestRepository{NameWithOwner: "other/fork"}, IsCrossRepository: &foreign},
		{Number: 2, State: "OPEN", HeadRefOID: matchingOID, HeadRepository: pullRequestRepository{NameWithOwner: "owner/fork"}, IsCrossRepository: &foreign},
	}
	if got, ok := pullRequestForBranch(repo, "feature", candidates); !ok || got.Number != 2 {
		t.Fatalf("tracked fork did not win: %#v, %v", got, ok)
	}
	if _, ok := pullRequestForBranch(repo, "feature", []pullRequest{{Number: 1, State: "OPEN", HeadRefOID: matchingOID, HeadRepository: pullRequestRepository{NameWithOwner: "other/fork"}, IsCrossRepository: &foreign}}); ok {
		t.Fatal("foreign matching OID was selected")
	}
	if got, ok := pullRequestForBranch(repo, "feature", []pullRequest{{Number: 3, State: "OPEN", IsCrossRepository: &local}}); !ok || got.Number != 3 {
		t.Fatalf("unique same-repository fallback = %#v, %v", got, ok)
	}
	if got, ok := pullRequestForBranch(repo, "feature", []pullRequest{{Number: 4, State: "MERGED", IsCrossRepository: &local}, {Number: 5, State: "OPEN", IsCrossRepository: &local}}); !ok || got.Number != 5 {
		t.Fatalf("open PR did not beat merged history: %#v, %v", got, ok)
	}
	if got, ok := pullRequestForBranch("/missing", "feature", []pullRequest{{Number: 6, State: "OPEN"}}); !ok || got.Number != 6 {
		t.Fatalf("legacy unique fallback = %#v, %v", got, ok)
	}
}

func TestLegacyPullRequestFallbackAndEmptyCandidates(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(`#!/bin/sh
case "$*" in
  *headRefOid*) exit 1 ;;
esac
printf '%s\n' '[{"number":7,"state":"OPEN","isDraft":false,"headRefName":"feature"}]'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	gitLog := filepath.Join(t.TempDir(), "git.log")
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho called >>\"$GIT_LOG\"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_LOG", gitLog)
	pullRequestMemory.Lock()
	pullRequestMemory.values = nil
	pullRequestMemory.Unlock()
	repo := t.TempDir()
	listed, loaded := listPullRequests(repo)
	if !loaded || len(listed["feature"]) != 1 || listed["feature"][0].IdentityAvailable {
		t.Fatalf("legacy PR lookup = %#v, loaded=%v", listed, loaded)
	}
	if got, ok := pullRequestForBranch(repo, "feature", listed["feature"]); !ok || got.Number != 7 {
		t.Fatalf("legacy PR selection = %#v, %v", got, ok)
	}
	if _, ok := pullRequestForBranch(repo, "feature", nil); ok {
		t.Fatal("empty candidate list selected a PR")
	}
	if _, err := os.Stat(gitLog); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty or legacy PR selection invoked Git: %v", err)
	}
}

func TestAgentPRStatusOutsideLaunchRepo(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "feature"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.com"}, {"commit", "--allow-empty", "-qm", "base"}} {
		if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(`#!/bin/sh
printf '%s\n' '[{"number":23,"state":"OPEN","isDraft":true,"headRefName":"feature","isCrossRepository":false,"statusCheckRollup":[{"status":"COMPLETED","conclusion":"FAILURE"}]}]'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	pullRequestMemory.Lock()
	pullRequestMemory.values = nil
	pullRequestMemory.Unlock()

	details := agentGitDetails([]item{{cwd: repo}})
	if len(details) != 1 || details[0].prNumber != 23 || details[0].prCheck != checkFailure {
		t.Fatalf("cross-repository agent PR details = %#v", details)
	}
}

func TestAgentGitStatusStopsLoadingWithoutCurrentRepoWorktree(t *testing.T) {
	nonGit := t.TempDir()
	details := agentGitDetails([]item{{cwd: nonGit}, {cwd: nonGit}})
	if len(details) != 1 || !details[0].gitLoaded {
		t.Fatalf("non-Git agent details = %#v", details)
	}

	model := newDashboard("/current-repo")
	agent := item{kind: "session", target: "%7", cwd: "/other-repo"}
	updated, command := model.Update(dashboardDataMsg(dashboardData{agents: []item{agent}}))
	model = updated.(dashboardModel)
	if command == nil || !model.agentGitInFlight {
		t.Fatal("agent Git refresh was not started")
	}
	if got := gitStatusText(model.gitItem(agent), model.now); got != spinnerFrame(model.now) {
		t.Fatalf("initial agent Git status = %q", got)
	}

	updated, _ = model.Update(agentGitMsg{{cwd: agent.cwd, gitLoaded: true, prLoaded: true, prNumber: 23, prState: "OPEN", prDraft: true, prCheck: checkFailure}})
	model = updated.(dashboardModel)
	if got := gitStatusText(model.gitItem(agent), model.now); got != "-" {
		t.Fatalf("loaded agent Git status = %q", got)
	}
	if got := prText(model.gitItem(agent), model.now); got != "#23 "+dashboardIcon(prDraftIcon, "D")+" "+dashboardIcon(checkFailureIcon, "x") {
		t.Fatalf("cross-repository agent PR status = %q", got)
	}

	updated, _ = model.Update(agentGitMsg{{cwd: agent.cwd, gitLoaded: true, dirty: true, added: 3}})
	model = updated.(dashboardModel)
	if got := gitStatusText(model.gitItem(agent), model.now); !strings.Contains(got, "+3") {
		t.Fatalf("dirty agent Git status = %q", got)
	}
	if got := prText(model.gitItem(agent), model.now); got != "#23 "+dashboardIcon(prDraftIcon, "D")+" "+dashboardIcon(checkFailureIcon, "x") {
		t.Fatalf("cached PR lost after failed refresh = %q", got)
	}
}

func TestPRCacheMatchesLoadedBranch(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path := "/repo"
	if err := saveGitStatusCache(map[string]item{path: {cwd: path, branch: "feature", gitLoaded: true}}); err != nil {
		t.Fatal(err)
	}
	if err := savePRStatusCache(map[string]item{path: {cwd: path, branch: "feature", prLoaded: true, prNumber: 23, prState: "OPEN"}}); err != nil {
		t.Fatal(err)
	}
	model := newDashboardForLaunch(path, "", false)
	updated, _ := model.Update(worktreeDataMsg{stage: worktreeListStage, generation: model.worktreeGeneration, worktrees: []item{{kind: "worktree", target: path, cwd: path, branch: "feature"}}})
	if got := updated.(dashboardModel).worktrees[0].prNumber; got != 23 {
		t.Fatalf("matching cached PR = %d", got)
	}
	model = newDashboardForLaunch(path, "", false)
	updated, _ = model.Update(worktreeDataMsg{stage: worktreeListStage, generation: model.worktreeGeneration, worktrees: []item{{kind: "worktree", target: path, cwd: path, branch: "other"}}})
	if got := updated.(dashboardModel).worktrees[0].prNumber; got != 0 {
		t.Fatalf("stale cached PR applied to another branch: %d", got)
	}
	model = newDashboardForLaunch(path, "", false)
	model.allAgents = []item{{kind: "session", cwd: path}}
	updated, _ = model.Update(agentGitMsg{{cwd: path, branch: "other", gitLoaded: true}})
	if got := updated.(dashboardModel).agentGit[path].prNumber; got != 0 {
		t.Fatalf("failed refresh retained PR on another branch: %d", got)
	}
}
