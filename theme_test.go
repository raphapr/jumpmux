package main

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestColorSchemes(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	defer applyColorScheme(schemeDefault)

	if len(colorSchemes) != 16 {
		t.Fatalf("scheme count = %d", len(colorSchemes))
	}
	if themePalettes[schemeDefault].accent.Dark != "#CBA6F7" || themePalettes[schemeEmberforge].header.Light != "#AA641E" {
		t.Fatal("dashboard palette values changed")
	}
	for _, test := range []struct {
		scheme   colorScheme
		slug     string
		expected []string
	}{
		{schemeCatppuccinLatte, "catppuccin-latte", []string{"#EFF1F5", "#CCD0DA", "#D2D4DC", "#7287FD", "#4C4F69", "#8C8FA1", "#9CA0B0", "#7287FD", "#7287FD", "#DF8E1D", "#DC8A78", "#179299", "#1E66F5", "#40A02B", "#DF8E1D", "#D20F39", "#8839EF"}},
		{schemeCatppuccinFrappe, "catppuccin-frappe", []string{"#303446", "#414559", "#494E63", "#BABBF1", "#C6D0F5", "#838BA7", "#737994", "#BABBF1", "#BABBF1", "#E5C890", "#F2D5CF", "#81C8BE", "#8CAAEE", "#A6D189", "#E5C890", "#E78284", "#CA9EE6"}},
		{schemeCatppuccinMacchiato, "catppuccin-macchiato", []string{"#24273A", "#363A4F", "#404459", "#B7BDF8", "#CAD3F5", "#8087A2", "#6E738D", "#B7BDF8", "#B7BDF8", "#EED49F", "#F4DBD6", "#8BD5CA", "#8AADF4", "#A6DA95", "#EED49F", "#ED8796", "#C6A0F6"}},
		{schemeCatppuccinMocha, "catppuccin-mocha", []string{"#1E1E2E", "#313244", "#3B3D4F", "#B4BEFE", "#CDD6F4", "#7F849C", "#6C7086", "#B4BEFE", "#B4BEFE", "#F9E2AF", "#F5E0DC", "#94E2D5", "#89B4FA", "#A6E3A1", "#F9E2AF", "#F38BA8", "#CBA6F7"}},
	} {
		palette := themePalettes[test.scheme]
		colors := []lipgloss.AdaptiveColor{palette.background, palette.currentRow, palette.selected, palette.currentWorktree, palette.text, palette.dimmed, palette.border, palette.activeBorder, palette.header, palette.keycap, palette.cursor, palette.info, palette.diff, palette.success, palette.warning, palette.danger, palette.accent}
		if test.scheme.slug() != test.slug || colorSchemeFromSlug(test.slug) != test.scheme {
			t.Fatalf("Catppuccin scheme = %#v", test)
		}
		for index, color := range colors {
			if color.Light != test.expected[index] || color.Dark != test.expected[index] {
				t.Fatalf("%s color %d = %#v, want %s", test.slug, index, color, test.expected[index])
			}
		}
		applyColorScheme(test.scheme)
		if !dashboardBackgroundEnabled || dashboardBackgroundColor != palette.background {
			t.Fatalf("%s background was not applied", test.slug)
		}
	}
	applyColorScheme(schemeDefault)
	if dashboardBackgroundEnabled {
		t.Fatal("default scheme retained the Catppuccin background")
	}
	if input := "plain\x1b[0m styled\nnext"; paintDashboardBackground(input) != input {
		t.Fatal("default scheme painted the dashboard background")
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
	if !got.themePicker || got.scheme != schemeEmberforge {
		t.Fatalf("theme picker = %#v", got)
	}
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	got = updated.(dashboardModel)
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(dashboardModel)
	config, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.themePicker || got.scheme != schemeGlacierSignal || !config.hasTheme || config.theme != schemeGlacierSignal {
		t.Fatalf("selected scheme = %s", got.scheme.slug())
	}
}

func TestCatppuccinDashboardBackground(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(0) // termenv.TrueColor; avoid a direct test dependency.
	defer func() {
		lipgloss.SetColorProfile(previousProfile)
		applyColorScheme(schemeDefault)
	}()

	applyColorScheme(schemeCatppuccinMocha)
	input := "plain\x1b[0m styled\nnext"
	painted := paintDashboardBackground(input)
	if strings.Count(painted, "48;2;30;30;46m") < 3 || !strings.HasSuffix(painted, "\x1b[0m") || ansi.Strip(painted) != ansi.Strip(input) {
		t.Fatalf("painted dashboard = %q", painted)
	}

	applyColorScheme(schemeDefault)
	if got := paintDashboardBackground(input); got != input {
		t.Fatalf("default dashboard = %q", got)
	}
}

func TestThemePickerFiltersAndCancels(t *testing.T) {
	defer applyColorScheme(schemeDefault)
	model := newDashboard("/repo")
	model.width, model.height = 40, 10

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	model = updated.(dashboardModel)
	for _, key := range "cat" {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		model = updated.(dashboardModel)
	}
	if !model.themePicker || model.scheme != schemeCatppuccinLatte || model.themePickerInput.Value() != "cat" {
		t.Fatalf("filtered picker = %#v", model)
	}
	picker := ansi.Strip(model.View())
	if !strings.Contains(picker, "[Agents") || !strings.Contains(picker, "catppuccin-latte") || strings.Contains(picker, "Built-in") || strings.Contains(picker, "Catppuccin\n") || strings.Contains(picker, "▌") || !strings.Contains(picker, "Apply") || !strings.Contains(picker, "Cancel") || len(strings.Split(picker, "\n")) != model.height {
		t.Fatalf("picker view = %q", picker)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(dashboardModel)
	if model.themePicker || model.scheme != schemeDefault {
		t.Fatalf("cancelled picker = %#v", model)
	}
}

func TestGitAndPRIcons(t *testing.T) {
	now := time.Unix(0, 0)
	if got := gitStatusText(item{}, now); got != spinnerFrame(now) {
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
		{item{prNumber: 7, prState: "OPEN", prCheck: checkPending}, "#7 " + dashboardIcon(prOpenIcon, "O") + " " + spinnerFrame(now)},
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
