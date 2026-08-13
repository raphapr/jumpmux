package main

import (
	"errors"
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
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
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
	model.tab, model.actionMenu, model.actionMenuIndex = tabWorktrees, true, 6
	model.worktrees = []item{{kind: "worktree", cwd: "/repo", branch: "feature", prNumber: 42, prURL: "https://example.test/pr/42", prunable: true}}
	view := ansi.Strip(model.renderActionMenu(40, 5))
	if !strings.Contains(view, "▌ Clean up stale record") {
		t.Fatalf("selected action is not visible:\n%s", view)
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
	if footer := ansi.Strip(model.renderFooter(80)); !strings.Contains(footer, "Error:") || !strings.Contains(footer, "Esc Dismiss") {
		t.Fatalf("persistent error footer = %q", footer)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.(dashboardModel).err != nil {
		t.Fatal("Esc did not clear action error")
	}
}
