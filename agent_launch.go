package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode"
)

const (
	agentNameTimeout     = 10 * time.Second
	agentLaunchMaxAge    = time.Hour
	agentPromptMaxAge    = 7 * 24 * time.Hour
	agentBranchMaxLength = 40
	agentFallbackPrefix  = "agent/"
	agentNameInstruction = "Reply with only a Git branch name for the task below. " +
		"Use <type>/<kebab-case-slug>, at most 40 characters, where <type> is one of " +
		"feat, fix, docs, style, refactor, perf, test, chore, ci, or revert.\n\nTask:\n"
)

var (
	defaultAgentCommand     = []string{"pi", "--name", "{branch}", "@{prompt_file}"}
	defaultAgentNameCommand = []string{"pi", "-p", "--no-session", "--no-tools", "--no-extensions", "--no-skills", "--no-context-files", "--no-prompt-templates", "--no-mcp", "--offline"}
	agentBranchTypes        = []string{"feat", "fix", "docs", "style", "refactor", "perf", "test", "chore", "ci", "revert"}
	agentBranchPattern      = regexp.MustCompile(`^([a-z]+)/[a-z0-9]+(-[a-z0-9]+)*$`)
	agentLaunchIDPattern    = regexp.MustCompile(`^[0-9a-f]{32}$`)
	shellSafeWord           = regexp.MustCompile(`^[A-Za-z0-9_./+-]+$`)
	agentPlaceholders       = []string{"prompt", "prompt_file", "branch", "session"}
)

type agentLaunch struct {
	branch    string
	session   string
	sessionID string
	windowID  string
	pane      string
}

type agentLaunchFile struct {
	Argv    []string  `json:"argv"`
	Dir     string    `json:"dir"`
	Created time.Time `json:"created"`
}

func agentPlaceholderNames(arg string) ([]string, error) {
	var names []string
	for rest := arg; ; {
		open, close := strings.IndexByte(rest, '{'), strings.IndexByte(rest, '}')
		if open < 0 && close < 0 {
			return names, nil
		}
		if open < 0 || (close >= 0 && close < open) {
			return nil, fmt.Errorf("unbalanced } in %q", arg)
		}
		end := strings.IndexByte(rest[open+1:], '}')
		if end < 0 {
			return nil, fmt.Errorf("unbalanced { in %q", arg)
		}
		name := rest[open+1 : open+1+end]
		if !slices.Contains(agentPlaceholders, name) {
			return nil, fmt.Errorf("unknown placeholder {%s} in %q", name, arg)
		}
		names = append(names, name)
		rest = rest[open+end+2:]
	}
}

func validateAgentCommand(key string, command []string, placeholders bool) error {
	if len(command) == 0 {
		return fmt.Errorf("agent.%s must be a non-empty array", key)
	}
	for index, arg := range command {
		if strings.ContainsFunc(arg, unicode.IsControl) || (index == 0 && strings.TrimSpace(arg) == "") {
			return fmt.Errorf("agent.%s must start with an executable and contain printable strings", key)
		}
		if !placeholders {
			continue
		}
		names, err := agentPlaceholderNames(arg)
		if err != nil {
			return fmt.Errorf("agent.%s: %w", key, err)
		}
		if index == 0 && len(names) > 0 {
			return fmt.Errorf("agent.%s executable cannot contain placeholders", key)
		}
	}
	return nil
}

// expandAgentCommand fills placeholders in one pass, so placeholder text inside
// a value stays literal. Without a prompt, arguments that need one are dropped;
// a prompt the command never references becomes the last argument.
func expandAgentCommand(command []string, values map[string]string) []string {
	pairs := make([]string, 0, 2*len(agentPlaceholders))
	for _, name := range agentPlaceholders {
		pairs = append(pairs, "{"+name+"}", values[name])
	}
	replacer := strings.NewReplacer(pairs...)
	hasPrompt, usedPrompt := values["prompt"] != "", false
	argv := make([]string, 0, len(command)+1)
	for _, arg := range command {
		names, _ := agentPlaceholderNames(arg)
		needsPrompt := slices.Contains(names, "prompt") || slices.Contains(names, "prompt_file")
		if needsPrompt && !hasPrompt {
			continue
		}
		usedPrompt = usedPrompt || needsPrompt
		argv = append(argv, replacer.Replace(arg))
	}
	if hasPrompt && !usedPrompt {
		argv = append(argv, values["prompt"])
	}
	return argv
}

func validAgentBranch(name string) bool {
	match := agentBranchPattern.FindStringSubmatch(name)
	return len(name) <= agentBranchMaxLength && match != nil && slices.Contains(agentBranchTypes, match[1])
}

func parseAgentBranch(output string) (string, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	name := strings.Trim(strings.TrimSpace(lines[len(lines)-1]), "`'\" ")
	if !validAgentBranch(name) {
		return "", fmt.Errorf("naming command returned invalid branch %q", truncate(safeText(name), 60))
	}
	return name, nil
}

// fallbackAgentBranch builds agent/<slug> from the prompt's leading words.
func fallbackAgentBranch(prompt string) string {
	limit := agentBranchMaxLength - len(agentFallbackPrefix)
	var slug string
	for _, word := range strings.FieldsFunc(strings.ToLower(prompt), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	}) {
		next := word
		if slug != "" {
			next = slug + "-" + word
		}
		if len(next) > limit {
			if slug == "" {
				slug = word[:limit]
			}
			break
		}
		slug = next
	}
	if slug == "" {
		slug = "task"
	}
	return agentFallbackPrefix + slug
}

func suggestAgentBranch(command []string, dir, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentNameTimeout)
	defer cancel()
	args := append(slices.Clone(command[1:]), agentNameInstruction+prompt)
	cmd := boundedCommand(ctx, command[0], args...)
	cmd.Dir = dir
	output := cappedBuffer{limit: 4096}
	cmd.Stdout = &output
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", fmt.Errorf("naming command timed out after %s", agentNameTimeout)
	}
	if err != nil {
		return "", fmt.Errorf("naming command failed: %w", err)
	}
	return parseAgentBranch(output.String())
}

// nameAgentBranch asks the configured naming command for a branch. When that
// fails, it returns a prompt slug and the naming error as a warning.
func nameAgentBranch(repo, prompt string) (branch string, warning, err error) {
	config, err := loadConfig()
	if err != nil {
		return "", nil, err
	}
	root, err := primaryWorktree(repo)
	if err != nil {
		return "", nil, err
	}
	branch, warning = suggestAgentBranch(config.agentNameCommand, root, prompt)
	if warning != nil {
		return fallbackAgentBranch(prompt), warning, nil
	}
	return branch, nil, nil
}

// availableAgentBranch adds the first -N suffix that is free for both the
// branch and its worktree path.
func availableAgentBranch(root, branch string) (string, error) {
	for attempt := 1; attempt <= 100; attempt++ {
		candidate := branch
		if attempt > 1 {
			candidate = fmt.Sprintf("%s-%d", branch, attempt)
		}
		if _, err := gitOutput(root, "show-ref", "--verify", "--quiet", "refs/heads/"+candidate); err == nil {
			continue
		}
		if _, err := os.Lstat(worktreePath(root, candidate)); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("no free name for branch %q", branch)
}

// projectSession finds the tmux session the repository belongs to: a live
// session started at its root, else the configured or discovered session for
// that root, else one named after the repository. It creates a missing one.
func projectSession(root string) (string, string, error) {
	output, err := tmuxOutput("list-sessions", "-F", "#{session_id}"+tmuxFieldSeparator+"#{session_name}"+tmuxFieldSeparator+"#{session_path}")
	if err != nil && !tmuxUnavailable(err) {
		return "", "", err
	}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		parts := strings.Split(line, tmuxFieldSeparator)
		if len(parts) == 3 && validTmuxSessionID(parts[0]) && samePath(parts[2], root) {
			return parts[0], parts[1], nil
		}
	}
	name := tmuxSessionName(filepath.Base(root))
	sessions, err := listSessions(true)
	if sessions == nil && err != nil {
		return "", "", err
	}
	for _, session := range sessions {
		// Live sessions only report their active pane path, which says nothing about
		// ownership; the session_path check above already covered them.
		if session.sessionSource != "" && samePath(session.cwd, root) {
			name = session.target
			break
		}
	}
	live, exists, err := sessionByName(name)
	if err != nil && !tmuxUnavailable(err) {
		return "", "", err
	}
	if exists {
		return live.id, name, nil
	}
	output, err = tmuxOutput("new-session", "-d", "-P", "-F", "#{session_id}", "-s", tmuxLiteral(name), "-c", tmuxLiteral(root))
	if err != nil {
		return "", "", err
	}
	if id := strings.TrimSpace(output); validTmuxSessionID(id) {
		return id, name, nil
	}
	return "", "", errors.New("tmux returned an invalid session ID")
}

// startAgent creates the worktree and opens the agent in a new window of the
// project's tmux session. Errors after the worktree exists keep it.
func startAgent(repo, branch, prompt string) (agentLaunch, error) {
	config, err := loadConfig()
	if err != nil {
		return agentLaunch{}, err
	}
	root, err := primaryWorktree(repo)
	if err != nil {
		return agentLaunch{}, err
	}
	if _, err := gitOutput(root, "check-ref-format", "--branch", branch); err != nil {
		return agentLaunch{}, fmt.Errorf("invalid branch name %q", branch)
	}
	branch, err = availableAgentBranch(root, branch)
	if err != nil {
		return agentLaunch{}, err
	}
	created, err := addWorktree(root, branch)
	if err != nil {
		return agentLaunch{}, err
	}
	launch := agentLaunch{branch: branch}
	failed := func(err error) (agentLaunch, error) {
		return launch, fmt.Errorf("created worktree %s, but the agent did not start: %w", compactHome(created.cwd), err)
	}
	// Resolve the session after the worktree exists, so a failed add leaves no empty session.
	launch.sessionID, launch.session, err = projectSession(root)
	if err != nil {
		return failed(err)
	}
	id, err := writeAgentLaunch(created.cwd, config.agentCommand, prompt, branch, launch.session)
	if err != nil {
		return failed(err)
	}
	// A trailing colon targets the session, so tmux appends the window at its next free index.
	args := append([]string{"new-window", "-d", "-P", "-F", "#{window_id}\t#{pane_id}", "-t", launch.sessionID + ":", "-n", tmuxLiteral(branch), "-c", tmuxLiteral(created.cwd)}, agentPaneCommand(id)...)
	output, err := tmuxOutput(args...)
	if err != nil {
		removeAgentLaunch(id)
		return failed(err)
	}
	parts := strings.Split(strings.TrimSpace(output), "\t")
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "@") || !validTmuxPaneID(parts[1]) {
		return failed(errors.New("tmux returned an invalid window"))
	}
	launch.windowID, launch.pane = parts[0], parts[1]
	if _, err := tmuxOutput("set-option", "-w", "-t", launch.windowID, "@jumpmux_worktree", created.cwd); err != nil {
		return failed(err)
	}
	invalidateTmuxPaneCache()
	return launch, nil
}

// agentPaneCommand runs the launch token through the user's interactive shell,
// so its PATH and tool activations apply, then keeps that shell open after the
// agent exits. Typing the token instead races shell startup: fish keeps early
// typed text but drops an Enter that arrives before its first prompt.
func agentPaneCommand(id string) []string {
	shell, err := tmuxOutput("show-options", "-gv", "default-shell")
	if shell = strings.TrimSpace(shell); err != nil || !filepath.IsAbs(shell) {
		shell = os.Getenv("SHELL")
	}
	if !filepath.IsAbs(shell) {
		shell = "/bin/sh"
	}
	return []string{"/bin/sh", "-c", `"$0" -i -c "$1"; exec "$0"`, shell, agentExecCommand(id)}
}

func openAgentWindow(launch agentLaunch) error {
	if os.Getenv("TMUX") != "" {
		return focusTmuxPane(item{muxSessionID: launch.sessionID, muxWindowID: launch.windowID, pane: launch.pane})
	}
	if _, err := tmuxOutput("select-window", "-t", launch.windowID); err != nil {
		return err
	}
	command := exec.Command("tmux", "attach-session", "-t", launch.sessionID)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	return command.Run()
}

// agentExecCommand is the only text the user's shell parses, so it must be
// valid in every shell. A temporary build (go run) falls back to jumpmux on PATH.
func agentExecCommand(id string) string {
	binary := "jumpmux"
	if path, err := os.Executable(); err == nil && shellSafeWord.MatchString(path) && !pathWithin(path, os.TempDir()) {
		binary = path
	}
	return binary + " agent-exec " + id
}

// jumpmuxStateDir follows XDG on every OS, like configPath.
func jumpmuxStateDir() (string, error) {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" || !filepath.IsAbs(state) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "jumpmux"), nil
}

func agentLaunchPaths(id string) (string, string, error) {
	state, err := jumpmuxStateDir()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(state, "launches", id+".json"), filepath.Join(state, "prompts", id+".md"), nil
}

func writeAgentLaunch(dir string, command []string, prompt, branch, session string) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	id := hex.EncodeToString(random)
	launchPath, promptPath, err := agentLaunchPaths(id)
	if err != nil {
		return "", err
	}
	pruneOldFiles(filepath.Dir(launchPath), agentLaunchMaxAge)
	pruneOldFiles(filepath.Dir(promptPath), agentPromptMaxAge)
	values := map[string]string{"branch": branch, "session": session}
	if prompt != "" {
		if err := atomicWrite(promptPath, []byte(prompt+"\n"), 0o600); err != nil {
			return "", err
		}
		values["prompt"], values["prompt_file"] = prompt, promptPath
	}
	data, err := json.Marshal(agentLaunchFile{Argv: expandAgentCommand(command, values), Dir: dir, Created: time.Now()})
	if err == nil {
		err = atomicWrite(launchPath, data, 0o600)
	}
	if err != nil {
		removeAgentLaunch(id)
		return "", err
	}
	return id, nil
}

func removeAgentLaunch(id string) {
	if launchPath, promptPath, err := agentLaunchPaths(id); err == nil {
		_ = os.Remove(launchPath)
		_ = os.Remove(promptPath)
	}
}

func pruneOldFiles(dir string, maxAge time.Duration) {
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if info, err := entry.Info(); err == nil && info.Mode().IsRegular() && time.Since(info.ModTime()) > maxAge {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

// consumeAgentLaunch reads and deletes a launch file, so one launch ID cannot
// start the agent twice.
func consumeAgentLaunch(id string) (agentLaunchFile, error) {
	if !agentLaunchIDPattern.MatchString(id) {
		return agentLaunchFile{}, fmt.Errorf("invalid agent launch ID %q", id)
	}
	path, _, err := agentLaunchPaths(id)
	if err != nil {
		return agentLaunchFile{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return agentLaunchFile{}, errors.New("agent launch not found or already used")
	}
	if err != nil {
		return agentLaunchFile{}, err
	}
	if err := os.Remove(path); err != nil {
		return agentLaunchFile{}, err
	}
	var launch agentLaunchFile
	if err := json.Unmarshal(data, &launch); err != nil || len(launch.Argv) == 0 {
		return agentLaunchFile{}, errors.New("invalid agent launch file")
	}
	if age := time.Since(launch.Created); age < -time.Minute || age > agentLaunchMaxAge {
		return agentLaunchFile{}, errors.New("agent launch expired")
	}
	return launch, nil
}

func runAgentExec(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: jumpmux agent-exec <launch-id>")
	}
	launch, err := consumeAgentLaunch(args[0])
	if err != nil {
		return err
	}
	if err := os.Chdir(launch.Dir); err != nil {
		return err
	}
	binary, err := exec.LookPath(launch.Argv[0])
	if err != nil {
		return err
	}
	return syscall.Exec(binary, launch.Argv, os.Environ())
}
