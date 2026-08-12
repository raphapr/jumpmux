package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	version          = "0.23.0"
	workingIcon      = "🤖"
	doneIcon         = "✅"
	staleAgentIcon   = "󰔛"
	gitDiffIcon      = "󰏫"
	gitConflictIcon  = "󰀪"
	gitRebaseIcon    = ""
	prDraftIcon      = ""
	prOpenIcon       = ""
	prMergedIcon     = ""
	prClosedIcon     = ""
	checkSuccessIcon = "󰄴"
	checkFailureIcon = "󰅙"
	metadataTimeout  = 5 * time.Second
	commandWait      = 250 * time.Millisecond
	maxFileScan      = 8 << 20
)

type item struct {
	kind             string
	target           string
	cwd              string
	title            string
	updated          time.Time
	current          bool
	dirty            bool
	gitLoaded        bool
	branch           string
	baseBranch       string
	committedAdded   int
	committedRemoved int
	ahead            int
	behind           int
	hasConflict      bool
	isRebasing       bool
	sessionTitle     string
	status           string
	pane             string
	muxSessionID     string
	muxSessionName   string
	muxWindowID      string
	muxWindowName    string
	prNumber         int
	prState          string
	prDraft          bool
	prCheck          string
	prLoaded         bool
	sessionSource    string
	tmuxWindows      int
	added            int
	removed          int
	untracked        int
}

type worktree struct {
	path   string
	branch string
}

type contextItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
	Icon string `json:"icon"`
}

var fileLineCache = struct {
	sync.Mutex
	values map[string]fileLineCount
}{values: map[string]fileLineCount{}}

type fileLineCount struct {
	size    int64
	modTime time.Time
	lines   int
}

var defaultBranchCache = struct {
	sync.Mutex
	values map[string]cachedDefaultBranch
}{values: map[string]cachedDefaultBranch{}}

type cachedDefaultBranch struct {
	branch  string
	expires time.Time
}

func boundedCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, name, args...)
	command.WaitDelay = commandWait
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		} else {
			return err
		}
	}
	return command
}

func dashboardTab(name string) (int, error) {
	switch name {
	case "agents":
		return tabAgents, nil
	case "worktrees":
		return tabWorktrees, nil
	case "sessions":
		return tabSessions, nil
	default:
		return 0, fmt.Errorf("invalid tab %q: want agents, worktrees, or sessions", name)
	}
}

func contextCommand(args []string) (bool, error) {
	if len(args) == 0 || (args[0] != "agents" && args[0] != "worktrees" && args[0] != "sessions") {
		return false, nil
	}
	if len(args) < 2 {
		return true, fmt.Errorf("%s requires list or open", args[0])
	}
	if args[0] == "sessions" && args[1] == "last" {
		if len(args) != 2 {
			return true, errors.New("usage: jumpmux sessions last")
		}
		if os.Getenv("TMUX") == "" {
			return true, errors.New("run jumpmux inside tmux to switch sessions")
		}
		return true, switchLastTmuxSession()
	}
	cwd, err := os.Getwd()
	if err != nil {
		return true, err
	}
	var items []item
	switch args[0] {
	case "agents":
		items, err = listLiveAgents()
	case "sessions":
		items, err = listSessions(true)
	case "worktrees":
		items, err = listWorktreeItems(cwd)
		if err == nil {
			var agents []item
			agents, err = listLiveAgents()
			if err == nil {
				attachAgentsToWorktrees(items, agents)
				err = attachTmuxWorktrees(items)
			}
		}
	}
	if err != nil {
		return true, err
	}
	switch args[1] {
	case "list":
		if len(args) > 3 || (len(args) == 3 && args[2] != "--json") {
			return true, fmt.Errorf("usage: jumpmux %s list [--json]", args[0])
		}
		config, err := loadConfig()
		if err != nil {
			return true, err
		}
		nerdFontEnabled = !config.hasNerdFont || config.nerdFont
		entries := contextJSON(items)
		if len(args) == 3 {
			return true, json.NewEncoder(os.Stdout).Encode(entries)
		}
		for _, entry := range entries {
			fmt.Printf("%s %-24s %s\n", entry.Icon, safeText(entry.Name), safeText(compactHome(entry.Path)))
		}
		return true, nil
	case "open":
		if len(args) != 3 {
			return true, fmt.Errorf("usage: jumpmux %s open <id>", args[0])
		}
		for _, item := range items {
			if item.target == args[2] {
				return true, jump(item)
			}
		}
		return true, fmt.Errorf("%s %q not found", strings.TrimSuffix(args[0], "s"), args[2])
	default:
		return true, fmt.Errorf("unknown %s command %q", args[0], args[1])
	}
}

func contextJSON(items []item) []contextItem {
	result := make([]contextItem, 0, len(items))
	for _, item := range items {
		entry := contextItem{ID: item.target, Name: item.title, Path: item.cwd}
		switch item.kind {
		case "session":
			entry.Icon = dashboardIcon(workingIcon, "A")
			if item.status == "done" {
				entry.Icon = doneIcon
			}
		case "worktree":
			entry.Icon = dashboardIcon("", "W")
		case "tmux-session":
			entry.Icon, _ = sessionIcon(item)
		}
		result = append(result, entry)
	}
	return result
}

func main() {
	if handled, err := contextCommand(os.Args[1:]); handled {
		exitOnError(err)
		return
	}
	forceSessionScope, initialTab := false, tabAgents
	for index := 1; index < len(os.Args); index++ {
		switch os.Args[index] {
		case "-h", "--help":
			fmt.Print("jumpmux — dashboard for Git worktrees, live Pi agents, and tmux sessions\n\nUsage:\n  jumpmux [--session] [-t|--tab <agents|worktrees|sessions>]\n  jumpmux <agents|worktrees|sessions> list [--json]\n  jumpmux <agents|worktrees|sessions> open <id>\n  jumpmux sessions last\n  jumpmux [--list|--version|setup]\n")
			return
		case "-s", "--session":
			forceSessionScope = true
		case "-t", "--tab":
			flag := os.Args[index]
			index++
			if index >= len(os.Args) {
				exitOnError(fmt.Errorf("%s requires agents, worktrees, or sessions", flag))
			}
			var err error
			initialTab, err = dashboardTab(os.Args[index])
			exitOnError(err)
		case "setup":
			path, err := setupPIExtension()
			exitOnError(err)
			fmt.Println("Installed Pi extension:", path)
			fmt.Println("Restart Pi or run /reload in existing sessions.")
			return
		case "agent-status":
			exitOnError(setAgentStatus(os.Args[index+1:]))
			return
		case "pane-focused":
			if index+1 >= len(os.Args) {
				exitOnError(errors.New("pane-focused requires a tmux pane ID"))
			}
			exitOnError(clearFocusedPaneStatus(os.Args[index+1]))
			return
		case "-v", "--version":
			fmt.Println("jumpmux " + version)
			return
		case "--list":
			cwd, err := os.Getwd()
			exitOnError(err)
			items, err := collectItemsFor(cwd)
			exitOnError(err)
			for _, item := range items {
				fmt.Println(item.display())
			}
			return
		default:
			exitOnError(fmt.Errorf("unknown argument %q", os.Args[index]))
		}
	}

	cwd, err := os.Getwd()
	exitOnError(err)
	model := newDashboardForLaunch(cwd, activeTmuxSession(), forceSessionScope)
	model.tab = initialTab
	result, err := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	exitOnError(err)
	model, ok := result.(dashboardModel)
	if !ok {
		return
	}
	_ = saveGitStatusCache(model.gitCache)
	_ = savePRStatusCache(model.prCache)
	if !model.chosen {
		return
	}
	exitOnError(jump(model.selection))
}

func collectItemsFor(cwd string) ([]item, error) {
	items, _ := listWorktreeItems(cwd)
	items = worktreeGitDetails(items)
	items = worktreePRDetails(cwd, items)

	agents, err := listLiveAgents()
	if err != nil {
		return nil, err
	}
	attachAgentsToWorktrees(items, agents)
	if err := attachTmuxWorktrees(items); err != nil {
		return nil, err
	}
	return append(items, agents...), nil
}

func listWorktreeItems(cwd string) ([]item, error) {
	worktrees, current, err := listWorktrees(cwd)
	if err != nil {
		return nil, err
	}
	items := make([]item, 0, len(worktrees))
	for _, wt := range worktrees {
		items = append(items, item{
			kind:    "worktree",
			target:  wt.path,
			cwd:     wt.path,
			branch:  wt.branch,
			title:   wt.branch,
			current: samePath(wt.path, current),
		})
	}
	return items, nil
}

func worktreeGitDetails(items []item) []item {
	items = append([]item(nil), items...)
	if len(items) == 0 {
		return items
	}
	baseBranch := worktrunkDefaultBranch(items[0].cwd)
	loadGitDetailsParallel(items, func(item item) item { return loadGitDetails(item, baseBranch) })
	return items
}

func agentGitDetails(agents []item) []item {
	seen := make(map[string]bool, len(agents))
	items := make([]item, 0, len(agents))
	for _, agent := range agents {
		if agent.cwd == "" || seen[agent.cwd] {
			continue
		}
		seen[agent.cwd] = true
		items = append(items, item{kind: "worktree", target: agent.cwd, cwd: agent.cwd})
	}
	loadGitDetailsParallel(items, func(item item) item {
		item = loadGitDetails(item, worktrunkDefaultBranch(item.cwd))
		if !item.gitLoaded {
			item.gitLoaded = true
			return item
		}
		if item.branch == "main" || item.branch == "master" {
			item.prLoaded = true
			return item
		}
		pullRequests, loaded := listPullRequests(item.cwd)
		item.prLoaded = loaded
		if pr, ok := pullRequestForBranch(item.cwd, item.branch, pullRequests[item.branch]); ok {
			item.prNumber, item.prState, item.prDraft, item.prCheck = pr.Number, pr.State, pr.Draft, pr.Check
		}
		return item
	})
	return items
}

func loadGitDetailsParallel(items []item, load func(item) item) {
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(4, len(items)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				items[index] = load(items[index])
			}
		}()
	}
	for index := range items {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
}

func loadGitDetails(item item, baseBranch string) item {
	item.gitLoaded = false
	item.isRebasing = gitRebaseInProgress(item.cwd)
	status, err := gitOutput(item.cwd, "status", "--porcelain=v2", "--branch")
	if err != nil {
		return item
	}
	item.gitLoaded = true
	item.branch, item.ahead, item.behind, item.dirty = parseGitStatus(status)
	if item.branch == "(detached)" {
		item.branch = "detached"
	}
	if item.branch == "" || item.branch == "detached" {
		return item
	}

	item.baseBranch = baseBranch
	if item.baseBranch == "" {
		item.baseBranch = gitDefaultBranch(item.cwd)
	}
	item.added, item.removed = diffStats(item.cwd)
	untrackedAdded, untracked := untrackedStats(item.cwd)
	item.added += untrackedAdded
	item.untracked = untracked
	if item.branch != item.baseBranch {
		item.committedAdded, item.committedRemoved = diffStatsRange(item.cwd, item.baseBranch+"...HEAD")
		item.hasConflict = gitHasConflict(item.cwd, item.baseBranch)
	}
	return item
}

func parseGitStatus(output string) (branch string, ahead, behind int, dirty bool) {
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			branch = strings.TrimPrefix(line, "# branch.head ")
		case strings.HasPrefix(line, "# branch.ab "):
			fields := strings.Fields(line)
			if len(fields) == 4 {
				ahead, _ = strconv.Atoi(strings.TrimPrefix(fields[2], "+"))
				behind, _ = strconv.Atoi(strings.TrimPrefix(fields[3], "-"))
			}
		case line != "" && !strings.HasPrefix(line, "#"):
			dirty = true
		}
	}
	return branch, ahead, behind, dirty
}

func worktrunkDefaultBranch(dir string) string {
	defaultBranchCache.Lock()
	cached, ok := defaultBranchCache.values[dir]
	defaultBranchCache.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return cached.branch
	}

	branch := ""
	ctx, cancel := context.WithTimeout(context.Background(), metadataTimeout)
	defer cancel()
	command := boundedCommand(ctx, "wt", "-C", dir, "--config-set", "list.json-schema=2", "list", "--format=json")
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	if output, err := command.Output(); err == nil {
		var listing struct {
			Repo struct {
				DefaultBranch string `json:"default_branch"`
			} `json:"repo"`
		}
		if json.Unmarshal(output, &listing) == nil {
			branch = listing.Repo.DefaultBranch
		}
	}
	if branch == "" {
		branch = gitDefaultBranch(dir)
	}
	defaultBranchCache.Lock()
	defaultBranchCache.values[dir] = cachedDefaultBranch{branch: branch, expires: time.Now().Add(time.Minute)}
	defaultBranchCache.Unlock()
	return branch
}

func gitDefaultBranch(dir string) string {
	if head, err := gitOutput(dir, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if _, name, ok := strings.Cut(strings.TrimSpace(head), "/"); ok && name != "" {
			return name
		}
	}
	for _, candidate := range []string{"main", "master"} {
		if _, err := gitOutput(dir, "show-ref", "--verify", "--quiet", "refs/heads/"+candidate); err == nil {
			return candidate
		}
	}
	return "main"
}

func gitRebaseInProgress(dir string) bool {
	gitDir, err := gitOutput(dir, "rev-parse", "--git-dir")
	if err != nil {
		return false
	}
	path := strings.TrimSpace(gitDir)
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	for _, state := range []string{"rebase-merge", "rebase-apply"} {
		if info, err := os.Stat(filepath.Join(path, state)); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

func gitHasConflict(dir, base string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), metadataTimeout)
	defer cancel()
	cmd := boundedCommand(ctx, "git", "-C", dir, "--no-optional-locks", "merge-tree", "--write-tree", base, "HEAD")
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	err := cmd.Run()
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() == 1
}

func untrackedStats(dir string) (lines, files int) {
	output, err := gitOutput(dir, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return 0, 0
	}
	for _, name := range strings.Split(output, "\x00") {
		if name == "" {
			continue
		}
		files++
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		if !info.Mode().IsRegular() {
			lines++
			continue
		}
		lines += countFileLines(path, info)
	}
	return lines, files
}

func countFileLines(path string, _ os.FileInfo) int {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return 0
	}
	file := os.NewFile(uintptr(fd), path)
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return 0
	}
	fileLineCache.Lock()
	cached, ok := fileLineCache.values[path]
	fileLineCache.Unlock()
	if ok && cached.size == info.Size() && cached.modTime.Equal(info.ModTime()) {
		return cached.lines
	}

	// ponytail: scan at most 8 MiB per untracked file; raise maxFileScan if exact counts for generated files matter.
	reader := io.LimitReader(file, maxFileScan)
	buffer := make([]byte, 32*1024)
	count, size := 0, 0
	var last byte
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			count += bytes.Count(buffer[:n], []byte{'\n'})
			size += n
			last = buffer[n-1]
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0
		}
	}
	if size > 0 && last != '\n' {
		count++
	}
	fileLineCache.Lock()
	fileLineCache.values[path] = fileLineCount{size: info.Size(), modTime: info.ModTime(), lines: count}
	fileLineCache.Unlock()
	return count
}

func worktreePRDetails(repo string, items []item) []item {
	items = append([]item(nil), items...)
	if len(items) == 0 {
		return items
	}
	pullRequests, loaded := listPullRequests(repo)
	for index := range items {
		items[index].prLoaded = loaded
		if items[index].branch == "main" || items[index].branch == "master" {
			continue
		}
		if pr, ok := pullRequestForBranch(items[index].cwd, items[index].branch, pullRequests[items[index].branch]); ok {
			items[index].prNumber = pr.Number
			items[index].prState = pr.State
			items[index].prDraft = pr.Draft
			items[index].prCheck = pr.Check
		}
	}
	return items
}

func attachAgentsToWorktrees(items, agents []item) {
	for index := range items {
		if items[index].sessionTitle != "" {
			items[index].pane = ""
			items[index].muxSessionID = ""
			items[index].muxSessionName = ""
			items[index].muxWindowID = ""
			items[index].muxWindowName = ""
		}
		items[index].sessionTitle = ""
		items[index].status = ""
		items[index].updated = time.Time{}

		currentAgent := false
		for _, agent := range agents {
			if !pathWithin(agent.cwd, items[index].cwd) || (currentAgent && !agent.current) {
				continue
			}
			if agent.current || items[index].sessionTitle == "" || agent.updated.After(items[index].updated) {
				items[index].sessionTitle = agent.title
				items[index].updated = agent.updated
				items[index].status = agent.status
				items[index].pane = agent.pane
				items[index].muxSessionID = agent.muxSessionID
				items[index].muxSessionName = agent.muxSessionName
				items[index].muxWindowID = agent.muxWindowID
				items[index].muxWindowName = agent.muxWindowName
				if agent.current {
					items[index].current = true
					currentAgent = true
				}
			}
		}
	}
}

func listWorktrees(cwd string) ([]worktree, string, error) {
	currentOutput, err := gitOutput(cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, "", err
	}
	output, err := gitOutput(cwd, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, "", err
	}
	return parseWorktrees([]byte(output)), strings.TrimSpace(string(currentOutput)), nil
}

func parseWorktrees(output []byte) []worktree {
	var result []worktree
	var current worktree
	appendCurrent := func() {
		if current.path == "" {
			return
		}
		if current.branch == "" {
			current.branch = "detached"
		}
		result = append(result, current)
		current = worktree{}
	}
	for _, field := range bytes.Split(output, []byte{0}) {
		if len(field) == 0 {
			appendCurrent()
			continue
		}
		value := string(field)
		switch {
		case strings.HasPrefix(value, "worktree "):
			current.path = strings.TrimPrefix(value, "worktree ")
		case strings.HasPrefix(value, "branch refs/heads/"):
			current.branch = strings.TrimPrefix(value, "branch refs/heads/")
		}
	}
	appendCurrent()
	return result
}

func gitOutput(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), metadataTimeout)
	defer cancel()
	cmd := boundedCommand(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	output, err := cmd.Output()
	if err != nil {
		return string(output), gitCommandError(args, output, err)
	}
	return string(output), nil
}

func gitCommandError(args []string, output []byte, err error) error {
	message := strings.TrimSpace(string(output))
	var exit *exec.ExitError
	if errors.As(err, &exit) && len(exit.Stderr) > 0 {
		message = strings.TrimSpace(string(exit.Stderr))
	}
	command := "git"
	if len(args) > 0 {
		command += " " + strings.Join(args, " ")
	}
	if message == "" {
		return fmt.Errorf("%s: %w", command, err)
	}
	return fmt.Errorf("%s: %s", command, truncate(safeText(message), 160))
}

func gitErrorLine(err error) string {
	return "(Git unavailable: " + truncate(safeText(err.Error()), 160) + ")"
}

type cappedBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (buffer *cappedBuffer) Write(data []byte) (int, error) {
	length := len(data)
	remaining := max(0, buffer.limit-buffer.Len())
	if remaining < length {
		buffer.truncated = true
		data = data[:remaining]
	}
	_, _ = buffer.Buffer.Write(data)
	return length, nil
}

func gitOutputLimited(dir string, limit int, args ...string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), metadataTimeout)
	defer cancel()
	cmd := boundedCommand(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	output := cappedBuffer{limit: limit}
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &stderr
	err := cmd.Run()
	if err != nil {
		err = gitCommandError(args, stderr.Bytes(), err)
	}
	return output.String(), output.truncated, err
}

func diffStats(dir string) (int, int) {
	output, err := gitOutput(dir, "diff", "--numstat", "HEAD")
	if err != nil {
		staged, _ := gitOutput(dir, "diff", "--numstat", "--cached")
		unstaged, _ := gitOutput(dir, "diff", "--numstat")
		output = staged + "\n" + unstaged
	}
	return parseDiffStats(output)
}

func diffStatsRange(dir, revision string) (int, int) {
	output, _ := gitOutput(dir, "diff", "--numstat", revision)
	return parseDiffStats(output)
}

func parseDiffStats(output string) (int, int) {
	var added, removed int
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		var a, r int
		if _, err := fmt.Sscanf(line, "%d\t%d\t", &a, &r); err == nil {
			added += a
			removed += r
		}
	}
	return added, removed
}

func jump(item item) error {
	switch item.kind {
	case "session":
		return jumpTmuxPane(item)
	case "worktree":
		if item.pane != "" {
			return jumpTmuxPane(item)
		}
		return openTmuxWorktree(item)
	case "tmux-session":
		return jumpTmuxSession(item)
	default:
		return fmt.Errorf("unknown item type %q", item.kind)
	}
}

func (item item) display() string {
	kind, marker := "PI", " "
	if item.current {
		marker = "@"
	}
	if item.kind == "worktree" {
		kind = "WT"
		if item.dirty {
			marker += "*"
		}
	}
	age := ""
	if !item.updated.IsZero() {
		age = relativeAge(item.updated)
	}
	return fmt.Sprintf("%-2s %-2s %-52s  %-45s %s", kind, marker, truncate(safeText(item.title), 52), safeText(compactHome(item.cwd)), age)
}

func compactHome(path string) string {
	home, err := os.UserHomeDir()
	if err == nil && (path == home || strings.HasPrefix(path, home+string(os.PathSeparator))) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

func samePath(a, b string) bool { return filepath.Clean(a) == filepath.Clean(b) }

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func safeText(value string) string {
	return strings.Join(strings.Fields(safeLine(value)), " ")
}

func safeLine(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
}

func truncate(value string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	if max == 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}

func relativeAge(value time.Time) string {
	duration := time.Since(value)
	switch {
	case duration < 0:
		return value.Format("2006-01-02")
	case duration < time.Minute:
		return "now"
	case duration < time.Hour:
		return fmt.Sprintf("%dm", int(duration.Minutes()))
	case duration < 24*time.Hour:
		return fmt.Sprintf("%dh", int(duration.Hours()))
	case duration < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(duration.Hours()/24))
	default:
		return value.Format("2006-01-02")
	}
}

func exitOnError(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "jumpmux:", err)
		os.Exit(1)
	}
}
