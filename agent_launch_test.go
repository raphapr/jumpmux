package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestParseAgentNew(t *testing.T) {
	options, err := parseAgentNew([]string{"fix the cache", "--branch", "fix/cache", "--repo", "~/src/app", "--background"})
	if err != nil || options != (agentNewOptions{prompt: "fix the cache", branch: "fix/cache", repo: "~/src/app", background: true}) {
		t.Fatalf("options = %#v, %v", options, err)
	}
	for _, args := range [][]string{nil, {"  "}, {"--background"}, {"one", "two"}, {"task", "--branch"}, {"task", "--repo", ""}, {"task", "--yes"}} {
		if _, err := parseAgentNew(args); err == nil {
			t.Errorf("parseAgentNew(%q) succeeded", args)
		}
	}
}

func TestAgentCommandValidationAndExpansion(t *testing.T) {
	for _, command := range [][]string{nil, {""}, {"{branch}"}, {"pi", "{nope}"}, {"pi", "{branch"}, {"pi", "branch}"}, {"pi", "a\nb"}} {
		if err := validateAgentCommand("command", command, true); err == nil {
			t.Errorf("command %q passed validation", command)
		}
	}
	if err := validateAgentCommand("name_command", []string{"pi", "{literal}"}, false); err != nil {
		t.Fatalf("name_command braces rejected: %v", err)
	}

	values := map[string]string{"branch": "fix/x", "session": "repo/fix-x", "prompt": "use {branch} literally", "prompt_file": "/state/p.md"}
	if got := expandAgentCommand(defaultAgentCommand, values); !slices.Equal(got, []string{"pi", "--name", "fix/x", "@/state/p.md"}) {
		t.Fatalf("default expansion = %q", got)
	}
	if got := expandAgentCommand([]string{"claude", "--branch={branch}"}, values); !slices.Equal(got, []string{"claude", "--branch=fix/x", "use {branch} literally"}) {
		t.Fatalf("appended prompt expansion = %q", got)
	}
	delete(values, "prompt")
	if got := expandAgentCommand([]string{"pi", "--name", "{session}", "@{prompt_file}"}, values); !slices.Equal(got, []string{"pi", "--name", "repo/fix-x"}) {
		t.Fatalf("promptless expansion = %q", got)
	}
}

func TestAgentConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	config, err := loadConfig()
	if err != nil || !slices.Equal(config.agentCommand, defaultAgentCommand) || !slices.Equal(config.agentNameCommand, defaultAgentNameCommand) {
		t.Fatalf("default agent config = %q, %q, %v", config.agentCommand, config.agentNameCommand, err)
	}
	writeSessionsConfig(t, "[agent]\ncommand = [\"pi\", \"--model\", \"x\"]\nname_command = [\"pi\", \"-p\", \"--model\", \"anthropic/claude-haiku-4-5\"]\n")
	if config, err = loadConfig(); err != nil || config.agentCommand[2] != "x" || config.agentNameCommand[3] != "anthropic/claude-haiku-4-5" {
		t.Fatalf("custom agent config = %q, %q, %v", config.agentCommand, config.agentNameCommand, err)
	}
	if _, err := loadSessionsConfig(); err != nil {
		t.Fatalf("sessions loader rejected [agent]: %v", err)
	}
	for data, want := range map[string]string{
		"[agent]\ncommand = []\n":                   "non-empty array",
		"[agent]\ncommand = [\"pi\", \"{oops}\"]\n": "unknown placeholder",
		"[agent]\nname_command = [\"\"]\n":          "start with an executable",
		"[agent]\nmodel = \"x\"\n":                  "agent uses unsupported \"model\"",
	} {
		writeSessionsConfig(t, data)
		if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("config %q error = %v, want %q", data, err, want)
		}
	}
}

func TestAgentBranchNames(t *testing.T) {
	for output, want := range map[string]string{
		"feat/pr-status\n":              "feat/pr-status",
		"Sure:\n`fix/flaky-cache-test`": "fix/flaky-cache-test",
		"\"chore/bump-go\"":             "chore/bump-go",
	} {
		if got, err := parseAgentBranch(output); err != nil || got != want {
			t.Errorf("parseAgentBranch(%q) = %q, %v", output, got, err)
		}
	}
	for _, output := range []string{"", "Feat/x", "wip/x", "feat/x--y", "feat/" + strings.Repeat("a", 40), "feat x"} {
		if got, err := parseAgentBranch(output); err == nil {
			t.Errorf("parseAgentBranch(%q) accepted %q", output, got)
		}
	}
	for prompt, want := range map[string]string{
		"Fix the flaky cache test!":                          "agent/fix-the-flaky-cache-test",
		"make the dashboard show pull request review status": "agent/make-the-dashboard-show-pull",
		strings.Repeat("x", 50):                              "agent/" + strings.Repeat("x", 34),
		"¿¿??":                                               "agent/task",
	} {
		if got := fallbackAgentBranch(prompt); got != want || len(got) > agentBranchMaxLength {
			t.Errorf("fallbackAgentBranch(%q) = %q, want %q", prompt, got, want)
		}
	}
}

func TestNameAgentBranchUsesConfiguredCommandAndFallsBack(t *testing.T) {
	repo := newAgentTestRepo(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	script := filepath.Join(t.TempDir(), "namer")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nfor last; do :; done\nprintf '%s' \"$last\" > \"$0.prompt\"\necho 'fix/cache-test'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSessionsConfig(t, "[agent]\nname_command = [\""+script+"\"]\n")
	branch, warning, err := nameAgentBranch(repo, "fix the cache test")
	if err != nil || warning != nil || branch != "fix/cache-test" {
		t.Fatalf("named branch = %q, %v, %v", branch, warning, err)
	}
	if got, _ := os.ReadFile(script + ".prompt"); !strings.HasPrefix(string(got), agentNameInstruction) || !strings.HasSuffix(string(got), "fix the cache test") {
		t.Fatalf("naming prompt = %q", got)
	}

	writeSessionsConfig(t, "[agent]\nname_command = [\"false\"]\n")
	branch, warning, err = nameAgentBranch(repo, "fix the cache test")
	if err != nil || warning == nil || branch != "agent/fix-the-cache-test" {
		t.Fatalf("fallback branch = %q, %v, %v", branch, warning, err)
	}
}

// fakeAgentTmux logs tmux calls. FAKE_LIVE_ROOT, when set, is the start
// directory of a live session $3 named live. FAKE_PANE_ROOT, when set, is the
// active pane path of an unconfigured live session $4 started elsewhere.
func fakeAgentTmux(t *testing.T) string {
	t.Helper()
	log := writeFakeTmux(t, `printf '%s\n' "$*" >> "$TMUX_LOG"
case "$1" in
  list-sessions)
    if [ -n "$FAKE_LIVE_ROOT" ]; then printf '$3\037live\037%s\n' "$FAKE_LIVE_ROOT"; fi
    if [ -n "$FAKE_PANE_ROOT" ]; then printf '$4\037session2\037/elsewhere\n'; fi ;;
  list-panes) if [ -n "$FAKE_PANE_ROOT" ]; then printf '$4\037session2\0371\0370\0371\0371\037%%4\037%s\036\n' "$FAKE_PANE_ROOT"; fi ;;
  display-message) echo "can't find session: x" >&2; exit 1 ;;
  show-options) echo /test/shell ;;
  new-session) printf '$9\n' ;;
  new-window) printf '@6\t%%7\n' ;;
esac
`)
	t.Setenv("TMUX_LOG", log)
	return log
}

func TestProjectSessionPrefersLiveThenConfiguredThenRepoName(t *testing.T) {
	root := newAgentTestRepo(t)
	for _, test := range []struct {
		name, liveRoot, paneRoot, config, wantID, wantName, wantCreate string
	}{
		{"live session at root", root, "", "", "$3", "live", ""},
		{"configured entry", "", "", "[[sessions.entries]]\nname = \"proj\"\npath = \"" + root + "\"\n", "$9", "proj", "new-session -d -P -F #{session_id} -s proj -c " + root},
		{"repository name", "", "", "", "$9", "app", "new-session -d -P -F #{session_id} -s app -c " + root},
		{"unrelated session visiting root", "", root, "", "$9", "app", "new-session -d -P -F #{session_id} -s app -c " + root},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			if test.config != "" {
				writeSessionsConfig(t, test.config)
			}
			t.Setenv("FAKE_LIVE_ROOT", test.liveRoot)
			t.Setenv("FAKE_PANE_ROOT", test.paneRoot)
			log := fakeAgentTmux(t)
			id, name, err := projectSession(root)
			if err != nil || id != test.wantID || name != test.wantName {
				t.Fatalf("projectSession = %q, %q, %v", id, name, err)
			}
			data, _ := os.ReadFile(log)
			if created := strings.Contains(string(data), "new-session"); created != (test.wantCreate != "") || !strings.Contains(string(data), test.wantCreate) {
				t.Fatalf("tmux calls:\n%s", data)
			}
		})
	}
}

func TestStartAgentCreatesNoSessionWhenWorktreeFails(t *testing.T) {
	repo := newAgentTestRepo(t)
	if output, err := exec.Command("git", "-C", repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/develop").CombinedOutput(); err != nil {
		t.Fatalf("git symbolic-ref: %v\n%s", err, output)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("FAKE_LIVE_ROOT", "")
	t.Setenv("FAKE_PANE_ROOT", "")
	log := fakeAgentTmux(t)
	if _, err := startAgent(repo, "fix/cache", "task"); err == nil || !strings.Contains(err.Error(), "not available locally") {
		t.Fatalf("startAgent = %v", err)
	}
	if data, _ := os.ReadFile(log); strings.Contains(string(data), "new-session") {
		t.Fatalf("failed worktree add left a session:\n%s", data)
	}
}

func TestStartAgentOpensWindowInProjectSession(t *testing.T) {
	repo := newAgentTestRepo(t)
	if output, err := exec.Command("git", "-C", repo, "branch", "fix/cache").CombinedOutput(); err != nil {
		t.Fatalf("git branch: %v\n%s", err, output)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("FAKE_LIVE_ROOT", repo)
	t.Setenv("FAKE_PANE_ROOT", "")
	log := fakeAgentTmux(t)

	launch, err := startAgent(repo, "fix/cache", "fix the cache\nwith 'quotes' and $HOME")
	if err != nil {
		t.Fatal(err)
	}
	if launch != (agentLaunch{branch: "fix/cache-2", session: "live", sessionID: "$3", windowID: "@6", pane: "%7"}) {
		t.Fatalf("launch = %#v", launch)
	}
	worktree := worktreePath(repo, "fix/cache-2")
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("worktree missing: %v", err)
	}
	data, _ := os.ReadFile(log)
	calls := string(data)
	if strings.Contains(calls, "new-session") {
		t.Fatalf("agent created a session instead of using the project session:\n%s", calls)
	}
	for _, want := range []string{
		"new-window -d -P -F #{window_id}\t#{pane_id} -t $3: -n fix/cache-2 -c " + worktree + ` /bin/sh -c "$0" -i -c "$1"; exec "$0" /test/shell `,
		"set-option -w -t @6 @jumpmux_worktree " + worktree,
	} {
		if !strings.Contains(calls, want) {
			t.Errorf("tmux calls missing %q:\n%s", want, calls)
		}
	}
	// Typed keys race shell startup, so the pane command must start the agent.
	if strings.Contains(calls, "send-keys") {
		t.Fatalf("agent launch typed into the shell:\n%s", calls)
	}
	match := regexp.MustCompile(`/test/shell \S+ agent-exec ([0-9a-f]{32})\n`).FindStringSubmatch(calls)
	if match == nil {
		t.Fatalf("pane command does not run the launch token:\n%s", calls)
	}

	launchPath, promptPath, _ := agentLaunchPaths(match[1])
	for _, path := range []string{launchPath, promptPath} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v, %v", path, info, err)
		}
	}
	got, err := consumeAgentLaunch(match[1])
	if err != nil || got.Dir != worktree || !slices.Equal(got.Argv, []string{"pi", "--name", "fix/cache-2", "@" + promptPath}) {
		t.Fatalf("launch file = %#v, %v", got, err)
	}
	if prompt, _ := os.ReadFile(promptPath); string(prompt) != "fix the cache\nwith 'quotes' and $HOME\n" {
		t.Fatalf("prompt file = %q", prompt)
	}
	if _, err := consumeAgentLaunch(match[1]); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("second launch = %v", err)
	}
}

func TestConsumeAgentLaunchRejectsExpiredAndInvalidIDs(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	id := strings.Repeat("a", 32)
	path, _, _ := agentLaunchPaths(id)
	data, _ := json.Marshal(agentLaunchFile{Argv: []string{"pi"}, Created: time.Now().Add(-2 * agentLaunchMaxAge)})
	if err := atomicWrite(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := consumeAgentLaunch(id); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired launch = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired launch file kept: %v", err)
	}
	if _, err := consumeAgentLaunch("../../etc/passwd"); err == nil || !strings.Contains(err.Error(), "invalid agent launch ID") {
		t.Fatalf("path traversal ID = %v", err)
	}
}

func TestDashboardNewAgentOutsideRepositoryFailsBeforePrompt(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-test,1,0")
	model := newDashboard(t.TempDir())
	model.width, model.height, model.tab = 100, 20, tabAgents
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	if model = updated.(dashboardModel); model.action != actionNone || model.err == nil {
		t.Fatalf("A outside a repository = %v, %v", model.action, model.err)
	}
}

func TestDashboardNewAgentFlowRejectsStaleNames(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-test,1,0")
	model := newDashboard(newAgentTestRepo(t))
	model.width, model.height, model.tab = 100, 20, tabAgents
	press := func(msg tea.KeyMsg) tea.Cmd {
		updated, command := model.Update(msg)
		model = updated.(dashboardModel)
		return command
	}

	press(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	if model.action != actionAgentPrompt {
		t.Fatalf("A on empty Agents tab = %v", model.action)
	}
	press(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("fix cache")})
	if footer := ansi.Strip(model.renderFooter(100)); !strings.Contains(footer, "prompt: ") || !strings.Contains(footer, "Enter Name") {
		t.Fatalf("prompt footer = %q", footer)
	}
	if command := press(tea.KeyMsg{Type: tea.KeyEnter}); command == nil || model.action != actionAgentNaming || model.agentPrompt != "fix cache" {
		t.Fatalf("Enter did not start naming: %v %q", model.action, model.agentPrompt)
	}
	if footer := ansi.Strip(model.renderFooter(100)); !strings.Contains(footer, "Naming branch") || model.modeLabel() != "WORKING" {
		t.Fatalf("naming footer = %q, mode %q", footer, model.modeLabel())
	}

	updated, _ := model.Update(agentNameMsg{request: model.agentNameRequest - 1, branch: "fix/stale"})
	if model = updated.(dashboardModel); model.action != actionAgentNaming {
		t.Fatalf("stale name was applied: %v", model.action)
	}
	updated, _ = model.Update(agentNameMsg{request: model.agentNameRequest, branch: "agent/fix-cache", warning: errors.New("timed out")})
	model = updated.(dashboardModel)
	if model.action != actionAgentBranch || model.actionTextInput.Value() != "agent/fix-cache" {
		t.Fatalf("name result = %v %q", model.action, model.actionTextInput.Value())
	}
	if footer := ansi.Strip(model.renderFooter(100)); !strings.Contains(footer, "naming failed, branch: ") || !strings.Contains(footer, "Enter Create") {
		t.Fatalf("fallback footer = %q", footer)
	}

	press(tea.KeyMsg{Type: tea.KeyEsc})
	press(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	press(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("other")})
	press(tea.KeyMsg{Type: tea.KeyEnter})
	request := model.agentNameRequest
	press(tea.KeyMsg{Type: tea.KeyEsc})
	updated, _ = model.Update(agentNameMsg{request: request, branch: "fix/late"})
	if model = updated.(dashboardModel); model.action != actionNone || model.actionTextInput.Value() != "" {
		t.Fatalf("cancelled naming result was applied: %v %q", model.action, model.actionTextInput.Value())
	}
}

func newAgentTestRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "app")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "config", "user.name", "Test"}, {"-C", repo, "config", "user.email", "test@example.com"}, {"-C", repo, "commit", "--allow-empty", "-qm", "base"}} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	return repo
}
