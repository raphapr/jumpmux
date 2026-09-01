package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

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
	legacySkill := filepath.Join(root, "skills", "jumpmux", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(legacySkill), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacySkill, []byte("unmanaged"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := setupPIExtension(); err != nil {
		t.Fatalf("reinstall was not idempotent: %v", err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(legacySkill); err != nil || string(data) != "unmanaged" {
		t.Fatalf("setup modified an unrelated legacy skill: %q, %v", data, err)
	}
	if !strings.Contains(string(data), "timeout: 30000") {
		t.Fatal("Pi lifecycle timeout is shorter than the tmux operation bound")
	}
	if !strings.Contains(string(data), `event.name ?? ""`) {
		t.Fatal("Pi rename event does not report its new title directly")
	}
	if !strings.Contains(string(data), `report("started", ctx)`) {
		t.Fatal("Pi session start is incorrectly reported as completed work")
	}
	for _, expected := range []string{`pi.on("ui_prompt_start"`, `report("question", ctx)`, `pi.on("ui_prompt_end"`, `ctx.isIdle() ? "done" : "working"`} {
		if !strings.Contains(string(data), expected) {
			t.Fatalf("Pi UI-prompt lifecycle is missing %q", expected)
		}
	}
	for _, removed := range []string{`question_tools`, `tool_call`, `tool_result`} {
		if strings.Contains(string(data), removed) {
			t.Fatalf("Pi extension still contains removed question-tool fallback %q", removed)
		}
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
	message := loadAgentPreview(item{kind: "session", target: "%7", pane: "%7", cwd: "/repo"}, schemeDefault, 1)().(previewMsg)
	if !message.followBottom || message.title != "Preview: repo" || len(message.lines) != 2 || message.lines[0] != "first line" || ansi.Strip(message.lines[1]) != "second line" {
		t.Fatalf("pane preview = %#v", message)
	}
	if !strings.Contains(message.lines[1], "\x1b[31m") || strings.Contains(strings.Join(message.lines, ""), "\x1b]") || strings.Contains(strings.Join(message.lines, ""), "\x1b[2J") {
		t.Fatalf("pane preview did not preserve only SGR color: %#v", message.lines)
	}
}

func TestSessionPreviewCapturesCurrentAlternateScreen(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf 'btop\\n\\033[31mcpu 42%%\\033[0m\\n'\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	message := loadSessionPreview(item{kind: "tmux-session", target: "dev", title: "dev", cwd: "/repo", muxSessionID: "$1", pane: "%7"}, schemeDefault, 1)().(previewMsg)
	if message.title != "Active pane: dev" || !message.followBottom || len(message.lines) != 2 || message.lines[0] != "btop" || ansi.Strip(message.lines[1]) != "cpu 42%" {
		t.Fatalf("session pane preview = %#v", message)
	}
}

func TestAgentPreviewFollowsBottomUntilScrolled(t *testing.T) {
	model := newDashboard("/repo")
	model.agents = []item{{kind: "session", target: "%7", pane: "%7", cwd: "/repo"}}
	model.previewRequest = 1
	lines := make([]string, 30)
	updated, command := model.Update(previewMsg(previewData{request: 1, scheme: schemeDefault, target: "%7", lines: lines, followBottom: true}))
	if _, ok := command().(previewTickMsg); !ok {
		t.Fatal("agent preview did not schedule its dedicated refresh tick")
	}
	model = updated.(dashboardModel)
	bottom := model.clampPreviewOffset(len(lines), lines)
	if model.previewOffset != bottom {
		t.Fatalf("initial pane preview offset = %d, want %d", model.previewOffset, bottom)
	}

	model.previewOffset = 2
	model.previewRequest = 2
	updated, _ = model.Update(previewMsg(previewData{request: 2, scheme: schemeDefault, target: "%7", lines: append(lines, "new"), followBottom: true}))
	if got := updated.(dashboardModel).previewOffset; got != 2 {
		t.Fatalf("scrolled pane preview jumped to %d", got)
	}

	updated, command = model.Update(previewTickMsg(2))
	if command == nil {
		t.Fatal("current preview tick did not refresh the agent")
	}
	if _, command = updated.(dashboardModel).Update(previewTickMsg(1)); command != nil {
		t.Fatal("stale preview tick refreshed the agent")
	}
}

func TestLiveAgentLifecycle(t *testing.T) {
	bin := t.TempDir()
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$1:$*" in
  display-message:*client_session*) printf '$1\n' ;;
  display-message:*pane_current_path*) printf '%s\t%s\n' "$FAKE_ACTIVE_PANE" "$FAKE_CWD" ;;
  display-message:*pane_active*)
    if test "$FAKE_ACTIVE_PANE" = '%7'; then printf '1\t1\t1\n'; else printf '0\t1\t1\n'; fi ;;
  display-message:*window_id*) printf '@2\n' ;;
  display-message:*) printf '4321\tpi\n' ;;
  set-option:*-up*jumpmux_pane_status*) rm -f "$TMUX_STATUS" ;;
  set-option:*-p*jumpmux_pane_status*) for value do :; done; printf '%s\n' "$value" > "$TMUX_STATUS" ;;
  show-option:*-pv*jumpmux_pane_status*) cat "$TMUX_STATUS" 2>/dev/null || true ;;
  list-panes:*jumpmux_pane_status*) cat "$TMUX_STATUS" 2>/dev/null || true ;;
  list-panes:*) printf '%%7\0374321\037%s\037Pi\037$1\037dev\037@2\037feature\037pi\037%s\036\n' "$FAKE_CWD" "$FAKE_WORKTREE" ;;
  new-window:*) printf '$1\t@9\t%%9\n' ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_CWD", cwd)
	t.Setenv("FAKE_ACTIVE_PANE", "%7")
	t.Setenv("FAKE_WORKTREE", "")
	logPath := filepath.Join(t.TempDir(), "tmux.log")
	t.Setenv("TMUX_LOG", logPath)
	t.Setenv("TMUX_STATUS", filepath.Join(t.TempDir(), "pane-status"))
	stateDir := t.TempDir()
	t.Setenv("JUMPMUX_STATE_DIR", stateDir)
	t.Setenv("TMUX", "/tmp/tmux,1,0")
	t.Setenv("TMUX_PANE", "%7")

	if err := setAgentStatus([]string{"started", "session-id", "", cwd, "Fix live tracking"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(os.Getenv("TMUX_STATUS")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("idle startup left a completion marker: %v", err)
	}
	if err := setAgentStatus([]string{"working", "session-id", "", cwd, "Fix live tracking"}); err != nil {
		t.Fatal(err)
	}
	if err := setAgentStatus([]string{"question", "session-id", "", cwd, "Fix live tracking"}); err != nil {
		t.Fatal(err)
	}
	state, _, err := readAgentState("%7")
	if err != nil || state.Status != "question" || state.Seen {
		t.Fatalf("question state = %#v, %v", state, err)
	}
	if data, err := os.ReadFile(os.Getenv("TMUX_STATUS")); err != nil || string(data) != questionIcon+"\n" {
		t.Fatalf("question tmux marker = %q, %v", data, err)
	}
	if marked, err := markAgentSeen(item{pane: "%7", agentSessionID: "session-id"}); err != nil || marked {
		t.Fatalf("mark question seen = %v, %v", marked, err)
	}
	if data, err := os.ReadFile(os.Getenv("TMUX_STATUS")); err != nil || string(data) != questionIcon+"\n" {
		t.Fatalf("marking a question seen cleared its tmux marker: %q, %v", data, err)
	}
	model := newDashboard(cwd)
	message := model.beginMarkAgentSeen(item{pane: "%7", agentSessionID: "session-id"})().(worktreeActionMsg)
	if message.err != nil || message.notice != "" {
		t.Fatalf("no-op mark seen result = %#v", message)
	}
	if err := setAgentStatus([]string{"working", "session-id", "", cwd, "Fix live tracking"}); err != nil {
		t.Fatal(err)
	}
	// A popup or key binding can inherit a stale TMUX_PANE. The active client pane wins.
	t.Setenv("TMUX_PANE", "%99")
	agents, err := listLiveAgents()
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].pane != "%7" || agents[0].status != "working" || agents[0].title != "π - Fix live tracking" || !agents[0].current {
		t.Fatalf("unexpected live agents: %#v", agents)
	}
	t.Setenv("TMUX_PANE", "%7")
	if err := setAgentStatus([]string{"update", "session-id", "", cwd, ""}); err != nil {
		t.Fatal(err)
	}
	invalidateTmuxPaneCache()
	unnamed, err := listLiveAgents()
	if err != nil || len(unnamed) != 1 || unnamed[0].title != "Pi" {
		t.Fatalf("unnamed agent title = %#v, %v", unnamed, err)
	}
	if err := setAgentStatus([]string{"update", "session-id", "", cwd, "Fix live tracking"}); err != nil {
		t.Fatal(err)
	}
	invalidateTmuxPaneCache()
	if err := jumpTmuxPane(agents[0]); err != nil {
		t.Fatal(err)
	}
	worktrees := []item{{kind: "worktree", target: cwd, cwd: cwd, branch: "feature"}}
	if err := attachTmuxWorktrees(worktrees); err != nil {
		t.Fatal(err)
	}
	if worktrees[0].pane != "" || !worktrees[0].current {
		t.Fatalf("unmanaged pane should only mark the current worktree: %#v", worktrees[0])
	}
	if err := jump(worktrees[0]); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_WORKTREE", cwd)
	invalidateTmuxPaneCache()
	if err := attachTmuxWorktrees(worktrees); err != nil {
		t.Fatal(err)
	}
	if worktrees[0].pane != "%7" || worktrees[0].muxSessionID != "$1" {
		t.Fatalf("managed worktree was not attached to tmux pane: %#v", worktrees[0])
	}
	if err := jump(worktrees[0]); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_PANE", "%7")
	t.Setenv("FAKE_ACTIVE_PANE", "%99")
	if err := setAgentStatus([]string{"done", "session-id", "", cwd, "Fix live tracking"}); err != nil {
		t.Fatal(err)
	}
	invalidateTmuxPaneCache()
	agents, err = listLiveAgents()
	if err != nil || len(agents) != 1 || agents[0].seen || agents[0].agentSessionID != "session-id" {
		t.Fatalf("unseen done agent = %#v, %v", agents, err)
	}
	if marked, err := markAgentSeen(agents[0]); err != nil || !marked {
		t.Fatalf("mark done agent seen = %v, %v", marked, err)
	}
	if _, err := os.Stat(os.Getenv("TMUX_STATUS")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marking an agent seen left its tmux completion marker: %v", err)
	}
	invalidateTmuxPaneCache()
	agents, err = listLiveAgents()
	if err != nil || len(agents) != 1 || !agents[0].seen {
		t.Fatalf("seen done agent = %#v, %v", agents, err)
	}
	if err := setAgentStatus([]string{"done", "session-id", "", cwd, "Fix live tracking"}); err != nil {
		t.Fatal(err)
	}
	invalidateTmuxPaneCache()
	agents, err = listLiveAgents()
	if err != nil || len(agents) != 1 || agents[0].seen {
		t.Fatalf("new done event did not restore attention = %#v, %v", agents, err)
	}
	t.Setenv("FAKE_ACTIVE_PANE", "%7")
	if err := setAgentStatus([]string{"done", "session-id", "", cwd, "Fix live tracking"}); err != nil {
		t.Fatal(err)
	}
	state, _, err = readAgentState("%7")
	if err != nil || !state.Seen {
		t.Fatalf("completion in the focused pane stayed unseen: %#v, %v", state, err)
	}
	if _, err := os.Stat(os.Getenv("TMUX_STATUS")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("focused completion left its tmux marker: %v", err)
	}
	if err := os.WriteFile(os.Getenv("TMUX_STATUS"), []byte(doneIcon+"\n"+workingIcon+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := syncTmuxWindowStatus("%7"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("TMUX_STATUS"), []byte(workingIcon+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := syncTmuxWindowStatus("%7"); err != nil {
		t.Fatal(err)
	}
	if err := setAgentStatus([]string{"closed", "session-id", "", cwd, ""}); err != nil {
		t.Fatal(err)
	}
	agents, err = listLiveAgents()
	if err != nil || len(agents) != 0 {
		t.Fatalf("closed agent still listed: %#v, %v", agents, err)
	}
	if err := os.WriteFile(os.Getenv("TMUX_STATUS"), []byte(doneIcon+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := clearFocusedPaneStatus("%7"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(os.Getenv("TMUX_STATUS")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphaned completion marker was not cleared: %v", err)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"new-window -d -P", "@jumpmux_worktree " + cwd, "switch-client -t $1", "window-status-format", "@jumpmux_status ", "@jumpmux_status ", "set-hook -g after-select-pane[987654]", "set-hook -g after-select-window[987654]", "set-hook -g client-session-changed[987654]", "set-hook -g client-focus-in[987654]", "pane-focused #{pane_id} '" + stateDir + "'", "set-hook -gu after-select-pane[987654]", "set-option -uw -t %7 @jumpmux_status"} {
		if !strings.Contains(string(log), expected) {
			t.Fatalf("tmux log missing %q:\n%s", expected, log)
		}
	}
	if got := injectTmuxStatusFormat("#I:#W#{?window_flags,#{window_flags}, }"); got != "#I:#W"+jumpmuxStatusFormat+"#{?window_flags,#{window_flags}, }" {
		t.Fatalf("status format = %q", got)
	}
	if !strings.Contains(jumpmuxStatusFormat, "#[nobold]") || !strings.Contains(jumpmuxStatusFormat, "#{@jumpmux_status} #[default]") {
		t.Fatalf("status format does not reserve the glyph's second cell: %q", jumpmuxStatusFormat)
	}
	for _, existing := range []string{plainJumpmuxStatusFormat, boldJumpmuxStatusFormat, narrowJumpmuxStatusFormat, narrowJumpmuxStatusFormat + jumpmuxStatusFormat} {
		format := strings.Replace("#I:#W#{?window_flags,#{window_flags}, }", "#{?window_flags", existing+"#{?window_flags", 1)
		got := injectTmuxStatusFormat(format)
		if strings.Count(got, "@jumpmux_status") != 2 || !strings.Contains(got, jumpmuxStatusFormat) {
			t.Fatalf("status format migration = %q", got)
		}
	}
	if got := windowStatusIcon(doneIcon + "\n" + workingIcon); got != workingIcon {
		t.Fatalf("multi-pane window status = %q", got)
	}
	if got := windowStatusIcon(doneIcon + "\n" + workingIcon + "\n" + questionIcon); got != questionIcon {
		t.Fatalf("question window status precedence = %q", got)
	}
}

func TestCurrentWorktreeUsesDeepestActiveTmuxPath(t *testing.T) {
	items := []item{
		{kind: "worktree", cwd: "/repo", current: true},
		{kind: "worktree", cwd: "/repo/nested", current: true},
	}
	if !setCurrentWorktree(items, "/repo/nested/subdir") || items[0].current || !items[1].current {
		t.Fatalf("active tmux path did not choose deepest worktree: %#v", items)
	}
	for _, activePath := range []string{"", "/outside"} {
		items = []item{{kind: "worktree", cwd: "/repo", current: true}, {kind: "worktree", cwd: "/other"}}
		if setCurrentWorktree(items, activePath) || !items[0].current || items[1].current {
			t.Fatalf("unmatched tmux path %q did not preserve launch-directory fallback: %#v", activePath, items)
		}
	}
}

func TestAgentStatusDisplay(t *testing.T) {
	previousNerd := nerdFontEnabled
	defer func() { nerdFontEnabled = previousNerd }()
	nerdFontEnabled = true
	t.Setenv("JUMPMUX_PLAIN", "")
	started := time.Unix(0, 0)
	working := item{status: "working", updated: started}
	if got := statusText(working, started); got != dashboardIcon(workingIcon, "W")+" "+spinnerFrame(started) {
		t.Fatalf("working status = %q", got)
	}
	if got := statusText(working, started.Add(clockInterval)); got != dashboardIcon(workingIcon, "W")+" "+spinnerFrame(started.Add(clockInterval)) {
		t.Fatalf("next spinner frame = %q", got)
	}
	if got := statusText(working, started.Add(staleThreshold+time.Second)); got != dashboardIcon(workingIcon, "W")+" "+dashboardIcon(staleAgentIcon, "old") {
		t.Fatalf("stale status = %q", got)
	}
	if got := statusText(item{status: "done", updated: started}, started); got != doneIcon+" "+unseenAgentIcon {
		t.Fatalf("unseen status = %q", got)
	}
	if got := statusText(item{status: "done", seen: true, updated: started}, started); got != doneIcon {
		t.Fatalf("acknowledged status = %q", got)
	}
	if got := statusText(item{status: "question", updated: started}, started); got != questionIcon {
		t.Fatalf("question status = %q", got)
	}
	if strings.Contains(statusText(item{status: "done"}, started), "unseen") || strings.Contains(statusText(item{status: "done", seen: true}, started), "seen") {
		t.Fatal("status text includes an attention label")
	}
	nerdFontEnabled = false
	if got := statusText(item{status: "done"}, started); got != "D U" {
		t.Fatalf("plain unseen status = %q", got)
	}
	if got := statusText(item{status: "done", seen: true}, started); got != "D" {
		t.Fatalf("plain acknowledged status = %q", got)
	}
	if got := statusText(item{status: "question"}, started); got != "?" {
		t.Fatalf("plain question status = %q", got)
	}
}

func TestAgentStatusRejectsStaleSessionEvents(t *testing.T) {
	bin := t.TempDir()
	script := `#!/bin/sh
case "$1:$*" in
  display-message:*window_id*) printf '@2\n' ;;
  display-message:*) printf '4321\tpi\n' ;;
  list-panes:*jumpmux_pane_status*) : ;;
  list-panes:*) printf '%%7\0374321\037/repo\037Pi\037$1\037dev\037@2\037x\037pi\037\036\n' ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", "/tmp/stale-session,1,0")
	t.Setenv("TMUX_PANE", "%7")
	t.Setenv("JUMPMUX_STATE_DIR", t.TempDir())
	if err := setAgentStatus([]string{"started", "old", "", "/repo", "Pi"}); err != nil {
		t.Fatal(err)
	}
	if err := setAgentStatus([]string{"working", "old", "", "/repo", "Pi"}); err != nil {
		t.Fatal(err)
	}
	if err := setAgentStatus([]string{"started", "new", "", "/repo", "Pi"}); err != nil {
		t.Fatal(err)
	}
	if err := setAgentStatus([]string{"done", "old", "", "/repo", "Pi"}); err != nil {
		t.Fatal(err)
	}
	state, _, err := readAgentState("%7")
	if err != nil || state.SessionID != "new" || state.Status != "done" || !state.Seen {
		t.Fatalf("stale event overwrote new session: %#v, %v", state, err)
	}
	if err := setAgentStatus([]string{"closed", "old", "", "/repo", "Pi"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readAgentState("%7"); err != nil {
		t.Fatalf("stale close removed new session: %v", err)
	}
}

func TestAgentLockSerializesGlobalHookUpdates(t *testing.T) {
	t.Setenv("JUMPMUX_STATE_DIR", t.TempDir())
	t.Setenv("TMUX", "/tmp/jumpmux-lock,1,0")
	entered, release := make(chan struct{}), make(chan struct{})
	first := make(chan error, 1)
	go func() {
		first <- withAgentLock("hooks", "", func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	secondStarted, secondEntered, second := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		close(secondStarted)
		second <- withAgentLock("hooks", "", func() error {
			close(secondEntered)
			return nil
		})
	}()
	<-secondStarted
	select {
	case <-secondEntered:
		close(release)
		t.Fatal("global hook lock allowed concurrent updates")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
}

func TestTmuxStateIsServerNamespacedWithoutLegacyFallback(t *testing.T) {
	if got := shellArg("/tmp/state dir/it's"); got != "'/tmp/state dir/it'\"'\"'s'" {
		t.Fatalf("shell argument = %q", got)
	}
	stateDir := t.TempDir()
	t.Setenv("JUMPMUX_STATE_DIR", stateDir)
	t.Setenv("TMUX", "/tmp/tmux-one,1,0")
	first, err := agentStatePath("%7")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", "/tmp/tmux-two,1,0")
	second, err := agentStatePath("%7")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("state paths collide: %q", first)
	}
	legacy := filepath.Join(stateDir, "7.json")
	if err := atomicWrite(legacy, []byte(`{"pane":"%7","status":"done"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readAgentState("%7"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy state was read: %v", err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", "")
	if _, err := agentStatePath("%7"); err == nil || !strings.Contains(err.Error(), "server identity") {
		t.Fatalf("missing server identity = %v", err)
	}
}

func TestTmuxPaneParserPreservesTabsAndNewlines(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(`#!/bin/sh
printf '%%7\0374321\037/repo\tname\037title line 1\ntitle line 2\037$1\037dev\037@2\037window\037pi\037/worktree\036\n'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	panes, err := listTmuxPanes()
	if err != nil || len(panes) != 1 || panes[0].Path != "/repo\tname" || panes[0].Title != "title line 1\ntitle line 2" {
		t.Fatalf("pane parsing = %#v, %v", panes, err)
	}
}

func TestTmuxPaneParserFailsClosedOnMalformedRecord(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\nprintf '%b' '%7\\0374321\\037/repo\\036hidden\\037title\\037$1\\037dev\\037@2\\037window\\037pi\\037/worktree\\036\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := listTmuxPanes(); err == nil || !strings.Contains(err.Error(), "malformed pane record") {
		t.Fatalf("malformed tmux record was accepted: %v", err)
	}
	if err := validateWorktreeRemoval("/repo"); err == nil {
		t.Fatal("removal validation did not fail closed")
	}
}
