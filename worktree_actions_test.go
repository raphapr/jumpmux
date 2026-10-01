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
	if _, err := addWorktree(repo, "feature"); err == nil || !strings.Contains(err.Error(), "local default branch") {
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
	created, err := addWorktree(repo, "feature")
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
	if _, err := addWorktree(repo, "feature"); err == nil || !strings.Contains(err.Error(), "not available locally") {
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
printf '%%7\0374321\037%s\037Pi\037$1\037dev\037@2\037feature\037pi\037%s\036\n' "$FAKE_PANE_PATH" "$FAKE_WORKTREE"
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", "/tmp/tmux-test,1,0")
	worktree := filepath.Join(parent, "feature")
	for _, scenario := range []struct{ panePath, marker string }{{worktree, ""}, {parent, worktree}} {
		t.Setenv("FAKE_PANE_PATH", scenario.panePath)
		t.Setenv("FAKE_WORKTREE", scenario.marker)
		if err := removeWorktree(repo, worktree); err == nil || !strings.Contains(err.Error(), "tmux pane %7") {
			t.Fatalf("worktree in use was removed: %v", err)
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
	if err := removeWorktree(worktree, worktree); err == nil || !strings.Contains(err.Error(), "current worktree") {
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
	if err := removeWorktree(repo, worktree); err == nil {
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
	created, err := addWorktree(repo, "feature/test")
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
	if err := removeWorktree(repo, worktree); err != nil {
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
	for _, expected := range []string{"a Add", "r Remove", "t Theme"} {
		if !strings.Contains(footer, expected) {
			t.Fatalf("worktree footer missing %q: %s", expected, footer)
		}
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
			model.width, model.height, model.action = 100, 20, test.action
			model.actionTarget = item{kind: "worktree", title: "dev", branch: "feature", cwd: "/feature"}

			if preview := strings.Join(model.removePreviewLines(), "\n"); !strings.Contains(preview, "Enter "+test.label+"    Esc Cancel") {
				t.Fatalf("confirmation preview = %q", preview)
			}
			if footer := ansi.Strip(model.renderFooter(model.width)); !strings.Contains(footer, "Enter "+test.label) {
				t.Fatalf("confirmation footer = %q", footer)
			}
			updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(test.key)})
			model = updated.(dashboardModel)
			if model.action != test.action || command != nil {
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
	model = updated.(dashboardModel)
	preview := ansi.Strip(model.renderPreview(model.width))
	for _, expected := range []string{"Will remove worktree /feature.", "The branch stays.", "Enter Remove    Esc Cancel"} {
		if !strings.Contains(preview, expected) {
			t.Fatalf("removal preview missing %q:\n%s", expected, preview)
		}
	}
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if updated.(dashboardModel).action != actionRemoveWorktree || command != nil {
		t.Fatal("r confirmed worktree removal")
	}
	updated, command = updated.(dashboardModel).Update(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.(dashboardModel).action != actionRunning || command == nil {
		t.Fatal("Enter did not confirm removal")
	}
	model.action = actionRemoveWorktree
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.(dashboardModel).action != actionNone {
		t.Fatal("Esc did not cancel removal")
	}
}
