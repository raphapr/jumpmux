package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func TestCachedExecutableIsLookedUpAgainWhenRemoved(t *testing.T) {
	for _, version := range []string{"old", "new"} {
		bin := filepath.Join(t.TempDir(), version)
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, "jm-fake-tool"), []byte("#!/bin/sh\necho "+version+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if version == "old" {
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			continue
		}
		// The new install sits later in the same PATH, like a version manager's shim.
		t.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+bin)
	}
	run := func() string {
		output, err := boundedCommand(context.Background(), "jm-fake-tool").Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(output))
	}
	if got := run(); got != "old" {
		t.Fatalf("first run = %q", got)
	}
	oldDir := strings.SplitN(os.Getenv("PATH"), string(os.PathListSeparator), 2)[0]
	if err := os.RemoveAll(oldDir); err != nil {
		t.Fatal(err)
	}
	if got := run(); got != "new" {
		t.Fatalf("run after removal = %q", got)
	}
}

func TestPaintMatchesRender(t *testing.T) {
	previous := lipgloss.ColorProfile()
	defer func() { lipgloss.SetColorProfile(previous); applyColorScheme(schemeDefault) }()
	lipgloss.SetColorProfile(0) // termenv.TrueColor, so styles emit escape codes
	if !strings.Contains(headerStyle.Render("x"), "\x1b[") {
		t.Fatal("test needs a color profile")
	}
	for _, scheme := range []colorScheme{schemeDefault, colorSchemeFromSlug("catppuccin-mocha")} {
		applyColorScheme(scheme)
		for _, style := range []lipgloss.Style{textStyle, headerStyle, mutedStyle, withBackground(dangerStyle, &selectedColor), lipgloss.NewStyle()} {
			for _, value := range []string{"", "main", " · ↑2"} {
				if got, want := paint(style, value), style.Render(value); got != want {
					t.Fatalf("%s: paint(%q) = %q, Render = %q", scheme.slug(), value, got, want)
				}
			}
		}
	}
}

func TestStatusUntrackedReplacesLsFiles(t *testing.T) {
	repo := newAgentTestRepo(t)
	for _, name := range []string{"plain.txt", "dir/nested.txt", "café \"q\".txt"} {
		path := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("a\nb\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	loaded := loadGitDetails(item{kind: "worktree", cwd: repo}, "main")
	if loaded.untracked != 3 || loaded.added != 6 || loaded.removed != 0 {
		t.Fatalf("untracked = %d, added = %d, removed = %d", loaded.untracked, loaded.added, loaded.removed)
	}
}

func TestWorktreePreviewSkipsGitForPRAndListStages(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height, model.tab = 120, 30, tabWorktrees
	worktrees := []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main"}}
	updated, _ := model.Update(worktreeDataMsg{stage: worktreeListStage, generation: model.worktreeGeneration, worktrees: worktrees})
	model = updated.(dashboardModel)
	if model.preview.target != "/repo" {
		t.Fatalf("first list stage did not load the preview: %#v", model.preview)
	}
	request := model.previewRequest
	pr := []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main", prNumber: 7, prCheck: "failure", prFailedChecks: []string{"lint"}, prLoaded: true}}
	updated, command := model.Update(worktreeDataMsg{stage: worktreePRStage, generation: model.worktreeGeneration, worktrees: pr})
	model = updated.(dashboardModel)
	if command != nil || model.previewRequest != request || !strings.Contains(strings.Join(model.preview.lines, "\n"), "lint") {
		t.Fatalf("PR stage reloaded Git or skipped metadata: request %d -> %d, lines %q", request, model.previewRequest, model.preview.lines)
	}
	model.worktreeGeneration++
	updated, _ = model.Update(worktreeDataMsg{stage: worktreeListStage, generation: model.worktreeGeneration, worktrees: worktrees})
	if updated.(dashboardModel).previewRequest != request {
		t.Fatal("list stage reloaded the unchanged preview")
	}
}

func TestLoggedOutGhRunsOnceAndBacksOff(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "gh.log")
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\necho call >>\"$GH_LOG\"\necho 'To get started with GitHub CLI, please run:  gh auth login' >&2\nexit 4\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_LOG", log)
	pullRequestMemory.Lock()
	pullRequestMemory.values = nil
	pullRequestMemory.Unlock()
	repo := t.TempDir()
	if _, loaded := listPullRequests(repo); loaded {
		t.Fatal("logged-out gh reported pull requests")
	}
	if data, _ := os.ReadFile(log); strings.Count(string(data), "call") != 1 {
		t.Fatalf("logged-out gh ran %d times, want 1", strings.Count(string(data), "call"))
	}
	key := pullRequestCacheKey(repo)
	pullRequestMemory.Lock()
	entry := pullRequestMemory.values[key]
	pullRequestMemory.Unlock()
	if until := time.Until(entry.expires); until < pullRequestUnavailablePeriod-time.Minute {
		t.Fatalf("logged-out gh retries in %s", until)
	}
}

func TestClockSlowsDownWithoutSpinner(t *testing.T) {
	t.Setenv("JUMPMUX_REDUCED_MOTION", "")
	spinnerDrawn.Store(false)
	if got := clockDelay(true); got != time.Second {
		t.Fatalf("idle clock = %s", got)
	}
	spinnerFrame(time.Now())
	if got := clockDelay(true); got != clockInterval {
		t.Fatalf("animating clock = %s", got)
	}
	spinnerFrame(time.Now())
	if got := clockDelay(false); got != time.Second {
		t.Fatalf("unfocused clock = %s", got)
	}
}

func TestHiddenDashboardSkipsRefreshes(t *testing.T) {
	for output, hidden := range map[string]bool{"1 1": false, "0 1": true, "1 0": true} {
		writeFakeTmux(t, "echo '"+output+"'")
		t.Setenv("TMUX_PANE", "%3")
		if got := checkVisibility()().(visibilityMsg); got.hidden != hidden {
			t.Fatalf("tmux %q: hidden = %v", output, got.hidden)
		}
	}
	model := newDashboard("/repo")
	model.agentsInFlight, model.worktreesInFlight, model.sessionsInFlight = false, false, false
	updated, _ := model.Update(visibilityMsg{hidden: true})
	model = updated.(dashboardModel)
	updated, _ = model.Update(tickMsg{})
	if model = updated.(dashboardModel); model.agentsInFlight || model.worktreesInFlight || model.sessionsInFlight {
		t.Fatal("hidden dashboard refreshed")
	}
	updated, command := model.Update(visibilityMsg{hidden: false})
	if model = updated.(dashboardModel); command == nil || !model.agentsInFlight {
		t.Fatal("dashboard did not refresh when shown again")
	}
}

func TestGitRefreshCoversOnlyVisibleWorktrees(t *testing.T) {
	worktrees := []item{{kind: "worktree", target: "/a", cwd: "/a"}, {kind: "worktree", target: "/b", cwd: "/b"}}
	model := newDashboard("/repo")
	model.worktrees = slices.Clone(worktrees)
	model.worktrees[1].agents = []item{{kind: "session", pane: "%1"}}
	for tab, want := range map[int][]string{tabWorktrees: {"/a", "/b"}, tabAgents: {"/b"}, tabSessions: nil} {
		model.tab = tab
		var got []string
		for _, worktree := range model.worktreesNeedingDetails(worktrees) {
			got = append(got, worktree.target)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("tab %d refreshes %v, want %v", tab, got, want)
		}
	}
	// Switching tabs refreshes at once, even right after a narrower refresh.
	model.tab, model.worktreesInFlight, model.worktreeGitRefreshed = tabAgents, false, time.Now()
	updated, command := model.switchTab(tabWorktrees)
	if model = updated.(dashboardModel); command == nil || !model.worktreesInFlight || !model.worktreeGitRefreshed.IsZero() {
		t.Fatal("switching to Worktrees did not refresh")
	}
}

func TestGitDetailsTrackNewCommitsAndCleanTrees(t *testing.T) {
	repo := newAgentTestRepo(t)
	worktree := filepath.Join(t.TempDir(), "feature")
	if _, err := gitOutput(repo, "worktree", "add", "-q", "-b", "feature", worktree); err != nil {
		t.Fatal(err)
	}
	commit := func(lines string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(worktree, "f.txt"), []byte(lines), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"add", "f.txt"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "c"}} {
			if _, err := gitOutput(worktree, args...); err != nil {
				t.Fatal(err)
			}
		}
	}
	commit("a\n")
	loaded := loadGitDetails(item{kind: "worktree", cwd: worktree}, "main")
	if loaded.committedAdded != 1 {
		t.Fatalf("first commit: committed +%d", loaded.committedAdded)
	}
	commit("a\nb\nc\n")
	if loaded = loadGitDetails(loaded, "main"); loaded.committedAdded != 3 {
		t.Fatalf("cached comparison ignored a new commit: committed +%d", loaded.committedAdded)
	}
	if err := os.WriteFile(filepath.Join(worktree, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if loaded = loadGitDetails(loaded, "main"); loaded.added == 0 {
		t.Fatal("dirty tree shows no changes")
	}
	if _, err := gitOutput(worktree, "checkout", "--", "f.txt"); err != nil {
		t.Fatal(err)
	}
	if loaded = loadGitDetails(loaded, "main"); loaded.added != 0 || loaded.removed != 0 {
		t.Fatalf("clean tree kept old counts: +%d -%d", loaded.added, loaded.removed)
	}
}

func TestWorktreeGitRefreshTiers(t *testing.T) {
	worktrees := []item{{kind: "worktree", target: "/a", cwd: "/a"}, {kind: "worktree", target: "/b", cwd: "/b"}, {kind: "worktree", target: "/c", cwd: "/c"}}
	model := newDashboard("/repo")
	model.tab = tabWorktrees
	model.gitCache = map[string]item{"/a": {}, "/b": {}, "/c": {}}
	refreshed := func() int {
		model.worktreeGeneration++
		model.worktreeGitRefreshed = time.Time{}
		updated, _ := model.Update(worktreeDataMsg{stage: worktreeListStage, generation: model.worktreeGeneration, worktrees: worktrees})
		model = updated.(dashboardModel)
		return model.worktreePending - 2 // minus the PR and mux stages
	}
	// Everything is cached, so the full refresh sends one message for all rows.
	if got := refreshed(); got != 1 || model.worktreeFullRefreshed.IsZero() {
		t.Fatalf("full refresh pending = %d", got)
	}
	// Within the full-refresh period only the selected row (/b) and rows never
	// loaded (/c) refresh; uncached rows stream, one message each.
	model.index = 1
	delete(model.gitCache, "/c")
	if got := refreshed(); got != 2 {
		t.Fatalf("tiered refresh pending = %d, want the selected row and the uncached row", got)
	}
}
