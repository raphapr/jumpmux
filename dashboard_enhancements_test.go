package main

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestLivePreviewFeedbackResumesFollowing(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height = 80, 20
	model.agents = []item{{kind: "session", target: "%1", cwd: "/repo"}}
	model.preview = previewData{target: "%1", kind: "session", followBottom: true, lines: make([]string, 30)}
	model.previewOffset = model.previewBottomOffset(model.preview.lines)
	if title := model.previewTitle(model.agents[0], "Preview"); strings.Contains(title, "FOLLOW") || strings.Contains(title, "PAUSED") {
		t.Fatalf("following preview title is noisy: %q", title)
	}
	model.scrollFocusedPanel(-1)
	if title := model.previewTitle(model.agents[0], "Preview"); !strings.Contains(title, "PAUSED") || !strings.Contains(title, "/30") {
		t.Fatalf("paused preview title = %q", title)
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	model = updated.(dashboardModel)
	if model.previewOffset != model.previewBottomOffset(model.preview.lines) || strings.Contains(model.previewTitle(model.agents[0], "Preview"), "PAUSED") {
		t.Fatalf("G did not resume following: %#v", model.preview)
	}
	model.scrollFocusedPanel(-1)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if got := updated.(dashboardModel).previewOffset; got != model.previewBottomOffset(model.preview.lines) {
		t.Fatalf("End did not resume following: %d", got)
	}
}

func TestSessionsUseSourceActivityLastAndRecentOrder(t *testing.T) {
	defer func() { nerdFontEnabled = true }()
	now := time.Now()
	model := newDashboard("/repo")
	nerdFontEnabled = false
	model.tab, model.width, model.height = tabSessions, 100, 20
	model.sessions = []item{
		{kind: "tmux-session", target: "configured", title: "configured", sessionSource: "config", lastAttached: now.Add(-time.Hour)},
		{kind: "tmux-session", target: "current", title: "current", muxSessionID: "$2", current: true, lastAttached: now.Add(-2 * time.Hour)},
		{kind: "tmux-session", target: "live", title: "live", muxSessionID: "$3", lastAttached: now},
	}
	for index, want := range []string{"C", "L", "L"} {
		if got, _ := sessionIcon(model.sessions[index]); got != want {
			t.Fatalf("session %d icon = %q, want %q", index, got, want)
		}
	}
	if view := ansi.Strip(model.renderTable(model.width)); !strings.Contains(view, "Last") || !strings.Contains(view, "C configured") {
		t.Fatalf("session icons/last column missing:\n%s", view)
	}
	model.index = 1
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	model = updated.(dashboardModel)
	if rows := model.rows(); rows[0].target != "live" {
		t.Fatalf("recent sessions = %#v", rows)
	}
	if selected, _ := model.selected(); selected.target != "current" {
		t.Fatalf("recent sort changed selection to %#v", selected)
	}
}

func TestPreviewFailedChecksAndWorktreeIndicators(t *testing.T) {
	defer func() { nerdFontEnabled = true }()
	nerdFontEnabled = false
	prs := parsePullRequests([]byte(`[{"number":42,"state":"OPEN","headRefName":"feature","statusCheckRollup":[{"name":"unit","conclusion":"FAILURE"},{"context":"lint","status":"ERROR"},{"name":"ok","conclusion":"SUCCESS"}]}]`))
	pr := prs["feature"][0]
	if pr.Check != checkFailure || strings.Join(pr.FailedChecks, ",") != "unit,lint" {
		t.Fatalf("failed checks = %#v", pr)
	}
	preview := worktreePreview(item{kind: "worktree", target: "/repo", cwd: "/repo", branch: "feature", prCheck: checkFailure, prFailedChecks: pr.FailedChecks, locked: true, prunable: true}, schemeDefault, 1)
	plain := ansi.Strip(strings.Join(preview.lines, "\n"))
	for _, want := range []string{"Failed checks: unit, lint", "LOCK", "PRUNE"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("preview missing %q:\n%s", want, plain)
		}
	}
	if line := preview.lines[len(preview.lines)-1]; !strings.Contains(line, dangerStyle.Render("Failed checks: ")) {
		t.Fatalf("worktree failed-check label is not emphasized: %q", line)
	}

	agent := item{kind: "session", target: "%1", cwd: "/repo", muxSessionName: "dev"}
	model := newDashboard("/repo")
	model.width, model.height = 80, 20
	model.agents = []item{agent}
	model.agentGit[agent.cwd] = item{gitLoaded: true, prCheck: checkFailure, prFailedChecks: pr.FailedChecks}
	model.preview = previewData{target: agent.target, kind: agent.kind, title: "Preview", lines: []string{"Session dev", failedChecksPreview(pr.FailedChecks), "Session captured pane output"}}
	raw := strings.Join(model.preview.lines, "\n")
	rendered := model.renderPreview(model.width)
	for _, style := range []string{mutedStyle.Render("Session "), dangerStyle.Render("Failed checks: ")} {
		if !strings.Contains(rendered, style) {
			t.Fatalf("agent preview missing styled label %q:\n%s", ansi.Strip(style), rendered)
		}
	}
	if got := strings.Join(model.preview.lines, "\n"); got != raw {
		t.Fatalf("rendering mutated raw preview lines: %q", got)
	}
	writeFakeTmux(t, "printf '1\\t4\\nfirst\\nsecond\\nthird\\nfourth\\n'")
	message := loadAgentPreview(item{kind: "session", target: "%1", pane: "%1", prCheck: checkFailure, prFailedChecks: pr.FailedChecks}, schemeDefault, 1)().(previewMsg)
	if !strings.Contains(ansi.Strip(strings.Join(message.lines, "\n")), "Failed checks: unit, lint") {
		t.Fatalf("agent preview omitted failed checks: %#v", message.lines)
	}
}

func TestActionMenuKeepsSelectionVisible(t *testing.T) {
	model := newDashboard("/repo")
	model.tab, model.actionMenu = tabWorktrees, true
	model.worktrees = []item{{kind: "worktree", cwd: "/repo", branch: "feature", prNumber: 42, prURL: "https://example.test/pr/42", prunable: true}}
	model.actionMenuIndex = slices.IndexFunc(model.actionMenuEntries(), func(entry actionMenuEntry) bool { return entry.action == menuCleanup })
	view := ansi.Strip(model.renderActionMenu(40, 5))
	if !strings.Contains(view, "  [x] Clean up stale record") || strings.Contains(view, "▌") {
		t.Fatalf("selected action is not visible without a marker:\n%s", view)
	}
}

func TestActionMenuUsesSidebarAndOmitsWorktreeCopyActions(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height, model.tab, model.actionMenu = 120, 24, tabWorktrees, true
	model.worktrees = []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main"}, {kind: "worktree", target: "/feature", cwd: "/feature", branch: "feature", baseBranch: "main", prNumber: 42, prURL: "https://example.test/pr/42"}}
	model.index = 1
	model.worktreesInFlight = true
	view := ansi.Strip(model.View())
	for _, expected := range []string{"WORKTREE ACTIONS", "feature", "/feature", "CREATE", "OPEN", "MANAGE", "[a] Add worktree", "Rebase onto main", "Merge into main", "Navigate · key/↵ Run · Esc"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("action sidebar missing %q:\n%s", expected, view)
		}
	}
	if strings.Contains(view, "refreshing") {
		t.Fatalf("action sidebar still shows refresh activity:\n%s", view)
	}
	if strings.Contains(view, "▌") {
		t.Fatalf("action sidebar uses a selection marker:\n%s", view)
	}
	for _, removed := range []string{"Copy path", "Copy branch"} {
		if strings.Contains(view, removed) {
			t.Fatalf("action sidebar still contains %q:\n%s", removed, view)
		}
	}
	if model.tableHeight() != model.contentHeight()+1 || !model.actionSidebar() {
		t.Fatalf("action sidebar geometry = table %d content %d sidebar %v", model.tableHeight(), model.contentHeight(), model.actionSidebar())
	}
	model.width = 80
	if model.actionSidebar() {
		t.Fatal("narrow action menu did not fall back to stacked layout")
	}
}

func TestAgentActionMenu(t *testing.T) {
	model := newDashboard("/repo")
	model.tab, model.actionMenu = tabAgents, true
	model.agents = []item{{kind: "session", target: "%7", pane: "%7", status: "done", agentSessionID: "session-id"}}
	entries := model.actionMenuEntries()
	labels := make([]string, len(entries))
	for index := range entries {
		labels[index] = entries[index].label
	}
	joined := strings.Join(labels, ",")
	if !strings.Contains(joined, "Mark seen") || strings.Contains(joined, "Fork") || strings.Contains(joined, "Prompt") {
		t.Fatalf("agent action menu = %s", joined)
	}
	if actionMenuGroup(menuMarkAgentSeen) != "Attention" {
		t.Fatalf("mark-seen action group = %q", actionMenuGroup(menuMarkAgentSeen))
	}

	model.actionMenuIndex = slices.IndexFunc(entries, func(entry actionMenuEntry) bool { return entry.action == menuMarkAgentSeen })
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(dashboardModel)
	if command == nil || model.action != actionRunning {
		t.Fatalf("mark-seen action did not start: action=%d command=%v", model.action, command != nil)
	}

	model = newDashboard("/repo")
	model.tab = tabAgents
	model.agents = []item{{kind: "session", target: "%7", pane: "%7", status: "working", agentSessionID: "session-id"}}
	for _, entry := range model.actionMenuEntries() {
		if entry.action == menuMarkAgentSeen || strings.Contains(entry.label, "Fork") {
			t.Fatalf("working agent exposed removed control action %#v", entry)
		}
	}
}

func TestDashboardErrorText(t *testing.T) {
	tests := []struct {
		name string
		tab  int
		err  string
		want string
	}{
		{"backend", tabWorktrees, "worktree_backend is wt but wt is not installed", "Install `wt` or select Git"},
		{"stale worktree", tabWorktrees, "the selected worktree changed; refresh and try again", "Worktree changed. Refresh and retry"},
		{"dirty merge", tabWorktrees, "cannot merge with uncommitted changes in ~/repo", "Commit or stash changes in ~/repo first"},
		{"wrong branch", tabWorktrees, "cannot merge: primary worktree is not on main", "Switch the primary worktree to main first"},
		{"open pane", tabWorktrees, "cannot remove worktree open in tmux pane %7", "Close tmux pane %7 before removing"},
		{"closed session", tabSessions, "the selected tmux session is no longer open", "Session closed. Refresh to update"},
		{"agent state", tabAgents, "agent state is unavailable: missing file", "Agent state unavailable. Refresh and retry"},
		{"tmux required", tabAgents, "run jumpmux inside tmux to jump to a pane", "Run jumpmux inside tmux"},
		{"agent panes", tabAgents, "tmux returned a malformed pane record", "Could not read tmux panes"},
		{"session panes", tabSessions, "tmux returned a malformed pane record", "Could not read tmux sessions"},
		{"fallback", tabAgents, "failed action", "Failed action"},
		{"brand", tabAgents, "tmux list-panes: failed", "tmux list-panes: failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := newDashboard("/repo")
			model.tab = test.tab
			if got := model.dashboardErrorText(errors.New(test.err)); got != test.want {
				t.Fatalf("dashboard error = %q, want %q", got, test.want)
			}
		})
	}
}

func TestActionMenuPausesAndResumesRefreshes(t *testing.T) {
	model := newDashboard("/repo")
	model.tab, model.actionMenu = tabWorktrees, true
	model.agentsInFlight, model.worktreesInFlight, model.sessionsInFlight = false, false, false
	model.agents = []item{{target: "old-agent"}}
	model.worktrees = []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main"}}
	model.sessions = []item{{target: "old-session"}}
	model.preview = previewData{request: 1, target: "/repo", kind: "worktree", lines: []string{"old preview"}}
	model.previewRequest = 1

	updated, command := model.Update(tickMsg(time.Now()))
	model = updated.(dashboardModel)
	if command == nil || model.agentsInFlight || model.worktreesInFlight || model.sessionsInFlight {
		t.Fatalf("paused tick launched refreshes: agents=%v worktrees=%v sessions=%v", model.agentsInFlight, model.worktreesInFlight, model.sessionsInFlight)
	}
	if _, command = model.Update(previewTickMsg(model.previewRequest)); command != nil {
		t.Fatal("paused action menu scheduled a live preview capture")
	}
	before := model.now
	updated, command = model.Update(clockMsg(before.Add(time.Second)))
	model = updated.(dashboardModel)
	if command == nil || !model.now.Equal(before) {
		t.Fatalf("paused action menu advanced the dashboard clock: before=%v after=%v", before, model.now)
	}

	updated, _ = model.Update(dashboardDataMsg(dashboardData{agents: []item{{target: "new-agent"}}}))
	model = updated.(dashboardModel)
	updated, _ = model.Update(sessionDataMsg{generation: model.sessionGeneration, sessions: []item{{target: "new-session"}}})
	model = updated.(dashboardModel)
	updated, _ = model.Update(worktreeDataMsg{stage: worktreeListStage, generation: model.worktreeGeneration, worktrees: []item{{target: "/new"}}})
	model = updated.(dashboardModel)
	updated, _ = model.Update(previewMsg(previewData{request: 1, target: "/repo", kind: "worktree", lines: []string{"new preview"}}))
	model = updated.(dashboardModel)
	if model.agents[0].target != "old-agent" || model.sessions[0].target != "old-session" || model.worktrees[0].target != "/repo" || model.preview.lines[0] != "old preview" {
		t.Fatalf("paused refresh mutated dashboard data: %#v", model)
	}

	updated, command = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(dashboardModel)
	if command == nil || model.actionMenu || !model.agentsInFlight || !model.worktreesInFlight || !model.sessionsInFlight || model.previewRequest == 1 {
		t.Fatalf("closing actions did not resume refreshes: menu=%v agents=%v worktrees=%v sessions=%v preview=%d", model.actionMenu, model.agentsInFlight, model.worktreesInFlight, model.sessionsInFlight, model.previewRequest)
	}
}

func TestActionMenuKeysAreUniqueVisibleAndDispatchDirectly(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TMUX", "/tmp/test,1,0")
	check := func(name string, makeModel func() dashboardModel) {
		t.Helper()
		entries := makeModel().actionMenuEntries()
		keys := map[string]bool{}
		for _, entry := range entries {
			if strings.Contains(entry.label, "Copy") {
				t.Fatalf("%s exposed copy action %#v", name, entry)
			}
			if entry.key == "" || entry.footer == "" || keys[entry.key] {
				t.Fatalf("%s action metadata = %#v", name, entries)
			}
			keys[entry.key] = true

			key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(entry.key)}
			if entry.key == "ctrl+r" {
				key = tea.KeyMsg{Type: tea.KeyCtrlR}
			}
			menu := makeModel()
			menu.actionMenu = true
			updated, menuCommand := menu.Update(key)
			menu = updated.(dashboardModel)

			direct := makeModel()
			updated, directCommand := direct.Update(key)
			direct = updated.(dashboardModel)
			if menu.actionMenu || direct.actionMenu || menu.action != direct.action || menu.diff != direct.diff || menu.chosen != direct.chosen || (menuCommand == nil) != (directCommand == nil) {
				t.Fatalf("%s key %q diverged: menu=%#v direct=%#v", name, entry.key, menu, direct)
			}
		}
	}
	worktree := func() dashboardModel {
		model := newDashboard("/repo")
		model.tab, model.width = tabWorktrees, 40
		model.worktrees = []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main"}, {kind: "worktree", target: "/feature", cwd: "/feature", branch: "feature", prNumber: 1, prURL: "https://example.test/pr/1"}}
		model.index = 1
		return model
	}
	check("worktree", worktree)
	check("prunable worktree", func() dashboardModel {
		model := newDashboard("/repo")
		model.tab = tabWorktrees
		model.worktrees = []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main"}, {kind: "worktree", target: "/gone", cwd: "/gone", branch: "gone", prunable: true}}
		model.index = 1
		return model
	})
	if view := ansi.Strip(func() string { model := worktree(); model.actionMenu = true; return model.renderActionMenu(40, 12) }()); !strings.Contains(view, "[a] Add worktree") || strings.Contains(view, "Open new window") || strings.Contains(view, "Run worktree command") {
		t.Fatalf("unexpected narrow action menu:\n%s", view)
	}
	check("agent", func() dashboardModel {
		model := newDashboard("/repo")
		model.tab = tabAgents
		model.agents = []item{{kind: "session", target: "%1", pane: "%1", cwd: "/repo", status: "done", agentSessionID: "session-id", muxWindowName: "feature", prNumber: 1, prURL: "https://example.test/pr/1"}}
		return model
	})
	check("session", func() dashboardModel {
		model := newDashboard("/repo")
		model.tab = tabSessions
		model.sessions = []item{{kind: "tmux-session", target: "dev", title: "dev", muxSessionID: "$1"}}
		return model
	})
	check("empty sessions", func() dashboardModel {
		model := newDashboard("/repo")
		model.tab = tabSessions
		return model
	})
}

func TestSessionActionMenuDefaultsToOpen(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height, model.tab = 80, 20, tabSessions
	model.sessions = []item{{kind: "tmux-session", target: "dev", title: "dev", muxSessionID: "$1"}}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	model = updated.(dashboardModel)
	if !model.actionMenu || model.actionMenuEntries()[0].action != menuOpen {
		t.Fatalf("Session action menu does not default to Open: %#v", model.actionMenuEntries())
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(dashboardModel)
	if !model.chosen || model.selection.target != "dev" {
		t.Fatalf("Space then Enter did not open selected Session: %#v", model)
	}
}

func TestPreviousSessionActionRunsWithoutSelection(t *testing.T) {
	writeFakeTmux(t, `
case "$1" in
  list-sessions) printf '$1\037current\037300\036\n$2\037previous\037200\036\n' ;;
esac
`)
	t.Setenv("TMUX", "/tmp/test,1,0")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	model := newDashboard("/repo")
	model.tab = tabSessions
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	model = updated.(dashboardModel)
	if command == nil || model.action != actionRunning {
		t.Fatalf("previous session action = %#v, command=%v", model, command != nil)
	}
	message, ok := command().(worktreeActionMsg)
	if !ok || message.err != nil || message.notice != "Opened previous session" {
		t.Fatalf("previous session result = %#v", message)
	}
}

func TestPrunableCleanupAndErrorPersistence(t *testing.T) {
	model := newDashboard("/repo")
	model.tab = tabWorktrees
	model.worktrees = []item{{kind: "worktree", target: "/repo", cwd: "/repo", current: true}, {kind: "worktree", target: "/gone", cwd: "/gone", branch: "gone", prunable: true}}
	model.index = 1
	entries := model.actionMenuEntries()
	if !strings.Contains(strings.Join(func() []string {
		labels := make([]string, len(entries))
		for i := range entries {
			labels[i] = entries[i].label
		}
		return labels
	}(), ","), "Clean up stale record") {
		t.Fatalf("prunable worktree has no cleanup action: %#v", entries)
	}
	model.err = errors.New("failed action")
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	model = updated.(dashboardModel)
	if model.err == nil {
		t.Fatal("navigation cleared action error")
	}
	if footer := ansi.Strip(model.renderFooter(80)); strings.Contains(footer, "Error:") || !strings.Contains(footer, "Failed action") || !strings.Contains(footer, "Esc Dismiss") {
		t.Fatalf("persistent error footer = %q", footer)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.(dashboardModel).err != nil {
		t.Fatal("Esc did not clear action error")
	}
}
