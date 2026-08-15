package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestWorktreeBackendConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if backend, err := loadWorktreeBackend(); err != nil || backend != backendAuto {
		t.Fatalf("default backend = %q err=%v", backend, err)
	}
	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "config.toml" {
		t.Fatalf("config path = %s", path)
	}
	if err := atomicWrite(path, []byte("# jumpmux\nworktree_backend = \"git\" # native Git\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if backend, err := loadWorktreeBackend(); err != nil || backend != backendGit {
		t.Fatalf("configured backend = %q err=%v", backend, err)
	}
	if err := atomicWrite(path, []byte("worktree_backend = 'wt'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if backend, err := loadWorktreeBackend(); err != nil || backend != backendWT {
		t.Fatalf("literal-string backend = %q err=%v", backend, err)
	}
	if err := atomicWrite(path, []byte("worktree_backend = invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadWorktreeBackend(); err == nil {
		t.Fatal("unquoted TOML backend was accepted")
	}
	if err := atomicWrite(path, []byte("worktree_backend = \"invalid\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadWorktreeBackend(); err == nil {
		t.Fatal("invalid backend was accepted")
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
		if err := removeWorktree(repo, worktree, backendGit); err == nil || !strings.Contains(err.Error(), "tmux pane %7") {
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
	if err := removeWorktree(worktree, worktree, backendGit); err == nil || !strings.Contains(err.Error(), "current worktree") {
		t.Fatalf("launch worktree removal = %v", err)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("current worktree was mutated: %v", err)
	}
}

func TestCleanupPrunableWorktreeRevalidatesAndPrunes(t *testing.T) {
	parent := t.TempDir()
	repo, stale := filepath.Join(parent, "repo"), filepath.Join(parent, "stale")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "config", "user.name", "Test"}, {"-C", repo, "config", "user.email", "test@example.com"}, {"-C", repo, "commit", "--allow-empty", "-qm", "base"}, {"-C", repo, "worktree", "add", "-qb", "stale", stale}} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	if err := os.RemoveAll(stale); err != nil {
		t.Fatal(err)
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
	for _, candidate := range items {
		if samePath(candidate.cwd, stale) {
			t.Fatalf("stale worktree record remained: %#v", candidate)
		}
	}
	if err := cleanupPrunableWorktree(repo, selected); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale cleanup was not revalidated: %v", err)
	}
}

func TestRemovalUsesConfirmedBackend(t *testing.T) {
	parent := t.TempDir()
	repo, worktree := filepath.Join(parent, "repo"), filepath.Join(parent, "feature")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "config", "user.name", "Test"}, {"-C", repo, "config", "user.email", "test@example.com"}, {"-C", repo, "commit", "--allow-empty", "-qm", "base"}, {"-C", repo, "worktree", "add", "-qb", "feature", worktree}} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	config, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(config, []byte("worktree_backend = \"git\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	wtLog := filepath.Join(t.TempDir(), "wt.log")
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "wt"), []byte("#!/bin/sh\necho called >>\"$WT_LOG\"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WT_LOG", wtLog)

	model := newDashboard(repo)
	model.width, model.height, model.tab = 100, 20, 1
	model.worktrees = []item{{kind: "worktree", target: repo, cwd: repo, branch: "main"}, {kind: "worktree", target: worktree, cwd: worktree, branch: "feature"}}
	model.index = 1
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = updated.(dashboardModel)
	if model.actionBackend != backendGit {
		t.Fatalf("confirmed backend = %q", model.actionBackend)
	}
	if err := atomicWrite(config, []byte("worktree_backend = \"wt\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if command == nil || updated.(dashboardModel).action != actionRunning {
		t.Fatal("Enter did not start removal")
	}
	message := command().(worktreeActionMsg)
	if message.err != nil {
		t.Fatal(message.err)
	}
	if _, err := os.Stat(worktree); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("native Git did not remove worktree: %v", err)
	}
	if _, err := os.Stat(wtLog); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Worktrunk ran after Git confirmation: %v", err)
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
	created, err := addWorktree(repo, "feature/test", backendGit)
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
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\necho 'error connecting to /tmp/tmux-1001/default (No such file or directory)' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := removeWorktree(repo, worktree, backendGit); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(worktree); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed worktree still exists: %v", err)
	}
	if output, err := exec.Command("git", "-C", repo, "show-ref", "--verify", "refs/heads/feature/test").CombinedOutput(); err != nil {
		t.Fatalf("native removal deleted branch: %v\n%s", err, output)
	}
}

func TestWorktrunkRemovalRefusesDirtyWorktree(t *testing.T) {
	parent := t.TempDir()
	repo, worktree := filepath.Join(parent, "repo"), filepath.Join(parent, "feature")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "config", "user.name", "Test"}, {"-C", repo, "config", "user.email", "test@example.com"}, {"-C", repo, "commit", "--allow-empty", "-qm", "base"}, {"-C", repo, "worktree", "add", "-qb", "feature", worktree}} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(worktree, "dirty"), []byte("dirty"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin, log := t.TempDir(), filepath.Join(t.TempDir(), "wt.log")
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "wt"), []byte("#!/bin/sh\necho called >>\"$WT_LOG\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WT_LOG", log)
	if err := removeWorktree(repo, worktree, backendWT); err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("dirty worktree removal = %v", err)
	}
	if _, err := os.Stat(log); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wt was invoked for dirty worktree: %v", err)
	}
}

func TestWorktrunkActionCommands(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.com"}, {"commit", "--allow-empty", "-qm", "base"}} {
		if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	worktree := filepath.Join(filepath.Dir(repo), "feature")
	if output, err := exec.Command("git", "-C", repo, "worktree", "add", "-qb", "feature", worktree).CombinedOutput(); err != nil {
		t.Fatalf("add worktree: %v\n%s", err, output)
	}
	bin, log := t.TempDir(), filepath.Join(t.TempDir(), "wt.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >>\"$WT_LOG\"\n"
	if err := os.WriteFile(filepath.Join(bin, "wt"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\necho 'error connecting to /tmp/tmux-1001/default (No such file or directory)' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WT_LOG", log)
	created, err := addWorktree(repo, "feature", backendWT)
	if err != nil {
		t.Fatal(err)
	}
	if created.cwd != worktree || created.branch != "feature" {
		t.Fatalf("created worktree = %#v", created)
	}
	if err := removeWorktree(repo, worktree, backendWT); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	commands := string(data)
	if !strings.Contains(commands, "-C "+repo+" switch --create feature --no-cd --format=json") || !strings.Contains(commands, "-y -C "+repo+" remove ") || !strings.Contains(commands, "--foreground") {
		t.Fatalf("worktrunk commands:\n%s", commands)
	}
}

func TestWorktrunkRebaseAndMergeActions(t *testing.T) {
	parent := t.TempDir()
	repo, worktree := filepath.Join(parent, "repo"), filepath.Join(parent, "feature")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "config", "user.name", "Test"}, {"-C", repo, "config", "user.email", "test@example.com"}, {"-C", repo, "commit", "--allow-empty", "-qm", "base"}, {"-C", repo, "worktree", "add", "-qb", "feature", worktree}} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	bin, log := t.TempDir(), filepath.Join(t.TempDir(), "wt.log")
	if err := os.WriteFile(filepath.Join(bin, "wt"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >>\"$WT_LOG\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WT_LOG", log)
	if err := updateWorktree(worktree, "feature", "rebase", false, backendWT); err != nil {
		t.Fatal(err)
	}
	if err := updateWorktree(worktree, "feature", "merge", false, backendWT); err != nil {
		t.Fatal(err)
	}
	if err := updateWorktree(worktree, "feature", "merge", true, backendWT); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	commands := string(data)
	for _, expected := range []string{"-C " + worktree + " step rebase --format=json", "-C " + worktree + " merge --no-remove --format=json", "-C " + worktree + " merge --no-remove --no-squash --format=json"} {
		if !strings.Contains(commands, expected) {
			t.Fatalf("Worktrunk log missing %q:\n%s", expected, commands)
		}
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
	if err := updateWorktree(worktree, "feature", "rebase", false, backendGit); err != nil {
		t.Fatal(err)
	}
	if err := updateWorktree(worktree, "feature", "merge", false, backendGit); err != nil {
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
	if err := updateWorktree(worktree, "feature", "merge", false, backendGit); err == nil || !strings.Contains(err.Error(), "primary worktree is not on main") {
		t.Fatalf("merge from wrong primary branch = %v", err)
	}
	head, err := exec.Command("git", "-C", repo, "branch", "--show-current").Output()
	if err != nil || strings.TrimSpace(string(head)) != "other" {
		t.Fatalf("primary branch changed: %q, %v", head, err)
	}
}

func TestOpenPullRequestCommand(t *testing.T) {
	repo, bin, log := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "gh.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >\"$GH_LOG\"\n"
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
	model.worktrees = []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main"}, {kind: "worktree", target: "/feature", cwd: "/feature", branch: "feature"}}
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
	model.actionBackend = backendWT
	preview := ansi.Strip(model.renderPreview(100))
	if model.action != actionMergeWorktree || !strings.Contains(preview, "Squash: on") || !strings.Contains(preview, "May commit changes; keeps worktree.") {
		t.Fatalf("merge confirmation = action %v\n%s", model.action, preview)
	}
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	model = updated.(dashboardModel)
	preview = ansi.Strip(model.renderPreview(100))
	if command != nil || !model.actionNoSquash || !strings.Contains(preview, "Squash: off") {
		t.Fatalf("no-squash confirmation = action %v noSquash=%v\n%s", model.action, model.actionNoSquash, preview)
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
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(path, []byte("worktree_backend = \"git\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	model := newDashboard("/repo")
	model.width, model.height, model.tab = 100, 20, 1
	model.worktrees = []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main"}, {kind: "worktree", target: "/feature", cwd: "/feature", branch: "feature"}}
	model.index = 1
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = updated.(dashboardModel)
	preview := ansi.Strip(model.renderPreview(model.width))
	for _, expected := range []string{"Branch: feature", "Path:   /feature", "Native Git removes the worktree and keeps its branch.", "Enter Remove    Esc Cancel"} {
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
