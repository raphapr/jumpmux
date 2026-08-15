package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParsers(t *testing.T) {
	t.Run("git worktrees", func(t *testing.T) {
		data := []byte("worktree /repo\x00HEAD abc\x00branch refs/heads/main\x00\x00worktree /repo-feature\x00HEAD def\x00detached")
		got := parseWorktrees(data)
		if len(got) != 2 || got[0].branch != "main" || got[1].branch != "detached" {
			t.Fatalf("unexpected worktrees: %#v", got)
		}
	})

	t.Run("pull requests", func(t *testing.T) {
		got := parsePullRequests([]byte(`[{"number":42,"state":"OPEN","isDraft":false,"headRefName":"feature","isCrossRepository":false,"statusCheckRollup":[{"status":"COMPLETED","conclusion":"SUCCESS"},{"status":"COMPLETED","conclusion":"FAILURE"}]}]`))
		if len(got["feature"]) != 1 || got["feature"][0].Number != 42 || got["feature"][0].State != "OPEN" || got["feature"][0].Check != checkFailure {
			t.Fatalf("unexpected pull requests: %#v", got)
		}
	})
}

func TestMainHelpAndRemovedDashboardSelectors(t *testing.T) {
	if args := os.Getenv("JUMPMUX_MAIN_TEST_ARGS"); args != "" {
		os.Args = append([]string{"jumpmux"}, strings.Fields(args)...)
		main()
		return
	}
	run := func(args string) ([]byte, error) {
		command := exec.Command(os.Args[0], "-test.run=^TestMainHelpAndRemovedDashboardSelectors$")
		command.Env = append(os.Environ(), "JUMPMUX_MAIN_TEST_ARGS="+args)
		return command.CombinedOutput()
	}
	help, err := run("--help")
	if err != nil || strings.Contains(string(help), "--session") || strings.Contains(string(help), "jumpmux <agents|worktrees|sessions>") || !strings.Contains(string(help), "--tab <agents|worktrees|sessions>") || !strings.Contains(string(help), "--yes for non-interactive use") {
		t.Fatalf("help = %q, %v", help, err)
	}
	for _, args := range []string{"agents", "worktrees", "sessions", "--session", "-s"} {
		output, err := run(args)
		if err == nil || !strings.Contains(string(output), "unknown argument") {
			t.Fatalf("removed selector %q = %q, %v", args, output, err)
		}
	}
}

func TestDashboardTab(t *testing.T) {
	for name, want := range map[string]int{
		"agents": tabAgents, "worktrees": tabWorktrees, "sessions": tabSessions,
	} {
		got, err := dashboardTab(name)
		if err != nil || got != want {
			t.Fatalf("dashboardTab(%q) = %d, %v", name, got, err)
		}
	}
	if _, err := dashboardTab("other"); err == nil || !strings.Contains(err.Error(), "agents, worktrees, or sessions") {
		t.Fatalf("invalid tab error is not plural canonical: %v", err)
	}
}

func TestContextJSON(t *testing.T) {
	t.Setenv("JUMPMUX_PLAIN", "")
	entries := contextJSON([]item{
		{kind: "session", target: "%1", title: "agent", cwd: "/agent", status: "working"},
		{kind: "worktree", target: "/repo", title: "feature", cwd: "/repo", pane: "%2"},
		{kind: "tmux-session", target: "dev", title: "dev", cwd: "/dev", muxSessionID: "$1", tmuxWindows: 2},
	})
	if len(entries) != 3 || entries[0].ID != "%1" || entries[0].Icon != workingIcon || entries[1].Name != "feature" || entries[2].Name != "dev" || entries[2].Path != "/dev" {
		t.Fatalf("context JSON = %#v", entries)
	}
}

func TestContextJSONCompletionAndQuestionIcons(t *testing.T) {
	t.Setenv("JUMPMUX_PLAIN", "")
	defer func() { nerdFontEnabled = true }()
	nerdFontEnabled = false
	entries := contextJSON([]item{{kind: "session", status: "done", seen: true}, {kind: "session", status: "done"}, {kind: "session", status: "question"}})
	if entries[0].Icon != "D" || entries[1].Icon != "D U" || entries[2].Icon != "?" || entries[2].Status != "question" || entries[2].Attention != "question" {
		t.Fatalf("plain agent icons = %#v", entries)
	}
	nerdFontEnabled = true
	entries = contextJSON([]item{{kind: "session", status: "done", seen: true}, {kind: "session", status: "done"}, {kind: "session", status: "question"}})
	if entries[0].Icon != doneIcon || entries[1].Icon != doneIcon+" "+unseenAgentIcon || entries[2].Icon != questionIcon {
		t.Fatalf("Nerd Font agent icons = %#v", entries)
	}
}

func TestSessionIconPrecedenceAcrossContext(t *testing.T) {
	defer func() { nerdFontEnabled = true }()
	nerdFontEnabled = false
	items := []item{
		{kind: "tmux-session", title: "live", sessionSource: "config", muxSessionID: "$1"},
		{kind: "tmux-session", title: "configured", sessionSource: "config"},
		{kind: "tmux-session", title: "discovered", sessionSource: "discovered"},
	}
	want := []string{"L", "C", "R"}
	for index, session := range items {
		icon, _ := sessionIcon(session)
		if icon != want[index] || contextJSON([]item{session})[0].Icon != want[index] {
			t.Fatalf("session %q icon = %q / %#v, want %q", session.title, icon, contextJSON([]item{session})[0], want[index])
		}
	}
}

func TestContextCommandValidation(t *testing.T) {
	for _, args := range [][]string{{"agents"}, {"worktrees"}, {"sessions"}, {"--help"}} {
		if handled, err := contextCommand(args); handled || err != nil {
			t.Fatalf("dashboard command %q = %t, %v", args, handled, err)
		}
	}
	for _, args := range [][]string{{"agent"}, {"worktree"}, {"session"}, {"agent", "--json"}, {"session", "list", "--yaml"}, {"agent", "connect", "%1"}, {"session", "connect", "dev"}, {"session", "last", "extra"}, {"worktree", "other", "x"}} {
		if handled, err := contextCommand(args); !handled || err == nil {
			t.Fatalf("contextCommand(%q) = %t, %v", args, handled, err)
		}
	}
	for _, args := range [][]string{{"agents", "other"}, {"worktrees", "other"}, {"sessions", "other"}} {
		if handled, err := contextCommand(args); handled || err != nil {
			t.Fatalf("plural command %q was accepted: %t, %v", args, handled, err)
		}
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

func TestCountFileLinesFIFOReturnsPromptly(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := exec.Command("mkfifo", fifo).Run(); err != nil {
		t.Skip("mkfifo unavailable")
	}
	info, err := os.Lstat(fifo)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() { done <- countFileLines(fifo, info) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("FIFO line count blocked")
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
