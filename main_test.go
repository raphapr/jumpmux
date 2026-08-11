package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestParsers(t *testing.T) {
	t.Run("git worktrees", func(t *testing.T) {
		data := []byte("worktree /repo\x00HEAD abc\x00branch refs/heads/main\x00\x00worktree /repo-feature\x00HEAD def\x00detached")
		got := parseWorktrees(data)
		if len(got) != 2 || got[0].branch != "main" || got[1].branch != "detached" {
			t.Fatalf("unexpected worktrees: %#v", got)
		}
	})

	t.Run("pull requests", func(t *testing.T) {
		got := parsePullRequests([]byte(`[{"number":42,"state":"OPEN","isDraft":false,"headRefName":"feature","statusCheckRollup":[{"status":"COMPLETED","conclusion":"SUCCESS"},{"status":"COMPLETED","conclusion":"FAILURE"}]}]`))
		if got["feature"].Number != 42 || got["feature"].State != "OPEN" || got["feature"].Check != checkFailure {
			t.Fatalf("unexpected pull requests: %#v", got)
		}
	})
}

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

func TestCommandTimeoutKillsDescendants(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := boundedCommand(ctx, "sh", "-c", "sleep 10 & wait").Run(); err == nil {
		t.Fatal("timed command unexpectedly succeeded")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("timed command took %s", elapsed)
	}
}

func TestUntrackedSpecialFileDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := exec.Command("mkfifo", fifo).Run(); err != nil {
		t.Skip("mkfifo unavailable")
	}
	if err := exec.Command("git", "init", "-q", dir).Run(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		untrackedStats(dir)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("special file scan blocked")
	}
}

func TestWorktrunkDefaultBranch(t *testing.T) {
	bin := t.TempDir()
	script := `#!/bin/sh
printf '%s\n' '{"schema":2,"repo":{"default_branch":"develop"},"items":[]}'
`
	if err := os.WriteFile(filepath.Join(bin, "wt"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if got := worktrunkDefaultBranch(t.TempDir()); got != "develop" {
		t.Fatalf("default branch = %q, want develop", got)
	}
}

func TestPIExtensionSetup(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", root)
	path, err := setupPIExtension()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(piExtension) {
		t.Fatal("installed extension differs from embedded source")
	}
}

func TestAgentPreviewCapturesPaneHistory(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf '1\\t4\\n\\033]0;owned\\007first\\033[2J line\\n\\033[31msecond line\\033[0m\\n\\033[90m────────────────\\033[0m\\ncustom input> \\033[7m \\033[0m\\n\\033[90m────────────────\\033[0m\\ncustom footer\\n'\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	applyColorScheme(schemeDefault)
	message := loadPreview(item{kind: "session", target: "%7", pane: "%7", cwd: "/repo"}, schemeDefault, 1)().(previewMsg)
	if !message.followBottom || message.title != "Preview: repo" || len(message.lines) != 2 || message.lines[0] != "first line" || ansi.Strip(message.lines[1]) != "second line" {
		t.Fatalf("pane preview = %#v", message)
	}
	if !strings.Contains(message.lines[1], "\x1b[31m") || strings.Contains(strings.Join(message.lines, ""), "\x1b]") || strings.Contains(strings.Join(message.lines, ""), "\x1b[2J") {
		t.Fatalf("pane preview did not preserve only SGR color: %#v", message.lines)
	}
}

func TestAgentPreviewFollowsBottomUntilScrolled(t *testing.T) {
	model := newDashboard("/repo")
	model.agents = []item{{kind: "session", target: "%7", pane: "%7", cwd: "/repo"}}
	model.previewRequest = 1
	lines := make([]string, 30)
	updated, _ := model.Update(previewMsg(previewData{request: 1, scheme: schemeDefault, target: "%7", lines: lines, followBottom: true}))
	model = updated.(dashboardModel)
	bottom := model.clampOffset(len(lines), lines)
	if model.previewOffset != bottom {
		t.Fatalf("initial pane preview offset = %d, want %d", model.previewOffset, bottom)
	}

	model.previewOffset = 2
	model.previewRequest = 2
	updated, _ = model.Update(previewMsg(previewData{request: 2, scheme: schemeDefault, target: "%7", lines: append(lines, "new"), followBottom: true}))
	if got := updated.(dashboardModel).previewOffset; got != 2 {
		t.Fatalf("scrolled pane preview jumped to %d", got)
	}
}

func TestLiveAgentLifecycle(t *testing.T) {
	bin := t.TempDir()
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$1:$*" in
  display-message:*client_session*) printf '$1\n' ;;
  display-message:*pane_current_path*) printf '%%7\t%s\n' "$FAKE_CWD" ;;
  display-message:*window_id*) printf '@2\n' ;;
  display-message:*) printf '4321\tpi\n' ;;
  set-option:*-up*jumpmux_pane_status*) rm -f "$TMUX_STATUS" ;;
  set-option:*-p*jumpmux_pane_status*) for value do :; done; printf '%s\n' "$value" > "$TMUX_STATUS" ;;
  list-panes:*jumpmux_pane_status*) cat "$TMUX_STATUS" 2>/dev/null || true ;;
  list-panes:*) printf '%%7\t4321\t%s\tPi\t$1\tdev\t@2\tfeature\tpi\t%s\n' "$FAKE_CWD" "$FAKE_WORKTREE" ;;
  new-window:*) printf '$1\t@9\t%%9\n' ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_CWD", cwd)
	t.Setenv("FAKE_WORKTREE", "")
	logPath := filepath.Join(t.TempDir(), "tmux.log")
	t.Setenv("TMUX_LOG", logPath)
	t.Setenv("TMUX_STATUS", filepath.Join(t.TempDir(), "pane-status"))
	t.Setenv("JUMPMUX_STATE_DIR", t.TempDir())
	t.Setenv("TMUX", "/tmp/tmux,1,0")
	t.Setenv("TMUX_PANE", "%7")

	if err := setAgentStatus([]string{"working", "session-id", "", cwd, "Fix live tracking"}); err != nil {
		t.Fatal(err)
	}
	// A popup or key binding can inherit a stale TMUX_PANE. The active client pane wins.
	t.Setenv("TMUX_PANE", "%99")
	agents, err := listLiveAgents()
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].pane != "%7" || agents[0].status != "working" || agents[0].title != "Fix live tracking" || !agents[0].current {
		t.Fatalf("unexpected live agents: %#v", agents)
	}
	if err := jumpTmuxPane(agents[0]); err != nil {
		t.Fatal(err)
	}
	worktrees := []item{{kind: "worktree", target: cwd, cwd: cwd, branch: "feature"}}
	attachTmuxWorktrees(worktrees)
	if worktrees[0].pane != "" || !worktrees[0].current {
		t.Fatalf("unmanaged pane should only mark the current worktree: %#v", worktrees[0])
	}
	if err := jump(worktrees[0]); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_WORKTREE", cwd)
	invalidateTmuxPaneCache()
	attachTmuxWorktrees(worktrees)
	if worktrees[0].pane != "%7" || worktrees[0].muxSessionID != "$1" {
		t.Fatalf("managed worktree was not attached to tmux pane: %#v", worktrees[0])
	}
	if err := jump(worktrees[0]); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_PANE", "%7")
	if err := setAgentStatus([]string{"done", "session-id", "", cwd, "Fix live tracking"}); err != nil {
		t.Fatal(err)
	}
	if err := setAgentStatus([]string{"closed", "session-id", "", cwd, ""}); err != nil {
		t.Fatal(err)
	}
	agents, err = listLiveAgents()
	if err != nil || len(agents) != 0 {
		t.Fatalf("closed agent still listed: %#v, %v", agents, err)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"new-window -d -P", "@jumpmux_worktree " + cwd, "switch-client -t $1", "window-status-format", "@jumpmux_status 🤖", "@jumpmux_status ✅", "pane-focus-in", "set-option -uw -t %7 @jumpmux_status"} {
		if !strings.Contains(string(log), expected) {
			t.Fatalf("tmux log missing %q:\n%s", expected, log)
		}
	}
	if got := injectTmuxStatusFormat("#I:#W#{?window_flags,#{window_flags}, }"); got != "#I:#W"+jumpmuxStatusFormat+"#{?window_flags,#{window_flags}, }" {
		t.Fatalf("status format = %q", got)
	}
	if got := windowStatusIcon(doneIcon + "\n" + workingIcon); got != workingIcon {
		t.Fatalf("multi-pane window status = %q", got)
	}
}

func TestDiffSupportsUnbornRepository(t *testing.T) {
	dir := t.TempDir()
	if err := exec.Command("git", "init", "-q", dir).Run(); err != nil {
		t.Fatal(err)
	}
	item := item{kind: "worktree", target: dir, cwd: dir}
	clean, ok := loadDiff(item, 1)().(diffMsg)
	if !ok || len(clean.lines) != 1 || clean.lines[0] != "Working tree is clean." {
		t.Fatalf("unexpected clean diff: %#v", clean)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	dirty := loadDiff(item, 2)().(diffMsg)
	if len(dirty.lines) != 2 || dirty.lines[0] != "Untracked files:" || dirty.lines[1] != "? new.txt" {
		t.Fatalf("unexpected untracked diff: %#v", dirty)
	}
}

func TestColorSchemes(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	defer applyColorScheme(schemeDefault)

	if len(colorSchemes) != 12 || schemeTealDrift.next() != schemeDefault {
		t.Fatalf("scheme cycle = %#v", colorSchemes)
	}
	if themePalettes[schemeDefault].accent.Dark != "#CBA6F7" || themePalettes[schemeEmberforge].header.Light != "#AA641E" {
		t.Fatal("Dashboard palette values changed")
	}
	saveColorScheme(schemeEmberforge)
	model := newDashboardForLaunch("/repo", "dev", false)
	if model.scheme != schemeEmberforge || accentColor != themePalettes[schemeEmberforge].accent {
		t.Fatalf("loaded scheme = %s", model.scheme.slug())
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	got := updated.(dashboardModel)
	if got.scheme != schemeGlacierSignal || loadColorScheme() != schemeGlacierSignal {
		t.Fatalf("cycled scheme = %s", got.scheme.slug())
	}
}

func TestDashboardScope(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	saveScopeMode(scopeSession)
	if got := loadScopeMode(); got != scopeSession {
		t.Fatalf("saved scope = %q", got.label())
	}

	model := newDashboardForLaunch("/repo", "dev", false)
	model.allAgents = []item{
		{kind: "session", target: "%1", muxSessionName: "dev"},
		{kind: "session", target: "%2", muxSessionName: "other"},
	}
	model.applyAgentScope()
	if len(model.agents) != 1 || model.agents[0].target != "%1" {
		t.Fatalf("session scope = %#v", model.agents)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	got := updated.(dashboardModel)
	if got.scope != scopeAll || len(got.agents) != 2 || loadScopeMode() != scopeAll {
		t.Fatalf("all scope = %s, agents = %#v", got.scope.label(), got.agents)
	}
}

func TestPreviewSize(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	model := newDashboard("/repo")
	model.width, model.height = 80, 33
	if model.previewSize != 50 || model.tableHeight() != 15 || model.previewHeight() != 15 {
		t.Fatalf("default split = %d%%, table=%d preview=%d", model.previewSize, model.tableHeight(), model.previewHeight())
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'+'}})
	model = updated.(dashboardModel)
	if model.previewSize != 60 || model.tableHeight() != 12 || model.previewHeight() != 18 || loadPreviewSize() != 60 {
		t.Fatalf("grown split = %d%%, table=%d preview=%d", model.previewSize, model.tableHeight(), model.previewHeight())
	}
	path, err := settingsPath()
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("preview size mode: info=%v err=%v", info, err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "{\"preview_size\":60}\n" {
		t.Fatalf("settings file: %q err=%v", data, err)
	}

	loaded := newDashboardForLaunch("/repo", "", false)
	if loaded.previewSize != 60 {
		t.Fatalf("persisted preview size = %d", loaded.previewSize)
	}
	updated, _ = loaded.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'-'}})
	if got := updated.(dashboardModel).previewSize; got != 50 {
		t.Fatalf("shrunk preview size = %d", got)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	legacy, err := legacyPreviewSizePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(legacy, []byte("70\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadPreviewSize(); got != 70 {
		t.Fatalf("legacy preview size = %d", got)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "{\"preview_size\":70}\n" {
		t.Fatalf("migrated settings file: %q err=%v", data, err)
	}
}

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
	for _, expected := range []string{"a Add", "r Remove", "o PR", "t Theme"} {
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

func TestDashboardRefreshIsIncremental(t *testing.T) {
	model := newDashboard("/repo")
	model.worktrees = []item{{kind: "worktree", target: "/repo", cwd: "/repo"}}

	updated, _ := model.Update(dashboardDataMsg(dashboardData{
		agents: []item{{kind: "session", target: "%7", cwd: "/repo"}},
	}))
	got := updated.(dashboardModel)
	if len(got.agents) != 1 || len(got.worktrees) != 1 {
		t.Fatalf("agent refresh replaced unrelated data: %#v", got)
	}

	updated, command := got.Update(worktreeDataMsg{
		stage:      worktreeListStage,
		generation: got.worktreeGeneration,
		worktrees:  []item{{kind: "worktree", target: "/other", cwd: "/other", branch: "feature"}},
	})
	got = updated.(dashboardModel)
	if command == nil || len(got.agents) != 1 || len(got.worktrees) != 1 || got.worktrees[0].target != "/other" {
		t.Fatalf("worktree list did not render incrementally: %#v", got)
	}

	updated, _ = got.Update(worktreeDataMsg{
		stage:      worktreeGitStage,
		generation: got.worktreeGeneration,
		worktrees:  []item{{target: "/other", dirty: true, gitLoaded: true, added: 12, removed: 3}},
	})
	got = updated.(dashboardModel)
	updated, _ = got.Update(worktreeDataMsg{
		stage:      worktreePRStage,
		generation: got.worktreeGeneration,
		worktrees:  []item{{target: "/other", prLoaded: true, prNumber: 42, prState: "OPEN", prCheck: checkFailure}},
	})
	got = updated.(dashboardModel)
	if got.worktrees[0].added != 12 || got.worktrees[0].prNumber != 42 || got.worktrees[0].prCheck != checkFailure || len(got.agents) != 1 {
		t.Fatalf("worktree details did not merge: %#v", got.worktrees)
	}

	updated, _ = got.Update(worktreeDataMsg{
		stage:      worktreeListStage,
		generation: got.worktreeGeneration - 1,
		worktrees:  []item{{target: "/stale", cwd: "/stale"}},
	})
	if stale := updated.(dashboardModel); stale.worktrees[0].target != "/other" {
		t.Fatalf("stale worktree list was applied: %#v", stale.worktrees)
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
	if err := savePRStatusCache(map[string]item{path: {cwd: path, prLoaded: true, prNumber: 23, prState: "OPEN", prDraft: true, prCheck: checkFailure}}); err != nil {
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
	if got := prText(model.gitItem(agent), model.now); got != "#23 "+prDraftIcon+" "+checkFailureIcon {
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

func TestAgentPRStatusOutsideLaunchRepo(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "feature"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.com"}, {"commit", "--allow-empty", "-qm", "base"}} {
		if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(`#!/bin/sh
printf '%s\n' '[{"number":23,"state":"OPEN","isDraft":true,"headRefName":"feature","statusCheckRollup":[{"status":"COMPLETED","conclusion":"FAILURE"}]}]'
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
	if got := prText(model.gitItem(agent), model.now); got != "#23 "+prDraftIcon+" "+checkFailureIcon {
		t.Fatalf("cross-repository agent PR status = %q", got)
	}

	updated, _ = model.Update(agentGitMsg{{cwd: agent.cwd, gitLoaded: true, dirty: true, added: 3}})
	model = updated.(dashboardModel)
	if got := gitStatusText(model.gitItem(agent), model.now); !strings.Contains(got, "+3") {
		t.Fatalf("dirty agent Git status = %q", got)
	}
	if got := prText(model.gitItem(agent), model.now); got != "#23 "+prDraftIcon+" "+checkFailureIcon {
		t.Fatalf("cached PR lost after failed refresh = %q", got)
	}
}

func TestDashboardLayout(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height = 100, 30
	model.agents = []item{
		{kind: "session", target: "current", cwd: "/repo", title: "Current project", updated: time.Now(), current: true},
		{kind: "session", target: "selected", cwd: "/other", title: "Selected agent", updated: time.Now()},
	}
	model.worktrees = []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main", dirty: true, gitLoaded: true, added: 12, removed: 3, untracked: 2, prNumber: 42, prState: "OPEN", prCheck: checkFailure}}
	model.index = 1
	model.preview = previewData{target: "selected", title: "Preview: repo", lines: []string{"Recent session output"}}

	view := model.View()
	plain := ansi.Strip(view)
	for _, expected := range []string{"  Agents │ Worktrees", "# Project", "Git", gitDiffIcon, "+12", "-3", "PR", "#42 " + prOpenIcon + " " + checkFailureIcon, "Status", doneIcon, "Time", "Title", "┌─ Preview: repo ", "↵ Open", "? Help"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("dashboard missing %q:\n%s", expected, plain)
		}
	}
	if strings.Contains(plain, "working") || strings.Contains(plain, "done") {
		t.Fatalf("dashboard status includes redundant text:\n%s", plain)
	}
	lines := strings.Split(view, "\n")
	for _, line := range lines {
		if !strings.Contains(ansi.Strip(line), "Current project") {
			continue
		}
		for _, style := range []string{currentStyle.Render("probe"), currentWorktreeStyle.Render("probe")} {
			prefix := strings.SplitN(style, "probe", 2)[0]
			if prefix != "" && !strings.Contains(line, prefix) {
				t.Fatalf("current project row missing Dashboard highlight: %q", line)
			}
		}
	}
	if len(lines) != model.height {
		t.Fatalf("dashboard height = %d, want %d", len(lines), model.height)
	}
	for index, line := range lines {
		if width := ansi.StringWidth(line); width > model.width {
			t.Fatalf("line %d width = %d, want <= %d", index, width, model.width)
		}
	}
}

func TestSelectedRowsPreserveGitStyle(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height = 100, 30
	worktree := item{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main", gitLoaded: true, dirty: true, added: 2}
	model.worktrees = []item{worktree}
	styledIcon := accentStyle.Render(gitDiffIcon)

	for _, tab := range []int{0, 1} {
		model.tab = tab
		if tab == 0 {
			model.agents = []item{{kind: "session", target: "%1", cwd: "/repo"}}
		}
		rows := model.rows()
		row := model.tableRow(rows[0], 0, model.width, model.columns(model.width, rows))
		if styledIcon != gitDiffIcon && !strings.Contains(row, styledIcon) {
			t.Fatalf("tab %d selected row lost Git styling: %q", tab, row)
		}
		backgroundPrefix := strings.SplitN(selectedStyle.Render("probe"), "probe", 2)[0]
		if backgroundPrefix != "" && strings.Count(row, backgroundPrefix) < 3 {
			t.Fatalf("tab %d selected background is not continuous: %q", tab, row)
		}
	}
}

func TestAgentJumpNumbersStopAtNine(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height = 140, 35
	for index := range 11 {
		model.agents = append(model.agents, item{kind: "session", target: fmt.Sprintf("%%%d", index), cwd: fmt.Sprintf("/repo/%d", index), gitLoaded: true})
	}
	columns := model.columns(model.width, model.rows())
	if row := ansi.Strip(model.tableRow(model.agents[8], 8, model.width, columns)); !strings.HasPrefix(row, "  9 ") {
		t.Fatalf("ninth jump key = %q", row[:min(len(row), 8)])
	}
	for _, index := range []int{9, 10} {
		row := ansi.Strip(model.tableRow(model.agents[index], index, model.width, columns))
		if !strings.HasPrefix(row, "    ") {
			t.Fatalf("row %d repeated a jump number: %q", index+1, row[:min(len(row), 8)])
		}
	}
}

func TestDashboardGitAndPRIcons(t *testing.T) {
	now := time.Unix(0, 0)
	if got := gitStatusText(item{}, now); got != spinnerFrames[0] {
		t.Fatalf("loading Git status = %q", got)
	}
	if got := gitStatusText(item{gitLoaded: true, dirty: true, untracked: 1}, now); got != gitDiffIcon {
		t.Fatalf("untracked Git status = %q", got)
	}
	complex := item{
		gitLoaded: true, baseBranch: "develop", committedAdded: 10, committedRemoved: 2,
		dirty: true, added: 3, removed: 1, isRebasing: true, hasConflict: true, ahead: 2, behind: 1,
	}
	want := strings.Join([]string{gitRebaseIcon, "→develop", "+10", "-2", gitDiffIcon, "+3", "-1", gitConflictIcon, "↑2", "↓1"}, " ")
	if got := gitStatusText(complex, now); got != want {
		t.Fatalf("complex Git status = %q, want %q", got, want)
	}
	cases := []struct {
		item item
		want string
	}{
		{item{prNumber: 1, prDraft: true}, "#1 " + prDraftIcon},
		{item{prNumber: 2, prState: "OPEN"}, "#2 " + prOpenIcon},
		{item{prNumber: 3, prState: "MERGED"}, "#3 " + prMergedIcon},
		{item{prNumber: 4, prState: "CLOSED"}, "#4 " + prClosedIcon},
		{item{prNumber: 5, prState: "OPEN", prCheck: checkSuccess}, "#5 " + prOpenIcon + " " + checkSuccessIcon},
		{item{prNumber: 6, prState: "OPEN", prCheck: checkFailure}, "#6 " + prOpenIcon + " " + checkFailureIcon},
		{item{prNumber: 7, prState: "OPEN", prCheck: checkPending}, "#7 " + prOpenIcon + " " + spinnerFrames[0]},
	}
	for _, test := range cases {
		if got := prText(test.item, now); got != test.want {
			t.Errorf("prText(%#v) = %q, want %q", test.item, got, test.want)
		}
	}
	if got := aggregateCheckState([]checkRollupItem{{Status: "IN_PROGRESS"}, {State: "SUCCESS"}}); got != checkPending {
		t.Fatalf("pending check aggregate = %q", got)
	}
	if got := aggregateCheckState([]checkRollupItem{{Conclusion: "SKIPPED"}}); got != checkSuccess {
		t.Fatalf("skipped check aggregate = %q", got)
	}
	if got := aggregateCheckState([]checkRollupItem{{Status: "IN_PROGRESS"}, {Conclusion: "CANCELLED"}}); got != checkFailure {
		t.Fatalf("failed check aggregate = %q", got)
	}
}

func TestDashboardAgentStatusDisplay(t *testing.T) {
	started := time.Unix(0, 0)
	working := item{status: "working", updated: started}
	if got := statusText(working, started); got != workingIcon+" "+spinnerFrames[0] {
		t.Fatalf("working status = %q", got)
	}
	if got := statusText(working, started.Add(clockInterval)); got != workingIcon+" "+spinnerFrames[1] {
		t.Fatalf("next spinner frame = %q", got)
	}
	if got := statusText(working, started.Add(staleThreshold+time.Second)); got != workingIcon+" "+staleAgentIcon {
		t.Fatalf("stale status = %q", got)
	}
}

func TestDashboardElapsedTimeStyle(t *testing.T) {
	if got, want := elapsedStyle(4*time.Minute).Render("x"), successStyle.Render("x"); got != want {
		t.Fatalf("recent elapsed style = %q, want %q", got, want)
	}
	if got, want := elapsedStyle(30*time.Minute).Render("x"), warningStyle.Render("x"); got != want {
		t.Fatalf("warm elapsed style = %q, want %q", got, want)
	}
	if got, want := elapsedStyle(time.Hour).Render("x"), accentStyle.Faint(true).Render("x"); got != want {
		t.Fatalf("old elapsed style = %q, want %q", got, want)
	}
	now := time.Unix(1000, 0)
	cell := elapsedCell(now.Add(-71*time.Second), 10, now, nil)
	if inactive := successStyle.Faint(true).Render("00"); !strings.Contains(cell, inactive) {
		t.Fatalf("zero clock units are not dimmed: %q", cell)
	}
	if got := elapsedClock(now.Add(-101*time.Hour), now); got != "101:00:00" {
		t.Fatalf("long elapsed clock = %q", got)
	}
}

func TestClockTickUpdatesElapsedTime(t *testing.T) {
	started := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	model := newDashboard("/repo")
	model.width, model.height = 100, 30
	model.now = started.Add(5 * time.Second)
	model.agents = []item{{kind: "session", target: "agent", cwd: "/repo", updated: started}}
	if view := ansi.Strip(model.View()); !strings.Contains(view, "00:00:05") {
		t.Fatalf("initial elapsed time missing:\n%s", view)
	}
	updated, cmd := model.Update(clockMsg(started.Add(6 * time.Second)))
	got := updated.(dashboardModel)
	if view := ansi.Strip(got.View()); !strings.Contains(view, "00:00:06") || cmd == nil {
		t.Fatalf("clock tick did not advance elapsed time:\n%s", view)
	}
}

func TestMouseClickChoosesRow(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height = 100, 30
	model.agents = []item{
		{kind: "session", target: "first", cwd: "/repo"},
		{kind: "session", target: "second", cwd: "/repo"},
	}
	click := tea.MouseMsg{X: 5, Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
	updated, cmd := model.Update(click)
	got := updated.(dashboardModel)
	if got.chosen || got.index != 1 || cmd == nil {
		t.Fatalf("single click did not select second row: %#v", got)
	}
	updated, cmd = got.Update(click)
	got = updated.(dashboardModel)
	if !got.chosen || got.selection.target != "second" || cmd == nil {
		t.Fatalf("double click did not choose second row: %#v", got)
	}
}

func TestAsyncResultsStayInTheirView(t *testing.T) {
	model := newDashboard("/repo")
	model.agents = []item{{kind: "session", target: "agent", cwd: "/repo"}}
	model.previewRequest = 2
	updated, _ := model.Update(previewMsg(previewData{request: 1, target: "agent", title: "stale"}))
	model = updated.(dashboardModel)
	if model.preview.title == "stale" {
		t.Fatal("stale preview request was applied")
	}

	model.diff, model.diffRequest = true, 2
	updated, _ = model.Update(previewMsg(previewData{request: 2, target: "agent", title: "normal"}))
	model = updated.(dashboardModel)
	updated, _ = model.Update(diffMsg{request: 1, target: "agent", title: "stale diff"})
	model = updated.(dashboardModel)
	if model.preview.title != "" {
		t.Fatalf("wrong-mode async result was applied: %#v", model.preview)
	}
	updated, _ = model.Update(diffMsg{request: 2, target: "agent", title: "current diff"})
	if got := updated.(dashboardModel).preview.title; got != "current diff" {
		t.Fatalf("current diff = %q", got)
	}
}

func TestBranchChangeClearsMetadata(t *testing.T) {
	current := []item{{target: "/repo", branch: "old", gitLoaded: true, dirty: true, baseBranch: "main", committedAdded: 3, added: 2, ahead: 1, hasConflict: true, prNumber: 4}}
	got := mergeWorktreeData(current, []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "new"}}, worktreeListStage)
	if got[0].gitLoaded || got[0].dirty || got[0].baseBranch != "" || got[0].committedAdded != 0 || got[0].added != 0 || got[0].ahead != 0 || got[0].hasConflict || got[0].prNumber != 0 {
		t.Fatalf("branch metadata leaked: %#v", got[0])
	}
}

func TestSmallTerminalAndFilterState(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height = 30, 5
	view := model.View()
	lines := strings.Split(view, "\n")
	if len(lines) != model.height {
		t.Fatalf("small view height = %d", len(lines))
	}
	for _, line := range lines {
		if width := ansi.StringWidth(line); width != model.width {
			t.Fatalf("small view width = %d", width)
		}
	}
	model.agents = []item{{kind: "session", target: "hidden", cwd: "/repo"}}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.(dashboardModel).chosen {
		t.Fatal("tiny terminal activated a hidden row")
	}

	model.width, model.height = 40, 10
	model.agents = []item{{kind: "session", target: "agent", cwd: "/repo", title: "Agent"}}
	for _, line := range strings.Split(model.View(), "\n") {
		if width := ansi.StringWidth(line); width > model.width {
			t.Fatalf("compact view width = %d", width)
		}
	}

	model.width, model.height = 100, 30
	model.query, model.queries[0] = "agent", "agent"
	updated, _ = model.switchTab(1)
	model = updated.(dashboardModel)
	if model.query != "" {
		t.Fatalf("agent filter leaked to worktrees: %q", model.query)
	}
	updated, _ = model.switchTab(0)
	if got := updated.(dashboardModel).query; got != "agent" {
		t.Fatalf("agent filter was not restored: %q", got)
	}
}

func TestPreviewRefreshPreservesScroll(t *testing.T) {
	model := newDashboard("/repo")
	model.agents = []item{{kind: "session", target: "agent", cwd: "/repo"}}
	lines := make([]string, 20)
	model.preview = previewData{target: "agent", lines: lines}
	model.previewOffset, model.previewRequest = 4, 2
	updated, _ := model.Update(previewMsg(previewData{request: 2, target: "agent", lines: lines}))
	if got := updated.(dashboardModel).previewOffset; got != 4 {
		t.Fatalf("preview refresh reset offset to %d", got)
	}
}

func TestDashboardEmptyAndErrorStates(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height = 80, 24
	if view := ansi.Strip(model.View()); !strings.Contains(view, "Loading live agents") {
		t.Fatalf("missing loading state:\n%s", view)
	}
	model.agentsLoaded = true
	if view := ansi.Strip(model.View()); !strings.Contains(view, "No live agents") || strings.Contains(view, "Loading…\n") {
		t.Fatalf("missing loaded-empty state:\n%s", view)
	}
	model.query = "missing"
	if view := ansi.Strip(model.View()); !strings.Contains(view, "No matches for /missing") || !strings.Contains(view, "Filter:missing") {
		t.Fatalf("missing filtered-empty state:\n%s", view)
	}
	model.agentErr = errors.New("tmux unavailable")
	if view := ansi.Strip(model.View()); !strings.Contains(view, "tmux unavailable") {
		t.Fatalf("missing source error:\n%s", view)
	}
}

func TestDisplaySafetyAndFiltering(t *testing.T) {
	if got := safeText("one\x1b[31m\ttwo\nthree"); got != "one [31m two three" {
		t.Fatalf("safeText() = %q", got)
	}
	if got := safeLine("  indented\ttext\x1b"); got != "  indented text " {
		t.Fatalf("safeLine() = %q", got)
	}
	if got := (item{kind: "worktree", title: "bad\x1b[31m", cwd: "/tmp/bad\x1b[31m"}).display(); strings.ContainsRune(got, '\x1b') {
		t.Fatalf("display contains terminal control sequence: %q", got)
	}
	if got := truncate("éclair", 1); got != "…" {
		t.Fatalf("truncate() = %q", got)
	}
	if !pathWithin("/repo/worktree/subdir", "/repo/worktree") || pathWithin("/repo/other", "/repo/worktree") {
		t.Fatal("pathWithin did not keep the worktree boundary")
	}

	model := newDashboard("/repo")
	model.agents = []item{{kind: "session", target: "one", title: "Build repair", cwd: "/repo"}, {kind: "session", target: "two", title: "Docs", cwd: "/repo"}}
	model.query = "REPAIR"
	rows := model.rows()
	if len(rows) != 1 || rows[0].target != "one" {
		t.Fatalf("case-insensitive filter = %#v", rows)
	}
}
