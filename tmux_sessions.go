package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/pelletier/go-toml/v2"
)

const (
	tmuxFieldSeparator     = "\x1f"
	tmuxRecordSeparator    = "\x1e"
	discoveryTimeout       = 5 * time.Second
	maxDiscoveryOutputSize = 1 << 20
)

type configuredSession struct {
	name string
	path string
}

type sessionsConfig struct {
	sessions []configuredSession
	exclude  []*regexp.Regexp
	discover []string
}

type tmuxSession struct {
	id           string
	name         string
	windows      int
	lastAttached time.Time
	path         string
	pane         string
	current      bool
}

type sessionsFile struct {
	Sessions        *sessionsSection `toml:"sessions"`
	WorktreeBackend any              `toml:"worktree_backend"`
	Theme           any              `toml:"theme"`
	DefaultScope    any              `toml:"default_scope"`
	NerdFont        any              `toml:"nerdfont"`
	Preview         any              `toml:"preview"`
}

type sessionsSection struct {
	Exclude  []string       `toml:"exclude"`
	Discover *[]string      `toml:"discover"`
	Entries  []sessionEntry `toml:"entries"`
}

type sessionEntry struct {
	Name string `toml:"name"`
	Path string `toml:"path"`
}

func loadSessionsConfig() (*sessionsConfig, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var file sessionsFile
	decoder := toml.NewDecoder(strings.NewReader(string(data))).DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		var unknown *toml.StrictMissingError
		if !errors.As(err, &unknown) {
			return nil, fmt.Errorf("sessions config %s: %w", path, err)
		}
		for _, detail := range unknown.Errors {
			key := detail.Key()
			if len(key) == 0 || key[0] != "sessions" {
				continue
			}
			if len(key) > 2 && key[1] == "entries" {
				return nil, fmt.Errorf("sessions config %s: session uses unsupported %q", path, key[len(key)-1])
			}
			return nil, fmt.Errorf("sessions config %s: sessions uses unsupported %q", path, key[len(key)-1])
		}
	}
	if file.Sessions == nil {
		return &sessionsConfig{}, nil
	}

	exclude := make([]*regexp.Regexp, 0, len(file.Sessions.Exclude))
	for _, pattern := range file.Sessions.Exclude {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("sessions config %s: invalid exclusion pattern %q: %w", path, pattern, err)
		}
		exclude = append(exclude, compiled)
	}
	var discover []string
	if file.Sessions.Discover != nil {
		discover = *file.Sessions.Discover
		if len(discover) == 0 {
			return nil, fmt.Errorf("sessions config %s: sessions.discover must be a non-empty array", path)
		}
		for _, argument := range discover {
			if strings.TrimSpace(argument) == "" || strings.ContainsFunc(argument, unicode.IsControl) {
				return nil, fmt.Errorf("sessions config %s: sessions.discover must contain non-empty strings", path)
			}
		}
		discover[0] = expandSessionPath(discover[0])
	}

	seen := make(map[string]bool, len(file.Sessions.Entries))
	sessions := make([]configuredSession, 0, len(file.Sessions.Entries))
	for index, entry := range file.Sessions.Entries {
		name, location := strings.TrimSpace(entry.Name), expandSessionPath(entry.Path)
		if name == "" {
			return nil, fmt.Errorf("sessions config %s: session %d needs a non-empty name", path, index+1)
		}
		if strings.ContainsAny(name, tmuxFieldSeparator+tmuxRecordSeparator) || strings.ContainsFunc(name, unicode.IsControl) {
			return nil, fmt.Errorf("sessions config %s: session %d has an unsafe name", path, index+1)
		}
		if seen[name] {
			return nil, fmt.Errorf("sessions config %s: duplicate session name %q", path, name)
		}
		if location == "" {
			return nil, fmt.Errorf("sessions config %s: session %d needs a path", path, index+1)
		}
		info, err := os.Stat(location)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("sessions config %s: session %q path %q is not an existing directory", path, name, location)
		}
		seen[name] = true
		sessions = append(sessions, configuredSession{name: name, path: filepath.Clean(location)})
	}
	return &sessionsConfig{sessions: sessions, exclude: exclude, discover: discover}, nil
}

func expandSessionPath(path string) string {
	path = os.ExpandEnv(strings.TrimSpace(path))
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/"))
}

func listLiveTmuxSessions(includeServer bool) ([]tmuxSession, error) {
	if !includeServer && os.Getenv("TMUX") == "" {
		return nil, nil
	}
	format := strings.Join([]string{
		"#{session_id}", "#{session_name}", "#{session_windows}", "#{session_last_attached}",
		"#{window_active}", "#{pane_active}", "#{pane_id}", "#{pane_current_path}",
	}, tmuxFieldSeparator) + tmuxRecordSeparator
	output, err := tmuxOutput("list-panes", "-a", "-F", format)
	if err != nil {
		if includeServer && (strings.Contains(err.Error(), "no server running") || strings.Contains(err.Error(), "failed to connect")) {
			return nil, nil
		}
		return nil, err
	}
	if !validTmuxRecordOutput(output) {
		return nil, errors.New("tmux returned a malformed pane record")
	}
	current := activeTmuxSession()
	sessionsByID := map[string]tmuxSession{}
	for _, record := range tmuxRecords(output) {
		parts := strings.Split(record, tmuxFieldSeparator)
		if len(parts) != 7 && len(parts) != 8 {
			return nil, errors.New("tmux returned a malformed pane record")
		}
		lastAttached, offset := time.Time{}, 0
		if len(parts) == 8 {
			if parts[3] != "" && parts[3] != "0" {
				seconds, err := strconv.ParseInt(parts[3], 10, 64)
				if err != nil || seconds < 0 {
					return nil, errors.New("tmux returned a malformed pane record")
				}
				lastAttached = time.Unix(seconds, 0)
			}
			offset = 1
		}
		if !validTmuxSessionID(parts[0]) || parts[1] == "" || (parts[3+offset] != "0" && parts[3+offset] != "1") || (parts[4+offset] != "0" && parts[4+offset] != "1") || !validTmuxPaneID(parts[5+offset]) {
			return nil, errors.New("tmux returned a malformed pane record")
		}
		windows, err := strconv.Atoi(parts[2])
		if err != nil || windows < 0 {
			return nil, errors.New("tmux returned a malformed pane record")
		}
		session, exists := sessionsByID[parts[0]]
		if exists && (session.name != parts[1] || session.windows != windows || !session.lastAttached.Equal(lastAttached)) {
			return nil, errors.New("tmux returned inconsistent session metadata")
		}
		if !exists {
			session = tmuxSession{id: parts[0], name: parts[1], windows: windows, lastAttached: lastAttached, current: parts[1] == current}
		}
		if parts[3+offset] == "1" && parts[4+offset] == "1" {
			if session.pane != "" {
				return nil, errors.New("tmux returned multiple active panes for a session")
			}
			session.pane, session.path = parts[5+offset], parts[6+offset]
		}
		sessionsByID[parts[0]] = session
	}
	sessions := make([]tmuxSession, 0, len(sessionsByID))
	for _, session := range sessionsByID {
		if session.pane == "" {
			return nil, errors.New("tmux returned a session without an active pane")
		}
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].name < sessions[j].name })
	return sessions, nil
}

func validTmuxRecordOutput(output string) bool {
	return strings.TrimSpace(output) == "" || strings.HasSuffix(strings.TrimRight(output, "\n"), tmuxRecordSeparator)
}

func tmuxRecords(output string) []string {
	var records []string
	for _, record := range strings.Split(output, tmuxRecordSeparator) {
		record = strings.TrimPrefix(record, "\n")
		record = strings.TrimSuffix(record, "\n")
		if record != "" {
			records = append(records, record)
		}
	}
	return records
}

func discoverSessions(command []string) ([]configuredSession, error) {
	if len(command) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), discoveryTimeout)
	defer cancel()
	stdout, stderr := &cappedBuffer{limit: maxDiscoveryOutputSize}, &cappedBuffer{limit: 4096}
	process := boundedCommand(ctx, command[0], command[1:]...)
	process.Stdout, process.Stderr = stdout, stderr
	if err := process.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("sessions discover: %w", ctx.Err())
		}
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return nil, fmt.Errorf("sessions discover: %s", truncate(safeText(message), 160))
		}
		return nil, fmt.Errorf("sessions discover: %w", err)
	}
	if stdout.truncated {
		return nil, errors.New("sessions discover output exceeds 1 MiB")
	}
	seen := map[string]bool{}
	sessions := []configuredSession{}
	for lineNumber, line := range strings.Split(stdout.String(), "\n") {
		location := strings.TrimSuffix(line, "\r")
		if location == "" {
			continue
		}
		if !filepath.IsAbs(location) {
			return nil, fmt.Errorf("sessions discover line %d is not an absolute path", lineNumber+1)
		}
		location = filepath.Clean(location)
		info, err := os.Stat(location)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("sessions discover line %d is not an existing directory", lineNumber+1)
		}
		name := filepath.Base(location)
		if strings.ContainsAny(name, tmuxFieldSeparator+tmuxRecordSeparator) || strings.ContainsFunc(name, unicode.IsControl) {
			return nil, fmt.Errorf("sessions discover line %d has an unsafe directory name", lineNumber+1)
		}
		if seen[location] {
			continue
		}
		seen[location] = true
		sessions = append(sessions, configuredSession{name: name, path: location})
	}
	return sessions, nil
}

func listSessions(includeServer bool) ([]item, error) {
	config, err := loadSessionsConfig()
	if err != nil {
		return nil, err
	}
	if config == nil {
		config = &sessionsConfig{}
	}
	discovered, discoverErr := discoverSessions(config.discover)
	if discoverErr != nil {
		return nil, discoverErr
	}
	live, liveErr := listLiveTmuxSessions(includeServer)
	items := make(map[string]item, len(config.sessions)+len(discovered)+len(live))
	for _, session := range config.sessions {
		if sessionExcluded(config.exclude, session.name) {
			continue
		}
		items[session.name] = item{kind: "tmux-session", target: session.name, title: session.name, cwd: session.path, sessionSource: "config"}
	}
	for _, session := range discovered {
		if sessionExcluded(config.exclude, session.name) || sessionExcluded(config.exclude, session.path) {
			continue
		}
		name := uniqueDiscoveredSessionName(items, session)
		items[name] = item{kind: "tmux-session", target: name, title: name, cwd: session.path, sessionSource: "discovered"}
	}
	for _, session := range live {
		if sessionExcluded(config.exclude, session.name) {
			continue
		}
		entry, configured := items[session.name]
		if !configured {
			entry = item{kind: "tmux-session", target: session.name, title: session.name, cwd: session.path}
		}
		entry.muxSessionID, entry.muxSessionName, entry.pane = session.id, session.name, session.pane
		entry.current, entry.tmuxWindows = session.current, session.windows
		entry.lastAttached = session.lastAttached
		items[session.name] = entry
	}
	result := make([]item, 0, len(items))
	for _, session := range items {
		result = append(result, session)
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := sessionSortRank(result[i]), sessionSortRank(result[j])
		if left != right {
			return left < right
		}
		return result[i].title < result[j].title
	})
	return result, liveErr
}

func sessionSortRank(session item) int {
	if session.muxSessionID != "" {
		return 0
	}
	if session.sessionSource == "config" {
		return 1
	}
	return 2
}

func uniqueDiscoveredSessionName(items map[string]item, session configuredSession) string {
	name := session.name
	for parent := filepath.Dir(session.path); ; parent = filepath.Dir(parent) {
		if _, exists := items[name]; !exists {
			return name
		}
		base := filepath.Base(parent)
		if base == "." || base == string(filepath.Separator) {
			return session.path
		}
		name = base + "-" + name
	}
}

func sessionExcluded(patterns []*regexp.Regexp, name string) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(name) {
			return true
		}
	}
	return false
}

func validTmuxSessionID(id string) bool {
	return len(id) > 1 && id[0] == '$' && strings.Trim(id[1:], "0123456789") == ""
}

func validTmuxSessionName(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && !strings.ContainsAny(name, ":"+tmuxFieldSeparator+tmuxRecordSeparator) && !strings.ContainsFunc(name, unicode.IsControl)
}

func validTmuxPaneID(id string) bool {
	return len(id) > 1 && id[0] == '%' && strings.Trim(id[1:], "0123456789") == ""
}

func sessionByName(name string) (tmuxSession, bool, error) {
	output, err := tmuxOutput("display-message", "-p", "-t", "="+name+":", "#{session_id}")
	if err != nil {
		if strings.Contains(err.Error(), "can't find session") {
			return tmuxSession{}, false, nil
		}
		return tmuxSession{}, false, err
	}
	id := strings.TrimSpace(output)
	if id == "" {
		return tmuxSession{}, false, nil
	}
	if !validTmuxSessionID(id) {
		return tmuxSession{}, false, errors.New("tmux returned an invalid session ID")
	}
	return tmuxSession{id: id, name: name}, true, nil
}

func jumpTmuxSession(selected item) error {
	if selected.muxSessionID != "" && !validTmuxSessionID(selected.muxSessionID) {
		return fmt.Errorf("invalid tmux session ID %q", selected.muxSessionID)
	}
	if os.Getenv("TMUX") == "" {
		if selected.sessionSource == "" && selected.muxSessionID == "" {
			return errors.New("the selected tmux session is no longer open")
		}
		args := []string{"new-session", "-A", "-s", selected.target}
		if selected.cwd != "" {
			args = append(args, "-c", selected.cwd)
		}
		command := exec.Command("tmux", args...)
		command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
		return command.Run()
	}
	live, exists, err := sessionByName(selected.target)
	if err != nil {
		return err
	}
	if exists {
		return switchTmuxSession(live.id)
	}
	if selected.sessionSource == "" {
		return errors.New("the selected tmux session is no longer open")
	}
	format := "#{session_id}" + tmuxFieldSeparator + "#{session_name}" + tmuxFieldSeparator + "#{pane_id}"
	output, err := tmuxOutput("new-session", "-d", "-P", "-F", format, "-s", selected.target, "-c", selected.cwd)
	if err != nil {
		live, exists, lookupErr := sessionByName(selected.target)
		if lookupErr == nil && exists {
			return switchTmuxSession(live.id)
		}
		return err
	}
	parts := strings.Split(strings.TrimSpace(output), tmuxFieldSeparator)
	if len(parts) != 3 || !validTmuxSessionID(parts[0]) || parts[1] != selected.target || !validTmuxPaneID(parts[2]) {
		if len(parts) > 0 && validTmuxSessionID(parts[0]) {
			_, _ = tmuxOutput("kill-session", "-t", parts[0])
		}
		return errors.New("tmux returned incomplete session identity")
	}
	return switchTmuxSession(parts[0])
}

func switchTmuxSession(id string) error {
	if !validTmuxSessionID(id) {
		return fmt.Errorf("invalid tmux session ID %q", id)
	}
	_, err := tmuxOutput("switch-client", "-t", id)
	return err
}

func switchLastTmuxSession() error {
	config, err := loadSessionsConfig()
	if err != nil {
		return err
	}
	format := "#{session_id}" + tmuxFieldSeparator + "#{session_name}" + tmuxFieldSeparator + "#{session_last_attached}" + tmuxRecordSeparator
	output, err := tmuxOutput("list-sessions", "-F", format)
	if err != nil {
		return err
	}
	type recentSession struct {
		id       string
		attached int64
	}
	sessions := []recentSession{}
	for _, record := range tmuxRecords(output) {
		parts := strings.Split(record, tmuxFieldSeparator)
		if len(parts) != 3 || !validTmuxSessionID(parts[0]) {
			return errors.New("tmux returned malformed session history")
		}
		var attached int64
		if parts[2] != "" {
			attached, err = strconv.ParseInt(parts[2], 10, 64)
			if err != nil || attached < 0 {
				return errors.New("tmux returned malformed session history")
			}
		}
		if config != nil && sessionExcluded(config.exclude, parts[1]) {
			continue
		}
		sessions = append(sessions, recentSession{id: parts[0], attached: attached})
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].attached > sessions[j].attached })
	if len(sessions) < 2 {
		return errors.New("no last session found")
	}
	return switchTmuxSession(sessions[1].id)
}

func renameTmuxSession(selected item, name string) error {
	name = strings.TrimSpace(name)
	if !validTmuxSessionName(name) {
		return errors.New("session name must be non-empty and contain no colons or control characters")
	}
	if !validTmuxSessionID(selected.muxSessionID) {
		return fmt.Errorf("invalid tmux session ID %q", selected.muxSessionID)
	}
	if name == selected.target {
		return errors.New("session name is unchanged")
	}
	live, exists, err := sessionByName(selected.target)
	if err != nil {
		return err
	}
	if !exists || live.id != selected.muxSessionID {
		return errors.New("the selected tmux session changed; refresh and try again")
	}
	if _, exists, err = sessionByName(name); err != nil {
		return err
	} else if exists {
		return errors.New("a tmux session already uses that name")
	}
	if _, err := tmuxOutput("rename-session", "-t", live.id, name); err != nil {
		return err
	}
	renamed, exists, err := sessionByName(name)
	if err != nil {
		return err
	}
	if !exists || renamed.id != live.id {
		return errors.New("tmux renamed a different session; refresh and try again")
	}
	return nil
}

func removeTmuxSession(selected item) error {
	if !validTmuxSessionID(selected.muxSessionID) {
		return fmt.Errorf("invalid tmux session ID %q", selected.muxSessionID)
	}
	live, exists, err := sessionByName(selected.target)
	if err != nil {
		return err
	}
	if !exists || live.id != selected.muxSessionID {
		return errors.New("the selected tmux session changed; refresh and try again")
	}
	current, err := tmuxOutput("display-message", "-p", "#{session_id}")
	if err != nil {
		return errors.New("cannot determine the current tmux session")
	}
	current = strings.TrimSpace(current)
	if !validTmuxSessionID(current) {
		return errors.New("tmux returned an invalid current session ID")
	}
	if live.id == current {
		return errors.New("cannot remove the current tmux session")
	}
	_, err = tmuxOutput("kill-session", "-t", live.id)
	return err
}
