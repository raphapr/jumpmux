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

func TestParsers(t *testing.T) {
	t.Run("Pi session", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.jsonl")
		data := `{"type":"session","version":3,"id":"abc","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}
{"type":"message","id":"1","parentId":null,"message":{"role":"user","content":[{"type":"text","text":"Fix the build"}]}}
{"type":"message","id":"2","parentId":"1","message":{"role":"assistant","content":"I will inspect the failure."}}
`
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		user, assistant, err := sessionExcerpts(path)
		if err != nil || user != "Fix the build" || assistant != "I will inspect the failure." {
			t.Fatalf("unexpected excerpts: user=%q assistant=%q err=%v", user, assistant, err)
		}
		user, assistant, err = sessionExcerpts(filepath.Join(t.TempDir(), "not-created-yet.jsonl"))
		if err != nil || user != "" || assistant != "" {
			t.Fatalf("new session preview: user=%q assistant=%q err=%v", user, assistant, err)
		}
	})

	t.Run("git worktrees", func(t *testing.T) {
		data := []byte("worktree /repo\x00HEAD abc\x00branch refs/heads/main\x00\x00worktree /repo-feature\x00HEAD def\x00detached")
		got := parseWorktrees(data)
		if len(got) != 2 || got[0].branch != "main" || got[1].branch != "detached" {
			t.Fatalf("unexpected worktrees: %#v", got)
		}
	})

	t.Run("pull requests", func(t *testing.T) {
		got := parsePullRequests([]byte(`[{"number":42,"state":"OPEN","isDraft":false,"headRefName":"feature"}]`))
		if got["feature"].Number != 42 || got["feature"].State != "OPEN" {
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

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'T'}})
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

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	got := updated.(dashboardModel)
	if got.scope != scopeAll || len(got.agents) != 2 || loadScopeMode() != scopeAll {
		t.Fatalf("all scope = %s, agents = %#v", got.scope.label(), got.agents)
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
		worktrees:  []item{{target: "/other", prNumber: 42, prState: "OPEN"}},
	})
	got = updated.(dashboardModel)
	if got.worktrees[0].added != 12 || got.worktrees[0].prNumber != 42 || len(got.agents) != 1 {
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

func TestDashboardLayout(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height = 100, 30
	model.agents = []item{
		{kind: "session", target: "current", cwd: "/repo", title: "Current project", updated: time.Now(), current: true},
		{kind: "session", target: "selected", cwd: "/other", title: "Selected agent", updated: time.Now()},
	}
	model.worktrees = []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main", dirty: true, gitLoaded: true, added: 12, removed: 3, untracked: 2, prNumber: 42, prState: "OPEN"}}
	model.index = 1
	model.preview = previewData{target: "selected", title: "Preview: repo", lines: []string{"Recent session output"}}

	view := model.View()
	plain := ansi.Strip(view)
	for _, expected := range []string{"  Agents │ Worktrees", "# Project", "Git", gitDiffIcon, "+12", "-3", "PR", "#42 " + prOpenIcon, "Status", doneIcon, "Time", "Title", "┌─ Preview: repo ", "↵ Open", "? Help"} {
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
	}
	for _, test := range cases {
		if got := prText(test.item); got != test.want {
			t.Errorf("prText(%#v) = %q, want %q", test.item, got, test.want)
		}
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
