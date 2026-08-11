package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

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
		worktrees:  []item{{target: "/other", branch: "feature", prLoaded: true, prNumber: 42, prState: "OPEN", prCheck: checkFailure}},
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

func TestWorktreeGitRefreshStreamsRows(t *testing.T) {
	command := refreshWorktreeGit([]item{{target: "/repo", cwd: "/repo"}, {target: "/feature", cwd: "/feature"}}, 1)
	message := command()
	batch, ok := message.(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("worktree Git refresh = %#v, want one command per row", message)
	}
}

func TestWorktreePreviewRendersMetadataBeforeGitCommands(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height, model.tab = 100, 30, 1
	updated, command := model.Update(worktreeDataMsg{
		stage:      worktreeListStage,
		generation: model.worktreeGeneration,
		worktrees:  []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main"}, {kind: "worktree", target: "/feature", cwd: "/feature", branch: "feature"}},
	})
	model = updated.(dashboardModel)
	if command == nil || model.preview.target != "/repo" || model.preview.title != "repo" || model.preview.rightTitle != "Git Log" || len(model.preview.lines) != worktreeMetadataRows {
		t.Fatalf("worktree metadata was not rendered immediately: %#v", model.preview)
	}
	if model.worktreePending != 4 {
		t.Fatalf("worktree refresh pending count = %d, want 4", model.worktreePending)
	}

	updated, command = model.Update(worktreeDataMsg{
		stage:      worktreeGitStage,
		generation: model.worktreeGeneration,
		worktrees:  []item{{target: "/feature", cwd: "/feature", branch: "feature", gitLoaded: true}},
	})
	if command != nil {
		t.Fatal("unselected worktree Git update refreshed the preview")
	}
	model = updated.(dashboardModel)
	updated, _ = model.Update(worktreeStatusMsg{request: model.previewRequest, scheme: model.scheme, target: "/repo", status: " M changed.go"})
	model = updated.(dashboardModel)
	updated, _ = model.Update(worktreeLogMsg{request: model.previewRequest, scheme: model.scheme, target: "/repo", log: "abc123\t1 minute ago\tmessage"})
	preview := updated.(dashboardModel).preview
	if !strings.Contains(strings.Join(preview.lines, "\n"), "M changed.go") || len(preview.rightLines) != 1 || preview.rightLines[0] != "abc123\t1 minute ago\tmessage" {
		t.Fatalf("worktree preview did not apply independent Git results: %#v", preview)
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
	for _, expected := range []string{"[Agents 2 · all] │ Worktrees 1", "# Project", "Git", dashboardIcon(gitDiffIcon, "*"), "+12", "-3", "PR", "#42 " + dashboardIcon(prOpenIcon, "O") + " " + dashboardIcon(checkFailureIcon, "x"), "Status", doneIcon, "Time", "Title", "┌─ ▶ Preview: repo ", "↵ Open", "? Help"} {
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
				t.Fatalf("current project row missing highlight: %q", line)
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
	styledIcon := accentStyle.Render(dashboardIcon(gitDiffIcon, "*"))

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

func TestDashboardTextInputsAndGlobalQuit(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height = 40, 10
	model.agents = []item{{kind: "session", target: "one", cwd: "/repo"}, {kind: "session", target: "two", cwd: "/other"}}
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	model = updated.(dashboardModel)
	if !model.filter || !model.filterInputs[0].Focused() || command == nil {
		t.Fatalf("filter input was not focused: %#v", model.filterInputs[0])
	}
	updated, _ = model.Update(cursor.Blink())
	model = updated.(dashboardModel)
	if !model.filterInputs[0].Focused() {
		t.Fatal("text input did not receive its cursor message")
	}
	for _, key := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("éab")}, {Type: tea.KeyLeft}, {Type: tea.KeyDelete}} {
		updated, _ = model.Update(key)
		model = updated.(dashboardModel)
	}
	if model.query != "éa" || model.filterInputs[0].Position() != 2 {
		t.Fatalf("text input did not handle Unicode cursor/delete: %q at %d", model.query, model.filterInputs[0].Position())
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(dashboardModel)
	if model.filter || model.filterInputs[0].Focused() || model.queries[0] != "éa" {
		t.Fatalf("filter did not blur and persist: %#v", model)
	}

	for _, state := range []func(*dashboardModel){
		func(m *dashboardModel) { m.help = true },
		func(m *dashboardModel) { m.filter = true },
		func(m *dashboardModel) { m.action = actionRunning },
		func(m *dashboardModel) { m.diff = true },
	} {
		candidate := model
		state(&candidate)
		_, command = candidate.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		if command == nil {
			t.Fatal("Ctrl+C was not handled globally")
		}
	}
}

func TestDashboardClipboardPasteRefreshesFilterPreview(t *testing.T) {
	previous, _ := clipboard.ReadAll()
	defer func() { _ = clipboard.WriteAll(previous) }()
	if err := clipboard.WriteAll("two"); err != nil {
		t.Skipf("clipboard unavailable: %v", err)
	}
	model := newDashboard("/repo")
	model.width, model.height, model.filter = 100, 30, true
	model.agents = []item{{kind: "session", target: "one", title: "one", cwd: "/repo"}, {kind: "session", target: "two", title: "two", cwd: "/repo"}}
	model.preview = previewData{target: "one", lines: []string{"old"}}
	model.filterInputs[0].SetValue("")
	model.filterInputs[0].Focus()
	updated, command := model.Update(textinput.Paste())
	model = updated.(dashboardModel)
	if model.query != "two" || model.index != 0 || !model.loading || model.previewRequest == 0 || command == nil {
		t.Fatalf("pasted filter did not refresh selection: query=%q index=%d loading=%v request=%d", model.query, model.index, model.loading, model.previewRequest)
	}
}

func TestDashboardBlocksModalMouseAndClearsDoubleClick(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height = 100, 30
	model.agents = []item{{kind: "session", target: "one", cwd: "/repo"}, {kind: "session", target: "two", cwd: "/other"}}
	click := tea.MouseMsg{X: 5, Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
	for _, state := range []func(*dashboardModel){
		func(m *dashboardModel) { m.filter = true },
		func(m *dashboardModel) { m.help = true },
		func(m *dashboardModel) { m.action = actionRunning },
	} {
		candidate := model
		state(&candidate)
		updated, _ := candidate.Update(click)
		if got := updated.(dashboardModel).index; got != 0 {
			t.Fatalf("modal mouse changed selection to %d", got)
		}
	}
	model.tab = 1
	model.worktrees = []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main"}, {kind: "worktree", target: "/feature", cwd: "/feature", branch: "feature"}}
	model.index, model.lastClickTarget, model.lastClickAt = 1, "/feature", time.Now()
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = updated.(dashboardModel)
	if model.lastClickTarget != "" || !model.lastClickAt.IsZero() {
		t.Fatal("opening removal confirmation retained double-click state")
	}
}

func TestDashboardPanelFocusAndHelpScroll(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height, model.tab = 120, 20, 1
	model.worktrees = []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main"}}
	model.preview = previewData{target: "/repo", title: "Status", lines: make([]string, 30), rightTitle: "Git Log", rightLines: make([]string, 30)}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyShiftRight})
	model = updated.(dashboardModel)
	if model.panelFocus != panelRight || !strings.Contains(ansi.Strip(model.View()), "▶ Git Log") {
		t.Fatalf("right preview panel was not structurally focused: %#v", model)
	}
	model.diff, model.preview = true, previewData{target: "/repo", title: "WIP", lines: make([]string, 30), rightLines: make([]string, 30)}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	model = updated.(dashboardModel)
	if model.rightOffset != 1 || model.diffOff != 0 {
		t.Fatalf("diff Files panel shared the left offset: %#v", model)
	}
	updated, _ = model.Update(tea.MouseMsg{X: 119, Y: 4, Button: tea.MouseButtonWheelDown})
	model = updated.(dashboardModel)
	if model.panelFocus != panelRight || model.rightOffset < 4 {
		t.Fatalf("mouse wheel did not choose right panel: %#v", model)
	}
	updated, _ = model.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
	if got := updated.(dashboardModel).panelFocus; got != panelLeft {
		t.Fatalf("narrow layout focus = %d", got)
	}

	model = newDashboard("/repo")
	model.width, model.height, model.help = 40, 10, true
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	model = updated.(dashboardModel)
	if model.helpOffset == 0 || !strings.Contains(ansi.Strip(model.View()), "Help (") || !strings.Contains(ansi.Strip(model.View()), "q Quit") {
		t.Fatalf("small help did not scroll with close guidance: %#v\n%s", model, model.View())
	}
	updated, _ = model.Update(tea.MouseMsg{X: 5, Y: 4, Button: tea.MouseButtonWheelDown})
	if got := updated.(dashboardModel).helpOffset; got <= model.helpOffset {
		t.Fatalf("help mouse wheel did not scroll: %d", got)
	}
}

func TestDashboardDiffOffsetsAndMinimumLayout(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height, model.previewSize = 40, 10, 10
	model.agentsLoaded = true
	if lines := strings.Split(model.View(), "\n"); len(lines) != 10 {
		t.Fatalf("40x10 view rendered %d lines", len(lines))
	}

	model.width, model.height, model.diff = 100, 20, true
	model.preview = previewData{target: "/repo", lines: make([]string, 30)}
	model.scrollFocusedPanel(100)
	if model.diffOff != 13 {
		t.Fatalf("diff offset = %d, want 13", model.diffOff)
	}
	model.scrollFocusedPanel(-1)
	if model.diffOff != 12 {
		t.Fatalf("diff upward scroll stalled at %d", model.diffOff)
	}

	model.width, model.height, model.diff, model.action = 40, 10, false, actionAddWorktree
	footer := ansi.Strip(model.renderFooter(40))
	if !strings.Contains(footer, "↵ Create") || !strings.Contains(footer, "Esc Cancel") || ansi.StringWidth(footer) != 40 {
		t.Fatalf("minimum-width add footer = %q", footer)
	}
}

func TestDashboardFooterAndHeaderWidths(t *testing.T) {
	for _, width := range []int{40, 60, 100, 120} {
		model := newDashboard("/repo")
		model.width, model.height, model.tab = width, 20, 1
		model.worktrees = []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main", prNumber: 42}}
		footer := ansi.Strip(model.renderFooter(width))
		for _, expected := range []string{"? Help", "q Quit"} {
			if !strings.Contains(footer, expected) {
				t.Fatalf("width %d footer missing %q: %s", width, expected, footer)
			}
		}
		if width >= 60 && !strings.Contains(footer, "d Diff") {
			t.Fatalf("width %d footer missing d Diff: %s", width, footer)
		}
		if width >= 100 && !strings.Contains(footer, "o PR") {
			t.Fatalf("width %d footer missing o PR: %s", width, footer)
		}
		if ansi.StringWidth(footer) != width {
			t.Fatalf("width %d footer width = %d", width, ansi.StringWidth(footer))
		}
		model.err = errors.New("something went wrong")
		footer = ansi.Strip(model.renderFooter(width))
		if !strings.Contains(footer, "? Help") || !strings.Contains(footer, "q Quit") {
			t.Fatalf("width %d error footer lost Help/Quit: %s", width, footer)
		}
	}

	model := newDashboard("/repo")
	model.width, model.height = 100, 20
	model.agents = []item{{kind: "session", target: "one", cwd: "/repo"}}
	hitboxes := model.tabHitboxes()
	updated, _ := model.Update(tea.MouseMsg{X: 0, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if updated.(dashboardModel).tab != 0 {
		t.Fatal("blank header space switched tabs")
	}
	updated, _ = model.Update(tea.MouseMsg{X: hitboxes[1][0], Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if updated.(dashboardModel).tab != 1 {
		t.Fatal("tab hitbox did not use rendered label")
	}
}
