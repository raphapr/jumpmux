package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestAddWorktreeRequiresLocalDefaultBranch(t *testing.T) {
	repo := t.TempDir()
	if output, err := exec.Command("git", "-C", repo, "init", "-q", "-b", "trunk").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	if _, _, err := addWorktree(repo, "feature", true); err == nil || !strings.Contains(err.Error(), "local default branch") {
		t.Fatalf("missing default branch error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"__worktrees")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing-base add created path: %v", err)
	}
}

func TestAddWorktreeUsesOriginHeadDefaultBranch(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "config", "user.name", "Test"}, {"-C", repo, "config", "user.email", "test@example.com"}, {"-C", repo, "commit", "--allow-empty", "-qm", "base"}, {"-C", repo, "branch", "develop"}, {"-C", repo, "update-ref", "refs/remotes/origin/develop", "refs/heads/develop"}, {"-C", repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/develop"}} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	if got := gitDefaultBranch(repo); got != "develop" {
		t.Fatalf("default branch = %q, want develop", got)
	}
	created, _, err := addWorktree(repo, "feature", true)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", created.cwd, "merge-base", "--is-ancestor", "develop", "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("worktree was not based on develop: %v\n%s", err, output)
	}
}

func TestAddWorktreeRejectsUnavailableOriginHead(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "repo")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "config", "user.name", "Test"}, {"-C", repo, "config", "user.email", "test@example.com"}, {"-C", repo, "commit", "--allow-empty", "-qm", "base"}, {"-C", repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/develop"}} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	if _, _, err := addWorktree(repo, "feature", true); err == nil || !strings.Contains(err.Error(), "not available locally") {
		t.Fatalf("unavailable origin default error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "repo__worktrees")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unavailable-base add created path: %v", err)
	}
}

func TestNativeGitUpdatesRejectUnavailableDefaultBranch(t *testing.T) {
	parent := t.TempDir()
	repo, worktree := filepath.Join(parent, "repo"), filepath.Join(parent, "feature")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "config", "user.name", "Test"}, {"-C", repo, "config", "user.email", "test@example.com"}, {"-C", repo, "commit", "--allow-empty", "-qm", "base"}, {"-C", repo, "worktree", "add", "-qb", "feature", worktree}, {"-C", worktree, "commit", "--allow-empty", "-qm", "feature"}, {"-C", repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/develop"}} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	for _, operation := range []string{"rebase", "merge"} {
		if err := updateWorktree(worktree, "feature", operation); err == nil || !strings.Contains(err.Error(), "not available locally") {
			t.Fatalf("unavailable default %s = %v", operation, err)
		}
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("worktree changed: %v", err)
	}
}

func TestWorktreeRemovalRevalidatesTmuxUse(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "repo")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "config", "user.name", "Test"}, {"-C", repo, "config", "user.email", "test@example.com"}, {"-C", repo, "commit", "--allow-empty", "-qm", "base"}, {"-C", repo, "worktree", "add", "-qb", "feature", filepath.Join(parent, "feature")}} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(`#!/bin/sh
printf '%%7\0374321\037%s\037Pi\037$1\037dev\037@2\037feature\037pi\037%s\0372\0370\036\n' "$FAKE_PANE_PATH" "$FAKE_WORKTREE"
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", "/tmp/tmux-test,1,0")
	worktree := filepath.Join(parent, "feature")
	for _, scenario := range []struct{ panePath, marker string }{{worktree, ""}, {parent, worktree}} {
		t.Setenv("FAKE_PANE_PATH", scenario.panePath)
		t.Setenv("FAKE_WORKTREE", scenario.marker)
		// Force and stash do not skip the pane check.
		for _, mode := range []removeMode{removeClean, removeStash, removeDiscard} {
			if err := removeWorktree(repo, worktree, mode); err == nil || err.Error() != "worktree is open in tmux pane dev:2.0 (feature); close it before removing the worktree" {
				t.Fatalf("worktree in use was removed in mode %d: %v", mode, err)
			}
		}
		// The dashboard confirmation finds the same pane before any key is pressed.
		model := newDashboard(repo)
		model.tab = tabWorktrees
		command := model.beginRemove(item{kind: "worktree", target: worktree, cwd: worktree, branch: "feature"})
		if msg, ok := command().(removeChangesMsg); !ok || msg.err != nil || len(msg.panes) != 1 || msg.panes[0].ID != "%7" {
			t.Fatalf("confirmation check = %#v", msg)
		}
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("worktree was mutated despite validation: %v", err)
	}
}

func TestWorktreeRemovalRefusesLaunchWorktreeWithoutTmux(t *testing.T) {
	parent := t.TempDir()
	repo, worktree := filepath.Join(parent, "repo"), filepath.Join(parent, "feature")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "config", "user.name", "Test"}, {"-C", repo, "config", "user.email", "test@example.com"}, {"-C", repo, "commit", "--allow-empty", "-qm", "base"}, {"-C", repo, "worktree", "add", "-qb", "feature", worktree}} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\necho 'no server running on /tmp/tmux-test' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := removeWorktree(worktree, worktree, removeClean); err == nil || !strings.Contains(err.Error(), "current worktree") {
		t.Fatalf("launch worktree removal = %v", err)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("current worktree was mutated: %v", err)
	}
}

func TestCleanupPrunableWorktreeRevalidatesAndPrunes(t *testing.T) {
	parent := t.TempDir()
	repo, stale, other := filepath.Join(parent, "repo"), filepath.Join(parent, "stale"), filepath.Join(parent, "other")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "config", "user.name", "Test"}, {"-C", repo, "config", "user.email", "test@example.com"}, {"-C", repo, "commit", "--allow-empty", "-qm", "base"}, {"-C", repo, "worktree", "add", "-qb", "stale", stale}, {"-C", repo, "worktree", "add", "-qb", "other", other}} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	for _, path := range []string{stale, other} {
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
	}
	items, err := listWorktreeItems(repo)
	if err != nil {
		t.Fatal(err)
	}
	var selected item
	for _, candidate := range items {
		if samePath(candidate.cwd, stale) {
			selected = candidate
		}
	}
	if !selected.prunable {
		t.Fatalf("stale worktree not marked prunable: %#v", items)
	}
	if err := cleanupPrunableWorktree(repo, selected); err != nil {
		t.Fatal(err)
	}
	items, err = listWorktreeItems(repo)
	if err != nil {
		t.Fatal(err)
	}
	otherKept := false
	for _, candidate := range items {
		if samePath(candidate.cwd, stale) {
			t.Fatalf("stale worktree record remained: %#v", candidate)
		}
		otherKept = otherKept || (samePath(candidate.cwd, other) && candidate.prunable)
	}
	if !otherKept {
		t.Fatalf("cleanup removed an unselected stale record: %#v", items)
	}
	if err := cleanupPrunableWorktree(repo, selected); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale cleanup was not revalidated: %v", err)
	}
}

func TestBareRepositoryWorktrees(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	parent := t.TempDir()
	source, bare := filepath.Join(parent, "source"), filepath.Join(parent, "repo.git")
	mainTree, feature := filepath.Join(parent, "main"), filepath.Join(parent, "feature")
	identity := []string{"-c", "user.name=Test", "-c", "user.email=test@example.com"}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", source},
		append(append([]string{"-C", source}, identity...), "commit", "--allow-empty", "-qm", "base"),
		{"clone", "-q", "--bare", source, bare},
		{"-C", bare, "worktree", "add", "-q", mainTree, "main"},
		{"-C", bare, "worktree", "add", "-qb", "feature", feature, "main"},
		append(append([]string{"-C", feature}, identity...), "commit", "--allow-empty", "-qm", "feature"),
	} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	items, err := listWorktreeItems(feature)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("bare layout rows = %#v", items)
	}
	for _, row := range items {
		if samePath(row.cwd, bare) || row.primary {
			t.Fatalf("bare repository leaked into the rows: %#v", row)
		}
	}
	if err := updateWorktree(feature, "feature", "rebase"); err != nil {
		t.Fatalf("bare layout rebase = %v", err)
	}
	if err := updateWorktree(feature, "feature", "merge"); err != nil {
		t.Fatalf("bare layout merge = %v", err)
	}
	head, headErr := exec.Command("git", "-C", mainTree, "rev-parse", "HEAD").Output()
	tip, tipErr := exec.Command("git", "-C", feature, "rev-parse", "HEAD").Output()
	if headErr != nil || tipErr != nil || string(head) != string(tip) {
		t.Fatalf("merge did not fast-forward main: %s %s, %v %v", head, tip, headErr, tipErr)
	}
	// The primary worktree is the bare repository, where rev-parse
	// --show-toplevel fails; adding and reusing branches must still work.
	for branch, want := range map[string]branchSource{"extra": branchNew, "feature": branchCheckedOut} {
		if _, plan, err := addWorktree(feature, branch, true); err != nil || plan.source != want {
			t.Fatalf("bare layout add %q = %#v, %v", branch, plan, err)
		}
	}
	if got, err := availableAgentBranch(bare, "extra"); err != nil || got != "extra-2" {
		t.Fatalf("bare layout agent branch = %q, %v", got, err)
	}
}

func TestActionCommandToleratesLingeringChildOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// gh pr view --web can exit while the browser it started still holds the output pipe.
	if _, err := runActionCommand(ctx, t.TempDir(), "sh", "-c", "sleep 1 & exit 0"); err != nil {
		t.Fatalf("successful command with a lingering child = %v", err)
	}
	if _, err := runActionCommand(ctx, t.TempDir(), "sh", "-c", "sleep 1 & exit 3"); err == nil {
		t.Fatal("failed command with a lingering child reported success")
	}
}

func TestNativeGitActionsRejectDirtyWorktrees(t *testing.T) {
	parent := t.TempDir()
	repo, worktree := filepath.Join(parent, "repo"), filepath.Join(parent, "feature")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "config", "user.name", "Test"}, {"-C", repo, "config", "user.email", "test@example.com"}, {"-C", repo, "commit", "--allow-empty", "-qm", "base"}, {"-C", repo, "worktree", "add", "-qb", "feature", worktree}} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(worktree, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := updateWorktree(worktree, "feature", "rebase"); err == nil || !strings.Contains(err.Error(), "commit or stash changes") {
		t.Fatalf("dirty rebase = %v", err)
	}
	if err := removeWorktree(repo, worktree, removeClean); err == nil {
		t.Fatal("dirty worktree was removed")
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("dirty worktree disappeared: %v", err)
	}
}

func TestNativeGitActionsRejectDirtyPrimaryForMerge(t *testing.T) {
	parent := t.TempDir()
	repo, worktree := filepath.Join(parent, "repo"), filepath.Join(parent, "feature")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "config", "user.name", "Test"}, {"-C", repo, "config", "user.email", "test@example.com"}, {"-C", repo, "commit", "--allow-empty", "-qm", "base"}, {"-C", repo, "worktree", "add", "-qb", "feature", worktree}, {"-C", worktree, "commit", "--allow-empty", "-qm", "feature"}} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := updateWorktree(worktree, "feature", "merge"); err == nil || !strings.Contains(err.Error(), "commit or stash changes in "+compactHome(repo)) {
		t.Fatalf("dirty merge = %v", err)
	}
}

func TestNativeGitRebaseLeavesConflictsOpen(t *testing.T) {
	parent := t.TempDir()
	repo, worktree := filepath.Join(parent, "repo"), filepath.Join(parent, "feature")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "config", "user.name", "Test"}, {"-C", repo, "config", "user.email", "test@example.com"}, {"-C", repo, "commit", "--allow-empty", "-qm", "base"}, {"-C", repo, "worktree", "add", "-qb", "feature", worktree}} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	for dir, content := range map[string]string{repo: "main\n", worktree: "feature\n"} {
		if err := os.WriteFile(filepath.Join(dir, "shared.txt"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command("git", "-C", dir, "add", "shared.txt").CombinedOutput(); err != nil {
			t.Fatalf("git add: %v\n%s", err, output)
		}
	}
	for dir, message := range map[string]string{repo: "main", worktree: "feature"} {
		if output, err := exec.Command("git", "-C", dir, "commit", "-m", message).CombinedOutput(); err != nil {
			t.Fatalf("git commit: %v\n%s", err, output)
		}
	}
	if err := updateWorktree(worktree, "feature", "rebase"); err == nil {
		t.Fatal("conflicting rebase succeeded")
	}
	if !gitRebaseInProgress(worktree) {
		t.Fatal("conflicting rebase was not left open")
	}
}

func TestNativeGitMergeRejectsDivergence(t *testing.T) {
	parent := t.TempDir()
	repo, worktree := filepath.Join(parent, "repo"), filepath.Join(parent, "feature")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "config", "user.name", "Test"}, {"-C", repo, "config", "user.email", "test@example.com"}, {"-C", repo, "commit", "--allow-empty", "-qm", "base"}, {"-C", repo, "worktree", "add", "-qb", "feature", worktree}, {"-C", worktree, "commit", "--allow-empty", "-qm", "feature"}, {"-C", repo, "commit", "--allow-empty", "-qm", "main-change"}} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	before, err := exec.Command("git", "-C", repo, "rev-parse", "main").Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := updateWorktree(worktree, "feature", "merge"); err == nil {
		t.Fatal("divergent merge succeeded")
	}
	after, err := exec.Command("git", "-C", repo, "rev-parse", "main").Output()
	if err != nil || string(before) != string(after) {
		t.Fatalf("divergent merge changed main: before=%q after=%q err=%v", before, after, err)
	}
}

func TestNativeGitWorktreeActionsKeepBranch(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.com"}, {"commit", "--allow-empty", "-qm", "base"}} {
		if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	bin := t.TempDir()
	wtLog := filepath.Join(t.TempDir(), "wt.log")
	if err := os.WriteFile(filepath.Join(bin, "wt"), []byte("#!/bin/sh\necho called >>\"$WT_LOG\"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WT_LOG", wtLog)
	created, _, err := addWorktree(repo, "feature/test", true)
	if err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(parent, "repo__worktrees", "feature", "test")
	if created.cwd != worktree || created.branch != "feature/test" {
		t.Fatalf("created worktree = %#v", created)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("created worktree: %v", err)
	}
	items, err := listWorktreeItems(repo)
	if err != nil || len(items) != 2 {
		t.Fatalf("native worktree listing = %#v, %v", items, err)
	}
	if got := worktreeGitDetails(items); len(got) != 2 || !got[1].gitLoaded {
		t.Fatalf("native worktree metadata = %#v", got)
	}
	if err := updateWorktree(worktree, "feature/test", "rebase"); err != nil {
		t.Fatal(err)
	}
	if err := updateWorktree(worktree, "feature/test", "merge"); err != nil {
		t.Fatal(err)
	}
	tmuxBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmuxBin, "tmux"), []byte("#!/bin/sh\necho 'error connecting to /tmp/tmux-1001/default (No such file or directory)' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmuxBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := removeWorktree(repo, worktree, removeClean); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(worktree); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed worktree still exists: %v", err)
	}
	if output, err := exec.Command("git", "-C", repo, "show-ref", "--verify", "refs/heads/feature/test").CombinedOutput(); err != nil {
		t.Fatalf("native removal deleted branch: %v\n%s", err, output)
	}
	if data, err := os.ReadFile(wtLog); err == nil && len(data) != 0 {
		t.Fatalf("wt was invoked: %s", data)
	}
}

func TestNativeGitRebaseAndMergeActions(t *testing.T) {
	parent := t.TempDir()
	repo, worktree := filepath.Join(parent, "repo"), filepath.Join(parent, "feature")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "config", "user.name", "Test"}, {"-C", repo, "config", "user.email", "test@example.com"}, {"-C", repo, "commit", "--allow-empty", "-qm", "base"}, {"-C", repo, "worktree", "add", "-qb", "feature", worktree}, {"-C", worktree, "commit", "--allow-empty", "-qm", "feature"}} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	if err := updateWorktree(worktree, "feature", "rebase"); err != nil {
		t.Fatal(err)
	}
	if err := updateWorktree(worktree, "feature", "merge"); err != nil {
		t.Fatal(err)
	}
	mainHead, err := exec.Command("git", "-C", repo, "rev-parse", "main").Output()
	if err != nil {
		t.Fatal(err)
	}
	featureHead, err := exec.Command("git", "-C", worktree, "rev-parse", "feature").Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(mainHead) != string(featureHead) {
		t.Fatalf("main %q did not fast-forward to feature %q", mainHead, featureHead)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("native merge removed worktree: %v", err)
	}
}

func TestNativeGitMergeRequiresDefaultBranch(t *testing.T) {
	parent := t.TempDir()
	repo, worktree := filepath.Join(parent, "repo"), filepath.Join(parent, "feature")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "config", "user.name", "Test"}, {"-C", repo, "config", "user.email", "test@example.com"}, {"-C", repo, "commit", "--allow-empty", "-qm", "base"}, {"-C", repo, "worktree", "add", "-qb", "feature", worktree}, {"-C", worktree, "commit", "--allow-empty", "-qm", "feature"}, {"-C", repo, "switch", "-qc", "other"}} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	if err := updateWorktree(worktree, "feature", "merge"); err == nil || !strings.Contains(err.Error(), "check out main in a worktree") {
		t.Fatalf("merge without main checked out = %v", err)
	}
	head, err := exec.Command("git", "-C", repo, "branch", "--show-current").Output()
	if err != nil || strings.TrimSpace(string(head)) != "other" {
		t.Fatalf("primary branch changed: %q, %v", head, err)
	}
}

func TestOpenPullRequestCommand(t *testing.T) {
	repo, bin, log := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "gh.log")
	script := "#!/bin/sh\nprintf '%s\n' \"$*\" >\"$GH_LOG\"\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_LOG", log)
	if err := openPullRequest(repo, 23); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "pr view 23 --web" {
		t.Fatalf("gh command = %q", data)
	}
}

func TestDashboardWorktreeActionModes(t *testing.T) {
	t.Setenv("TMUX", "/tmp/test,1,0")
	model := newDashboard("/repo")
	model.width, model.height, model.tab = 120, 30, 1
	model.worktrees = []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main", primary: true}, {kind: "worktree", target: "/feature", cwd: "/feature", branch: "feature"}}
	model.index = 1
	footer := ansi.Strip(model.renderFooter(120))
	for _, expected := range []string{"↵ Open", "a Add", "A Agent", "r Remove"} {
		if !strings.Contains(footer, expected) {
			t.Fatalf("worktree footer missing %q: %s", expected, footer)
		}
	}
	if strings.Contains(footer, "o Open") {
		t.Fatalf("worktree footer repeats Open: %s", footer)
	}
	// Theme is the first item dropped when the footer is crowded.
	if wide := ansi.Strip(model.renderFooter(140)); !strings.Contains(wide, "t Theme") {
		t.Fatalf("wide worktree footer missing t Theme: %s", wide)
	}
	if strings.Contains(footer, "Refresh") {
		t.Fatalf("worktree footer still shows refresh: %s", footer)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	model = updated.(dashboardModel)
	if model.action != actionAddWorktree || !strings.Contains(ansi.Strip(model.renderFooter(120)), "branch:") {
		t.Fatalf("add mode = %#v", model.action)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(dashboardModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = updated.(dashboardModel)
	if model.action != actionRemoveWorktree || model.actionTarget.cwd != "/feature" {
		t.Fatalf("remove mode = %#v target=%#v", model.action, model.actionTarget)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(dashboardModel)
	if model.action != actionNone {
		t.Fatal("remove cancellation did not close confirmation")
	}
	model.tab, model.index = 1, 0
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = updated.(dashboardModel)
	if model.action != actionNone || model.err != nil {
		t.Fatal("primary worktree removal was exposed")
	}
	model.tab, model.err = 0, nil
	footer = ansi.Strip(model.renderFooter(120))
	if !strings.Contains(footer, "s Scope (All)") || !strings.Contains(footer, "t Theme") || strings.Contains(footer, "Refresh") {
		t.Fatalf("agent footer = %s", footer)
	}
}

func TestSuccessfulNavigationQuitsDashboard(t *testing.T) {
	model := newDashboard("/repo")
	updated, command := model.Update(worktreeActionMsg{action: actionAddWorktree, notice: "Created worktree feature", quit: true})
	model = updated.(dashboardModel)
	if command == nil || model.err != nil {
		t.Fatalf("successful navigation error = %v", model.err)
	}
	message := command()
	if _, ok := message.(tea.QuitMsg); !ok {
		t.Fatalf("successful navigation command = %T", message)
	}

	failure := errors.New("create failed")
	updated, command = newDashboard("/repo").Update(worktreeActionMsg{action: actionAddWorktree, quit: true, err: failure})
	model = updated.(dashboardModel)
	if command == nil || !errors.Is(model.err, failure) {
		t.Fatalf("failed navigation error = %v", model.err)
	}
	if _, ok := command().(tea.QuitMsg); ok {
		t.Fatal("failed navigation quit the dashboard")
	}
}

func TestWorktreeRebaseAndMergeMenuActions(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height, model.tab = 100, 20, tabWorktrees
	model.worktrees = []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main"}, {kind: "worktree", target: "/feature", cwd: "/feature", branch: "feature"}}
	model.index = 1
	entries := model.actionMenuEntries()
	labels := make([]string, len(entries))
	for index := range entries {
		labels[index] = entries[index].label
	}
	joined := strings.Join(labels, ",")
	for _, expected := range []string{"Rebase onto default branch", "Merge into default branch"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("worktree actions missing %q: %s", expected, joined)
		}
	}
	model.beginWorktreeOperation(model.worktrees[1], actionRebaseWorktree)
	if model.action != actionRebaseWorktree || !strings.Contains(ansi.Strip(model.renderPreview(100)), "Conflicts leave the rebase open") {
		t.Fatalf("rebase confirmation = action %v\n%s", model.action, ansi.Strip(model.renderPreview(100)))
	}
	model.action = actionNone
	model.beginWorktreeOperation(model.worktrees[1], actionMergeWorktree)
	preview := ansi.Strip(model.renderPreview(100))
	if !strings.Contains(preview, "Will merge feature") || !strings.Contains(preview, "Fast-forward only") || strings.Contains(preview, "Squash") {
		t.Fatalf("native merge confirmation = %s", preview)
	}
}

func TestDestructiveActionsUseEnterConfirmation(t *testing.T) {
	tests := []struct {
		name   string
		action dashboardAction
		key    string
		label  string
	}{
		{"remove worktree", actionRemoveWorktree, "r", "Remove"},
		{"cleanup worktree", actionCleanupWorktree, "x", "Clean up"},
		{"rebase worktree", actionRebaseWorktree, "b", "Rebase"},
		{"merge worktree", actionMergeWorktree, "m", "Merge"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := newDashboard("/repo")
			model.width, model.height, model.action, model.tab = 100, 20, test.action, tabWorktrees
			model.actionTarget = item{kind: "worktree", title: "dev", branch: "feature", cwd: "/feature"}
			model.removeChanges = &worktreeChanges{}

			// Keys appear once, in the footer.
			if preview := promptText(model, false); strings.Contains(preview, "Enter") || strings.Contains(preview, "Esc") {
				t.Fatalf("confirmation preview repeats keys: %q", preview)
			}
			if footer := ansi.Strip(model.renderFooter(model.width)); !strings.Contains(footer, "Enter "+test.label+" Esc Cancel") {
				t.Fatalf("confirmation footer = %q", footer)
			}
			// The action's own key never confirms; in a removal it re-checks.
			updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(test.key)})
			if updated.(dashboardModel).action != test.action || (command != nil) != (test.action == actionRemoveWorktree) {
				t.Fatalf("%s confirmed %s", test.key, test.name)
			}
			updated, command = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
			if updated.(dashboardModel).action != actionRunning || command == nil {
				t.Fatalf("Enter did not confirm %s", test.name)
			}
		})
	}
}

func TestRemovalConfirmationPreviewAndInput(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height, model.tab = 100, 20, 1
	model.worktrees = []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main"}, {kind: "worktree", target: "/feature", cwd: "/feature", branch: "feature"}}
	model.index = 1
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	updated, _ = updated.(dashboardModel).Update(removeChangesMsg{request: updated.(dashboardModel).removeChangesRequest})
	model = updated.(dashboardModel)
	preview := ansi.Strip(model.renderPreview(model.width))
	for _, expected := range []string{"feature", "/feature", " No uncommitted changes", "The branch stays."} {
		if !strings.Contains(preview, expected) {
			t.Fatalf("removal preview missing %q:\n%s", expected, preview)
		}
	}
	// r inside the confirmation re-checks instead of confirming.
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if next := updated.(dashboardModel); next.action != actionRemoveWorktree || next.removeChanges != nil || next.removeChangesRequest != model.removeChangesRequest+1 || command == nil {
		t.Fatal("r did not re-check the worktree")
	}
	updated, command = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.(dashboardModel).action != actionRunning || command == nil {
		t.Fatal("Enter did not confirm removal")
	}
	model.action = actionRemoveWorktree
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.(dashboardModel).action != actionNone {
		t.Fatal("Esc did not cancel removal")
	}
}

// branchTestRepo has a local branch, a remote-only branch, a branch checked out
// in another worktree, and a second remote that shares a branch name with origin.
func branchTestRepo(t *testing.T) (repo, busy string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	parent := t.TempDir()
	repo, busy = filepath.Join(parent, "repo"), filepath.Join(parent, "busy")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", repo},
		{"-C", repo, "config", "user.name", "Test"},
		{"-C", repo, "config", "user.email", "test@example.com"},
		{"-C", repo, "commit", "--allow-empty", "-qm", "base"},
		{"-C", repo, "branch", "local"},
		{"-C", repo, "worktree", "add", "-qb", "busy", busy},
		{"-C", repo, "remote", "add", "origin", parent},
		{"-C", repo, "remote", "add", "fork", parent},
		{"-C", repo, "update-ref", "refs/remotes/origin/remote-only", "HEAD"},
		{"-C", repo, "update-ref", "refs/remotes/fork/remote-only", "HEAD"},
		{"-C", repo, "update-ref", "refs/remotes/fork/fork-only", "HEAD"},
	} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	return repo, busy
}

// promptText is the prompt panel's text without styling.
func promptText(model dashboardModel, compact bool) string {
	return ansi.Strip(strings.Join(model.promptLines(compact), "\n"))
}

func TestBranchRefsPlan(t *testing.T) {
	repo, busy := branchTestRepo(t)
	refs, err := loadBranchRefs(repo)
	if err != nil {
		t.Fatal(err)
	}
	if refs.root != repo {
		t.Errorf("root = %q, want %q", refs.root, repo)
	}
	for branch, want := range map[string]branchPlan{
		"fresh":       {branchNew, "main"},
		"local":       {branchLocal, "local"},
		"remote-only": {branchRemote, "origin/remote-only"},
		"fork-only":   {branchRemote, "fork/fork-only"},
		"busy":        {branchCheckedOut, busy},
		"main":        {branchCheckedOut, repo},
	} {
		if got := refs.plan(branch); got != want {
			t.Errorf("plan(%q) = %#v, want %#v", branch, got, want)
		}
	}
}

func TestAddWorktreeReusesExistingBranches(t *testing.T) {
	repo, busy := branchTestRepo(t)
	git := func(dir string, args ...string) string {
		t.Helper()
		output, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}

	added, plan, err := addWorktree(repo, "local", true)
	if err != nil || plan.source != branchLocal || added.cwd != worktreePath(repo, "local") {
		t.Fatalf("local add = %#v, %#v, %v", added, plan, err)
	}
	if got := git(added.cwd, "branch", "--show-current"); got != "local" {
		t.Fatalf("local worktree branch = %q", got)
	}

	added, plan, err = addWorktree(repo, "remote-only", true)
	if err != nil || plan.source != branchRemote || plan.ref != "origin/remote-only" {
		t.Fatalf("remote add = %#v, %#v, %v", added, plan, err)
	}
	if got := git(added.cwd, "rev-parse", "--abbrev-ref", "@{upstream}"); got != "origin/remote-only" {
		t.Fatalf("remote worktree upstream = %q", got)
	}

	added, plan, err = addWorktree(repo, "busy", true)
	if err != nil || plan.source != branchCheckedOut || added.cwd != busy || added.target != busy {
		t.Fatalf("checked-out add = %#v, %#v, %v", added, plan, err)
	}

	added, plan, err = addWorktree(repo, "fresh", true)
	if err != nil || plan.source != branchNew || plan.ref != "main" {
		t.Fatalf("new add = %#v, %#v, %v", added, plan, err)
	}
	if got := git(added.cwd, "branch", "--show-current"); got != "fresh" {
		t.Fatalf("new worktree branch = %q", got)
	}

	for plan, want := range map[branchPlan]string{
		{branchNew, "main"}:           "Created worktree x",
		{branchLocal, "x"}:            "Created worktree for existing branch x",
		{branchRemote, "origin/x"}:    "Created worktree x tracking origin/x",
		{branchCheckedOut, "/path/x"}: "Opened existing worktree x",
	} {
		if got := plan.notice("x"); got != want {
			t.Errorf("notice(%#v) = %q, want %q", plan, got, want)
		}
	}
}

func TestAvailableAgentBranchSkipsRemoteBranches(t *testing.T) {
	repo, _ := branchTestRepo(t)
	for branch, want := range map[string]string{"remote-only": "remote-only-2", "local": "local-2", "fresh": "fresh"} {
		if got, err := availableAgentBranch(repo, branch); err != nil || got != want {
			t.Errorf("availableAgentBranch(%q) = %q, %v, want %q", branch, got, err, want)
		}
	}
}

func TestAddWorktreePanelExplainsBranch(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height, model.action = 100, 24, actionAddWorktree
	if got := promptText(model, false); !strings.Contains(got, "Type a branch name. Existing branches are reused.") || strings.Contains(got, "Enter") {
		t.Fatalf("empty add panel = %q", got)
	}
	model.actionTextInput.SetValue("fresh")
	if got := promptText(model, false); !strings.Contains(got, "Checking branches…") {
		t.Fatalf("loading add panel = %q", got)
	}
	// A failed load says so instead of loading forever.
	updated, _ := model.Update(branchRefsMsg{request: model.branchRefsRequest, err: errors.New("not a git repository")})
	if got := promptText(updated.(dashboardModel), false); !strings.Contains(got, " Could not read branches: not a git repository") || strings.Contains(got, "Checking") {
		t.Fatalf("failed add panel = %q", got)
	}
	model.branchRefs = &branchRefs{local: map[string]bool{"main": true, "old": true}, remote: map[string][]string{"theirs": {"origin/theirs"}}, checkedOut: map[string]string{"busy": "/wt/busy"}, remotes: []string{"origin"}, base: "main", root: "/src/repo"}
	for value, want := range map[string][]string{
		"fresh":  {"fresh", "/src/repo__worktrees/fresh", " New branch from main"},
		"old":    {"/src/repo__worktrees/old", " Uses the existing local branch"},
		"theirs": {" Tracks origin/theirs", "Creates a local branch from it."},
		"busy":   {"/wt/busy", " Already checked out here"},
	} {
		model.actionTextInput.SetValue(value)
		panel := promptText(model, false)
		for _, text := range want {
			if !strings.Contains(panel, text) {
				t.Errorf("panel for %q = %q, missing %q", value, panel, text)
			}
		}
		if strings.Contains(panel, "Enter") {
			t.Errorf("panel for %q repeats footer keys: %q", value, panel)
		}
		view := ansi.Strip(model.View())
		if !strings.Contains(view, "Add worktree") || !strings.Contains(view, want[0]) {
			t.Errorf("view for %q lacks the add panel:\n%s", value, view)
		}
	}
	for _, verb := range []struct{ value, want string }{{"fresh", "Enter Create"}, {"busy", "Enter Open"}} {
		model.actionTextInput.SetValue(verb.value)
		if footer := ansi.Strip(model.renderFooter(120)); !strings.Contains(footer, verb.want) || ansi.StringWidth(footer) != 120 {
			t.Errorf("footer for %q = %q", verb.value, footer)
		}
	}
	// When Enter would fail, the panel says why and shows no path.
	for value, want := range map[string]string{
		"my branch":     " Not a valid branch name",
		"origin/theirs": " origin/theirs names a remote branch; type theirs to use it",
	} {
		model.actionTextInput.SetValue(value)
		if panel := promptText(model, false); !strings.Contains(panel, want) || strings.Contains(panel, "repo__worktrees") {
			t.Errorf("panel for %q = %q, want %q", value, panel, want)
		}
	}
	for base, want := range map[string]string{"develop": " Default branch develop is not available locally", "": " No local default branch"} {
		refs := *model.branchRefs
		refs.base = base
		noBase := model
		noBase.branchRefs = &refs
		noBase.actionTextInput.SetValue("fresh")
		if panel := promptText(noBase, false); !strings.Contains(panel, want) {
			t.Errorf("panel with base %q = %q, want %q", base, panel, want)
		}
	}
	// Compact form keeps only the status for short panels.
	model.actionTextInput.SetValue("fresh")
	if got := model.promptLines(true); len(got) != 1 || !strings.Contains(ansi.Strip(got[0]), "New branch from main") {
		t.Errorf("compact add panel = %q", got)
	}

	// A stale lookup from an earlier prompt must not replace the hint snapshot.
	model.branchRefsRequest = 2
	updated, _ = model.Update(branchRefsMsg{request: 1, refs: branchRefs{base: "stale"}})
	if got := updated.(dashboardModel).branchRefs.base; got != "main" {
		t.Errorf("stale refs applied: base = %q", got)
	}
	updated, _ = model.Update(branchRefsMsg{request: 2, refs: branchRefs{base: "trunk"}})
	if got := updated.(dashboardModel).branchRefs.base; got != "trunk" {
		t.Errorf("current refs ignored: base = %q", got)
	}
}

func TestRemoveWorktreeWithChanges(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// A tmux server with no panes, so no pane blocks the removal.
	writeFakeTmux(t, "exit 0\n")
	invalidateTmuxPaneCache()
	t.Setenv("TMUX", "/tmp/tmux-test,1,0")
	parent := t.TempDir()
	repo := filepath.Join(parent, "repo")
	git := func(args ...string) string {
		t.Helper()
		output, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
		return string(output)
	}
	git("init", "-q", "-b", "main", repo)
	git("-C", repo, "config", "user.name", "Test")
	git("-C", repo, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("-C", repo, "add", "a.txt")
	git("-C", repo, "commit", "-qm", "base")
	dirtyWorktree := func(branch string) string {
		t.Helper()
		path := filepath.Join(parent, branch)
		git("-C", repo, "worktree", "add", "-qb", branch, path)
		for name, data := range map[string]string{"a.txt": "changed\n", "new.txt": "new\n"} {
			if err := os.WriteFile(filepath.Join(path, name), []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return path
	}
	exists := func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	}

	stash := dirtyWorktree("stash-me")
	if changes, err := readWorktreeChanges(context.Background(), stash); err != nil || changes != (worktreeChanges{changed: 1, untracked: 1}) {
		t.Fatalf("changes = %#v, %v", changes, err)
	}
	err := removeWorktree(repo, stash, removeClean)
	if dirty := (dirtyWorktreeError{}); !errors.As(err, &dirty) || err.Error() != "worktree has 1 changed file and 1 untracked file" || !exists(stash) {
		t.Fatalf("plain removal of dirty worktree = %v, exists=%v", err, exists(stash))
	}
	if err := removeWorktree(repo, stash, removeStash); err != nil || exists(stash) {
		t.Fatalf("stash removal = %v, exists=%v", err, exists(stash))
	}
	if list := git("-C", repo, "stash", "list"); !strings.Contains(list, "jumpmux: removed worktree stash-me") {
		t.Fatalf("stash list = %q", list)
	}
	if files := git("-C", repo, "show", "--name-only", "--format=", "stash@{0}^3"); !strings.Contains(files, "new.txt") {
		t.Fatalf("stash lost the untracked file: %q", files)
	}

	discard := dirtyWorktree("discard-me")
	if err := removeWorktree(repo, discard, removeDiscard); err != nil || exists(discard) {
		t.Fatalf("discard removal = %v, exists=%v", err, exists(discard))
	}

	// Stash mode on a clean worktree removes it without an empty stash entry.
	clean := filepath.Join(parent, "clean")
	git("-C", repo, "worktree", "add", "-qb", "clean", clean)
	if err := removeWorktree(repo, clean, removeStash); err != nil || exists(clean) {
		t.Fatalf("clean stash removal = %v, exists=%v", err, exists(clean))
	}
	if list := strings.TrimSpace(git("-C", repo, "stash", "list")); strings.Count(list, "\n") != 0 {
		t.Fatalf("stash list = %q, want one entry", list)
	}

	for changes, want := range map[worktreeChanges]string{
		{changed: 2}:               "2 changed files",
		{untracked: 3}:             "3 untracked files",
		{changed: 2, untracked: 1}: "2 changed files and 1 untracked file",
	} {
		if got := changes.String(); got != want {
			t.Errorf("%#v = %q, want %q", changes, got, want)
		}
	}
}

func TestDirtyRemovalConfirmation(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height, model.tab = 100, 20, tabWorktrees
	model.worktrees = []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main", primary: true}, {kind: "worktree", target: "/feature", cwd: "/feature", branch: "feature"}}
	model.index = 1
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if model = updated.(dashboardModel); model.action != actionRemoveWorktree || command == nil {
		t.Fatalf("r did not start the change check: %v", model.action)
	}
	press := func(model dashboardModel, key tea.KeyMsg) (dashboardModel, tea.Cmd) {
		updated, command := model.Update(key)
		return updated.(dashboardModel), command
	}
	enter, stash, discard := tea.KeyMsg{Type: tea.KeyEnter}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}}

	// While Git runs, no key removes anything.
	if !strings.Contains(promptText(model, false), "Checking for changes and open panes…") {
		t.Fatalf("loading preview = %q", promptText(model, false))
	}
	for _, key := range []tea.KeyMsg{enter, stash, discard} {
		if next, command := press(model, key); next.action != actionRemoveWorktree || command != nil {
			t.Fatalf("%q acted before changes loaded", key.String())
		}
	}

	// A result from an earlier confirmation is ignored.
	updated, _ = model.Update(removeChangesMsg{request: model.removeChangesRequest - 1})
	if updated.(dashboardModel).removeChanges != nil {
		t.Fatal("stale change check applied")
	}
	updated, _ = model.Update(removeChangesMsg{request: model.removeChangesRequest, changes: worktreeChanges{changed: 2, untracked: 1}})
	model = updated.(dashboardModel)
	preview := ansi.Strip(model.renderPreview(model.width))
	footer := ansi.Strip(model.renderFooter(model.width))
	// The panel explains each choice by the footer's labels, without the keys.
	for _, want := range []string{" Has 2 changed files and 1 untracked file", "The branch stays.", "Stash    Keeps changes in git stash list", "Discard  Deletes changes; cannot be undone"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("dirty preview missing %q:\n%s", want, preview)
		}
	}
	if strings.Contains(promptText(model, false), "Esc") {
		t.Fatalf("dirty preview repeats footer keys:\n%s", preview)
	}
	if !strings.Contains(footer, "s Stash F Discard Esc Cancel") || strings.Contains(footer, "Enter") {
		t.Fatalf("dirty footer = %q", footer)
	}
	// Habitual Enter and lowercase f never discard work.
	for _, key := range []tea.KeyMsg{enter, {Type: tea.KeyRunes, Runes: []rune{'f'}}} {
		if next, command := press(model, key); next.action != actionRemoveWorktree || command != nil {
			t.Fatalf("%q acted on a dirty worktree", key.String())
		}
	}
	// Each key must reach its own mode: a swap would turn "keep my changes"
	// into "delete my changes". /repo is not a repository, so the removal fails
	// before touching anything, but the notice already names the mode.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for key, want := range map[string]string{
		"s": "Removed worktree feature; any changes are in git stash list",
		"F": "Removed worktree feature and discarded its changes",
	} {
		next, command := press(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		if next.action != actionRunning || command == nil {
			t.Fatalf("%q did not start removal", key)
		}
		if msg, ok := command().(worktreeActionMsg); !ok || msg.notice != want || msg.err == nil {
			t.Fatalf("%q dispatched %#v, want notice %q", key, msg, want)
		}
	}

	// When Git cannot read the changes, only stash and discard remain.
	model.removeChanges, model.removeChangesErr = &worktreeChanges{}, errors.New("boom")
	if preview := promptText(model, false); !strings.Contains(preview, " Could not check the worktree: boom") || !strings.Contains(preview, "Discard  Deletes changes") {
		t.Fatalf("error preview = %q", preview)
	}
	if next, command := press(model, enter); next.action != actionRemoveWorktree || command != nil {
		t.Fatal("Enter removed a worktree with unknown changes")
	}
	if next, command := press(model, discard); next.action != actionRunning || command == nil {
		t.Fatal("F did not start removal after a failed change check")
	}

	// An open pane blocks every key but Esc, even with changes to stash.
	model.removeChanges, model.removeChangesErr = &worktreeChanges{changed: 1}, nil
	model.removePanes = []tmuxPane{
		{ID: "%3", SessionName: "dev", WindowIndex: "3", PaneIndex: "1", WindowName: "editor"},
		{ID: "%4", SessionName: "dev", WindowIndex: "3", PaneIndex: "2", WindowName: "editor"},
	}
	preview = promptText(model, false)
	if want := " Open in 2 tmux panes\n  dev:3.1 (editor)\n  dev:3.2 (editor)\n  Close them, then press r again."; !strings.Contains(preview, want) || strings.Contains(preview, "Esc") {
		t.Fatalf("pane-blocked preview = %q, want %q and no keys", preview, want)
	}
	if footer := ansi.Strip(model.renderFooter(model.width)); !strings.Contains(footer, "Remove feature?  Esc Cancel") || strings.Contains(footer, "Stash") || strings.Contains(footer, "Enter") || strings.Contains(footer, "Close") {
		t.Fatalf("pane-blocked footer = %q", footer)
	}
	for _, key := range []tea.KeyMsg{enter, stash, discard} {
		if next, command := press(model, key); next.action != actionRemoveWorktree || command != nil {
			t.Fatalf("%q acted while a pane was open", key.String())
		}
	}
	if next, _ := press(model, tea.KeyMsg{Type: tea.KeyEsc}); next.action != actionNone {
		t.Fatal("Esc did not cancel a pane-blocked removal")
	}

	// Without window indices, a pane falls back to its ID.
	if got := describePanes([]tmuxPane{{ID: "%9"}}); got != "tmux pane %9" {
		t.Fatalf("bare pane = %q", got)
	}
}

func TestWorktreeRemoveFlagUsage(t *testing.T) {
	for _, args := range [][]string{{"remove", "x", "--bogus"}, {"remove", "x", "--stash", "--force"}, {"rebase", "x", "--stash"}} {
		if err := runWorktreeCommand(t.TempDir(), args); err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Errorf("%q error = %v", args, err)
		}
	}
	if err := runWorktreeCommand(t.TempDir(), []string{"remove", "x", "--bogus"}); err == nil || !strings.Contains(err.Error(), "[--stash|--force]") {
		t.Errorf("remove usage = %v", err)
	}
}

// removalTestRepo is a repository with a committed a.txt and a tmux server
// with no panes, so no pane blocks a removal.
func removalTestRepo(t *testing.T) (parent, repo string, git func(...string) string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeFakeTmux(t, "exit 0\n")
	invalidateTmuxPaneCache()
	t.Setenv("TMUX", "/tmp/tmux-test,1,0")
	parent = t.TempDir()
	repo = filepath.Join(parent, "repo")
	git = func(args ...string) string {
		t.Helper()
		output, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
		return string(output)
	}
	git("init", "-q", "-b", "main", repo)
	git("-C", repo, "config", "user.name", "Test")
	git("-C", repo, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("-C", repo, "add", "a.txt")
	git("-C", repo, "commit", "-qm", "base")
	return parent, repo, git
}

func stashCount(t *testing.T, repo string) int {
	t.Helper()
	output, err := exec.Command("git", "-C", repo, "stash", "list").Output()
	if err != nil {
		t.Fatal(err)
	}
	if list := strings.TrimSpace(string(output)); list != "" {
		return strings.Count(list, "\n") + 1
	}
	return 0
}

func TestCLIRemoveFlagsMapToModes(t *testing.T) {
	parent, repo, git := removalTestRepo(t)
	for _, test := range []struct {
		flag   string
		stash  int
		branch string
	}{{"--stash", 1, "stash-flag"}, {"--force", 1, "force-flag"}} {
		path := filepath.Join(parent, test.branch)
		git("-C", repo, "worktree", "add", "-qb", test.branch, path)
		if err := os.WriteFile(filepath.Join(path, "a.txt"), []byte("changed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// Without a flag, a dirty worktree is refused and the error names both flags.
		if err := runWorktreeCommand(repo, []string{"remove", path, "--yes"}); err == nil || !strings.Contains(err.Error(), "--stash") || !strings.Contains(err.Error(), "--force") {
			t.Fatalf("flagless dirty removal = %v", err)
		}
		if err := runWorktreeCommand(repo, []string{"remove", path, test.flag, "--yes"}); err != nil {
			t.Fatalf("remove %s = %v", test.flag, err)
		}
		// --stash adds the one stash entry; --force must not add another.
		if got := stashCount(t, repo); got != test.stash {
			t.Fatalf("after %s, stash entries = %d, want %d", test.flag, got, test.stash)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s left the worktree: %v", test.flag, err)
		}
	}
}

func TestRemoveWorktreeEdgeCases(t *testing.T) {
	parent, repo, git := removalTestRepo(t)

	// A worktree whose directory is gone has nothing to lose, so a plain
	// removal clears its record, and the dashboard check reports it clean.
	missing := filepath.Join(parent, "missing")
	git("-C", repo, "worktree", "add", "-qb", "missing", missing)
	if err := os.RemoveAll(missing); err != nil {
		t.Fatal(err)
	}
	if changes, err := readWorktreeChanges(context.Background(), missing); err != nil || changes.dirty() {
		t.Fatalf("missing worktree changes = %#v, %v", changes, err)
	}
	if err := removeWorktree(repo, missing, removeClean); err != nil {
		t.Fatalf("missing worktree removal = %v", err)
	}
	if list := git("-C", repo, "worktree", "list"); strings.Contains(list, missing) {
		t.Fatalf("missing worktree record stayed:\n%s", list)
	}

	// A nested worktree without its .git file would make Git read the main
	// repository; stashing there would take the main worktree's changes.
	nested := filepath.Join(repo, "nested")
	git("-C", repo, "worktree", "add", "-qb", "nested", nested)
	if err := os.Remove(filepath.Join(nested, ".git")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("main change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeWorktree(repo, nested, removeStash); err == nil || !strings.Contains(err.Error(), "not a Git worktree") {
		t.Fatalf("stash of a broken nested worktree = %v", err)
	}
	if got := stashCount(t, repo); got != 0 {
		t.Fatalf("broken nested worktree stashed %d entries from the main repository", got)
	}
	if data, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(data) != "main change\n" {
		t.Fatalf("main worktree change lost: %q", data)
	}

	// A locked worktree is refused in every mode, before any stash.
	locked := filepath.Join(parent, "locked")
	git("-C", repo, "worktree", "add", "-qb", "locked", locked)
	git("-C", repo, "worktree", "lock", locked)
	if err := os.WriteFile(filepath.Join(locked, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []removeMode{removeClean, removeStash, removeDiscard} {
		if err := removeWorktree(repo, locked, mode); err == nil || !strings.Contains(err.Error(), "locked") {
			t.Fatalf("locked removal in mode %d = %v", mode, err)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(locked, "a.txt")); string(data) != "changed\n" || stashCount(t, repo) != 0 {
		t.Fatalf("locked worktree lost its change: %q, stash entries %d", data, stashCount(t, repo))
	}
}

func TestStashedRemovalFailureNamesTheStash(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	parent, repo, git := removalTestRepo(t)
	path := filepath.Join(parent, "stuck")
	git("-C", repo, "worktree", "add", "-qb", "stuck", path)
	// A read-only tracked folder survives the stash but blocks deletion.
	sub := filepath.Join(path, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("-C", path, "add", "sub/b.txt")
	git("-C", path, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-qm", "sub")
	if err := os.WriteFile(filepath.Join(path, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o755) })

	err := removeWorktree(repo, path, removeStash)
	if err == nil || !strings.Contains(err.Error(), `changes are in the stash as "jumpmux: removed worktree stuck"`) {
		t.Fatalf("failed removal after stash = %v", err)
	}
	if stashCount(t, repo) != 1 {
		t.Fatal("the stash named in the error does not exist")
	}
}

func TestAddWorktreeOpensExistingWorktreePane(t *testing.T) {
	_, repo, git := removalTestRepo(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	busy := filepath.Join(filepath.Dir(repo), "busy")
	git("-C", repo, "worktree", "add", "-qb", "busy", busy)
	log := filepath.Join(t.TempDir(), "tmux.log")
	// One pane in a window jumpmux opened for busy; every other call is logged.
	writeFakeTmux(t, `case "$1" in
list-panes) printf '%%7\0374321\037%s\037sh\037$1\037dev\037@2\037busy\037sh\037%s\0372\0370\036\n' "$FAKE_WT" "$FAKE_WT" ;;
display-message) echo %7 ;;
*) echo "$@" >>"$FAKE_LOG" ;;
esac
`)
	t.Setenv("FAKE_WT", busy)
	t.Setenv("FAKE_LOG", log)
	invalidateTmuxPaneCache()

	added, plan, err := addWorktree(repo, "busy", true)
	if err != nil || plan.source != branchCheckedOut {
		t.Fatalf("add = %#v, %v", plan, err)
	}
	if err := openAddedWorktree(repo, added, plan); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "select-pane -t %7") || strings.Contains(string(calls), "new-window") {
		t.Fatalf("existing worktree did not reuse its pane:\n%s", calls)
	}
}

func TestNewOnlyAddRefusesExistingBranch(t *testing.T) {
	repo, _ := branchTestRepo(t)
	// Agents pass reuse=false: a name taken after they chose it must fail,
	// not join that branch's worktree or check out a remote branch.
	for _, branch := range []string{"local", "busy"} {
		if _, _, err := addWorktree(repo, branch, false); err == nil {
			t.Errorf("new-only add reused %q", branch)
		}
	}
	if _, _, err := addWorktree(repo, "origin/remote-only", true); err == nil || err.Error() != "origin/remote-only names a remote branch; type remote-only to use it" {
		t.Errorf("remote-prefixed add = %v", err)
	}
}

func TestValidBranchName(t *testing.T) {
	for _, name := range []string{"feat/x", "fix-1", "a.b", "user/feat/x.y"} {
		if !validBranchName(name) {
			t.Errorf("validBranchName(%q) = false", name)
		}
	}
	for _, name := range []string{"", "-x", "my branch", "a..b", "a@{b", "a//b", "/a", "a/", "a.", ".a", "a/.b", "a.lock", "a~b", "a^b", "a:b", "a?b", "a*b", "a[b", "a\\b", "a\tb"} {
		if validBranchName(name) {
			t.Errorf("validBranchName(%q) = true", name)
		}
		// Git must reject it too, or the panel would flag a usable name.
		if name != "" && exec.Command("git", "check-ref-format", "--branch", name).Run() == nil {
			t.Errorf("git accepts %q, so the panel would wrongly flag it", name)
		}
	}
}
