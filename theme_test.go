package main

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestColorSchemes(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	defer applyColorScheme(schemeDefault)

	if len(colorSchemes) != 12 || schemeTealDrift.next() != schemeDefault {
		t.Fatalf("scheme cycle = %#v", colorSchemes)
	}
	if themePalettes[schemeDefault].accent.Dark != "#CBA6F7" || themePalettes[schemeEmberforge].header.Light != "#AA641E" {
		t.Fatal("dashboard palette values changed")
	}
	if err := saveColorScheme(schemeEmberforge); err != nil {
		t.Fatal(err)
	}
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

func TestGitAndPRIcons(t *testing.T) {
	now := time.Unix(0, 0)
	if got := gitStatusText(item{}, now); got != spinnerFrames[0] {
		t.Fatalf("loading Git status = %q", got)
	}
	if got := gitStatusText(item{gitLoaded: true, dirty: true, untracked: 1}, now); got != dashboardIcon(gitDiffIcon, "*") {
		t.Fatalf("untracked Git status = %q", got)
	}
	complex := item{
		gitLoaded: true, baseBranch: "develop", committedAdded: 10, committedRemoved: 2,
		dirty: true, added: 3, removed: 1, isRebasing: true, hasConflict: true, ahead: 2, behind: 1,
	}
	want := strings.Join([]string{dashboardIcon(gitRebaseIcon, "R"), "→develop", "+10", "-2", dashboardIcon(gitDiffIcon, "*"), "+3", "-1", dashboardIcon(gitConflictIcon, "!"), "↑2", "↓1"}, " ")
	if got := gitStatusText(complex, now); got != want {
		t.Fatalf("complex Git status = %q, want %q", got, want)
	}
	cases := []struct {
		item item
		want string
	}{
		{item{prNumber: 1, prDraft: true}, "#1 " + dashboardIcon(prDraftIcon, "D")},
		{item{prNumber: 2, prState: "OPEN"}, "#2 " + dashboardIcon(prOpenIcon, "O")},
		{item{prNumber: 3, prState: "MERGED"}, "#3 " + dashboardIcon(prMergedIcon, "M")},
		{item{prNumber: 4, prState: "CLOSED"}, "#4 " + dashboardIcon(prClosedIcon, "X")},
		{item{prNumber: 5, prState: "OPEN", prCheck: checkSuccess}, "#5 " + dashboardIcon(prOpenIcon, "O") + " " + dashboardIcon(checkSuccessIcon, "+")},
		{item{prNumber: 6, prState: "OPEN", prCheck: checkFailure}, "#6 " + dashboardIcon(prOpenIcon, "O") + " " + dashboardIcon(checkFailureIcon, "x")},
		{item{prNumber: 7, prState: "OPEN", prCheck: checkPending}, "#7 " + dashboardIcon(prOpenIcon, "O") + " " + spinnerFrames[0]},
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

func TestPlainIconFallback(t *testing.T) {
	t.Setenv("JUMPMUX_PLAIN", "1")
	now := time.Unix(0, 0)
	if got := gitStatusText(item{gitLoaded: true, dirty: true, hasConflict: true}, now); got != "* !" {
		t.Fatalf("plain Git status = %q", got)
	}
	if got := prText(item{prNumber: 7, prState: "MERGED", prCheck: checkFailure}, now); got != "#7 M x" {
		t.Fatalf("plain PR status = %q", got)
	}
}

func TestElapsedTimeStyle(t *testing.T) {
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
