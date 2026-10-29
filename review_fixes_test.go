package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestActiveClientContextTargetsSessionNotPaneIndex(t *testing.T) {
	log := writeFakeTmux(t, `printf '%s\n' "$*" >> "$TMUX_LOG"
case "$*" in
  *client_session*) echo 0 ;;
  *pane_current_path*) printf '%%2\t/repo\n' ;;
esac
`)
	t.Setenv("TMUX_LOG", log)
	if pane, _ := activeTmuxClientContext(); pane != "%2" {
		t.Fatalf("pane = %q", pane)
	}
	if data, _ := os.ReadFile(log); !strings.Contains(string(data), "-t =0: ") {
		t.Fatalf("session 0 was not targeted as a session:\n%s", data)
	}
}

func TestJumpToClosedPaneReportsClosed(t *testing.T) {
	// tmux 3.7 prints an empty line and exits 0 for a missing pane.
	writeFakeTmux(t, `case "$1" in display-message) echo ;; *) exit 1 ;; esac`)
	t.Setenv("TMUX", "/tmp/tmux,1,0")
	if err := jumpTmuxPane(item{pane: "%99", muxWindowID: "@1", muxSessionID: "$1"}); !errors.Is(err, errPaneClosed) {
		t.Fatalf("jump to closed pane = %v", err)
	}
}

func TestBackgroundActionResultKeepsOpenPrompt(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height, model.tab = 100, 20, tabWorktrees
	model.worktrees = []item{{kind: "worktree", target: "/feature", cwd: "/feature", branch: "feature"}}
	model.beginRemove(model.worktrees[0])
	updated, _ := model.Update(worktreeActionMsg{notice: "Opened PR #1", background: true})
	if model = updated.(dashboardModel); model.action != actionRemoveWorktree || model.actionTarget.target != "/feature" {
		t.Fatalf("PR result cleared the removal prompt: %v %#v", model.action, model.actionTarget)
	}
	// Once confirmed, the removal runs; a late PR result must not end it early.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated, _ = updated.(dashboardModel).Update(worktreeActionMsg{notice: "Opened PR #1", background: true})
	if model = updated.(dashboardModel); model.action != actionRunning {
		t.Fatalf("PR result ended the running removal: %v", model.action)
	}
	updated, _ = model.Update(worktreeActionMsg{action: actionRemoveWorktree})
	if model = updated.(dashboardModel); model.action != actionNone {
		t.Fatalf("removal result left action %v", model.action)
	}
}

func TestSwitchLastTmuxSessionSkipsExcludedCurrent(t *testing.T) {
	log := writeFakeTmux(t, `printf '%s\n' "$*" >> "$TMUX_LOG"
case "$1" in
  display-message) echo scratch ;;
  list-sessions) printf '$1\037scratch\037300\036\n$2\037api\037250\036\n$3\037web\037200\036\n' ;;
esac
`)
	t.Setenv("TMUX", "/tmp/tmux,1,0")
	t.Setenv("TMUX_LOG", log)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeSessionsConfig(t, "[sessions]\nexclude = [\"^scratch$\"]\n")
	if err := switchLastTmuxSession(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(log); !strings.Contains(string(data), "switch-client -t $2") {
		t.Fatalf("previous session command:\n%s", data)
	}
}

func TestDiscoveredSessionMatchingConfiguredPathIsMerged(t *testing.T) {
	writeFakeTmux(t, "echo 'no server running on /tmp/x' >&2; exit 1")
	dir := filepath.Join(t.TempDir(), "api")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "discover")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho "+dir+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeSessionsConfig(t, "[sessions]\ndiscovery_command = [\""+script+"\"]\n\n[[sessions.entries]]\nname = \"api\"\npath = \""+dir+"\"\n")
	discoveredSessionsCache.Lock()
	discoveredSessionsCache.values = nil
	discoveredSessionsCache.Unlock()
	sessions, err := listSessions(true)
	if err != nil || len(sessions) != 1 || sessions[0].sessionSource != "config" {
		t.Fatalf("sessions = %#v, %v", sessions, err)
	}
}

func TestBranchChangeClearsStalePRDetails(t *testing.T) {
	current := []item{{kind: "worktree", target: "/w", branch: "old", prNumber: 4, prURL: "https://example.test/4", prFailedChecks: []string{"lint"}}}
	merged := mergeWorktreeData(current, []item{{kind: "worktree", target: "/w", branch: "new"}}, worktreeListStage)
	if len(merged) != 1 || merged[0].prFailedChecks != nil || merged[0].prURL != "" {
		t.Fatalf("stale PR details kept: %#v", merged)
	}
}

func TestRelativeStateHomeIsIgnored(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "state")
	t.Setenv("HOME", "/home/test")
	if path, err := settingsPath(); err != nil || path != "/home/test/.local/state/jumpmux/settings.json" {
		t.Fatalf("settings path = %q, %v", path, err)
	}
}

func TestWorktreeOpenReusesExistingWindow(t *testing.T) {
	repo := newAgentTestRepo(t)
	worktree := worktreePath(repo, "feature")
	if err := os.MkdirAll(filepath.Dir(worktree), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := gitOutput(repo, "worktree", "add", "-q", "-b", "feature", worktree); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("JUMPMUX_STATE_DIR", t.TempDir())
	t.Setenv("TMUX", "/tmp/tmux,1,0")
	t.Setenv("FAKE_WORKTREE", worktree)
	log := writeFakeTmux(t, `printf '%s\n' "$*" >> "$TMUX_LOG"
case "$1" in
  list-panes) printf '%%5\0371\037%s\037sh\037$1\037dev\037@3\037feature\037fish\037%s\0373\0370\036\n' "$FAKE_WORKTREE" "$FAKE_WORKTREE" ;;
  display-message) case "$*" in *pane_id*) echo %5 ;; esac ;;
esac
`)
	t.Setenv("TMUX_LOG", log)
	invalidateTmuxPaneCache()
	if err := runWorktreeCommand(repo, []string{"open", worktree}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(log)
	if strings.Contains(string(data), "new-window") || !strings.Contains(string(data), "select-pane -t %5") {
		t.Fatalf("worktree open did not reuse the window:\n%s", data)
	}
}
