package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func runeKey(key rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}} }

func TestAgentsNeedingAttentionSortFirstAndCycle(t *testing.T) {
	question := item{target: "%1", status: "question"}
	unseen := item{target: "%2", status: "done"}
	working := item{target: "%3", status: "working"}
	seen := item{target: "%4", status: "done", seen: true}
	ranks := []int{attentionRank(question), attentionRank(unseen), attentionRank(working), attentionRank(seen)}
	if !slices.IsSorted(ranks) || ranks[1] == ranks[2] {
		t.Fatalf("attention ranks = %v; an unseen completion must outrank working", ranks)
	}

	if _, ok := nextAttentionAgent([]item{working, seen}); ok {
		t.Fatal("agents that do not wait were offered")
	}
	if next, ok := nextAttentionAgent([]item{question, unseen, working}); !ok || next.target != "%1" {
		t.Fatalf("first waiting agent = %#v, %v", next, ok)
	}
	// From the focused question, go on to the next waiting agent, then wrap.
	question.current = true
	if next, ok := nextAttentionAgent([]item{question, unseen, working}); !ok || next.target != "%2" {
		t.Fatalf("next after the focused agent = %#v, %v", next, ok)
	}
	if _, ok := nextAttentionAgent([]item{question, working}); ok {
		t.Fatal("the focused agent was offered as the next one")
	}
	question.current, unseen.current = false, true
	if next, ok := nextAttentionAgent([]item{question, unseen}); !ok || next.target != "%1" {
		t.Fatalf("next did not wrap: %#v, %v", next, ok)
	}
}

func TestAgentJumpNumbersStayWithAgentsWhenRowsResort(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-test,1,0")
	agent := func(pane string) item { return item{kind: "session", target: pane, pane: pane, cwd: "/repo/" + pane} }
	model := newDashboard("/repo")
	model.width, model.height = 120, 30
	load := func(panes ...string) {
		t.Helper()
		agents := make([]item, len(panes))
		for index, pane := range panes {
			agents[index] = agent(pane)
		}
		updated, _ := model.Update(dashboardDataMsg(dashboardData{agents: agents}))
		model = updated.(dashboardModel)
	}
	number := func(pane string) string {
		t.Helper()
		rows := model.rows()
		index := slices.IndexFunc(rows, func(row item) bool { return row.target == pane })
		if index < 0 {
			t.Fatalf("agent %s is not listed", pane)
		}
		return strings.Fields(ansi.Strip(model.tableRow(rows[index], index, model.width, model.columns(model.width, rows)))[2:])[0]
	}

	load("a", "b", "c")
	load("c", "a", "b") // c moved to the top, as when it asks a question
	for pane, want := range map[string]string{"a": "1", "b": "2", "c": "3"} {
		if got := number(pane); got != want {
			t.Fatalf("agent %s number = %s, want %s", pane, got, want)
		}
	}
	updated, command := model.Update(runeKey('1'))
	if got := updated.(dashboardModel); command == nil || got.rows()[got.index].target != "a" {
		t.Fatalf("1 opened row %d (%s), not agent a", got.index, got.rows()[got.index].target)
	}

	load("c", "a", "d") // b closed; d is new and takes the freed number
	if got := number("d"); got != "2" {
		t.Fatalf("new agent number = %s, want the freed 2", got)
	}
	if got := number("c"); got != "3" {
		t.Fatalf("agent c number changed to %s", got)
	}
}

func TestFilterIsFuzzyOnEveryTab(t *testing.T) {
	model := newDashboard("/repo")
	model.agents = []item{
		{kind: "session", target: "%1", cwd: "/src/helm-charts", title: "π - helm-charts"},
		{kind: "session", target: "%2", cwd: "/src/labs", title: "π - labs", muxSessionName: "labs", muxWindowIndex: "4"},
	}
	model.worktrees = []item{
		{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main"},
		{kind: "worktree", target: "/wt", cwd: "/wt", branch: "fix/review-findings"},
	}
	model.sessions = []item{{kind: "tmux-session", target: "nvim-conf", title: "nvim-conf", cwd: "/cfg"}, {kind: "tmux-session", target: "api", title: "api", cwd: "/api"}}
	for _, test := range []struct {
		tab         int
		query, want string
	}{
		{tabAgents, "hlmch", "%1"},
		{tabAgents, "labs:4", "%2"},
		{tabWorktrees, "fxrev", "/wt"},
		{tabSessions, "nvcf", "nvim-conf"},
	} {
		model.tab, model.query = test.tab, test.query
		if rows := model.rows(); len(rows) != 1 || rows[0].target != test.want {
			t.Fatalf("tab %d query %q = %#v, want %s", test.tab, test.query, rows, test.want)
		}
	}
}

func TestBranchDiffShowsCommittedWork(t *testing.T) {
	parent := t.TempDir()
	repo, feature := filepath.Join(parent, "repo"), filepath.Join(parent, "feature")
	git := func(args ...string) {
		t.Helper()
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	write := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main", repo)
	git("-C", repo, "config", "user.name", "Test")
	git("-C", repo, "config", "user.email", "test@example.com")
	write(filepath.Join(repo, "a.txt"), "a\n")
	git("-C", repo, "add", "a.txt")
	git("-C", repo, "commit", "-qm", "base")
	git("-C", repo, "worktree", "add", "-qb", "feature", feature)
	write(filepath.Join(feature, "a.txt"), "committed\n")
	write(filepath.Join(feature, "b.txt"), "new file\n")
	git("-C", feature, "add", ".")
	git("-C", feature, "commit", "-qm", "work")
	// main moves on; the branch diff must not show main's change as a removal.
	write(filepath.Join(repo, "main-only.txt"), "main\n")
	git("-C", repo, "add", ".")
	git("-C", repo, "commit", "-qm", "main moves")
	write(filepath.Join(feature, "untracked.txt"), "untracked\n")

	selected := item{kind: "worktree", target: feature, cwd: feature}
	wip := loadDiff(selected, 1, false)().(diffMsg)
	if text := strings.Join(wip.lines, "\n"); strings.Contains(text, "committed") || !strings.Contains(text, "? untracked.txt") || !strings.HasPrefix(wip.title, "WIP: feature") {
		t.Fatalf("WIP diff = %q %q", wip.title, text)
	}
	branch := loadDiff(selected, 2, true)().(diffMsg)
	text := strings.Join(branch.lines, "\n")
	for _, want := range []string{"+committed", "+new file", "? untracked.txt"} {
		if !strings.Contains(text, want) {
			t.Fatalf("branch diff misses %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "main-only") {
		t.Fatalf("branch diff shows the default branch's own change:\n%s", text)
	}
	if branch.title != "Branch: feature vs main +2 -1" {
		t.Fatalf("branch diff title = %q", branch.title)
	}
	if want := []string{"M  a.txt", "A  b.txt", "?? untracked.txt"}; !slices.Equal(branch.files, want) {
		t.Fatalf("branch diff files = %q, want %q", branch.files, want)
	}

	if onMain := loadDiff(item{kind: "worktree", target: repo, cwd: repo}, 3, true)().(diffMsg); len(onMain.lines) != 1 || onMain.lines[0] != "No changes since main." {
		t.Fatalf("default branch compared with itself = %#v", onMain.lines)
	}
}

func TestDiffFilesKeepUnstagedFileNames(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	write := func(name, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.com")
	write("a.txt", "a\n")
	write("b.txt", "b\n")
	git("add", ".")
	git("commit", "-qm", "base")
	// The first status line starts with a space: a.txt is changed but not staged.
	write("a.txt", "changed\n")
	write("b.txt", "staged\n")
	git("add", "b.txt")

	diff := loadDiff(item{kind: "worktree", target: repo, cwd: repo}, 1, false)().(diffMsg)
	if want := []string{" M a.txt", "M  b.txt"}; !slices.Equal(diff.files, want) {
		t.Fatalf("diff files = %q, want %q", diff.files, want)
	}
}

func TestDiffKeysToggleBaseAndJumpBetweenFiles(t *testing.T) {
	lines := []string{"diff --git a/a b/a", "@@", "+1", "diff --git a/b b/b", "@@", "+2", "diff --git a/c b/c", "@@", "+3"}
	lines = append(lines, slices.Repeat([]string{" context"}, 10)...)
	for _, test := range []struct {
		offset int
		next   bool
		want   int
		name   string
	}{
		{0, true, 3, "next from the first file"},
		{4, true, 6, "next from inside the second file"},
		{6, true, 6, "next from the last file stays"},
		{6, false, 3, "previous from the last file"},
		{4, false, 3, "previous from inside a file goes to its start"},
		{0, false, 0, "previous from the first file stays"},
	} {
		if got := diffFileOffset(lines, test.offset, test.next); got != test.want {
			t.Fatalf("%s = %d, want %d", test.name, got, test.want)
		}
	}

	model := newDashboard("/repo")
	model.width, model.height, model.tab, model.diff = 100, 10, tabWorktrees, true
	model.worktrees = []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "feature"}}
	model.preview = previewData{target: "/repo", title: "WIP", lines: lines}
	if footer := ansi.Strip(model.View()); !strings.Contains(footer, "b Branch diff") || !strings.Contains(footer, "[/] File") {
		t.Fatalf("diff footer lacks the new keys:\n%s", footer)
	}
	updated, _ := model.Update(runeKey(']'))
	model = updated.(dashboardModel)
	if model.diffOff != 3 {
		t.Fatalf("] moved to line %d", model.diffOff)
	}
	updated, _ = model.Update(runeKey('['))
	model = updated.(dashboardModel)
	if model.diffOff != 0 {
		t.Fatalf("[ moved to line %d", model.diffOff)
	}
	request := model.diffRequest
	updated, command := model.Update(runeKey('b'))
	model = updated.(dashboardModel)
	if command == nil || !model.diffBase || !model.loading || model.diffRequest == request {
		t.Fatalf("b did not load the branch diff: base=%v loading=%v", model.diffBase, model.loading)
	}
	if footer := ansi.Strip(model.View()); !strings.Contains(footer, "b WIP diff") {
		t.Fatalf("branch diff footer does not offer the way back:\n%s", footer)
	}
	// Opening a diff again starts from the uncommitted changes.
	model.diff = false
	updated, _ = model.Update(runeKey('d'))
	if updated.(dashboardModel).diffBase {
		t.Fatal("a new diff kept the branch mode")
	}
}

func TestActionMenuClickRunsEntry(t *testing.T) {
	for _, width := range []int{120, 80} { // sidebar, then stacked panel
		model := newDashboard("/repo")
		model.width, model.height, model.tab = width, 24, tabWorktrees
		model.worktrees = []item{{kind: "worktree", target: "/repo", cwd: "/repo", branch: "main"}, {kind: "worktree", target: "/feature", cwd: "/feature", branch: "feature"}}
		model.index = 1
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeySpace})
		model = updated.(dashboardModel)
		if !model.actionMenu {
			t.Fatal("Space did not open the action menu")
		}
		x, y := -1, -1
		for row, line := range strings.Split(ansi.Strip(model.View()), "\n") {
			if column := strings.Index(line, "[d] View diff"); column >= 0 {
				x, y = ansi.StringWidth(line[:column]), row
			}
		}
		if y < 0 {
			t.Fatalf("width %d: menu does not show View diff:\n%s", width, ansi.Strip(model.View()))
		}
		click := tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}

		miss := click
		miss.Y = 0
		updated, _ = model.Update(miss)
		if got := updated.(dashboardModel); !got.actionMenu || got.diff {
			t.Fatalf("width %d: a click outside the entries ran an action", width)
		}
		updated, command := model.Update(click)
		if got := updated.(dashboardModel); got.actionMenu || !got.diff || command == nil {
			t.Fatalf("width %d: clicking View diff at %d,%d did not open the diff (menu=%v diff=%v)", width, x, y, got.actionMenu, got.diff)
		}
	}

	// A confirmation ignores the mouse, so a stray click cannot remove anything.
	model := newDashboard("/repo")
	model.width, model.height, model.tab = 120, 24, tabAgents
	model.agents = []item{{kind: "session", target: "%1", pane: "%1", cwd: "/repo"}}
	model.action, model.actionTarget = actionRemoveAgent, model.agents[0]
	for y := range model.height {
		updated, command := model.Update(tea.MouseMsg{X: 10, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
		if got := updated.(dashboardModel); got.action != actionRemoveAgent || command != nil {
			t.Fatalf("click at row %d changed the confirmation", y)
		}
	}
}

func TestSessionsShowAgentAttention(t *testing.T) {
	t.Setenv("JUMPMUX_PLAIN", "1")
	t.Setenv("JUMPMUX_REDUCED_MOTION", "1")
	model := newDashboard("/repo")
	model.width, model.height, model.tab = 100, 20, tabSessions
	model.now = time.Now()
	model.sessions = []item{
		{kind: "tmux-session", target: "dev", title: "dev", cwd: "/dev", muxSessionID: "$1"},
		{kind: "tmux-session", target: "ops", title: "ops", cwd: "/ops", muxSessionID: "$2"},
		{kind: "tmux-session", target: "idle", title: "idle", cwd: "/idle", sessionSource: "config"},
	}
	if view := ansi.Strip(model.renderTable(model.width)); strings.Contains(view, "Agent") {
		t.Fatalf("Agent column shown without agents:\n%s", view)
	}
	model.allAgents = []item{
		{kind: "session", target: "%1", status: "question", muxSessionID: "$1", updated: model.now},
		{kind: "session", target: "%2", status: "working", muxSessionID: "$1", updated: model.now},
		{kind: "session", target: "%3", status: "done", muxSessionID: "$2", updated: model.now},
	}
	lines := strings.Split(ansi.Strip(model.renderTable(model.width)), "\n")
	if !strings.Contains(lines[0], "Agent") {
		t.Fatalf("header lacks the Agent column: %q", lines[0])
	}
	for row, want := range map[int]string{1: "? +1", 2: "D U", 3: " - "} {
		if !strings.Contains(lines[row], want) {
			t.Fatalf("session row %q lacks agent status %q", lines[row], want)
		}
	}
	for _, line := range lines {
		if width := ansi.StringWidth(line); width > model.width {
			t.Fatalf("session row is %d wide: %q", width, line)
		}
	}
	model.width = 70
	if view := ansi.Strip(model.renderTable(model.width)); strings.Contains(view, "Agent") {
		t.Fatalf("narrow Sessions table kept the Agent column:\n%s", view)
	}
}

func TestNextAttentionKeyOpensWaitingAgent(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-test,1,0")
	model := newDashboard("/repo")
	model.width, model.height, model.tab = 140, 24, tabAgents
	model.agents = []item{{kind: "session", target: "%1", pane: "%1", status: "working"}, {kind: "session", target: "%2", pane: "%2", status: "done", seen: true}}
	if updated, command := model.Update(runeKey('n')); command != nil || updated.(dashboardModel).action != actionNone {
		t.Fatal("n acted although no agent needs attention")
	}
	if footer := ansi.Strip(model.renderFooter(model.width)); strings.Contains(footer, "n Next") {
		t.Fatalf("footer offers Next without a waiting agent: %q", footer)
	}
	model.agents[1].seen = false
	if footer := ansi.Strip(model.renderFooter(model.width)); !strings.Contains(footer, "n Next") {
		t.Fatalf("footer does not offer Next: %q", footer)
	}
	updated, command := model.Update(runeKey('n'))
	if got := updated.(dashboardModel); command == nil || got.action != actionRunning {
		t.Fatalf("n did not open the waiting agent: action=%d", got.action)
	}
}

func TestFetchUpdatesRemoteBranchesAndReportsProgress(t *testing.T) {
	parent := t.TempDir()
	remote, clone, other := filepath.Join(parent, "remote.git"), filepath.Join(parent, "clone"), filepath.Join(parent, "other")
	git := func(args ...string) string {
		t.Helper()
		output, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "-q", "--bare", "-b", "main", remote)
	for _, dir := range []string{clone, other} {
		git("clone", "-q", remote, dir)
		git("-C", dir, "config", "user.name", "Test")
		git("-C", dir, "config", "user.email", "test@example.com")
	}
	git("-C", other, "commit", "--allow-empty", "-qm", "base")
	git("-C", other, "push", "-q", "origin", "HEAD:main")
	if err := fetchRemote(clone); err != nil {
		t.Fatal(err)
	}
	if got, want := git("-C", clone, "rev-parse", "origin/main"), git("-C", other, "rev-parse", "HEAD"); got != want {
		t.Fatalf("origin/main = %s after fetch, want %s", got, want)
	}
	if err := fetchRemote(filepath.Join(parent, "missing")); err == nil {
		t.Fatal("fetch in a missing directory succeeded")
	}

	model := newDashboard(clone)
	model.width, model.height, model.tab, model.focused = 120, 24, tabWorktrees, true
	model.worktrees = []item{{kind: "worktree", target: clone, cwd: clone, branch: "main", gitLoaded: true}}
	updated, command := model.Update(runeKey('f'))
	model = updated.(dashboardModel)
	if command == nil || !model.fetching || !strings.Contains(ansi.Strip(model.renderHeader(model.width)), "fetching") {
		t.Fatalf("f did not start a visible fetch: fetching=%v", model.fetching)
	}
	if _, second := model.Update(runeKey('f')); second != nil {
		t.Fatal("a second fetch started while one was running")
	}
	result, ok := command().(worktreeActionMsg)
	if !ok || result.err != nil || result.action != actionFetch || !result.background {
		t.Fatalf("fetch result = %#v", result)
	}
	model.worktreeGitRefreshed = time.Now()
	updated, _ = model.Update(result)
	model = updated.(dashboardModel)
	if model.fetching || !model.worktreeGitRefreshed.IsZero() || model.notice != "Fetched clone" {
		t.Fatalf("fetch result was not applied: fetching=%v notice=%q", model.fetching, model.notice)
	}
}

// mergedTestRepo adds worktrees whose pull requests gh reports as merged or
// open: done matches its merged commit, moved has a commit made after the
// merge, dirty has an uncommitted file, and open is not merged.
func mergedTestRepo(t *testing.T) (repo string, worktrees map[string]item, git func(...string) string) {
	t.Helper()
	parent, repo, git := removalTestRepo(t)
	worktrees = map[string]item{}
	var pullRequests []string
	for number, branch := range []string{"done", "moved", "dirty", "open"} {
		path := filepath.Join(parent, branch)
		git("-C", repo, "worktree", "add", "-qb", branch, path)
		git("-C", path, "commit", "--allow-empty", "-qm", branch)
		state := "MERGED"
		if branch == "open" {
			state = "OPEN"
		}
		pullRequests = append(pullRequests, fmt.Sprintf(`{"number":%d,"state":%q,"headRefName":%q,"headRefOid":%q,"isCrossRepository":false}`,
			number+1, state, branch, strings.TrimSpace(git("-C", path, "rev-parse", "HEAD"))))
		worktrees[branch] = item{kind: "worktree", target: path, cwd: path, repo: repo, branch: branch, prNumber: number + 1, prState: state, prLoaded: true}
	}
	git("-C", worktrees["moved"].cwd, "commit", "--allow-empty", "-qm", "after the merge")
	if err := os.WriteFile(filepath.Join(worktrees["dirty"].cwd, "wip.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin, response := t.TempDir(), filepath.Join(t.TempDir(), "prs.json")
	if err := os.WriteFile(response, []byte("["+strings.Join(pullRequests, ",")+"]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\ncat \""+response+"\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	resetPullRequests := func() {
		pullRequestMemory.Lock()
		pullRequestMemory.values = nil
		pullRequestMemory.Unlock()
	}
	resetPullRequests()
	t.Cleanup(resetPullRequests)
	return repo, worktrees, git
}

func TestRemoveMergedWorktreeChecksPullRequestChangesAndBranch(t *testing.T) {
	repo, worktrees, git := mergedTestRepo(t)
	branchExists := func(branch string) bool {
		return exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch).Run() == nil
	}

	if _, err := removeMergedWorktree(repo, worktrees["open"], true); err == nil || !strings.Contains(err.Error(), "not merged") {
		t.Fatalf("open pull request removal = %v", err)
	}
	if _, err := removeMergedWorktree(repo, worktrees["dirty"], true); err == nil || !strings.Contains(err.Error(), "untracked") {
		t.Fatalf("dirty worktree removal = %v", err)
	}
	switched := worktrees["done"]
	switched.branch = "moved"
	if _, err := removeMergedWorktree(repo, switched, true); err == nil || !strings.Contains(err.Error(), "worktree changed") {
		t.Fatalf("removal of a worktree that no longer has the listed branch = %v", err)
	}
	for _, branch := range []string{"open", "dirty", "done"} {
		if !fileExists(worktrees[branch].cwd) || !branchExists(branch) {
			t.Fatalf("refused removal still touched %s", branch)
		}
	}

	if deleted, err := removeMergedWorktree(repo, worktrees["done"], false); err != nil || deleted || fileExists(worktrees["done"].cwd) || !branchExists("done") {
		t.Fatalf("removal that keeps the branch = %v, %v", deleted, err)
	}
	git("-C", repo, "worktree", "add", "-q", worktrees["done"].cwd, "done")
	if deleted, err := removeMergedWorktree(repo, worktrees["done"], true); err != nil || !deleted || fileExists(worktrees["done"].cwd) || branchExists("done") {
		t.Fatalf("removal with the branch = %v, %v", deleted, err)
	}
	// The branch gained a commit after its pull request merged; only the worktree goes.
	if deleted, err := removeMergedWorktree(repo, worktrees["moved"], true); err != nil || deleted || fileExists(worktrees["moved"].cwd) || !branchExists("moved") {
		t.Fatalf("removal of a branch that moved on = %v, %v", deleted, err)
	}
}

func TestRemoveMergedPromptSkipsDirtyWorktreesAndConfirms(t *testing.T) {
	repo, worktrees, _ := mergedTestRepo(t)
	model := newDashboard(repo)
	model.width, model.height, model.tab = 120, 30, tabWorktrees
	model.worktrees = []item{{kind: "worktree", target: repo, cwd: repo, repo: repo, branch: "main", primary: true}, worktrees["done"], worktrees["moved"], worktrees["dirty"], worktrees["open"]}
	if labels := fmt.Sprint(model.actionMenuEntries()); !strings.Contains(labels, "Remove merged worktrees (3)") {
		t.Fatalf("action menu = %s", labels)
	}
	protected := model
	protected.worktrees = []item{model.worktrees[0], func() item { current := worktrees["done"]; current.current = true; return current }()}
	if updated, command := protected.Update(runeKey('X')); command != nil || updated.(dashboardModel).action != actionNone {
		t.Fatal("X offered to remove the current worktree")
	}

	updated, command := model.Update(runeKey('X'))
	model = updated.(dashboardModel)
	if command == nil || model.action != actionRemoveMerged || !model.confirming() {
		t.Fatalf("X did not open the prompt: action=%d", model.action)
	}
	if view := ansi.Strip(model.View()); !strings.Contains(view, "Checking for changes") || strings.Contains(view, "Enter Remove") {
		t.Fatalf("prompt offered keys before the checks finished:\n%s", view)
	}
	if updated, early := model.Update(tea.KeyMsg{Type: tea.KeyEnter}); early != nil || updated.(dashboardModel).action != actionRemoveMerged {
		t.Fatal("Enter ran before the checks finished")
	}
	updated, _ = model.Update(command())
	model = updated.(dashboardModel)
	view := ansi.Strip(model.View())
	for _, want := range []string{"Remove merged worktrees", "done #1", "moved #2", "dirty #3: skipped, has 1 untracked file", "Remove 2 merged?", "Enter Remove", "D +Branch", "Esc Cancel"} {
		if !strings.Contains(view, want) {
			t.Fatalf("prompt misses %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "open #4") {
		t.Fatalf("prompt lists an open pull request:\n%s", view)
	}

	if updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc}); updated.(dashboardModel).action != actionNone || !fileExists(worktrees["done"].cwd) {
		t.Fatal("Esc did not cancel the removal")
	}
	updated, command = model.Update(runeKey('D'))
	model = updated.(dashboardModel)
	if command == nil || model.action != actionRunning {
		t.Fatalf("D did not start the removal: action=%d", model.action)
	}
	result := command().(worktreeActionMsg)
	if result.err != nil || result.action != actionRemoveMerged || result.notice != "Removed 2 merged worktrees and deleted 1 branch; kept 1 that changed after merging" {
		t.Fatalf("removal result = %#v", result)
	}
	if fileExists(worktrees["done"].cwd) || fileExists(worktrees["moved"].cwd) || !fileExists(worktrees["dirty"].cwd) || !fileExists(worktrees["open"].cwd) {
		t.Fatal("removal did not match the prompt")
	}
	generation := model.worktreeGeneration
	updated, refresh := model.Update(result)
	if got := updated.(dashboardModel); refresh == nil || got.worktreeGeneration == generation || got.action != actionNone {
		t.Fatal("removal did not refresh the worktree list")
	}
}

func TestWorktreeScopeListsOtherRepositories(t *testing.T) {
	parent := t.TempDir()
	first, second, plain := filepath.Join(parent, "first"), filepath.Join(parent, "second"), filepath.Join(parent, "plain")
	git := func(args ...string) {
		t.Helper()
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	for repo, branch := range map[string]string{first: "main", second: "master"} {
		git("init", "-q", "-b", branch, repo)
		git("-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-qm", "base")
		git("-C", repo, "worktree", "add", "-qb", "feature", repo+"-feature")
	}
	if err := os.Mkdir(plain, 0o755); err != nil {
		t.Fatal(err)
	}

	items, err := listRepositoryWorktrees(first, []string{plain, second + "-feature", first + "-feature", second})
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, len(items))
	for index, worktree := range items {
		paths[index] = worktree.cwd
		wantRepo := first
		if strings.HasPrefix(worktree.cwd, second) {
			wantRepo = second
		}
		if worktree.repo != wantRepo || worktree.current != (worktree.cwd == first) {
			t.Fatalf("worktree %s: repo=%s current=%v", worktree.cwd, worktree.repo, worktree.current)
		}
	}
	if want := []string{first, first + "-feature", second, second + "-feature"}; !slices.Equal(paths, want) {
		t.Fatalf("worktrees = %q, want %q", paths, want)
	}
	// Each repository compares with its own default branch.
	for _, detail := range worktreeGitDetails(items) {
		want := map[string]string{first + "-feature": "main", second + "-feature": "master"}[detail.cwd]
		if detail.branch == "feature" && detail.baseBranch != want {
			t.Fatalf("%s base branch = %q, want %q", detail.cwd, detail.baseBranch, want)
		}
	}

	if _, err := listRepositoryWorktrees(plain, nil); err == nil {
		t.Fatal("a directory outside Git listed worktrees")
	}
	if items, err := listRepositoryWorktrees(plain, []string{second}); err != nil || len(items) != 2 {
		t.Fatalf("other repositories from outside Git = %d, %v", len(items), err)
	}
}

func TestWorktreeScopeToggle(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height, model.tab = 120, 24, tabWorktrees
	model.worktreesLoaded = true
	model.allAgents = []item{{kind: "session", target: "%1", cwd: "/other"}, {kind: "session", target: "%2", cwd: "/other"}}
	model.sessions = []item{{kind: "tmux-session", target: "api", cwd: "/api", sessionSource: "config"}, {kind: "tmux-session", target: "found", cwd: "/found", sessionSource: "discovered"}}
	model.worktrees = []item{{kind: "worktree", target: "/repo", cwd: "/repo", repo: "/repo", branch: "main"}}
	if roots := model.worktreeRoots(); roots != nil {
		t.Fatalf("one-repository scope lists other directories: %q", roots)
	}
	if footer := ansi.Strip(model.renderFooter(model.width)); !strings.Contains(footer, "s Scope (Repo)") {
		t.Fatalf("footer = %q", footer)
	}

	generation := model.worktreeGeneration
	updated, command := model.Update(runeKey('s'))
	model = updated.(dashboardModel)
	if command == nil || !model.worktreeScopeAll || model.worktreeGeneration == generation {
		t.Fatal("s did not reload the worktrees for every repository")
	}
	if roots := model.worktreeRoots(); !slices.Equal(roots, []string{"/api", "/other"}) {
		t.Fatalf("all-repositories scope reads %q", roots)
	}
	model.worktrees = append(model.worktrees, item{kind: "worktree", target: "/other", cwd: "/other", repo: "/other", branch: "main"})
	model.index = 1
	if header := ansi.Strip(model.renderHeader(model.width)); !strings.Contains(header, "[Worktrees 2 · All]") {
		t.Fatalf("header = %q", header)
	}
	model.notice = "" // the scope notice covers the footer for a moment
	if footer := ansi.Strip(model.renderFooter(model.width)); !strings.Contains(footer, "s Scope (All)") {
		t.Fatalf("footer = %q", footer)
	}
	// New worktrees and agents go to the selected row's repository.
	if repo := model.actionRepo(); repo != "/other" {
		t.Fatalf("action repository = %q", repo)
	}

	updated, _ = model.Update(runeKey('s'))
	model = updated.(dashboardModel)
	if model.worktreeScopeAll || len(model.worktrees) != 1 || model.worktrees[0].target != "/repo" {
		t.Fatalf("returning to one repository kept other rows: %#v", model.worktrees)
	}

	outside := newDashboard(t.TempDir())
	outside.width, outside.height, outside.tab, outside.worktreesLoaded = 120, 24, tabWorktrees, true
	_, outside.worktreeErr = listWorktreeItems(outside.cwd)
	if view := ansi.Strip(outside.renderTable(outside.width)); !strings.Contains(view, "s shows all repos") {
		t.Fatalf("empty state does not offer the wider scope: %q", view)
	}
}
