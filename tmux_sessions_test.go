package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func writeFakeTmux(t *testing.T, script string) string {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return filepath.Join(t.TempDir(), "tmux.log")
}

func writeSessionsConfig(t *testing.T, data string) {
	t.Helper()
	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadConfiguredSessions(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PROJECT", project)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	data := "[sessions]\nexclude = [\"Downloads\"]\ndiscover = [\"~/bin/list-sessions\", \"--all\"]\n[[sessions.entries]]\nname = \"project\"\npath = \"$PROJECT\"\n[future]\nvalue = true\n"
	writeSessionsConfig(t, data)
	if got := expandSessionPath("~/project"); got != filepath.Join(home, "project") {
		t.Fatalf("home expansion = %q", got)
	}
	config, err := loadSessionsConfig()
	if err != nil || config == nil || len(config.sessions) != 1 || config.sessions[0] != (configuredSession{name: "project", path: project}) {
		t.Fatalf("sessions = %#v, %v", config, err)
	}
	if !sessionExcluded(config.exclude, "Downloads") {
		t.Fatalf("exclude = %#v, %v", config, err)
	}
	if len(config.discover) != 2 || config.discover[0] != filepath.Join(home, "bin", "list-sessions") || config.discover[1] != "--all" {
		t.Fatalf("discover = %#v", config.discover)
	}
	for _, data := range []string{
		"[sessions]\nfuture = true\n",
		"[sessions]\nexclude = [\"[\"]\n",
		"[sessions]\ndiscover = []\n",
		"[sessions]\ndiscover = [1]\n",
		"[[sessions.entries]]\nname = \"project\"\npath = \"" + project + "\"\nstartup_command = \"make\"\n",
		"[[sessions.entries]]\nname = \"project\"\npath = \"/missing\"\n",
		"[[sessions.entries]]\nname = \"project\"\npath = \"" + project + "\"\n[[sessions.entries]]\nname = \"project\"\npath = \"" + project + "\"\n",
		"[[sessions.entries]\nname = \"project\"\n",
	} {
		writeSessionsConfig(t, data)
		if _, err := loadSessionsConfig(); err == nil {
			t.Fatalf("invalid config was accepted: %q", data)
		}
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	if got, err := configPath(); err != nil || got != filepath.Join(home, ".config", "jumpmux", "config.toml") {
		t.Fatalf("fallback config path = %q, %v", got, err)
	}
}

func TestSessionExcludeUsesPatterns(t *testing.T) {
	pattern := regexp.MustCompile(`_worktrees(/.*)?$`)
	if !sessionExcluded([]*regexp.Regexp{pattern}, "repo_worktrees/feature") || sessionExcluded([]*regexp.Regexp{pattern}, "repo") {
		t.Fatal("exclude pattern did not match regular-expression semantics")
	}
}

func TestDiscoverSessionsFromScript(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	script := filepath.Join(t.TempDir(), "discover")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n%s\\n%s\\n' \"$FIRST\" \"$SECOND\" \"$FIRST\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FIRST", first)
	t.Setenv("SECOND", second)
	sessions, err := discoverSessions([]string{script})
	if err != nil || len(sessions) != 2 || sessions[0].path != first || sessions[1].path != second {
		t.Fatalf("discovered sessions = %#v, %v", sessions, err)
	}
}

func TestDiscoverSessionsCachesSuccessfulCommand(t *testing.T) {
	project := t.TempDir()
	count := filepath.Join(t.TempDir(), "count")
	script := filepath.Join(t.TempDir(), "discover")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho run >>\"$DISCOVER_LOG\"\nprintf '%s\\n' \"$DISCOVER_PROJECT\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DISCOVER_LOG", count)
	t.Setenv("DISCOVER_PROJECT", project)
	discoveredSessionsCache.Lock()
	discoveredSessionsCache.values = nil
	discoveredSessionsCache.Unlock()
	for range 2 {
		if sessions, err := discoverSessions([]string{script}); err != nil || len(sessions) != 1 || sessions[0].path != project {
			t.Fatalf("discovered sessions = %#v, %v", sessions, err)
		}
	}
	data, err := os.ReadFile(count)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(strings.TrimSpace(string(data)), "run"); got != 1 {
		t.Fatalf("discover invocations = %d, want 1", got)
	}
}

func TestDiscoverSessionsRejectsInvalidOutput(t *testing.T) {
	script := filepath.Join(t.TempDir(), "discover")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'relative/path\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := discoverSessions([]string{script}); err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("relative discovery output = %v", err)
	}
}

func TestConfiguredSessionsLoadOutsideTmux(t *testing.T) {
	project := t.TempDir()
	t.Setenv("TMUX", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeSessionsConfig(t, "[[sessions.entries]]\nname = \"dev\"\npath = \""+project+"\"\n")
	sessions, err := listSessions(false)
	if err != nil || len(sessions) != 1 || sessions[0].sessionSource != "config" {
		t.Fatalf("configured sessions outside tmux = %#v, %v", sessions, err)
	}
}

func TestSessionsCLIListsLiveSessionsOutsideTmux(t *testing.T) {
	project := t.TempDir()
	t.Setenv("TMUX", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeSessionsConfig(t, "[[sessions.entries]]\nname = \"dev\"\npath = \""+project+"\"\n")
	writeFakeTmux(t, `
case "$1" in
  list-panes) printf '$1\037dev\037\061\037\061\037\061\037%%7\037/tmp/live\036\n' ;;
esac
`)
	sessions, err := listSessions(true)
	if err != nil || len(sessions) != 1 || sessions[0].muxSessionID != "$1" || sessions[0].cwd != project {
		t.Fatalf("CLI sessions outside tmux = %#v, %v", sessions, err)
	}
}

func TestListSessionsMergesDiscoveredConfiguredAndLive(t *testing.T) {
	configured, discovered := t.TempDir(), t.TempDir()
	t.Setenv("TMUX", "/tmp/tmux,1,0")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	script := filepath.Join(t.TempDir(), "discover")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$DISCOVERED\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DISCOVERED", discovered)
	data := "[sessions]\ndiscover = [\"" + script + "\"]\n[[sessions.entries]]\nname = \"dev\"\npath = \"" + configured + "\"\n"
	writeSessionsConfig(t, data)
	writeFakeTmux(t, `
case "$1" in
  display-message) printf 'dev\n' ;;
  list-panes) printf '$1\037dev\037\063\037\061\037\061\037%%7\037/tmp/live\036\n' ;;
esac
`)
	sessions, err := listSessions(false)
	if err != nil || len(sessions) != 2 {
		t.Fatalf("sessions = %#v, %v", sessions, err)
	}
	var sources = map[string]string{}
	for _, session := range sessions {
		sources[session.cwd] = session.sessionSource
	}
	if sources[configured] != "config" || sources[discovered] != "discovered" {
		t.Fatalf("session sources = %#v", sources)
	}
}

func TestListSessionsMergesLiveTmux(t *testing.T) {
	project := t.TempDir()
	t.Setenv("TMUX", "/tmp/tmux,1,0")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeSessionsConfig(t, "[[sessions.entries]]\nname = \"dev\"\npath = \""+project+"\"\n")
	writeFakeTmux(t, `
case "$1" in
  display-message) printf 'dev\n' ;;
  list-panes) printf '$1\037dev\037\063\037\061\037\061\037%%7\037/tmp/live\036\n$2\037other\037\061\037\061\037\061\037%%8\037/tmp/other\036\n' ;;
esac
`)
	sessions, err := listSessions(false)
	if err != nil || len(sessions) != 2 {
		t.Fatalf("sessions = %#v, %v", sessions, err)
	}
	if got := sessions[0]; got.title != "dev" || got.cwd != project || got.sessionSource != "config" || !got.current || got.tmuxWindows != 3 || got.pane != "%7" {
		t.Fatalf("merged configured session = %#v", got)
	}
	if got := sessions[1]; got.title != "other" || got.cwd != "/tmp/other" || got.sessionSource != "" {
		t.Fatalf("live-only session = %#v", got)
	}
}

func TestLiveTmuxSessionsReadAttachmentMetadata(t *testing.T) {
	writeFakeTmux(t, `
case "$1" in
  display-message) printf 'dev\n' ;;
  list-panes) printf '$1\037dev\0372\037100\0371\0371\037%%7\037/tmp/dev\036\n' ;;
esac
`)
	t.Setenv("TMUX", "/tmp/tmux,1,0")
	sessions, err := listLiveTmuxSessions(false)
	if err != nil || len(sessions) != 1 || !sessions[0].lastAttached.Equal(time.Unix(100, 0)) {
		t.Fatalf("last-attached metadata = %#v, %v", sessions, err)
	}
}

func TestLiveTmuxSessionsUseOneSnapshot(t *testing.T) {
	log := writeFakeTmux(t, `
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$1" in
  display-message) printf 'dev\n' ;;
  list-panes) printf '$1\037dev\037\061\037\061\037\061\037%%7\037/tmp/dev\036\n' ;;
esac
`)
	t.Setenv("TMUX", "/tmp/tmux,1,0")
	t.Setenv("TMUX_LOG", log)
	if _, err := listLiveTmuxSessions(false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "list-sessions") || strings.Count(string(data), "list-panes") != 1 {
		t.Fatalf("tmux discovery used multiple snapshots:\n%s", data)
	}
}

func TestLiveTmuxSessionsFailClosed(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux,1,0")
	writeFakeTmux(t, `
case "$1" in
  list-panes) printf '$1\037dev\0370\0371' ;;
esac
`)
	if _, err := listLiveTmuxSessions(false); err == nil || !strings.Contains(err.Error(), "malformed pane record") {
		t.Fatalf("malformed session listing was accepted: %v", err)
	}
}

func TestSwitchLastTmuxSession(t *testing.T) {
	log := writeFakeTmux(t, `
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$1" in
  list-sessions) printf '$1\037current\037300\036\n$2\037ignored\037250\036\n$3\037previous\037200\036\n' ;;
esac
`)
	t.Setenv("TMUX", "/tmp/tmux,1,0")
	t.Setenv("TMUX_LOG", log)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeSessionsConfig(t, "[sessions]\nexclude = [\"^ignored$\"]\n")
	if err := switchLastTmuxSession(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil || !strings.Contains(string(data), "switch-client -t $3") {
		t.Fatalf("last session command = %s, %v", data, err)
	}
}

func TestSwitchLastTmuxSessionAcceptsNeverAttached(t *testing.T) {
	log := writeFakeTmux(t, `
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$1" in
  list-sessions) printf '$1\037current\037300\036\n$2\037new\037\036\n' ;;
esac
`)
	t.Setenv("TMUX_LOG", log)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := switchLastTmuxSession(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(log)
	if !strings.Contains(string(data), "switch-client -t $2") {
		t.Fatalf("never-attached session command = %s", data)
	}
}

func TestSwitchLastTmuxSessionRequiresHistory(t *testing.T) {
	writeFakeTmux(t, `case "$1" in list-sessions) printf '$1\037only\037300\036\n' ;; esac`)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := switchLastTmuxSession(); err == nil || !strings.Contains(err.Error(), "no last session") {
		t.Fatalf("single session history = %v", err)
	}
}

func TestSessionOpenOutsideTmux(t *testing.T) {
	log := writeFakeTmux(t, `printf '%s\n' "$*" > "$TMUX_LOG"`)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_LOG", log)
	if err := jumpTmuxSession(item{target: "dev", cwd: "/tmp/dev", sessionSource: "config"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "new-session -A -s dev -c /tmp/dev") {
		t.Fatalf("outside tmux open = %s", data)
	}
}

func TestInactiveSessionCreatesWhenMissingLookupIsEmpty(t *testing.T) {
	log := writeFakeTmux(t, `
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$1" in
  display-message) exit 0 ;;
  new-session) printf '$2\037dev\037%%8\n' ;;
esac
`)
	t.Setenv("TMUX", "/tmp/tmux,1,0")
	t.Setenv("TMUX_LOG", log)
	if err := jumpTmuxSession(item{kind: "tmux-session", target: "dev", cwd: t.TempDir(), sessionSource: "config"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"display-message -p -t =dev: #{session_id}", "new-session -d -P -F", "switch-client -t $2"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("inactive session log missing %q:\n%s", want, data)
		}
	}
}

func TestConfiguredSessionReusesLiveName(t *testing.T) {
	log := writeFakeTmux(t, `
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$1" in
  display-message) printf '$1\n' ;;
esac
`)
	t.Setenv("TMUX", "/tmp/tmux,1,0")
	t.Setenv("TMUX_LOG", log)
	if err := jumpTmuxSession(item{kind: "tmux-session", target: "dev", cwd: t.TempDir(), sessionSource: "config"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "new-session") || strings.Contains(string(data), "list-panes") || !strings.Contains(string(data), "display-message -p -t =dev: #{session_id}") || !strings.Contains(string(data), "switch-client -t $1") {
		t.Fatalf("existing session was not reused:\n%s", data)
	}
}

func TestJumpTmuxSession(t *testing.T) {
	log := writeFakeTmux(t, `
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$1" in
  display-message)
    case "$4" in
      =dev:) printf '$1\n' ;;
      *) printf "can't find session: %s\n" "$4" >&2; exit 1 ;;
    esac ;;
  new-session) printf '$3\037wrong\037%%8\n' ;;
esac
`)
	t.Setenv("TMUX", "/tmp/tmux,1,0")
	t.Setenv("TMUX_LOG", log)
	if err := jumpTmuxSession(item{kind: "tmux-session", target: "dev", muxSessionID: "$1"}); err != nil {
		t.Fatal(err)
	}
	if err := jumpTmuxSession(item{kind: "tmux-session", target: "new", cwd: t.TempDir(), sessionSource: "config"}); err == nil || !strings.Contains(err.Error(), "incomplete session identity") {
		t.Fatalf("malformed creation identity = %v", err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"switch-client -t $1", "new-session -d -P -F", "kill-session -t $3"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("tmux log missing %q:\n%s", want, data)
		}
	}
}

func TestRenameTmuxSessionRevalidatesIdentity(t *testing.T) {
	log := writeFakeTmux(t, `
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$1" in
  display-message)
    case "$4" in
      =dev:) printf '$1\n' ;;
      =renamed:) if [ -f "$RENAMED" ]; then printf '$1\n'; else printf "can't find session\n" >&2; exit 1; fi ;;
    esac ;;
  rename-session) touch "$RENAMED" ;;
esac
`)
	t.Setenv("TMUX_LOG", log)
	t.Setenv("RENAMED", filepath.Join(t.TempDir(), "renamed"))
	if err := renameTmuxSession(item{target: "dev", muxSessionID: "$1"}, "renamed"); err != nil {
		t.Fatal(err)
	}
	if err := renameTmuxSession(item{target: "dev", muxSessionID: "$1"}, "bad:name"); err == nil || !strings.Contains(err.Error(), "colons") {
		t.Fatalf("unsafe rename = %v", err)
	}
	data, err := os.ReadFile(log)
	if err != nil || !strings.Contains(string(data), "rename-session -t $1 renamed") || !strings.Contains(string(data), "display-message -p -t =renamed:") {
		t.Fatalf("rename command = %s, %v", data, err)
	}
}

func TestRemoveTmuxSessionRevalidatesIdentity(t *testing.T) {
	log := writeFakeTmux(t, `
printf '%s\n' "$*" >> "$TMUX_LOG"
case "$1" in
  display-message)
    case "$4" in
      =dev:) printf '$1\n' ;;
      *) printf '$2\n' ;;
    esac ;;
esac
`)
	t.Setenv("TMUX", "/tmp/tmux,1,0")
	t.Setenv("TMUX_LOG", log)
	if err := removeTmuxSession(item{target: "dev", muxSessionID: "$1"}); err != nil {
		t.Fatal(err)
	}
	if err := removeTmuxSession(item{target: "dev", muxSessionID: "$2"}); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("changed identity was accepted: %v", err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "kill-session -t $1") != 1 {
		t.Fatalf("remove command = %s", data)
	}
}

func TestSessionSortOrder(t *testing.T) {
	sessions := []item{
		{title: "z-discovered", sessionSource: "discovered"},
		{title: "z-live", muxSessionID: "$2"},
		{title: "a-config", sessionSource: "config"},
		{title: "a-live", muxSessionID: "$1"},
		{title: "a-discovered", sessionSource: "discovered"},
	}
	sort.Slice(sessions, func(i, j int) bool {
		left, right := sessionSortRank(sessions[i]), sessionSortRank(sessions[j])
		if left != right {
			return left < right
		}
		return sessions[i].title < sessions[j].title
	})
	got := make([]string, len(sessions))
	for index := range sessions {
		got[index] = sessions[index].title
	}
	want := []string{"a-live", "z-live", "a-config", "a-discovered", "z-discovered"}
	if !slices.Equal(got, want) {
		t.Fatalf("session order = %v, want %v", got, want)
	}
}

func TestSessionIconsAndColumns(t *testing.T) {
	defer func() { nerdFontEnabled = true }()
	t.Setenv("JUMPMUX_PLAIN", "")
	model := newDashboard("/repo")
	model.tab, model.width, model.height = tabSessions, 100, 20
	model.sessions = []item{
		{title: "live", cwd: "/live", muxSessionID: "$1", tmuxWindows: 3},
		{title: "pinned", cwd: "/pinned", sessionSource: "config"},
		{title: "repo", cwd: "/repo", sessionSource: "discovered"},
	}
	view := ansi.Strip(model.renderTable(100))
	for _, want := range []string{"Session", "Path", "Win", "Last", " live", " pinned", " repo", "3"} {
		if !strings.Contains(view, want) {
			t.Fatalf("sessions table missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Source") || strings.Contains(view, "Att") {
		t.Fatalf("sessions table retained removed columns:\n%s", view)
	}
	nerdFontEnabled = false
	plain := ansi.Strip(model.renderTable(100))
	for _, want := range []string{"L live", "C pinned", "R repo"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("plain sessions table missing %q:\n%s", want, plain)
		}
	}
}

func TestSessionTabSwitchClosesSearchAndPreservesQuery(t *testing.T) {
	model := newDashboard("/repo")
	model.tab, model.filter, model.query = tabSessions, true, "dev"
	model.filterInputs[tabSessions].SetValue("dev")
	model.filterInputs[tabSessions].Focus()
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(dashboardModel)
	if model.tab != tabAgents || model.filter || model.filterInputs[tabSessions].Focused() || model.filterInputs[tabSessions].Value() != "dev" {
		t.Fatalf("search tab switch = tab %d, filter %v, focused %v, query %q", model.tab, model.filter, model.filterInputs[tabSessions].Focused(), model.filterInputs[tabSessions].Value())
	}
}

func TestSessionTypingStartsSearchAndEnterOpens(t *testing.T) {
	model := newDashboard("/repo")
	model.tab, model.sessionsLoaded = tabSessions, true
	model.sessions = []item{{kind: "tmux-session", target: "alpha", title: "alpha"}, {kind: "tmux-session", target: "beta", title: "beta"}}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("bet")})
	model = updated.(dashboardModel)
	if !model.filter || model.query != "bet" || len(model.rows()) != 1 || model.rows()[0].target != "beta" {
		t.Fatalf("typing did not start session search: filter=%v query=%q rows=%#v", model.filter, model.query, model.rows())
	}
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(dashboardModel)
	if !model.chosen || model.selection.target != "beta" || command == nil {
		t.Fatalf("search enter did not open selection: %#v", model)
	}
}

func TestSessionCtrlNavigationAndNoNumberJump(t *testing.T) {
	model := newDashboard("/repo")
	model.tab, model.sessionsLoaded = tabSessions, true
	model.sessions = []item{{target: "one", title: "one"}, {target: "two", title: "two"}}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	model = updated.(dashboardModel)
	if model.index != 1 {
		t.Fatalf("ctrl+j index = %d", model.index)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	model = updated.(dashboardModel)
	if model.index != 0 {
		t.Fatalf("ctrl+k index = %d", model.index)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	model = updated.(dashboardModel)
	if model.chosen || model.query != "2" {
		t.Fatalf("session number acted as jump: chosen=%v query=%q", model.chosen, model.query)
	}
	columns := model.columns(80, model.rows())
	if header := ansi.Strip(model.tableHeader(80, columns)); strings.Contains(header, "#") {
		t.Fatalf("session header retains number column: %q", header)
	}
}

func TestSessionRemoveShortcutConfirms(t *testing.T) {
	model := newDashboard("/repo")
	model.tab, model.sessionsLoaded = tabSessions, true
	model.sessions = []item{{kind: "tmux-session", target: "dev", title: "dev", muxSessionID: "$1"}}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	model = updated.(dashboardModel)
	if model.action != actionRemoveSession || model.actionTarget.target != "dev" {
		t.Fatalf("remove session mode = %#v target=%#v", model.action, model.actionTarget)
	}
	if view := ansi.Strip(model.View()); !strings.Contains(view, "Remove session") || !strings.Contains(view, "kills the live tmux session") || !strings.Contains(view, "Remove dev?") || !strings.Contains(view, "D Remove") || !strings.Contains(view, "Esc Cancel") {
		t.Fatalf("remove confirmation missing:\n%s", view)
	}
}

func TestRemoveTmuxSessionRejectsCurrentSession(t *testing.T) {
	writeFakeTmux(t, `
case "$1" in
  display-message) printf '$1\n' ;;
esac
`)
	t.Setenv("TMUX", "/tmp/tmux,1,0")
	if err := removeTmuxSession(item{target: "dev", muxSessionID: "$1"}); err == nil || !strings.Contains(err.Error(), "current") {
		t.Fatalf("current session removal = %v", err)
	}
}

func TestInactiveSessionPreviewDoesNotTick(t *testing.T) {
	model := newDashboard("/repo")
	model.tab, model.sessionsLoaded = tabSessions, true
	model.sessions = []item{{kind: "tmux-session", target: "build", title: "build", cwd: "/tmp/build", sessionSource: "config"}}
	updated, command := model.Update(sessionDataMsg{generation: model.sessionGeneration, sessions: model.sessions})
	model = updated.(dashboardModel)
	if command == nil {
		t.Fatal("initial session load did not request a preview")
	}
	message := command()
	preview, ok := message.(previewMsg)
	if !ok {
		t.Fatalf("preview command = %#v", message)
	}
	updated, next := model.Update(preview)
	if next != nil || updated.(dashboardModel).loading {
		t.Fatal("inactive session preview scheduled a refresh tick")
	}
}

func TestNarrowSessionPreviewScrollsPaneHistory(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height, model.tab = 40, 10, tabSessions
	model.sessions = []item{{kind: "tmux-session", target: "dev", title: "dev", cwd: "/tmp/dev", muxSessionID: "$1", pane: "%1"}}
	model.preview = sessionPreview(model.sessions[0], schemeDefault, 1)
	model.preview.lines = nil
	for index := range 20 {
		model.preview.lines = append(model.preview.lines, fmt.Sprintf("pane %d", index))
	}
	model.scrollFocusedPanel(100)
	view := ansi.Strip(model.renderPreview(model.width))
	if model.previewOffset == 0 || !strings.Contains(view, "pane 19") {
		t.Fatalf("narrow session preview did not scroll pane history: offset=%d\n%s", model.previewOffset, view)
	}
}

func TestSessionPreviewRefreshKeepsHistoryUntilCapture(t *testing.T) {
	model := newDashboard("/repo")
	model.tab = tabSessions
	selected := item{kind: "tmux-session", target: "dev", title: "dev", muxSessionID: "$1", pane: "%1"}
	model.sessions = []item{selected}
	model.preview = previewData{target: "dev", kind: "tmux-session", title: "Active pane: dev", lines: []string{"old output"}}
	model.previewOffset = model.previewBottomOffset(model.preview.lines)
	command := model.requestPreview(selected)
	if command == nil || len(model.preview.lines) != 1 || model.preview.lines[0] != "old output" {
		t.Fatalf("preview refresh replaced existing history: %#v", model.preview)
	}
}

func TestSessionRemovalSelectsPreRemovalNeighbor(t *testing.T) {
	for _, test := range []struct {
		name       string
		index      int
		remaining  []item
		wantTarget string
	}{
		{
			name:       "middle selects successor even when configured row remains",
			index:      1,
			remaining:  []item{{target: "one"}, {target: "two", sessionSource: "config"}, {target: "three"}},
			wantTarget: "three",
		},
		{name: "last selects predecessor", index: 2, remaining: []item{{target: "one"}, {target: "two"}}, wantTarget: "two"},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := newDashboard("/repo")
			model.tab, model.sessionsLoaded, model.index = tabSessions, true, test.index
			model.sessions = []item{{target: "one", muxSessionID: "$1"}, {target: "two", muxSessionID: "$2", sessionSource: "config"}, {target: "three", muxSessionID: "$3"}}
			selected, _ := model.selected()
			model.beginRemove(selected)
			updated, _ := model.Update(worktreeActionMsg{action: actionRemoveSession, notice: "removed"})
			model = updated.(dashboardModel)
			updated, _ = model.Update(sessionDataMsg{generation: model.sessionGeneration, sessions: test.remaining})
			model = updated.(dashboardModel)
			selected, ok := model.selected()
			if !ok || selected.target != test.wantTarget {
				t.Fatalf("selection = index %d, %#v", model.index, selected)
			}
		})
	}
}

func TestSessionRefreshIgnoresSupersededResult(t *testing.T) {
	model := newDashboard("/repo")
	model.tab, model.sessionsLoaded, model.sessionGeneration = tabSessions, true, 2
	model.sessions = []item{{kind: "tmux-session", target: "new", title: "new", muxSessionID: "$2"}}
	updated, _ := model.Update(sessionDataMsg{generation: 1, sessions: []item{{kind: "tmux-session", target: "old", title: "old", muxSessionID: "$1"}}})
	model = updated.(dashboardModel)
	if len(model.sessions) != 1 || model.sessions[0].target != "new" {
		t.Fatalf("superseded session refresh applied: %#v", model.sessions)
	}
}

func TestSessionRefreshKeepsLastGoodList(t *testing.T) {
	model := newDashboard("/repo")
	model.tab, model.sessionsLoaded = tabSessions, true
	model.sessions = []item{{kind: "tmux-session", target: "dev", title: "dev", cwd: "/tmp/dev", sessionSource: "config"}}
	model.preview = sessionPreview(model.sessions[0], schemeDefault, 1)
	updated, _ := model.Update(sessionDataMsg{generation: model.sessionGeneration, err: errors.New("tmux unavailable")})
	model = updated.(dashboardModel)
	if len(model.sessions) != 1 || model.sessions[0].target != "dev" || model.sessionsErr == nil || model.preview.target != "dev" {
		t.Fatalf("failed session refresh discarded state: %#v", model)
	}
}

func TestSessionPreviewKeepsBottomAcrossResize(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height, model.tab = 80, 20, tabSessions
	model.preview = previewData{target: "dev", kind: "tmux-session", lines: make([]string, 30)}
	model.previewOffset = model.previewBottomOffset(model.preview.lines)

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	model = updated.(dashboardModel)
	if model.previewOffset != model.previewBottomOffset(model.preview.lines) {
		t.Fatalf("resized bottom = %d, want %d", model.previewOffset, model.previewBottomOffset(model.preview.lines))
	}
}

func TestSessionsDashboardTab(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height, model.tab = 80, 20, tabSessions
	model.sessionsLoaded = true
	model.sessions = []item{
		{kind: "tmux-session", target: "build", title: "build", cwd: "/tmp/build", sessionSource: "config"},
		{kind: "tmux-session", target: "dev", title: "dev", cwd: "/tmp/dev", muxSessionID: "$1", pane: "%1", current: true, tmuxWindows: 2},
	}
	model.query = "dev"
	if rows := model.rows(); len(rows) != 1 || rows[0].title != "dev" {
		t.Fatalf("fuzzy session filter = %#v", rows)
	}
	if label := ansi.Strip(model.tabLabels()[tabSessions]); label != "[Sessions 1/2 · All]" {
		t.Fatalf("filtered Sessions label = %q", label)
	}
	model.query = "missing"
	if view := ansi.Strip(model.View()); !strings.Contains(view, "No sessions match “missing”") {
		t.Fatalf("Sessions empty search message:\n%s", view)
	}
	model.query = ""
	view := ansi.Strip(model.View())
	for _, want := range []string{"Agents 0", "Worktrees 0", "[Sessions 1/2 · All]", "Session", "Path", "Win", "↵ Open"} {
		if !strings.Contains(view, want) {
			t.Fatalf("sessions view missing %q:\n%s", want, view)
		}
	}
	model.query = "dev"
	footer := ansi.Strip(model.renderFooter(80))
	for _, want := range []string{"^j/k/n/p Move", "↵ Open", "^f All", "? Help", "Esc Clear", "^c Quit"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("sessions footer missing %q: %s", want, footer)
		}
	}
	if strings.Contains(footer, "Diff") || strings.Contains(footer, "PR") || strings.Contains(footer, "Scope") || strings.Contains(footer, "Add") || strings.Contains(footer, "q Quit") {
		t.Fatalf("sessions footer exposes unreachable or unrelated actions: %s", footer)
	}
	model.width, model.query, model.sessionFilter = 40, "", sessionFilterLive
	if header := ansi.Strip(model.renderHeader(40)); !strings.Contains(strings.Split(header, "\n")[0], "Live") {
		t.Fatalf("narrow Sessions header hides scope: %q", header)
	}
}
