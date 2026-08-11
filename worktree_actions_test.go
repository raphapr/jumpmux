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
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if command == nil || updated.(dashboardModel).action != actionRunning {
		t.Fatal("removal command did not start")
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
	if err := addWorktree(repo, "feature/test", backendGit); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(parent, "repo__worktrees", "feature", "test")
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("created worktree: %v", err)
	}
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

func TestWorktrunkActionCommands(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.com"}, {"commit", "--allow-empty", "-qm", "base"}} {
		if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	bin, log := t.TempDir(), filepath.Join(t.TempDir(), "wt.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >>\"$WT_LOG\"\n"
	if err := os.WriteFile(filepath.Join(bin, "wt"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WT_LOG", log)
	if err := addWorktree(repo, "feature", backendWT); err != nil {
		t.Fatal(err)
	}
	if err := removeWorktree(repo, filepath.Join(filepath.Dir(repo), "feature"), backendWT); err != nil {
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
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	model = updated.(dashboardModel)
	if model.action != actionNone {
		t.Fatal("remove cancellation did not close confirmation")
	}
	model.tab, model.index = 1, 0
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = updated.(dashboardModel)
	if model.action != actionNone || model.err == nil {
		t.Fatal("primary worktree removal was not rejected")
	}
	model.tab, model.err = 0, nil
	footer = ansi.Strip(model.renderFooter(120))
	if !strings.Contains(footer, "s Scope (all)") || !strings.Contains(footer, "t Theme") || strings.Contains(footer, "Refresh") {
		t.Fatalf("agent footer = %s", footer)
	}
}

func TestDashboardMutationReloadsConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.com"}, {"commit", "--allow-empty", "-qm", "base"}} {
		if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(path, []byte("worktree_backend = invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	model := newDashboard(repo)
	model.width, model.height, model.tab = 120, 30, 1
	model.action = actionAddWorktree
	model.actionTextInput.SetValue("feature")
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.(dashboardModel).action != actionRunning || command == nil {
		t.Fatal("add action did not start")
	}
	if err := command().(worktreeActionMsg).err; err == nil || !strings.Contains(err.Error(), "must be a quoted TOML string") {
		t.Fatalf("invalid config silently selected a backend: %v", err)
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
	for _, expected := range []string{"Branch: feature", "Path:   /feature", "Native Git removes the worktree and keeps its branch.", "y Remove    n or Esc Cancel"} {
		if !strings.Contains(preview, expected) {
			t.Fatalf("removal preview missing %q:\n%s", expected, preview)
		}
	}
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})
	if updated.(dashboardModel).action != actionRunning || command == nil {
		t.Fatal("uppercase Y did not confirm removal")
	}
	model.action = actionRemoveWorktree
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	if updated.(dashboardModel).action != actionNone {
		t.Fatal("uppercase N did not cancel removal")
	}
}
