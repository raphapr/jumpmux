package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

func TestWorktreeCurrentMarkerReplacesStaleAsyncState(t *testing.T) {
	current := []item{
		{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main", current: true},
		{kind: "worktree", target: "/repo/nested", cwd: "/repo/nested", branch: "nested", current: true},
	}
	fresh := []item{
		{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main", current: true},
		{kind: "worktree", target: "/repo/nested", cwd: "/repo/nested", branch: "nested"},
	}
	merged := mergeWorktreeData(current, fresh, worktreeListStage)
	if merged[0].current != true || merged[1].current {
		t.Fatalf("worktree list accumulated stale current markers: %#v", merged)
	}
	attachAgentsToWorktrees(merged, []item{{cwd: "/repo/nested", current: true, title: "agent"}})
	if merged[0].sessionTitle != "" || merged[1].sessionTitle != "agent" || merged[1].current {
		t.Fatalf("nested agent was not attached only to its deepest worktree: %#v", merged)
	}
}

func TestWorktreeRepresentativeAgentPriority(t *testing.T) {
	now := time.Now()
	items := []item{{kind: "worktree", cwd: "/repo"}}
	attachAgentsToWorktrees(items, []item{
		{cwd: "/repo", title: "older working", status: "working", updated: now.Add(-time.Minute)},
		{cwd: "/repo", title: "newer done", status: "done", updated: now},
	})
	if items[0].sessionTitle != "older working" {
		t.Fatalf("done agent replaced working representative: %#v", items[0])
	}
	attachAgentsToWorktrees(items, []item{
		{cwd: "/repo", title: "working", status: "working", updated: now},
		{cwd: "/repo", title: "current done", status: "done", current: true, updated: now.Add(-time.Hour)},
	})
	if items[0].sessionTitle != "current done" {
		t.Fatalf("current agent did not win representative priority: %#v", items[0])
	}
}

func TestWorktreeAgentRefreshPreservesErrorsAndClearsSuccessfulEmptyResults(t *testing.T) {
	now := time.Now()
	winner := item{
		kind: "session", cwd: "/repo", title: "winner", status: "working", updated: now,
		pane: "%2", muxSessionID: "$2", muxSessionName: "dev", muxWindowID: "@2", muxWindowName: "code",
	}
	model := newDashboard("/repo")
	model.tab, model.width, model.height = tabWorktrees, 100, 20
	model.worktrees = []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main"}}
	updated, _ := model.Update(dashboardDataMsg(dashboardData{agents: []item{
		{kind: "session", cwd: "/repo", title: "newer done", status: "done", updated: now.Add(time.Minute), pane: "%1"},
		winner,
	}}))
	model = updated.(dashboardModel)
	got := model.worktrees[0]
	if got.sessionTitle != winner.title || got.status != winner.status || !got.updated.Equal(winner.updated) || got.pane != winner.pane || got.muxSessionID != winner.muxSessionID || got.muxSessionName != winner.muxSessionName || got.muxWindowID != winner.muxWindowID || got.muxWindowName != winner.muxWindowName {
		t.Fatalf("representative fields came from different agents: %#v", got)
	}

	updated, _ = model.Update(dashboardDataMsg(dashboardData{err: errors.New("agents unavailable")}))
	model = updated.(dashboardModel)
	if got := model.worktrees[0]; got.sessionTitle != winner.title || got.pane != winner.pane {
		t.Fatalf("transient agent error cleared representative: %#v", got)
	}
	row := model.tableRow(model.worktrees[0], 1, model.width, model.columns(model.width, model.rows()))
	if !strings.Contains(ansi.Strip(row), dashboardIcon("󰅙", "!")) {
		t.Fatalf("agent error marker missing from Worktrees row: %q", row)
	}

	updated, _ = model.Update(dashboardDataMsg(dashboardData{}))
	got = updated.(dashboardModel).worktrees[0]
	if got.sessionTitle != "" || got.status != "" || !got.updated.IsZero() || got.pane != "" || got.muxSessionID != "" || got.muxSessionName != "" || got.muxWindowID != "" || got.muxWindowName != "" {
		t.Fatalf("successful empty agent refresh retained stale fields: %#v", got)
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
	for _, expected := range []string{"[Agents 2 · all] │ Worktrees 1", "# Project", "Git", dashboardIcon(gitDiffIcon, "*"), "+12", "-3", "PR", "#42 " + dashboardIcon(checkFailureIcon, "x"), "Status", dashboardIcon(doneIcon, "D"), "Time", "Title", "┌─ ▶ Preview: repo ", "↵ Open", "? Help"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("dashboard missing %q:\n%s", expected, plain)
		}
	}
	if strings.Contains(plain, "working") || strings.Contains(plain, "done") {
		t.Fatalf("dashboard status includes redundant text:\n%s", plain)
	}
	lines := strings.Split(view, "\n")
	for _, line := range lines {
		if strings.Contains(ansi.Strip(line), "Current project") && !strings.Contains(ansi.Strip(line), "▏") {
			t.Fatalf("current project row is missing its subtle marker: %q", line)
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

func TestCurrentRowsUseOnlySubtleMarker(t *testing.T) {
	for _, tab := range []int{tabAgents, tabWorktrees, tabSessions} {
		model := newDashboard("/repo")
		model.tab, model.width, model.height, model.index = tab, 100, 20, 1
		base := item{cwd: "/repo", title: "row", branch: "main", gitLoaded: true}
		switch tab {
		case tabAgents:
			base.kind, base.target = "session", "%1"
			model.agents = []item{base}
		case tabWorktrees:
			base.kind, base.target = "worktree", "/repo"
			model.worktrees = []item{base}
		case tabSessions:
			base.kind, base.target, base.muxSessionID = "tmux-session", "dev", "$1"
			model.sessions = []item{base}
		}
		columns := model.columns(model.width, model.rows())
		ordinary := model.tableRow(base, 0, model.width, columns)
		base.current = true
		current := model.tableRow(base, 0, model.width, columns)
		if !strings.Contains(ansi.Strip(current), "▏") || ansi.Strip(current)[len("▏ "):] != ansi.Strip(ordinary)[len("  "):] {
			t.Fatalf("tab %d current row changed beyond its marker: ordinary=%q current=%q", tab, ordinary, current)
		}
		model.index = 0
		selected := model.tableRow(base, 0, model.width, columns)
		if !strings.Contains(ansi.Strip(selected), "▌") || strings.Contains(ansi.Strip(selected), "▏") {
			t.Fatalf("tab %d selected current row did not prefer selection marker: %q", tab, selected)
		}
	}
}

func TestAgentsAndWorktreesRenderCompactAndRichGitPR(t *testing.T) {
	previousNerd, previousProfile := nerdFontEnabled, lipgloss.ColorProfile()
	defer func() {
		nerdFontEnabled = previousNerd
		lipgloss.SetColorProfile(previousProfile)
	}()
	nerdFontEnabled = false
	t.Setenv("JUMPMUX_PLAIN", "1")
	lipgloss.SetColorProfile(0)

	now := time.Now()
	git := item{gitLoaded: true, baseBranch: "release", committedAdded: 5, committedRemoved: 2, dirty: true, added: 3, removed: 1, hasConflict: true, isRebasing: true, ahead: 2, behind: 1, prNumber: 42, prState: "OPEN", prCheck: checkFailure}
	for _, test := range []struct {
		item item
		want string
	}{
		{item{}, "-"},
		{item{prNumber: 1, prState: "OPEN"}, "#1"},
		{item{prNumber: 2, prState: "OPEN", prCheck: checkFailure}, "#2 x"},
		{item{prNumber: 3, prDraft: true, prCheck: checkSuccess}, "#3 D +"},
		{item{prNumber: 4, prState: "MERGED", prCheck: checkFailure}, "#4 M"},
		{item{prNumber: 5, prState: "CLOSED", prCheck: checkFailure}, "#5 X"},
	} {
		if got := compactPRText(test.item, now); got != test.want {
			t.Errorf("compactPRText(%#v) = %q, want %q", test.item, got, test.want)
		}
	}

	agentModel := newDashboard("/repo")
	agentModel.width, agentModel.height = 140, 20
	agent := item{kind: "session", target: "%1", cwd: "/repo", title: "agent"}
	agentModel.agents = []item{agent}
	agentModel.agentGit[agent.cwd] = git
	agentRow := agentModel.tableRow(agent, 1, agentModel.width, agentModel.columns(agentModel.width, agentModel.rows()))

	worktreeModel := newDashboard("/repo")
	worktreeModel.width, worktreeModel.height, worktreeModel.tab = 140, 20, tabWorktrees
	worktree := git
	worktree.kind, worktree.target, worktree.cwd, worktree.branch = "worktree", "/repo", "/repo", "feature"
	worktreeModel.worktrees = []item{worktree}
	worktreeRow := worktreeModel.tableRow(worktree, 1, worktreeModel.width, worktreeModel.columns(worktreeModel.width, worktreeModel.rows()))

	agentPlain, worktreePlain := ansi.Strip(agentRow), ansi.Strip(worktreeRow)
	for _, unexpected := range []string{"→release", "+5", "-2", "#42 O"} {
		if strings.Contains(agentPlain, unexpected) {
			t.Errorf("Agents row retained rich detail %q: %q", unexpected, agentPlain)
		}
	}
	for _, expected := range []string{"R", "*", "+3", "-1", "!", "↑2", "↓1", "#42 x"} {
		if !strings.Contains(agentPlain, expected) {
			t.Errorf("Agents row missing compact detail %q: %q", expected, agentPlain)
		}
	}
	for _, expected := range []string{"→release", "+5", "-2", "#42 O x"} {
		if !strings.Contains(worktreePlain, expected) {
			t.Errorf("Worktrees row lost rich detail %q: %q", expected, worktreePlain)
		}
	}

	failureModel := newDashboard("/repo")
	failureModel.width, failureModel.height = 140, 20
	failureModel.agents = []item{agent}
	failureModel.agentGit[agent.cwd] = item{gitLoaded: true, prNumber: 42, prState: "OPEN", prCheck: checkFailure}
	failureRow := failureModel.tableRow(agent, 1, failureModel.width, failureModel.columns(failureModel.width, failureModel.rows()))
	failureToken := dangerStyle.Render(dashboardIcon(checkFailureIcon, "x"))
	if strings.Count(failureRow, failureToken) != 1 || strings.Contains(failureRow, dangerStyle.Render("#42")) {
		t.Fatalf("failed CI danger styling was not confined to its PR token: %q", failureRow)
	}
}

func TestWorktreeAgentCellStatesAndPlainIcons(t *testing.T) {
	previousNerd := nerdFontEnabled
	defer func() { nerdFontEnabled = previousNerd }()
	nerdFontEnabled = true
	t.Setenv("JUMPMUX_PLAIN", "1")
	now := time.Now()
	for _, test := range []struct {
		name   string
		item   item
		loaded bool
		err    error
		want   string
	}{
		{name: "loading", want: spinnerFrame(now)},
		{name: "error", loaded: true, err: errors.New("unavailable"), want: "!"},
		{name: "empty", loaded: true, want: "-"},
		{name: "working", item: item{sessionTitle: "agent", status: "working", updated: now}, loaded: true, want: "W " + spinnerFrame(now) + " agent"},
		{name: "done", item: item{sessionTitle: "agent", status: "done", updated: now}, loaded: true, want: "D agent"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := ansi.Strip(agentSummaryCell(test.item, 20, now, nil, test.loaded, test.err))
			if !strings.Contains(got, test.want) {
				t.Fatalf("Agent cell = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAgentRowsFitNarrowWidthsWithNerdAndPlainIcons(t *testing.T) {
	previousNerd := nerdFontEnabled
	defer func() { nerdFontEnabled = previousNerd }()
	for _, test := range []struct {
		name  string
		plain string
	}{
		{name: "nerd"},
		{name: "plain", plain: "1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			nerdFontEnabled = true
			t.Setenv("JUMPMUX_PLAIN", test.plain)
			model := newDashboard("/repo")
			model.width, model.height = 40, 12
			agent := item{kind: "session", target: "%1", cwd: "/repo", title: "agent"}
			model.agents = []item{agent}
			model.agentGit[agent.cwd] = item{gitLoaded: true, dirty: true, added: 1, prNumber: 42, prState: "OPEN", prCheck: checkFailure}
			rows := model.rows()
			columns := model.columns(model.width, rows)
			for _, line := range []string{model.tableHeader(model.width, columns), model.tableRow(rows[0], 1, model.width, columns)} {
				if got := ansi.StringWidth(line); got != model.width {
					t.Fatalf("narrow %s line width = %d, want %d: %q", test.name, got, model.width, line)
				}
			}
		})
	}
}

func TestAgentFilterAndPreviewIncludeSessionMetadata(t *testing.T) {
	model := newDashboard("/repo")
	model.agents = []item{{kind: "session", target: "%1", cwd: "/repo", title: "keep title", muxSessionName: "dev"}}
	model.query = "dev"
	if rows := model.rows(); len(rows) != 1 || rows[0].title != "keep title" {
		t.Fatalf("agent session filter changed Title or excluded row: %#v", rows)
	}
	model.tab = tabWorktrees
	model.worktrees = []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main", muxSessionName: "dev"}}
	if rows := model.rows(); len(rows) != 0 {
		t.Fatalf("worktree filter matched tmux session metadata: %#v", rows)
	}
	writeFakeTmux(t, "printf '1\\t1\\noutput\\n'")
	preview := loadAgentPreview(model.agents[0], schemeDefault, 1)().(previewMsg)
	if plain := ansi.Strip(strings.Join(preview.lines, "\n")); !strings.Contains(plain, "Session dev") || strings.Contains(preview.title, "dev") {
		t.Fatalf("agent preview session metadata = title %q lines %q", preview.title, plain)
	}
}

func TestAgentAndWorktreeIdentityTextStaysNeutral(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(0)
	defer lipgloss.SetColorProfile(previousProfile)
	applyColorScheme(schemeDefault)

	now := time.Now()
	agentModel := newDashboard("/repo")
	agentModel.width, agentModel.height, agentModel.index = 100, 20, 1
	agent := item{kind: "session", target: "%1", cwd: "/repo", title: "agent-title", status: "done", updated: now.Add(-staleThreshold - time.Minute)}
	agentModel.agents = []item{agent}
	agentModel.agentGit[agent.cwd] = item{gitLoaded: true}
	agentRow := agentModel.tableRow(agent, 0, agentModel.width, agentModel.columns(agentModel.width, agentModel.rows()))
	neutralPrefix := strings.SplitN(textStyle.Render("probe"), "probe", 2)[0]
	mutedPrefix := strings.SplitN(mutedStyle.Render("probe"), "probe", 2)[0]
	successPrefix := strings.SplitN(successStyle.Render("probe"), "probe", 2)[0]
	if !strings.Contains(agentRow, neutralPrefix+agent.title) || strings.Contains(agentRow, mutedPrefix+agent.title) || strings.Contains(agentRow, successPrefix+agent.title) {
		t.Fatalf("Agent identity is not neutral: %q", agentRow)
	}

	worktreeModel := newDashboard("/repo")
	worktreeModel.width, worktreeModel.height, worktreeModel.index, worktreeModel.tab = 100, 20, 1, tabWorktrees
	worktreeModel.agentsLoaded = true
	worktree := item{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main", gitLoaded: true, sessionTitle: "worktree-agent", status: "done", updated: now.Add(-staleThreshold - time.Minute), muxWindowID: "@1"}
	worktreeModel.worktrees = []item{worktree}
	worktreeRow := worktreeModel.tableRow(worktree, 0, worktreeModel.width, worktreeModel.columns(worktreeModel.width, worktreeModel.rows()))
	if !strings.Contains(worktreeRow, neutralPrefix+" "+worktree.sessionTitle) || strings.Contains(worktreeRow, mutedPrefix+" "+worktree.sessionTitle) || strings.Contains(worktreeRow, successPrefix+" "+worktree.sessionTitle) {
		t.Fatalf("Worktree Agent identity is not neutral: %q", worktreeRow)
	}
	infoPrefix := strings.SplitN(infoStyle.Render("probe"), "probe", 2)[0]
	if strings.Contains(worktreeRow, infoPrefix+"●") || !strings.Contains(worktreeRow, mutedPrefix+"●") {
		t.Fatalf("Worktree Mux marker is not muted: %q", worktreeRow)
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

func TestDashboardBlocksNonSearchModalMouseAndClearsDoubleClick(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height = 100, 30
	model.agents = []item{{kind: "session", target: "one", cwd: "/repo"}, {kind: "session", target: "two", cwd: "/other"}}
	click := tea.MouseMsg{X: 5, Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
	for _, state := range []func(*dashboardModel){
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

func TestDashboardCyclesSessionsTab(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height = 80, 20
	model.agents = []item{{kind: "session", target: "%1", cwd: "/repo"}}
	model.worktrees = []item{{kind: "worktree", target: "/repo", cwd: "/repo"}}
	model.sessions = []item{{kind: "tmux-session", target: "dev", title: "dev", cwd: "/repo", sessionSource: "config"}, {kind: "tmux-session", target: "ops", title: "ops", cwd: "/ops", sessionSource: "config"}}
	for _, tab := range []int{tabWorktrees, tabSessions} {
		updated, _ := model.switchTab(tab)
		model = updated.(dashboardModel)
		if model.tab != tab {
			t.Fatalf("tab = %d, want %d", model.tab, tab)
		}
	}
	model.index = 1
	updated, _ := model.switchTab(tabAgents)
	model = updated.(dashboardModel)
	updated, _ = model.switchTab(tabSessions)
	model = updated.(dashboardModel)
	if model.index != 1 || model.selectedTarget() != "ops" {
		t.Fatalf("session selection was not restored: index=%d target=%q", model.index, model.selectedTarget())
	}
	if labels := model.tabLabels(); len(labels) != tabCount {
		t.Fatalf("tab labels = %#v", labels)
	}
}

func TestSessionFilters(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height, model.tab = 100, 20, tabSessions
	model.sessions = []item{
		{kind: "tmux-session", target: "live", title: "live", muxSessionID: "$1"},
		{kind: "tmux-session", target: "configured", title: "configured", sessionSource: "config"},
		{kind: "tmux-session", target: "discovered", title: "discovered", sessionSource: "discovered"},
	}
	for filter, want := range map[sessionFilter][]string{
		sessionFilterAll:        {"live", "configured", "discovered"},
		sessionFilterLive:       {"live"},
		sessionFilterInactive:   {"configured", "discovered"},
		sessionFilterConfigured: {"configured"},
		sessionFilterDiscovered: {"discovered"},
	} {
		model.sessionFilter = filter
		rows := model.rows()
		got := make([]string, len(rows))
		for index := range rows {
			got[index] = rows[index].target
		}
		if !slices.Equal(got, want) {
			t.Fatalf("filter %s = %v, want %v", filter.label(), got, want)
		}
	}

	model.sessionFilter = sessionFilterAll
	for _, want := range []sessionFilter{sessionFilterLive, sessionFilterInactive, sessionFilterConfigured, sessionFilterDiscovered, sessionFilterAll} {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
		model = updated.(dashboardModel)
		if model.sessionFilter != want || !strings.Contains(ansi.Strip(model.tabLabels()[tabSessions]), want.label()) {
			t.Fatalf("Ctrl+f filter = %s, want %s", model.sessionFilter.label(), want.label())
		}
	}
}

func TestSessionSearchFooterFits(t *testing.T) {
	for _, width := range []int{40, 60, 80, 100} {
		model := newDashboard("/repo")
		model.width, model.height, model.tab, model.filter, model.query = width, 20, tabSessions, true, "x"
		model.filterInputs[tabSessions].SetValue("x")
		model.sessions = []item{{kind: "tmux-session", target: "live", title: "live", muxSessionID: "$1"}}
		footer := ansi.Strip(model.renderFooter(width))
		if ansi.StringWidth(footer) != width || !strings.Contains(footer, "search: x") || !strings.Contains(footer, "Esc Clear") || !strings.Contains(footer, "^c Quit") {
			t.Fatalf("width %d search footer = %q (%d cells)", width, footer, ansi.StringWidth(footer))
		}
		if width >= 80 && !strings.Contains(footer, "^j/k/n/p Move") {
			t.Fatalf("width %d search footer omits navigation: %q", width, footer)
		}
	}
}

func TestDashboardNavigationPreviewModesAndWideLayout(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height = 80, 20
	model.agents = []item{{kind: "session", target: "one", cwd: "/repo"}, {kind: "session", target: "two", cwd: "/two"}}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	model = updated.(dashboardModel)
	if model.index != 1 {
		t.Fatalf("G index = %d", model.index)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	model = updated.(dashboardModel)
	if model.index != 0 {
		t.Fatalf("g index = %d", model.index)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if got := updated.(dashboardModel).tab; got != tabSessions {
		t.Fatalf("Shift+Tab tab = %d", got)
	}

	model = newDashboard("/repo")
	model.width, model.height, model.tab = 40, 12, tabSessions
	model.sessions = []item{{kind: "tmux-session", target: "dev", title: "dev", muxSessionID: "$1"}}
	if strings.Contains(ansi.Strip(model.View()), "Preview") {
		t.Fatal("narrow dashboard showed the default preview")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	model = updated.(dashboardModel)
	if model.previewOverride[tabSessions] != 1 || !strings.Contains(ansi.Strip(model.View()), "Preview") {
		t.Fatalf("Ctrl+V did not force a narrow preview: %#v", model.previewOverride)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	model = updated.(dashboardModel)
	if !model.actionMenu || model.filter {
		t.Fatalf("Space did not open actions before session search: %#v", model)
	}
	actions := ansi.Strip(model.View())
	if !strings.Contains(actions, "Open") || !strings.Contains(actions, "Remove") || strings.Contains(actions, "Diff") {
		t.Fatalf("invalid session action menu:\n%s", actions)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(dashboardModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	model = updated.(dashboardModel)
	if !model.filter || model.query != "g" {
		t.Fatalf("Sessions g did not search: %#v", model)
	}

	model = newDashboard("/repo")
	model.width, model.height, model.tab = 140, 20, tabWorktrees
	model.worktrees = []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main"}}
	model.preview = previewData{target: "/repo", title: "repo", lines: []string{"preview"}}
	view := ansi.Strip(model.View())
	if !model.wideLayout() || !strings.Contains(view, "# Project") || !strings.Contains(view, "preview") {
		t.Fatalf("wide layout is not readable:\n%s", view)
	}
	updated, _ = model.Update(tea.MouseMsg{X: 139, Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if got := updated.(dashboardModel); !got.previewFocused {
		t.Fatal("wide preview click did not focus the preview")
	}
}

func TestSessionsReservedKeysAndPreviewPaging(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height, model.tab, model.filter = 80, 20, tabSessions, true
	model.sessions = []item{{kind: "tmux-session", target: "dev", title: "dev", muxSessionID: "$1"}}
	model.filterInputs[tabSessions].Focus()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	model = updated.(dashboardModel)
	if !model.help || model.query != "" {
		t.Fatalf("Sessions ? did not open Help before search: %#v", model)
	}
	model.help = false
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	model = updated.(dashboardModel)
	if model.actionMenu || model.query != " " {
		t.Fatalf("Sessions Space was not inserted into search: %#v", model)
	}
	model.filter = false
	model.preview = previewData{target: "dev", kind: "tmux-session", lines: make([]string, 100)}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	model = updated.(dashboardModel)
	if model.previewOffset != model.previewVisibleHeight() {
		t.Fatalf("PgDn offset = %d, want %d", model.previewOffset, model.previewVisibleHeight())
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	model = updated.(dashboardModel)
	if model.action != actionRemoveSession {
		t.Fatalf("Ctrl+D action = %d, want remove session", model.action)
	}
}

func TestSessionSearchClearsActionErrorAndSupportsMouse(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height, model.tab, model.filter = 80, 20, tabSessions, true
	model.sessions = []item{{kind: "tmux-session", target: "one", title: "one"}, {kind: "tmux-session", target: "two", title: "two"}}
	model.filterInputs[tabSessions].Focus()
	model.err = errors.New("old action failed")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	model = updated.(dashboardModel)
	if model.err != nil || model.query != "o" {
		t.Fatalf("search input = query %q, err %v", model.query, model.err)
	}
	updated, _ = model.Update(tea.MouseMsg{X: 3, Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	model = updated.(dashboardModel)
	if model.index != 1 {
		t.Fatalf("search mouse selection index = %d", model.index)
	}
	x := model.tabHitboxes()[tabAgents][0]
	updated, _ = model.Update(tea.MouseMsg{X: x, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if got := updated.(dashboardModel); got.tab != tabAgents || got.filter {
		t.Fatalf("search tab click did not leave search: tab %d filter %v", got.tab, got.filter)
	}
}

func TestWideWorktreePreviewPanelFocus(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height, model.tab = 160, 24, tabWorktrees
	model.worktrees = []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main"}}
	model.preview = previewData{target: "/repo", kind: "worktree", title: "main", lines: []string{"status"}, rightTitle: "Git Log", rightLines: []string{"commit"}}
	if !model.wideLayout() || !model.hasRightPanel() {
		t.Fatalf("wide worktree preview has no split: table=%d preview=%d", model.wideTableWidth(), model.previewRenderWidth())
	}
	updated, _ := model.Update(tea.MouseMsg{X: 150, Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	model = updated.(dashboardModel)
	if !model.previewFocused || model.panelFocus != panelRight {
		t.Fatalf("wide right panel focus = preview:%v panel:%d", model.previewFocused, model.panelFocus)
	}
}

func TestDashboardFocusAndReducedMotion(t *testing.T) {
	model := newDashboard("/repo")
	model.agentsInFlight, model.worktreesInFlight, model.sessionsInFlight = false, false, false
	updated, command := model.Update(tea.BlurMsg{})
	model = updated.(dashboardModel)
	if model.focused || command != nil {
		t.Fatalf("blur state = focused:%v command:%v", model.focused, command)
	}
	updated, command = model.Update(tickMsg(time.Now()))
	model = updated.(dashboardModel)
	if model.focused || command == nil || model.agentsInFlight || model.worktreesInFlight || model.sessionsInFlight {
		t.Fatalf("blurred refresh launched work: %#v", model)
	}
	updated, command = model.Update(dashboardDataMsg(dashboardData{agents: []item{{kind: "session", target: "agent", cwd: "/repo"}}}))
	model = updated.(dashboardModel)
	if model.agentGitInFlight || command != nil {
		t.Fatalf("blurred agent result launched Git refresh: %#v", model)
	}
	updated, command = model.Update(worktreeDataMsg{stage: worktreeListStage, generation: model.worktreeGeneration, worktrees: []item{{kind: "worktree", target: "/repo", cwd: "/repo"}}})
	model = updated.(dashboardModel)
	if model.worktreesInFlight || command != nil {
		t.Fatalf("blurred worktree result launched detail refresh: %#v", model)
	}
	updated, command = model.Update(tea.FocusMsg{})
	model = updated.(dashboardModel)
	if !model.focused || command == nil || !model.agentsInFlight || !model.worktreesInFlight || !model.sessionsInFlight {
		t.Fatalf("focus did not refresh immediately: %#v", model)
	}
	t.Setenv("JUMPMUX_REDUCED_MOTION", "1")
	if spinnerFrame(time.Now()) != "·" {
		t.Fatal("reduced motion spinner animates")
	}
}

func TestDashboardWidthAndUnicodeSafety(t *testing.T) {
	for _, width := range []int{40, 60, 80, 100, 140, 160} {
		model := newDashboard("/repo")
		model.width, model.height = width, 18
		model.agents = []item{{kind: "session", target: "one", cwd: "/repo/表", title: "e\u0301 🚀 表", gitLoaded: true}}
		for _, line := range strings.Split(model.View(), "\n") {
			if got := ansi.StringWidth(line); got != width {
				t.Fatalf("width %d line width = %d", width, got)
			}
		}
		footer := ansi.Strip(model.renderFooter(width))
		if ansi.StringWidth(footer) != width || strings.Contains(footer, "BROWSE") {
			t.Fatalf("width %d footer = %q", width, footer)
		}
	}
}

func TestModeLabelMovesToHeader(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height = 100, 20
	browseHeader := ansi.Strip(model.renderHeader(model.width))
	if strings.Contains(browseHeader, "BROWSE") || strings.Contains(ansi.Strip(model.renderFooter(model.width)), "BROWSE") {
		t.Fatalf("default mode rendered: header=%q footer=%q", browseHeader, ansi.Strip(model.renderFooter(model.width)))
	}

	model.agentsInFlight = false
	model.lastRefresh[tabAgents] = model.now
	if header := ansi.Strip(model.renderHeader(model.width)); strings.Contains(header, "updated") {
		t.Fatalf("healthy refresh rendered: %q", header)
	}

	model.filter = true
	searchHeader := ansi.Strip(model.renderHeader(model.width))
	if !strings.Contains(searchHeader, "SEARCH") || strings.Contains(ansi.Strip(model.renderFooter(model.width)), "SEARCH") {
		t.Fatalf("SEARCH placement: header=%q footer=%q", searchHeader, ansi.Strip(model.renderFooter(model.width)))
	}

	model.width = 40
	narrowHeader := ansi.Strip(model.renderHeader(model.width))
	if !strings.Contains(narrowHeader, "SEARCH") {
		t.Fatalf("narrow header dropped mode: %q", narrowHeader)
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
	for tab := tabWorktrees; tab < tabCount; tab++ {
		updated, _ = model.Update(tea.MouseMsg{X: hitboxes[tab][0], Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
		model = updated.(dashboardModel)
		if model.tab != tab {
			t.Fatalf("tab %d hitbox did not use rendered label", tab)
		}
	}
}
