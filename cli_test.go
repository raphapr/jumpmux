package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestContextJSONKeepsAgentAndTmuxIdentity(t *testing.T) {
	entries := contextJSON([]item{{
		kind:           "session",
		target:         "%7",
		title:          "Pi",
		cwd:            "/repo",
		status:         "done",
		agentSessionID: "stable-session",
		muxSessionID:   "$1",
		muxWindowID:    "@2",
	}})
	if len(entries) != 1 {
		t.Fatalf("entries = %#v", entries)
	}
	entry := entries[0]
	if entry.ID != "%7" || entry.Name != "Pi" || entry.Path != "/repo" || entry.Icon != dashboardIcon(doneIcon, "D")+" "+dashboardIcon(unseenAgentIcon, "U") {
		t.Fatalf("base JSON fields changed: %#v", entry)
	}
	if entry.Kind != "agent" || entry.SessionID != "stable-session" || entry.Attention != "unseen" || entry.TmuxSessionID != "$1" || entry.TmuxWindowID != "@2" {
		t.Fatalf("agent JSON identity = %#v", entry)
	}
}

func TestContextJSONStaysResourceScoped(t *testing.T) {
	entries := contextJSON([]item{
		{kind: "tmux-session", target: "dev", muxSessionID: "$1", muxWindowID: "@2"},
		{kind: "worktree", target: "/repo", cwd: "/repo", branch: "feature", status: "question", agentSessionID: "agent-id", prNumber: 7, prURL: "https://example.test/7"},
	})
	if !entries[0].Live || entries[0].TmuxWindowID != "@2" || entries[1].Status != "question" || entries[1].Attention != "question" || entries[1].SessionID != "agent-id" {
		t.Fatalf("resource JSON = %#v", entries)
	}
	data, err := json.Marshal(entries)
	if err != nil || strings.Contains(string(data), "last_attached") || strings.Contains(string(data), "pr_draft") || strings.Contains(string(data), `"live":false`) {
		t.Fatalf("resource JSON leaked unrelated fields: %s, %v", data, err)
	}
}

func TestFindItemAcceptsStableAgentSessionIDAndPaneID(t *testing.T) {
	items := []item{{kind: "session", target: "%7", agentSessionID: "stable-session"}}
	for _, id := range []string{"stable-session", "%7"} {
		if selected, ok := findItem(items, id); !ok || selected.target != "%7" {
			t.Fatalf("findItem(%q) = %#v, %v", id, selected, ok)
		}
	}
}

func TestConfirmationParsing(t *testing.T) {
	args, yes, err := parseYes([]string{"%7", "--yes"})
	if err != nil || !yes || len(args) != 1 || args[0] != "%7" {
		t.Fatalf("parse yes = %#v, %v, %v", args, yes, err)
	}
	if _, _, err := parseYes([]string{"--yes", "%7"}); err == nil {
		t.Fatal("non-final --yes was accepted")
	}
	var output bytes.Buffer
	if err := confirmWith("remove", strings.NewReader("yes\n"), &output); err != nil || !strings.Contains(output.String(), "remove?") {
		t.Fatalf("confirmation = %q, %v", output.String(), err)
	}
	if err := confirmWith("remove", strings.NewReader("no\n"), &output); err == nil {
		t.Fatal("declined confirmation was accepted")
	}
}

func TestCommandDispatchRejectsUnknownAndMalformedOperations(t *testing.T) {
	for noun, err := range map[string]error{
		"agent":    runAgentCommand(nil),
		"worktree": runWorktreeCommand("", nil),
		"session":  runSessionCommand(nil),
	} {
		if err == nil || !strings.Contains(err.Error(), "jumpmux "+noun+" ") {
			t.Fatalf("%s usage is not canonical: %v", noun, err)
		}
	}
	for _, test := range []struct {
		name string
		err  error
	}{
		{"agent", runAgentCommand([]string{"other"})},
		{"worktree", runWorktreeCommand("", []string{"other"})},
		{"session", runSessionCommand([]string{"other"})},
		{"removed next", runAgentCommand([]string{"next"})},
		{"removed seen", runAgentCommand([]string{"seen"})},
		{"removed read", runAgentCommand([]string{"read", "%1"})},
		{"removed wait", runAgentCommand([]string{"wait", "%1"})},
		{"removed prompt", runAgentCommand([]string{"prompt", "%1"})},
		{"last", runSessionCommand([]string{"last", "extra"})},
		{"empty session", runSessionCommand(nil)},
		{"prompted worktree", func() error { _, err := parseWorktreeAdd([]string{"feature", "--prompt-file", "x"}); return err }()},
	} {
		if test.err == nil {
			t.Fatalf("%s operation was accepted", test.name)
		}
	}
}

func TestWorktreeAddParserSupportsDetachAndJSON(t *testing.T) {
	options, err := parseWorktreeAdd([]string{"feature", "--detach", "--json"})
	if err != nil || options.branch != "feature" || !options.detach || !options.json {
		t.Fatalf("worktree add options = %#v, %v", options, err)
	}
	data, err := json.Marshal(contextItem{ID: "/feature", Kind: "worktree"})
	if err != nil || !strings.Contains(string(data), `"id":"/feature"`) || strings.Contains(string(data), `"worktree":`) {
		t.Fatalf("worktree add JSON = %s, %v", data, err)
	}
}

func TestWorktreeListsDegradeWithoutTmux(t *testing.T) {
	repo, bin := t.TempDir(), t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.com"}, {"commit", "--allow-empty", "-qm", "base"}} {
		if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\necho 'no server running on /tmp/tmux-test' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	invalidateTmuxPaneCache()

	for name, load := range map[string]func() ([]item, error){
		"resource list": func() ([]item, error) { return cliItems("worktrees", repo) },
		"legacy list":   func() ([]item, error) { return collectItemsFor(repo) },
	} {
		items, err := load()
		if err != nil || len(items) == 0 || items[0].kind != "worktree" {
			t.Fatalf("%s without tmux = %#v, %v", name, items, err)
		}
	}
}

func TestWorktreeAddRequiresTmuxBeforeCreation(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(path, []byte("worktree_backend = \"git\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runWorktreeAdd(t.TempDir(), []string{"feature"}); err == nil || !strings.Contains(err.Error(), "inside tmux") {
		t.Fatalf("worktree add outside tmux = %v", err)
	}
}

func TestWorktreeAddOpensTmuxShell(t *testing.T) {
	repo, bin := t.TempDir(), t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.com"}, {"commit", "--allow-empty", "-qm", "base"}} {
		if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	meta := filepath.Join(t.TempDir(), "window")
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(`#!/bin/sh
if test "$1" = new-window; then
  shift
  cwd= name=
  while test $# -gt 0; do
    case "$1" in
      -c) cwd=$2; shift 2 ;;
      -n) name=$2; shift 2 ;;
      -F) shift 2 ;;
      -d|-P) shift ;;
      *) exit 2 ;;
    esac
  done
  printf '%s\n%s\n' "$cwd" "$name" >"$TMUX_META"
  printf '$1\t@9\t%%9\n'
fi
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", "/tmp/test,1,0")
	t.Setenv("TMUX_META", meta)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	config, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(config, []byte("worktree_backend = \"git\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runWorktreeAdd(repo, []string{"feature"}); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"__worktrees", "feature")
	if data, err := os.ReadFile(meta); err != nil || string(data) != worktree+"\nfeature\n" {
		t.Fatalf("tmux window = %q, %v", data, err)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("created worktree is missing: %v", err)
	}
}
