package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitDetailsMatchDashboard(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		command := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.name", "Test")
	run("config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "base")
	run("checkout", "-qb", "feature")
	if err := os.WriteFile(filepath.Join(dir, "committed.txt"), []byte("a\nb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "feature")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("x\ny\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := loadGitDetails(item{kind: "worktree", cwd: dir, branch: "feature"}, "main")
	if !got.gitLoaded || !got.dirty || got.branch != "feature" || got.baseBranch != "main" {
		t.Fatalf("Git identity = %#v", got)
	}
	if got.committedAdded != 2 || got.committedRemoved != 0 || got.added != 3 || got.removed != 0 || got.untracked != 1 {
		t.Fatalf("Git stats = %#v", got)
	}

	run("checkout", "-q", "--detach")
	detached := loadGitDetails(item{kind: "worktree", target: dir, cwd: dir, branch: "detached"}, "main")
	if !detached.gitLoaded || detached.branch != "detached" {
		t.Fatalf("detached Git status = %#v", detached)
	}
	merged := mergeWorktreeData([]item{{target: dir, branch: "detached"}}, []item{detached}, worktreeGitStage)
	if !merged[0].gitLoaded {
		t.Fatal("detached Git details were not merged")
	}
}

func TestGitStatusCacheHydratesFirstFrame(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path := "/repo"
	cached := item{cwd: path, target: path, branch: "feature", gitLoaded: true, dirty: true, added: 7, removed: 2, ahead: 1}
	if err := saveGitStatusCache(map[string]item{path: cached}); err != nil {
		t.Fatal(err)
	}
	if err := savePRStatusCache(map[string]item{path: {cwd: path, branch: "feature", prLoaded: true, prNumber: 23, prState: "OPEN", prDraft: true, prCheck: checkFailure}}); err != nil {
		t.Fatal(err)
	}
	cachePath, err := gitStatusCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(cachePath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("cache mode: info=%v err=%v", info, err)
	}
	prCachePath, err := prStatusCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(prCachePath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("PR cache mode: info=%v err=%v", info, err)
	}

	model := newDashboardForLaunch(path, "", false)
	agent := item{kind: "session", cwd: path}
	if got := gitStatusText(model.gitItem(agent), model.now); !strings.Contains(got, "+7") || !strings.Contains(got, "↑1") {
		t.Fatalf("cached agent Git status = %q", got)
	}
	if got := prText(model.gitItem(agent), model.now); got != "#23 "+dashboardIcon(prDraftIcon, "D")+" "+dashboardIcon(checkFailureIcon, "x") {
		t.Fatalf("cached agent PR status = %q", got)
	}
	updated, _ := model.Update(worktreeDataMsg{
		stage: worktreeListStage, generation: model.worktreeGeneration,
		worktrees: []item{{kind: "worktree", target: path, cwd: path, branch: "feature"}},
	})
	worktree := updated.(dashboardModel).worktrees[0]
	if !worktree.gitLoaded || worktree.added != 7 || worktree.removed != 2 {
		t.Fatalf("cached worktree Git status = %#v", worktree)
	}
	if worktree.prNumber != 23 || worktree.prCheck != checkFailure {
		t.Fatalf("cached worktree PR status = %#v", worktree)
	}

	if err := os.WriteFile(cachePath, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadGitStatusCache(); len(got) != 0 {
		t.Fatalf("malformed cache loaded: %#v", got)
	}
	if err := os.WriteFile(prCachePath, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadPRStatusCache(); len(got) != 0 {
		t.Fatalf("malformed PR cache loaded: %#v", got)
	}
}

func TestBranchChangeClearsMetadata(t *testing.T) {
	current := []item{{target: "/repo", branch: "old", gitLoaded: true, dirty: true, baseBranch: "main", committedAdded: 3, added: 2, ahead: 1, hasConflict: true, prNumber: 4}}
	got := mergeWorktreeData(current, []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "new"}}, worktreeListStage)
	if got[0].gitLoaded || got[0].dirty || got[0].baseBranch != "" || got[0].committedAdded != 0 || got[0].added != 0 || got[0].ahead != 0 || got[0].hasConflict || got[0].prNumber != 0 {
		t.Fatalf("branch metadata leaked: %#v", got[0])
	}
}

func TestGitFailuresAreNotRenderedAsClean(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nprintf '\\033[31mfailed\\033[0m\\n' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	item := item{kind: "worktree", target: "/repo", cwd: "/repo"}
	preview := loadPreview(item, schemeDefault, 1)().(previewMsg)
	if strings.Contains(strings.Join(preview.lines, "\n"), "clean") || strings.Contains(strings.Join(preview.rightLines, "\n"), "no commits") || strings.Contains(strings.Join(preview.lines, "\n"), "\x1b") {
		t.Fatalf("preview hid Git failure: %#v", preview)
	}
	diff := loadDiff(item, 1)().(diffMsg)
	if len(diff.lines) != 1 || strings.Contains(diff.lines[0], "\x1b") || !strings.Contains(diff.lines[0], "Git unavailable") {
		t.Fatalf("diff hid Git failure: %#v", diff)
	}
}
