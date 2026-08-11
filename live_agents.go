package main

import (
	"cmp"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed extension/jumpmux-status.ts
var piExtension []byte

const (
	jumpmuxStatusFormat = "#{?@jumpmux_status, #{@jumpmux_status},}"
	jumpmuxFocusHook    = "pane-focus-in[987654]"
	tmuxTimeout         = 2 * time.Second
)

var tmuxPaneCache struct {
	sync.Mutex
	at    time.Time
	panes []tmuxPane
	err   error
}

type agentState struct {
	Version     int       `json:"version"`
	Pane        string    `json:"pane"`
	PanePID     string    `json:"pane_pid"`
	PaneCommand string    `json:"pane_command"`
	SessionID   string    `json:"session_id"`
	SessionFile string    `json:"session_file,omitempty"`
	Cwd         string    `json:"cwd"`
	Title       string    `json:"title,omitempty"`
	Status      string    `json:"status"`
	Updated     time.Time `json:"updated"`
}

type tmuxPane struct {
	ID             string
	PID            string
	Path           string
	Title          string
	SessionID      string
	SessionName    string
	WindowID       string
	WindowName     string
	CurrentCommand string
	Worktree       string
}

func setupPIExtension() (string, error) {
	base := os.Getenv("PI_CODING_AGENT_DIR")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".pi", "agent")
	}
	path := filepath.Join(base, "extensions", "jumpmux-status.ts")
	if err := atomicWrite(path, piExtension, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func setAgentStatus(args []string) error {
	if len(args) != 5 {
		return errors.New("agent-status requires status, session ID, session file, cwd, and title")
	}
	status, sessionID, sessionFile, cwd, title := args[0], args[1], args[2], args[3], args[4]
	if status != "working" && status != "done" && status != "update" && status != "closed" {
		return fmt.Errorf("invalid agent status %q", status)
	}
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return errors.New("live agent tracking requires tmux")
	}
	path, err := agentStatePath(pane)
	if err != nil {
		return err
	}
	legacyPath, err := legacyAgentStatePath(pane)
	if err != nil {
		return err
	}
	return withTmuxWindowLock(pane, func() error {
		if status == "closed" {
			statusErr := clearTmuxStatusLocked(pane)
			removeErr := removeAgentState(path)
			return errors.Join(statusErr, removeErr, removeAgentState(legacyPath))
		}

		state := agentState{Status: "done"}
		if data, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(data, &state)
		} else if data, err := os.ReadFile(legacyPath); err == nil {
			_ = json.Unmarshal(data, &state)
		}
		if status != "update" {
			state.Status = status
		}
		live, err := tmuxOutput("display-message", "-p", "-t", pane, "#{pane_pid}\t#{pane_current_command}")
		if err != nil {
			return err
		}
		paneFields := strings.SplitN(strings.TrimSpace(live), "\t", 2)
		if len(paneFields) != 2 {
			return errors.New("tmux returned incomplete pane identity")
		}
		state.Version = 1
		state.Pane = pane
		state.PanePID = paneFields[0]
		state.PaneCommand = paneFields[1]
		state.SessionID = sessionID
		state.SessionFile = sessionFile
		state.Cwd = cwd
		state.Title = title
		state.Updated = time.Now()
		var statusErr error
		if status == "working" || status == "done" {
			statusErr = setTmuxStatusLocked(pane, status)
		}
		data, err := json.Marshal(state)
		if err != nil {
			return err
		}
		writeErr := atomicWrite(path, append(data, '\n'), 0o600)
		if writeErr == nil {
			writeErr = removeAgentState(legacyPath)
		}
		return errors.Join(statusErr, writeErr)
	})
}

func listLiveAgents() ([]item, error) {
	panes, err := cachedTmuxPanes()
	if err != nil {
		return nil, err
	}
	activePane, _ := activeTmuxContext()
	agents := make([]item, 0, len(panes))
	for _, pane := range panes {
		path, err := agentStatePath(pane.ID)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			legacyPath, legacyErr := legacyAgentStatePath(pane.ID)
			if legacyErr != nil {
				continue
			}
			data, err = os.ReadFile(legacyPath)
			if err != nil {
				continue
			}
		}
		var state agentState
		if json.Unmarshal(data, &state) != nil || state.Pane != pane.ID || state.PanePID == "" || state.PanePID != pane.PID || state.PaneCommand != pane.CurrentCommand {
			continue
		}
		cwd := state.Cwd
		if cwd == "" {
			cwd = pane.Path
		}
		title := cmp.Or(
			strings.TrimSpace(state.Title),
			strings.TrimSpace(pane.Title),
			strings.TrimSpace(pane.WindowName),
			strings.TrimSpace(state.SessionID),
			"Pi",
		)
		agents = append(agents, item{
			kind:           "session",
			target:         pane.ID,
			cwd:            cwd,
			title:          title,
			updated:        state.Updated,
			current:        pane.ID == activePane,
			status:         state.Status,
			pane:           pane.ID,
			muxSessionID:   pane.SessionID,
			muxSessionName: pane.SessionName,
			muxWindowID:    pane.WindowID,
			muxWindowName:  pane.WindowName,
		})
	}
	sort.Slice(agents, func(i, j int) bool {
		if agents[i].status != agents[j].status {
			return agents[i].status == "working"
		}
		return agents[i].updated.After(agents[j].updated)
	})
	return agents, nil
}

func cachedTmuxPanes() ([]tmuxPane, error) {
	tmuxPaneCache.Lock()
	defer tmuxPaneCache.Unlock()
	if time.Since(tmuxPaneCache.at) < 250*time.Millisecond {
		return append([]tmuxPane(nil), tmuxPaneCache.panes...), tmuxPaneCache.err
	}
	tmuxPaneCache.panes, tmuxPaneCache.err = listTmuxPanes()
	tmuxPaneCache.at = time.Now()
	return append([]tmuxPane(nil), tmuxPaneCache.panes...), tmuxPaneCache.err
}

func invalidateTmuxPaneCache() {
	tmuxPaneCache.Lock()
	tmuxPaneCache.at = time.Time{}
	tmuxPaneCache.Unlock()
}

func listTmuxPanes() ([]tmuxPane, error) {
	const fieldSeparator = "\x1f"
	const recordSeparator = "\x1e"
	const format = "#{pane_id}" + fieldSeparator + "#{pane_pid}" + fieldSeparator + "#{pane_current_path}" + fieldSeparator + "#{pane_title}" + fieldSeparator + "#{session_id}" + fieldSeparator + "#{session_name}" + fieldSeparator + "#{window_id}" + fieldSeparator + "#{window_name}" + fieldSeparator + "#{pane_current_command}" + fieldSeparator + "#{@jumpmux_worktree}" + recordSeparator
	output, err := tmuxOutput("list-panes", "-a", "-F", format)
	if err != nil {
		return nil, err
	}
	var panes []tmuxPane
	for _, record := range strings.Split(output, recordSeparator) {
		record = strings.TrimPrefix(record, "\n")
		record = strings.TrimSuffix(record, "\n")
		if record == "" {
			continue
		}
		parts := strings.Split(record, fieldSeparator)
		if len(parts) != 10 {
			return nil, errors.New("tmux returned a malformed pane record")
		}
		panes = append(panes, tmuxPane{
			ID: parts[0], PID: parts[1], Path: parts[2], Title: parts[3],
			SessionID: parts[4], SessionName: parts[5], WindowID: parts[6],
			WindowName: parts[7], CurrentCommand: parts[8], Worktree: parts[9],
		})
	}
	return panes, nil
}

func attachTmuxWorktrees(items []item) error {
	panes, err := cachedTmuxPanes()
	if err != nil {
		return err
	}
	currentPane, currentPath := activeTmuxContext()
	for index := range items {
		if items[index].kind != "worktree" {
			continue
		}
		var chosen tmuxPane
		for _, pane := range panes {
			if pane.ID == currentPane && pathWithin(cmp.Or(currentPath, pane.Path), items[index].cwd) {
				items[index].current = true
			}
			if pane.Worktree != "" && samePath(pane.Worktree, items[index].cwd) && chosen.ID == "" {
				chosen = pane
			}
		}
		if items[index].pane != "" || chosen.ID == "" {
			continue
		}
		items[index].pane = chosen.ID
		items[index].muxSessionID = chosen.SessionID
		items[index].muxSessionName = chosen.SessionName
		items[index].muxWindowID = chosen.WindowID
		items[index].muxWindowName = chosen.WindowName
	}
	return nil
}

func activeTmuxSession() string {
	session, err := tmuxOutput("display-message", "-p", "#{client_session}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(session)
}

func activeTmuxContext() (string, string) {
	if session := activeTmuxSession(); session != "" {
		output, err := tmuxOutput("display-message", "-p", "-t", session, "#{pane_id}\t#{pane_current_path}")
		if err == nil {
			parts := strings.SplitN(strings.TrimSpace(output), "\t", 2)
			if len(parts) == 2 && parts[0] != "" {
				return parts[0], parts[1]
			}
		}
	}
	return os.Getenv("TMUX_PANE"), ""
}

func jumpTmuxPane(selected item) error {
	if os.Getenv("TMUX") == "" {
		return errors.New("run jumpmux inside tmux to jump to a pane")
	}
	if _, err := tmuxOutput("display-message", "-p", "-t", selected.pane, "#{pane_id}"); err != nil {
		return errors.New("the selected tmux pane is no longer open")
	}
	return focusTmuxPane(selected)
}

func openTmuxWorktree(selected item) error {
	if os.Getenv("TMUX") == "" {
		return errors.New("run jumpmux inside tmux to open a worktree window")
	}
	name := selected.branch
	if name == "" {
		name = worktreeName(selected.cwd)
	}
	output, err := tmuxOutput("new-window", "-d", "-P", "-F", "#{session_id}\t#{window_id}\t#{pane_id}", "-c", selected.cwd, "-n", name)
	if err != nil {
		return err
	}
	parts := strings.Split(strings.TrimSpace(output), "\t")
	if len(parts) != 3 {
		return errors.New("tmux returned incomplete window identity")
	}
	selected.muxSessionID, selected.muxWindowID, selected.pane = parts[0], parts[1], parts[2]
	if _, err := tmuxOutput("set-option", "-w", "-t", selected.muxWindowID, "@jumpmux_worktree", selected.cwd); err != nil {
		_, _ = tmuxOutput("kill-window", "-t", selected.muxWindowID)
		return err
	}
	invalidateTmuxPaneCache()
	return focusTmuxPane(selected)
}

func focusTmuxPane(selected item) error {
	for _, args := range [][]string{
		{"select-window", "-t", selected.muxWindowID},
		{"select-pane", "-t", selected.pane},
		{"switch-client", "-t", selected.muxSessionID},
	} {
		if _, err := tmuxOutput(args...); err != nil {
			return err
		}
	}
	return nil
}

func withTmuxWindowLock(pane string, action func() error) error {
	window, err := tmuxOutput("display-message", "-p", "-t", pane, "#{window_id}")
	if err != nil {
		return err
	}
	id := strings.TrimPrefix(strings.TrimSpace(window), "@")
	if id == "" || strings.Trim(id, "0123456789") != "" {
		return fmt.Errorf("tmux returned invalid window ID %q", window)
	}
	stateDir, err := agentStateDir()
	if err != nil {
		return err
	}
	server, err := tmuxServerIdentity()
	if err != nil {
		return err
	}
	lock := filepath.Join(stateDir, "locks", "window-"+server+"-"+id+".lock")
	if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	deadline := time.Now().Add(tmuxTimeout)
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return err
		}
		if time.Now().After(deadline) {
			return errors.New("timed out waiting for tmux status lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }()
	return action()
}

func setTmuxStatusLocked(pane, status string) error {
	icon := workingIcon
	if status == "done" {
		icon = doneIcon
	}
	if err := ensureTmuxStatusFormat(pane); err != nil {
		return err
	}
	if _, err := tmuxOutput("set-option", "-p", "-t", pane, "@jumpmux_pane_status", icon); err != nil {
		return err
	}
	return syncTmuxWindowStatus(pane)
}

func clearTmuxStatusLocked(pane string) error {
	if _, err := tmuxOutput("set-option", "-up", "-t", pane, "@jumpmux_pane_status"); err != nil {
		return err
	}
	return syncTmuxWindowStatus(pane)
}

func clearFocusedPaneStatus(pane string) error {
	return withTmuxWindowLock(pane, func() error {
		status, err := tmuxOutput("show-option", "-pv", "-t", pane, "@jumpmux_pane_status")
		if err != nil || strings.TrimSpace(status) != doneIcon {
			return nil
		}
		return clearTmuxStatusLocked(pane)
	})
}

func syncTmuxWindowStatus(pane string) error {
	output, err := tmuxOutput("list-panes", "-t", pane, "-F", "#{@jumpmux_pane_status}")
	if err != nil {
		return err
	}
	icon := windowStatusIcon(output)
	if icon == "" {
		if _, err = tmuxOutput("set-option", "-uw", "-t", pane, "@jumpmux_status"); err != nil {
			return err
		}
	} else if _, err = tmuxOutput("set-option", "-w", "-t", pane, "@jumpmux_status", icon); err != nil {
		return err
	}
	return syncTmuxFocusHook(pane, output)
}

func syncTmuxFocusHook(pane, statuses string) error {
	if strings.Contains(statuses, doneIcon) {
		binary, err := os.Executable()
		if err != nil {
			return err
		}
		hook := fmt.Sprintf("run-shell %q", binary+" pane-focused #{pane_id}")
		_, err = tmuxOutput("set-hook", "-w", "-t", pane, jumpmuxFocusHook, hook)
		return err
	}
	_, err := tmuxOutput("set-hook", "-uw", "-t", pane, jumpmuxFocusHook)
	return err
}

func windowStatusIcon(statuses string) string {
	icon := ""
	for _, status := range strings.Split(strings.TrimSpace(statuses), "\n") {
		if status == workingIcon {
			return workingIcon
		}
		if status == doneIcon {
			icon = doneIcon
		}
	}
	return icon
}

func ensureTmuxStatusFormat(pane string) error {
	for _, option := range []string{"window-status-format", "window-status-current-format"} {
		format, err := tmuxOutput("show-option", "-wv", "-t", pane, option)
		format = strings.TrimRight(format, "\r\n")
		if err != nil || format == "" {
			format, err = tmuxOutput("show-option", "-gv", option)
			format = strings.TrimRight(format, "\r\n")
		}
		if err != nil || format == "" {
			format = "#I:#W#{?window_flags,#{window_flags}, }"
		}
		if strings.Contains(format, "@jumpmux_status") {
			continue
		}
		if _, err := tmuxOutput("set-option", "-w", "-t", pane, option, injectTmuxStatusFormat(format)); err != nil {
			return err
		}
	}
	return nil
}

func injectTmuxStatusFormat(format string) string {
	position := len(format)
	for _, pattern := range []string{"#{window_flags", "#{?window_flags", "#{F}"} {
		if index := strings.Index(format, pattern); index >= 0 && index < position {
			position = index
		}
	}
	return format[:position] + jumpmuxStatusFormat + format[position:]
}

func tmuxOutput(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	output, err := boundedCommand(ctx, "tmux", args...).CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("tmux %s: %w", args[0], ctx.Err())
		}
		return "", fmt.Errorf("tmux %s: %s", args[0], strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func agentStateDir() (string, error) {
	if dir := os.Getenv("JUMPMUX_STATE_DIR"); dir != "" {
		return dir, nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "jumpmux", "agents"), nil
}

func agentStatePath(pane string) (string, error) {
	id, err := tmuxPaneID(pane)
	if err != nil {
		return "", err
	}
	dir, err := agentStateDir()
	if err != nil {
		return "", err
	}
	server, err := tmuxServerIdentity()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, server, id+".json"), nil
}

func legacyAgentStatePath(pane string) (string, error) {
	id, err := tmuxPaneID(pane)
	if err != nil {
		return "", err
	}
	dir, err := agentStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, id+".json"), nil
}

func tmuxPaneID(pane string) (string, error) {
	id := strings.TrimPrefix(pane, "%")
	if id == "" || strings.Trim(id, "0123456789") != "" {
		return "", fmt.Errorf("invalid tmux pane ID %q", pane)
	}
	return id, nil
}

func tmuxServerIdentity() (string, error) {
	socket, _, _ := strings.Cut(os.Getenv("TMUX"), ",")
	if socket == "" {
		if output, err := tmuxOutput("display-message", "-p", "#{socket_path}"); err == nil {
			socket = strings.TrimSpace(output)
		}
	}
	if socket == "" {
		return "legacy", nil
	}
	sum := sha256.Sum256([]byte(socket))
	return hex.EncodeToString(sum[:8]), nil
}

func removeAgentState(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".jumpmux-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() { _ = os.Remove(temporary) }()
	if err := file.Chmod(mode); err != nil {
		return errors.Join(err, file.Close())
	}
	if _, err := file.Write(data); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
