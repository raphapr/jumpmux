package main

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	refreshInterval      = 2 * time.Second
	gitRefreshPeriod     = 5 * time.Second
	previewRefreshPeriod = 500 * time.Millisecond
	clockInterval        = 250 * time.Millisecond
	staleThreshold       = time.Hour
	maxDiffOutput        = 1 << 20
)

var spinnerFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

var (
	currentRowColor, currentWorktreeColor, selectedColor lipgloss.AdaptiveColor
	textColor, dimmedColor, borderColor, headerColor     lipgloss.AdaptiveColor
	keycapColor, infoColor, successColor, warningColor   lipgloss.AdaptiveColor
	dangerColor, accentColor                             lipgloss.AdaptiveColor

	textStyle, headerStyle, mutedStyle, borderStyle   lipgloss.Style
	keycapStyle, infoStyle, successStyle              lipgloss.Style
	warningStyle, dangerStyle, accentStyle            lipgloss.Style
	selectedStyle, currentStyle, currentWorktreeStyle lipgloss.Style
	addedStyle, removedStyle, diffHeadStyle           lipgloss.Style
)

type dashboardModel struct {
	cwd                                                 string
	now                                                 time.Time
	tab, index                                          int
	width, height                                       int
	agents, allAgents                                   []item
	worktrees                                           []item
	agentGit, gitCache, prCache                         map[string]item
	scope                                               scopeMode
	scheme                                              colorScheme
	launchSession                                       string
	worktreeGeneration                                  uint64
	previewRequest, diffRequest                         uint64
	filter, diff, help                                  bool
	query                                               string
	queries                                             [2]string
	preview                                             previewData
	previewOffset, diffOff, xOffset, previewSize        int
	loading                                             bool
	agentsLoaded, worktreesLoaded                       bool
	agentsInFlight, agentGitInFlight, worktreesInFlight bool
	agentGitRefreshed, previewRefreshed                 time.Time
	worktreePending                                     int
	err, agentErr, worktreeErr                          error
	chosen                                              bool
	selection                                           item
	lastClickTarget                                     string
	lastClickAt                                         time.Time
	worktreeBackend                                     worktreeBackend
	action                                              dashboardAction
	actionInput                                         string
	actionTarget                                        item
}

type dashboardData struct {
	agents []item
	err    error
}

type dashboardDataMsg dashboardData

type agentGitMsg []item

type dashboardAction uint8

const (
	actionNone dashboardAction = iota
	actionAddWorktree
	actionRemoveWorktree
	actionRunning
)

type worktreeActionMsg struct {
	action dashboardAction
	err    error
}

type worktreeStage uint8

const (
	worktreeListStage worktreeStage = iota
	worktreeGitStage
	worktreePRStage
	worktreeMuxStage
)

type worktreeDataMsg struct {
	stage      worktreeStage
	generation uint64
	worktrees  []item
	err        error
}

type previewData struct {
	request      uint64
	scheme       colorScheme
	target       string
	updated      time.Time
	title        string
	lines        []string
	rightTitle   string
	rightLines   []string
	followBottom bool
}

type previewMsg previewData

type previewRequestMsg struct {
	request uint64
	item    item
	scheme  colorScheme
}

type diffMsg struct {
	request uint64
	target  string
	title   string
	lines   []string
	files   []string
}

type tickMsg time.Time
type clockMsg time.Time

func newDashboard(cwd string) dashboardModel {
	applyColorScheme(schemeDefault)
	return dashboardModel{cwd: cwd, now: time.Now(), width: 80, height: 24, previewSize: defaultPreviewSize, scheme: schemeDefault, worktreeBackend: backendAuto, worktreeGeneration: 1, agentGit: map[string]item{}, gitCache: map[string]item{}, prCache: map[string]item{}, agentsInFlight: true, worktreesInFlight: true}
}

func newDashboardForLaunch(cwd, launchSession string, forceSession bool) dashboardModel {
	model := newDashboard(cwd)
	model.gitCache = loadGitStatusCache()
	model.prCache = loadPRStatusCache()
	model.agentGit = maps.Clone(model.gitCache)
	for path, cached := range model.prCache {
		detail := model.agentGit[path]
		detail.kind, detail.target, detail.cwd = "worktree", path, path
		copyPRStatus(&detail, cached)
		model.agentGit[path] = detail
	}
	model.scope = loadScopeMode()
	model.previewSize = loadPreviewSize()
	model.worktreeBackend, model.err = loadWorktreeBackend()
	model.scheme = loadColorScheme()
	applyColorScheme(model.scheme)
	model.launchSession = launchSession
	if forceSession {
		model.scope = scopeSession
	}
	return model
}

func (m dashboardModel) Init() tea.Cmd {
	return tea.Batch(refreshAgents(), refreshWorktreeList(m.cwd, m.worktreeGeneration), nextTick(), nextClock())
}

func nextTick() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func nextClock() tea.Cmd {
	return tea.Tick(clockInterval, func(t time.Time) tea.Msg { return clockMsg(t) })
}

func refreshAgents() tea.Cmd {
	return func() tea.Msg {
		agents, err := listLiveAgents()
		return dashboardDataMsg(dashboardData{agents: agents, err: err})
	}
}

func refreshAgentGit(agents []item) tea.Cmd {
	return func() tea.Msg { return agentGitMsg(agentGitDetails(agents)) }
}

func refreshWorktreeList(cwd string, generation uint64) tea.Cmd {
	return func() tea.Msg {
		items, err := listWorktreeItems(cwd)
		return worktreeDataMsg{stage: worktreeListStage, generation: generation, worktrees: items, err: err}
	}
}

func refreshWorktreeGit(items []item, generation uint64) tea.Cmd {
	return func() tea.Msg {
		return worktreeDataMsg{stage: worktreeGitStage, generation: generation, worktrees: worktreeGitDetails(items)}
	}
}

func refreshWorktreePR(cwd string, items []item, generation uint64) tea.Cmd {
	return func() tea.Msg {
		return worktreeDataMsg{stage: worktreePRStage, generation: generation, worktrees: worktreePRDetails(cwd, items)}
	}
}

func refreshWorktreeMux(items []item, generation uint64) tea.Cmd {
	return func() tea.Msg {
		items = append([]item(nil), items...)
		err := attachTmuxWorktrees(items)
		return worktreeDataMsg{stage: worktreeMuxStage, generation: generation, worktrees: items, err: err}
	}
}

func loadPreview(item item, scheme colorScheme, request uint64) tea.Cmd {
	return func() tea.Msg {
		preview := previewData{request: request, scheme: scheme, target: item.target, updated: item.updated}
		if item.kind == "session" {
			output, err := tmuxOutput(
				"display-message", "-p", "-t", item.pane, "#{cursor_y}\t#{pane_height}",
				";", "capture-pane", "-p", "-e", "-S", "-200", "-t", item.pane,
			)
			preview.title = "Preview: " + worktreeName(item.cwd)
			preview.followBottom = true
			if err != nil {
				preview.lines = []string{"(pane not available)"}
				return previewMsg(preview)
			}
			lines := paneHistoryLines(output)
			if len(lines) == 0 {
				preview.lines = []string{"(empty output)"}
				return previewMsg(preview)
			}
			for _, line := range lines {
				preview.lines = append(preview.lines, sanitizePaneLine(strings.TrimSuffix(line, "\r")))
			}
			if len(preview.lines) == 0 {
				preview.lines = []string{"(empty output)"}
			}
			return previewMsg(preview)
		}

		status, _ := gitOutput(item.cwd, "status", "--short")
		log, _ := gitOutput(item.cwd, "log", "--pretty=format:%h%x09%ar%x09%s", "-20")
		themeMu.RLock()
		defer themeMu.RUnlock()
		if appliedColorScheme != scheme {
			return previewMsg(preview)
		}
		preview.title = worktreeName(item.cwd)
		preview.lines = []string{
			mutedStyle.Render("Branch  ") + textStyle.Render(safeText(item.branch)),
			mutedStyle.Render("Path    ") + textStyle.Render(safeText(compactHome(item.cwd))),
			mutedStyle.Render("Diff    ") + gitStatusView(item),
			mutedStyle.Render("Agent   ") + agentSummary(item, time.Now()),
			mutedStyle.Render("Mux     ") + muxView(item),
			"",
		}
		if strings.TrimSpace(status) == "" {
			preview.lines = append(preview.lines, mutedStyle.Render("clean"))
		} else {
			for _, line := range strings.Split(strings.TrimSpace(status), "\n") {
				preview.lines = append(preview.lines, safeLine(line))
			}
		}
		preview.rightTitle = "Git Log"
		if strings.TrimSpace(log) == "" {
			preview.rightLines = []string{"(no commits)"}
		} else {
			preview.rightLines = strings.Split(strings.TrimSpace(log), "\n")
		}
		return previewMsg(preview)
	}
}

func loadDiff(item item, request uint64) tea.Cmd {
	return func() tea.Msg {
		if _, err := gitOutput(item.cwd, "rev-parse", "--is-inside-work-tree"); err != nil {
			return diffMsg{request: request, target: item.target, title: "WIP", lines: []string{"No Git repository at this path."}}
		}
		unstaged, unstagedTruncated, _ := gitOutputLimited(item.cwd, maxDiffOutput, "diff", "--no-ext-diff", "--no-color")
		staged, stagedTruncated, _ := gitOutputLimited(item.cwd, maxDiffOutput, "diff", "--no-ext-diff", "--no-color", "--cached")
		untracked, _ := gitOutput(item.cwd, "ls-files", "--others", "--exclude-standard")
		status, _ := gitOutput(item.cwd, "status", "--porcelain")
		branch, _ := gitOutput(item.cwd, "branch", "--show-current")
		added, removed := diffStats(item.cwd)
		var lines []string
		if strings.TrimSpace(unstaged) != "" {
			lines = append(lines, strings.Split(strings.TrimRight(unstaged, "\n"), "\n")...)
		}
		if strings.TrimSpace(staged) != "" {
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, strings.Split(strings.TrimRight(staged, "\n"), "\n")...)
		}
		if unstagedTruncated || stagedTruncated {
			lines = append(lines, "", "… diff truncated")
		}
		if strings.TrimSpace(untracked) != "" {
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, "Untracked files:")
			for _, name := range strings.Split(strings.TrimSpace(untracked), "\n") {
				lines = append(lines, "? "+safeLine(name))
			}
		}
		if len(lines) == 0 {
			lines = []string{"Working tree is clean."}
		}
		var files []string
		for _, line := range strings.Split(strings.TrimSpace(status), "\n") {
			if len(line) >= 4 {
				files = append(files, safeLine(line[:2]+" "+line[3:]))
			}
		}
		title := "WIP: " + strings.TrimSpace(branch)
		if strings.TrimSpace(branch) == "" {
			title = "WIP"
		}
		if added > 0 {
			title += fmt.Sprintf(" +%d", added)
		}
		if removed > 0 {
			title += fmt.Sprintf(" -%d", removed)
		}
		return diffMsg{request: request, target: item.target, title: title, lines: lines, files: files}
	}
}

func (m *dashboardModel) requestPreview(item item) tea.Cmd {
	m.previewRequest++
	if item.kind == "session" {
		m.previewRefreshed = time.Now()
	}
	request, scheme := m.previewRequest, m.scheme
	if m.preview.target != item.target {
		m.previewOffset, m.xOffset, m.loading = 0, 0, true
	}
	return tea.Tick(75*time.Millisecond, func(time.Time) tea.Msg {
		return previewRequestMsg{request: request, item: item, scheme: scheme}
	})
}

func (m dashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tickMsg:
		commands := []tea.Cmd{nextTick()}
		if !m.agentsInFlight {
			m.agentsInFlight = true
			commands = append(commands, refreshAgents())
		}
		if !m.worktreesInFlight {
			m.worktreesInFlight = true
			m.worktreeGeneration++
			commands = append(commands, refreshWorktreeList(m.cwd, m.worktreeGeneration))
		}
		return m, tea.Batch(commands...)
	case clockMsg:
		m.now = time.Time(msg)
		commands := []tea.Cmd{nextClock()}
		if selected, ok := m.selected(); ok && selected.kind == "session" && !m.diff && m.now.Sub(m.previewRefreshed) >= previewRefreshPeriod {
			commands = append(commands, m.requestPreview(selected))
		}
		return m, tea.Batch(commands...)
	case dashboardDataMsg:
		m.agentsInFlight, m.agentsLoaded, m.agentErr = false, true, msg.err
		if msg.err != nil {
			return m, nil
		}
		target := m.selectedTarget()
		m.allAgents = msg.agents
		m.applyAgentScope()
		attachAgentsToWorktrees(m.worktrees, m.allAgents)
		m.restoreSelection(target)
		activePaths := make(map[string]bool, len(m.allAgents))
		for _, agent := range m.allAgents {
			activePaths[agent.cwd] = true
		}
		for path := range m.agentGit {
			if !activePaths[path] {
				delete(m.agentGit, path)
			}
		}
		commands := []tea.Cmd{}
		if len(m.allAgents) > 0 && !m.agentGitInFlight && time.Since(m.agentGitRefreshed) >= gitRefreshPeriod {
			m.agentGitInFlight = true
			m.agentGitRefreshed = time.Now()
			commands = append(commands, refreshAgentGit(m.allAgents))
		}
		if selected, ok := m.selected(); ok && selected.kind == "session" && !m.diff {
			if m.preview.target != selected.target || !m.preview.updated.Equal(selected.updated) {
				commands = append(commands, m.requestPreview(selected))
			}
		} else if !m.diff && !ok {
			m.preview, m.loading = previewData{}, false
		}
		return m, tea.Batch(commands...)
	case agentGitMsg:
		m.agentGitInFlight = false
		if m.agentGit == nil {
			m.agentGit = map[string]item{}
		}
		if m.gitCache == nil {
			m.gitCache = map[string]item{}
		}
		if m.prCache == nil {
			m.prCache = map[string]item{}
		}
		activePaths := make(map[string]bool, len(m.allAgents))
		for _, agent := range m.allAgents {
			activePaths[agent.cwd] = true
		}
		for _, detail := range msg {
			if !activePaths[detail.cwd] {
				continue
			}
			if !detail.prLoaded {
				copyPRStatus(&detail, m.agentGit[detail.cwd])
			}
			m.agentGit[detail.cwd] = detail
			m.gitCache[detail.cwd] = detail
			if detail.prLoaded {
				if detail.prNumber == 0 {
					delete(m.prCache, detail.cwd)
				} else {
					m.prCache[detail.cwd] = detail
				}
			}
		}
		return m, nil
	case worktreeDataMsg:
		if msg.generation != m.worktreeGeneration {
			return m, nil
		}
		if msg.stage == worktreeListStage {
			m.worktreesLoaded, m.worktreeErr = true, msg.err
			if msg.err != nil {
				m.worktreesInFlight = false
				return m, nil
			}
			target := m.selectedTarget()
			m.worktrees = mergeWorktreeData(m.worktrees, msg.worktrees, msg.stage)
			cached := make([]item, 0, len(m.worktrees))
			for _, worktree := range m.worktrees {
				if detail, ok := m.gitCache[worktree.cwd]; ok {
					cached = append(cached, detail)
				}
			}
			m.worktrees = mergeWorktreeData(m.worktrees, cached, worktreeGitStage)
			cached = cached[:0]
			for _, worktree := range m.worktrees {
				if detail, ok := m.prCache[worktree.cwd]; ok {
					cached = append(cached, detail)
				}
			}
			m.worktrees = mergeWorktreeData(m.worktrees, cached, worktreePRStage)
			m.restoreSelection(target)
			attachAgentsToWorktrees(m.worktrees, m.allAgents)
			m.worktreePending = 3
			return m, tea.Batch(
				refreshWorktreeGit(msg.worktrees, msg.generation),
				refreshWorktreePR(m.cwd, msg.worktrees, msg.generation),
				refreshWorktreeMux(msg.worktrees, msg.generation),
			)
		}

		before, hadBefore := m.selected()
		target := m.selectedTarget()
		if msg.err != nil {
			m.worktreeErr = msg.err
		} else {
			m.worktrees = mergeWorktreeData(m.worktrees, msg.worktrees, msg.stage)
			if msg.stage == worktreeGitStage {
				if m.gitCache == nil {
					m.gitCache = map[string]item{}
				}
				for _, detail := range msg.worktrees {
					if detail.gitLoaded {
						m.gitCache[detail.cwd] = detail
					}
				}
			}
			if msg.stage == worktreePRStage {
				if m.prCache == nil {
					m.prCache = map[string]item{}
				}
				for _, detail := range msg.worktrees {
					if !detail.prLoaded {
						continue
					}
					if detail.prNumber == 0 {
						delete(m.prCache, detail.cwd)
					} else {
						m.prCache[detail.cwd] = detail
					}
				}
			}
		}
		m.restoreSelection(target)
		m.worktreePending = max(0, m.worktreePending-1)
		if m.worktreePending == 0 {
			m.worktreesInFlight = false
		}
		after, hasAfter := m.selected()
		changed := !hadBefore || before != after
		refreshPreview := msg.stage == worktreeGitStage || (msg.stage == worktreeMuxStage && changed)
		if msg.err == nil && !m.diff && hasAfter && after.kind == "worktree" && refreshPreview {
			return m, m.requestPreview(after)
		}
		return m, nil
	case previewRequestMsg:
		if !m.diff && msg.request == m.previewRequest && msg.scheme == m.scheme {
			if selected, ok := m.selected(); ok && selected.target == msg.item.target {
				return m, loadPreview(msg.item, msg.scheme, msg.request)
			}
		}
		return m, nil
	case previewMsg:
		if !m.diff && msg.request == m.previewRequest && msg.scheme == m.scheme {
			if selected, ok := m.selected(); ok && selected.target == msg.target {
				reset := m.preview.target != msg.target
				wasAtBottom := m.previewOffset == m.clampOffset(len(m.preview.lines), m.preview.lines)
				m.preview = previewData(msg)
				if reset {
					m.previewOffset, m.xOffset = 0, 0
				}
				if msg.followBottom && (reset || wasAtBottom) {
					m.previewOffset = m.clampOffset(len(msg.lines), msg.lines)
				} else {
					m.previewOffset = m.clampOffset(m.previewOffset, msg.lines)
				}
				m.loading = false
			}
		}
		return m, nil
	case diffMsg:
		if m.diff && msg.request == m.diffRequest {
			if selected, ok := m.selected(); ok && selected.target == msg.target {
				m.preview = previewData{target: msg.target, title: msg.title, lines: msg.lines, rightTitle: fmt.Sprintf("Files (%d)", len(msg.files)), rightLines: msg.files}
				m.diffOff, m.xOffset, m.loading = 0, 0, false
			}
		}
		return m, nil
	case worktreeActionMsg:
		m.action, m.actionInput, m.actionTarget, m.err = actionNone, "", item{}, msg.err
		if msg.err == nil && (msg.action == actionAddWorktree || msg.action == actionRemoveWorktree) {
			m.worktreesInFlight = true
			m.worktreeGeneration++
			return m, refreshWorktreeList(m.cwd, m.worktreeGeneration)
		}
		return m, nil
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m dashboardModel) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.width < 40 || m.height < 10 || m.filter || m.help || msg.X < 0 || msg.X >= m.width {
		return m, nil
	}
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		delta := 3
		if msg.Button == tea.MouseButtonWheelUp {
			delta = -delta
		}
		if m.diff {
			m.diffOff = m.clampOffset(m.diffOff+delta, m.preview.lines)
		} else if msg.Y >= 2+m.tableHeight() {
			m.previewOffset = m.clampOffset(m.previewOffset+delta, m.preview.lines)
		} else {
			m.move(delta)
			if selected, ok := m.selected(); ok {
				return m, m.requestPreview(selected)
			}
		}
		return m, nil
	}
	if m.diff || msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}
	if msg.Y == 0 {
		tab := 0
		if msg.X >= 10 {
			tab = 1
		}
		return m.switchTab(tab)
	}
	row := msg.Y - 3
	if row < 0 || row >= max(0, m.tableHeight()-1) {
		return m, nil
	}
	rows := m.rows()
	index := m.visibleRowStart() + row
	if index < 0 || index >= len(rows) {
		return m, nil
	}
	selected := rows[index]
	doubleClick := selected.target == m.lastClickTarget && time.Since(m.lastClickAt) < 400*time.Millisecond
	m.index, m.lastClickTarget, m.lastClickAt = index, selected.target, time.Now()
	if doubleClick {
		m.selection, m.chosen = selected, true
		return m, tea.Quit
	}
	return m, m.requestPreview(selected)
}

func (m dashboardModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.width < 40 || m.height < 10 {
		if key == "q" || key == "esc" || key == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	}
	if m.help {
		if key == "?" || key == "esc" || key == "q" {
			m.help = false
		}
		return m, nil
	}
	if m.action != actionNone {
		return m.handleActionKey(msg)
	}
	if m.filter {
		switch key {
		case "enter":
			m.filter = false
		case "esc":
			m.filter, m.query, m.index = false, "", 0
		case "backspace", "ctrl+h":
			runes := []rune(m.query)
			if len(runes) > 0 {
				m.query = string(runes[:len(runes)-1])
			}
			m.index = 0
		default:
			if len(msg.Runes) > 0 {
				m.query += string(msg.Runes)
				m.index = 0
			}
		}
		m.queries[m.tab] = m.query
		if selected, ok := m.selected(); ok {
			return m, m.requestPreview(selected)
		}
		m.preview, m.loading = previewData{}, false
		return m, nil
	}
	if key == "+" || key == "=" || key == "-" || key == "_" {
		delta := previewSizeStep
		if key == "-" || key == "_" {
			delta = -delta
		}
		m.resizePreview(delta)
		return m, nil
	}
	if m.diff {
		switch key {
		case "esc", "q", "ctrl+c":
			m.diff = false
			m.preview = previewData{}
			if selected, ok := m.selected(); ok {
				return m, m.requestPreview(selected)
			}
		case "k", "up":
			m.diffOff = m.clampOffset(m.diffOff-1, m.preview.lines)
		case "j", "down":
			m.diffOff = m.clampOffset(m.diffOff+1, m.preview.lines)
		case "ctrl+u":
			m.diffOff = m.clampOffset(m.diffOff-m.previewHeight()/2, m.preview.lines)
		case "ctrl+d":
			m.diffOff = m.clampOffset(m.diffOff+m.previewHeight()/2, m.preview.lines)
		case "h", "left":
			m.xOffset = max(0, m.xOffset-4)
		case "l", "right":
			m.xOffset = m.clampX(m.xOffset + 4)
		}
		return m, nil
	}

	m.err = nil
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		index := int(key[0] - '1')
		rows := m.rows()
		if index < len(rows) {
			m.index, m.selection, m.chosen = index, rows[index], true
			return m, tea.Quit
		}
	}

	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		if m.query != "" {
			m.query, m.queries[m.tab], m.index = "", "", 0
			if selected, ok := m.selected(); ok {
				return m, m.requestPreview(selected)
			}
			return m, nil
		}
		return m, tea.Quit
	case "?":
		m.help = true
		return m, nil
	case "tab":
		return m.switchTab(1 - m.tab)
	case "t":
		m.scheme = m.scheme.next()
		applyColorScheme(m.scheme)
		m.err = saveColorScheme(m.scheme)
		if selected, ok := m.selected(); ok {
			return m, m.requestPreview(selected)
		}
	case "s":
		if m.tab != 0 {
			return m, nil
		}
		target := m.selectedTarget()
		m.scope = m.scope.toggle()
		m.err = saveScopeMode(m.scope)
		m.applyAgentScope()
		m.restoreSelection(target)
		if selected, ok := m.selected(); ok {
			return m, m.requestPreview(selected)
		}
		m.preview, m.loading = previewData{}, false
	case "/":
		m.filter = true
	case "a":
		if m.tab == 1 {
			m.action, m.actionInput, m.err = actionAddWorktree, "", nil
		}
	case "r":
		if m.tab == 1 {
			selected, ok := m.selected()
			if !ok || selected.kind != "worktree" {
				return m, nil
			}
			if selected.current {
				m.err = errors.New("cannot remove the current worktree")
				return m, nil
			}
			if len(m.worktrees) > 0 && samePath(selected.cwd, m.worktrees[0].cwd) {
				m.err = errors.New("cannot remove the primary worktree")
				return m, nil
			}
			m.action, m.actionTarget, m.err = actionRemoveWorktree, selected, nil
		}
	case "o":
		if selected, ok := m.selected(); ok {
			pr := m.gitItem(selected)
			m.err = nil
			return m, func() tea.Msg {
				return worktreeActionMsg{err: openPullRequest(selected.cwd, pr.prNumber)}
			}
		}
	case "d":
		if selected, ok := m.selected(); ok {
			m.diff, m.loading = true, true
			m.diffRequest++
			return m, loadDiff(selected, m.diffRequest)
		}
	case "enter":
		if selected, ok := m.selected(); ok {
			m.selection, m.chosen = selected, true
			return m, tea.Quit
		}
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "ctrl+u":
		m.previewOffset = m.clampOffset(m.previewOffset-m.previewHeight()/2, m.preview.lines)
	case "ctrl+d":
		m.previewOffset = m.clampOffset(m.previewOffset+m.previewHeight()/2, m.preview.lines)
	case "h", "left":
		m.xOffset = max(0, m.xOffset-4)
	case "l", "right":
		m.xOffset = m.clampX(m.xOffset + 4)
	default:
		return m, nil
	}
	if key == "j" || key == "down" || key == "k" || key == "up" {
		if selected, ok := m.selected(); ok {
			return m, m.requestPreview(selected)
		}
	}
	return m, nil
}

func (m dashboardModel) handleActionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch m.action {
	case actionAddWorktree:
		switch key {
		case "esc":
			m.action, m.actionInput = actionNone, ""
		case "enter":
			branch := strings.TrimSpace(m.actionInput)
			if branch == "" {
				return m, nil
			}
			m.action, m.err = actionRunning, nil
			return m, func() tea.Msg {
				return worktreeActionMsg{action: actionAddWorktree, err: addWorktree(m.cwd, branch, m.worktreeBackend)}
			}
		case "backspace", "ctrl+h":
			runes := []rune(m.actionInput)
			if len(runes) > 0 {
				m.actionInput = string(runes[:len(runes)-1])
			}
		default:
			if len(msg.Runes) > 0 {
				m.actionInput += string(msg.Runes)
			}
		}
	case actionRemoveWorktree:
		switch key {
		case "y":
			m.action, m.err = actionRunning, nil
			return m, func() tea.Msg {
				return worktreeActionMsg{action: actionRemoveWorktree, err: removeWorktree(m.cwd, m.actionTarget.cwd, m.worktreeBackend)}
			}
		case "n", "esc":
			m.action, m.actionTarget = actionNone, item{}
		}
	}
	return m, nil
}

func (m dashboardModel) switchTab(tab int) (tea.Model, tea.Cmd) {
	if tab == m.tab {
		return m, nil
	}
	m.queries[m.tab] = m.query
	m.tab, m.index, m.previewOffset, m.xOffset = tab, 0, 0, 0
	m.query = m.queries[m.tab]
	m.preview = previewData{}
	if selected, ok := m.selected(); ok {
		return m, m.requestPreview(selected)
	}
	m.loading = false
	return m, nil
}

func (m dashboardModel) clampOffset(offset int, lines []string) int {
	return max(0, min(offset, max(0, len(lines)-max(1, m.previewHeight()-2))))
}

func (m dashboardModel) clampX(offset int) int {
	longest := 0
	for _, lines := range [][]string{m.preview.lines, m.preview.rightLines} {
		for _, line := range lines {
			longest = max(longest, ansi.StringWidth(line))
		}
	}
	viewport := max(1, m.width-2)
	if m.diff {
		rightWidth := max(24, m.width/4)
		if rightWidth >= m.width-20 {
			rightWidth = 0
		}
		viewport = max(1, m.width-rightWidth-2)
	} else if m.preview.rightTitle != "" && m.width >= 60 {
		viewport = max(1, min(40, m.width/2)-2)
	}
	return max(0, min(offset, max(0, longest-viewport+1)))
}

func (m *dashboardModel) move(delta int) {
	rows := m.rows()
	if len(rows) == 0 {
		m.index = 0
		return
	}
	m.index = max(0, min(len(rows)-1, m.index+delta))
	m.previewOffset, m.xOffset = 0, 0
}

func (m dashboardModel) rows() []item {
	var rows []item
	if m.tab == 0 {
		rows = m.agents
	} else {
		rows = m.worktrees
	}
	if m.query == "" {
		return rows
	}
	query := strings.ToLower(m.query)
	filtered := make([]item, 0, len(rows))
	for _, item := range rows {
		if strings.Contains(strings.ToLower(item.title+" "+item.cwd+" "+item.branch+" "+item.sessionTitle), query) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func (m dashboardModel) selected() (item, bool) {
	rows := m.rows()
	if m.index < 0 || m.index >= len(rows) {
		return item{}, false
	}
	return rows[m.index], true
}

func (m dashboardModel) selectedTarget() string {
	if item, ok := m.selected(); ok {
		return item.target
	}
	return ""
}

func (m *dashboardModel) restoreSelection(target string) {
	rows := m.rows()
	m.index = 0
	for index, item := range rows {
		if item.target == target {
			m.index = index
			return
		}
	}
}

func (m *dashboardModel) applyAgentScope() {
	m.agents = append(m.agents[:0], m.allAgents...)
	if m.scope == scopeSession {
		m.agents = slices.DeleteFunc(m.agents, func(item item) bool {
			return m.launchSession == "" || item.muxSessionName != m.launchSession
		})
	}
}

func mergeWorktreeData(current, incoming []item, stage worktreeStage) []item {
	if stage == worktreeListStage {
		existing := make(map[string]item, len(current))
		for _, item := range current {
			existing[item.target] = item
		}
		result := make([]item, 0, len(incoming))
		for _, fresh := range incoming {
			if old, ok := existing[fresh.target]; ok {
				branchChanged := old.branch != fresh.branch
				old.kind, old.cwd, old.branch, old.title = fresh.kind, fresh.cwd, fresh.branch, fresh.title
				old.current = old.current || fresh.current
				if branchChanged {
					old.prNumber, old.prState, old.prDraft, old.prCheck, old.prLoaded = 0, "", false, "", false
					old.dirty, old.gitLoaded = false, false
					old.baseBranch = ""
					old.committedAdded, old.committedRemoved = 0, 0
					old.added, old.removed, old.untracked = 0, 0, 0
					old.ahead, old.behind = 0, 0
					old.hasConflict, old.isRebasing = false, false
				}
				fresh = old
			}
			result = append(result, fresh)
		}
		return result
	}

	details := make(map[string]item, len(incoming))
	for _, item := range incoming {
		details[item.target] = item
	}
	for index := range current {
		detail, ok := details[current[index].target]
		if !ok || (detail.branch != "" && detail.branch != current[index].branch) {
			continue
		}
		switch stage {
		case worktreeGitStage:
			current[index].dirty = detail.dirty
			current[index].gitLoaded = detail.gitLoaded
			current[index].baseBranch = detail.baseBranch
			current[index].committedAdded = detail.committedAdded
			current[index].committedRemoved = detail.committedRemoved
			current[index].added = detail.added
			current[index].removed = detail.removed
			current[index].untracked = detail.untracked
			current[index].ahead = detail.ahead
			current[index].behind = detail.behind
			current[index].hasConflict = detail.hasConflict
			current[index].isRebasing = detail.isRebasing
		case worktreePRStage:
			if detail.prLoaded {
				copyPRStatus(&current[index], detail)
			}
		case worktreeMuxStage:
			current[index].current = detail.current
			if current[index].sessionTitle == "" {
				current[index].pane = detail.pane
				current[index].muxSessionID = detail.muxSessionID
				current[index].muxSessionName = detail.muxSessionName
				current[index].muxWindowID = detail.muxWindowID
				current[index].muxWindowName = detail.muxWindowName
			}
		}
	}
	return current
}

func copyPRStatus(dst *item, src item) {
	dst.prNumber, dst.prState, dst.prDraft, dst.prCheck, dst.prLoaded = src.prNumber, src.prState, src.prDraft, src.prCheck, src.prLoaded
}

func (m dashboardModel) contentHeight() int { return max(0, m.height-3) }
func (m dashboardModel) tableHeight() int {
	return max(2, m.contentHeight()*(100-m.previewSize)/100)
}
func (m dashboardModel) previewHeight() int { return max(3, m.contentHeight()-m.tableHeight()) }

func (m *dashboardModel) resizePreview(delta int) {
	m.previewSize = max(minPreviewSize, min(maxPreviewSize, m.previewSize+delta))
	m.err = savePreviewSize(m.previewSize)
	m.previewOffset = m.clampOffset(m.previewOffset, m.preview.lines)
	m.diffOff = m.clampOffset(m.diffOff, m.preview.lines)
}

func (m dashboardModel) visibleRowStart() int {
	visible := max(0, m.tableHeight()-1)
	if visible > 0 && m.index >= visible {
		return m.index - visible + 1
	}
	return 0
}

func (m dashboardModel) View() string {
	width, height := max(1, m.width), max(1, m.height)
	if width < 40 || height < 10 {
		message := fmt.Sprintf("jumpmux needs at least 40×10 (current %d×%d)", width, height)
		lines := []string{ansi.Truncate(message, width, "…")}
		for len(lines) < height {
			lines = append(lines, "")
		}
		for index := range lines {
			lines[index] = padANSI(lines[index], width)
		}
		return strings.Join(lines, "\n")
	}
	if m.help {
		return m.renderHelp(width)
	}
	if m.diff {
		return m.renderDiff(width)
	}
	return strings.Join([]string{
		m.renderHeader(width),
		m.renderTable(width),
		m.renderPreview(width),
		m.renderFooter(width),
	}, "\n")
}

func (m dashboardModel) renderHeader(width int) string {
	line := "  " + tabView("Agents", m.tab == 0) + borderStyle.Render(" │ ") + tabView("Worktrees", m.tab == 1)
	return padANSI(line, width) + "\n" + borderStyle.Render(strings.Repeat("─", width))
}

func tabView(name string, active bool) string {
	if active {
		return headerStyle.Render(name)
	}
	return mutedStyle.Render(name)
}

func (m dashboardModel) renderTable(width int) string {
	rows := m.rows()
	height := m.tableHeight()
	start := m.visibleRowStart()
	columns := m.columns(width, rows)

	lines := []string{m.tableHeader(width, columns)}
	for index := start; index < len(rows) && len(lines) < height; index++ {
		lines = append(lines, m.tableRow(rows[index], index, width, columns))
	}
	if len(rows) == 0 && len(lines) < height {
		label := "live agents"
		loaded := m.agentsLoaded
		if m.tab == 1 {
			label, loaded = "worktrees", m.worktreesLoaded
		}
		message := "No " + label + "."
		if !loaded {
			message = "Loading " + label + "…"
		} else if m.query != "" {
			message = "No matches for /" + safeText(m.query)
		}
		lines = append(lines, mutedStyle.Render("  "+message))
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

type tableColumns struct {
	project, worktree, git, pr, status, elapsed, tail int
}

func (m dashboardModel) columns(width int, rows []item) tableColumns {
	project, worktree, git, pr := 8, 9, 5, 4
	for _, item := range rows {
		gitItem := m.gitItem(item)
		project = max(project, ansi.StringWidth(projectName(item.cwd))+2)
		worktree = max(worktree, ansi.StringWidth(m.displayWorktree(item))+1)
		git = max(git, ansi.StringWidth(gitStatusText(gitItem, m.now))+1)
		pr = max(pr, ansi.StringWidth(prText(gitItem, m.now)))
	}
	project = min(project, 22)
	worktree = min(worktree, 26)
	git = min(git, 30)
	pr = min(pr, 20) + 1
	minimumFullWidth := 4 + 22 + pr + 4 + 5 + 8
	if m.tab == 0 {
		minimumFullWidth = 4 + 22 + pr + 7 + 10 + 8
	}
	if width < max(60, minimumFullWidth) {
		worktree = min(worktree, 12)
		git = min(git, 12)
		if m.tab == 0 {
			return tableColumns{worktree: worktree, git: git, status: 7, tail: max(1, width-4-worktree-git-7)}
		}
		return tableColumns{worktree: worktree, git: git, status: 4, tail: max(1, width-4-worktree-git-4)}
	}
	reserve := 4 + pr + 4 + 5 + 8
	if m.tab == 0 {
		reserve = 4 + pr + 7 + 10 + 8
	}
	budget := max(22, width-reserve)
	for project+worktree+git > budget {
		switch {
		case project >= worktree && project >= git && project > 8:
			project--
		case worktree >= git && worktree > 9:
			worktree--
		case git > 5:
			git--
		default:
			break
		}
	}
	fixed := 4 + project + worktree + git + pr
	if m.tab == 0 {
		fixed += 7 + 10
		return tableColumns{project: project, worktree: worktree, git: git, pr: pr, status: 7, elapsed: 10, tail: max(1, width-fixed)}
	}
	fixed += 4 + 5
	return tableColumns{project: project, worktree: worktree, git: git, pr: pr, status: 4, elapsed: 5, tail: max(1, width-fixed)}
}

func (m dashboardModel) tableHeader(width int, c tableColumns) string {
	cells := []string{
		cell("#", 2),
		cell("Project", c.project),
		cell("Worktree", c.worktree),
		cell("Git", c.git),
		cell("PR", c.pr),
	}
	if m.tab == 0 {
		cells = append(cells, cell("Status", c.status), cell("Time", c.elapsed), cell("Title", c.tail))
	} else {
		cells = append(cells, cell("Mux", c.status), cell("Age", c.elapsed), cell("Agent", c.tail))
	}
	return headerStyle.Render("  " + ansi.Truncate(strings.Join(cells, ""), width-2, ""))
}

func (m dashboardModel) tableRow(item item, index, width int, c tableColumns) string {
	gitItem := m.gitItem(item)
	isCurrent := item.current || (m.tab == 0 && gitItem.current)
	var background *lipgloss.AdaptiveColor
	if index == m.index {
		background = &selectedColor
	} else if isCurrent {
		background = &currentRowColor
	}
	worktreeStyle := textStyle
	if isCurrent {
		worktreeStyle = currentWorktreeStyle
	}
	prefix := renderCell(textStyle, "  ", 2, background)
	if index == m.index {
		prefix = renderCell(infoStyle, "▌ ", 2, background)
	} else if isCurrent {
		prefix = renderCell(currentWorktreeStyle, "▏ ", 2, background)
	}

	jumpKey := ""
	if index < 9 {
		jumpKey = strconv.Itoa(index + 1)
	}
	line := prefix + renderCell(keycapStyle, jumpKey, 2, background) + renderCell(textStyle, projectName(item.cwd), c.project, background) + renderCell(worktreeStyle, m.displayWorktree(item), c.worktree, background) + gitStatusCell(gitItem, c.git, m.now, background) + prCell(gitItem, c.pr, m.now, background)
	if m.tab == 0 {
		line += statusCell(item, c.status, m.now, background) + elapsedCell(item.updated, c.elapsed, m.now, background) + renderCell(textStyle, item.title, c.tail, background)
	} else {
		age := "-"
		if !item.updated.IsZero() {
			age = relativeAge(item.updated)
		}
		line += muxCell(item, c.status, background) + renderCell(mutedStyle, age, c.elapsed, background) + agentSummaryCell(item, c.tail, m.now, background)
	}
	return padANSIBackground(line, width, background)
}

func (m dashboardModel) displayWorktree(item item) string {
	if item.kind == "worktree" {
		return item.branch
	}
	if wt, ok := m.matchingWorktree(item); ok {
		return wt.branch
	}
	return worktreeName(item.cwd)
}

func (m dashboardModel) gitItem(selected item) item {
	if selected.kind == "worktree" {
		return selected
	}
	worktree, hasWorktree := m.matchingWorktree(selected)
	if hasWorktree && worktree.gitLoaded {
		return worktree
	}
	if detail, ok := m.agentGit[selected.cwd]; ok {
		return detail
	}
	if hasWorktree {
		return worktree
	}
	return item{}
}

func (m dashboardModel) matchingWorktree(session item) (item, bool) {
	best := item{}
	for _, wt := range m.worktrees {
		if pathWithin(session.cwd, wt.cwd) && len(wt.cwd) > len(best.cwd) {
			best = wt
		}
	}
	return best, best.cwd != ""
}

func (m dashboardModel) renderPreview(width int) string {
	height := m.previewHeight()
	selected, hasSelection := m.selected()
	if !hasSelection {
		loaded := m.agentsLoaded
		if m.tab == 1 {
			loaded = m.worktreesLoaded
		}
		message := "No selection."
		if !loaded {
			message = "Loading…"
		}
		return renderPanel("Preview", []string{message}, width, height, 0, 0, false)
	}
	if m.loading || m.preview.target == "" || m.preview.target != selected.target {
		return renderPanel("Preview: "+m.displayWorktree(selected), []string{"Loading…"}, width, height, 0, 0, false)
	}
	if m.tab == 0 || m.preview.rightTitle == "" || width < 60 {
		return renderPanel(m.preview.title, m.preview.lines, width, height, m.previewOffset, m.xOffset, false)
	}
	leftWidth := min(40, width/2)
	left := renderPanel(m.preview.title, m.preview.lines, leftWidth, height, m.previewOffset, m.xOffset, false)
	right := renderPanel(m.preview.rightTitle, styleGitLog(m.preview.rightLines), width-leftWidth, height, m.previewOffset, m.xOffset, false)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m dashboardModel) renderFooter(width int) string {
	switch m.action {
	case actionAddWorktree:
		return padANSI("  "+keycapStyle.Render("a Add")+textStyle.Render(" branch: ")+keycapStyle.Render(safeText(m.actionInput))+keycapStyle.Render("_")+"  "+mutedStyle.Render("Enter")+textStyle.Render(" create  ")+mutedStyle.Render("Esc")+textStyle.Render(" cancel"), width)
	case actionRemoveWorktree:
		return padANSI("  "+warningStyle.Render("Remove "+safeText(m.displayWorktree(m.actionTarget))+"?")+"  "+mutedStyle.Render("y")+textStyle.Render(" confirm  ")+mutedStyle.Render("n/Esc")+textStyle.Render(" cancel"), width)
	case actionRunning:
		return padANSI("  "+infoStyle.Render("Working…"), width)
	}

	viewErr := m.agentErr
	if m.tab == 1 {
		viewErr = m.worktreeErr
	}
	if m.err != nil {
		viewErr = m.err
	}
	if viewErr != nil {
		return padANSI(dangerStyle.Render("  Error: "+safeText(viewErr.Error())), width)
	}
	if m.filter {
		return padANSI("  "+keycapStyle.Render("/"+safeText(m.query))+keycapStyle.Render("_")+"  "+mutedStyle.Render("Enter")+textStyle.Render(" accept  ")+mutedStyle.Render("Esc")+textStyle.Render(" clear"), width)
	}
	filterLabel := "Filter"
	if m.query != "" {
		filterLabel = "Filter:" + truncate(safeText(m.query), 12)
	}
	tab := "Worktrees"
	if m.tab == 1 {
		tab = "Agents"
	}
	if width < 60 {
		parts := []string{mutedStyle.Render("↵"), mutedStyle.Render("/"), mutedStyle.Render("Tab"), mutedStyle.Render("?"), mutedStyle.Render("q")}
		if m.tab == 1 {
			parts = []string{mutedStyle.Render("a"), mutedStyle.Render("r"), mutedStyle.Render("o"), mutedStyle.Render("↵"), mutedStyle.Render("Tab"), mutedStyle.Render("?"), mutedStyle.Render("q")}
		}
		return padANSI("  "+strings.Join(parts, borderStyle.Render(" │ ")), width)
	}

	var parts []string
	if m.tab == 0 {
		parts = []string{footerCommand("↵", "Open"), footerCommand("o", "PR"), footerCommand("/", filterLabel), footerCommand("Tab", tab), footerToggle("s", "Scope ("+m.scope.label()+")", m.scope == scopeSession)}
		if width >= 100 {
			parts = append([]string{footerCommand("d", "Diff")}, parts...)
			parts = append(parts, footerCommand("t", "Theme"))
		}
	} else {
		parts = []string{footerCommand("a", "Add"), footerCommand("r", "Remove"), footerCommand("o", "PR"), footerCommand("↵", "Open"), footerCommand("/", filterLabel), footerCommand("Tab", tab)}
		if width >= 100 {
			parts = append(parts, footerCommand("t", "Theme"))
		}
	}
	parts = append(parts, footerCommand("?", "Help"), footerCommand("q", "Quit"))
	return padANSI("  "+strings.Join(parts, borderStyle.Render(" │ ")), width)
}

func footerCommand(key, label string) string {
	return mutedStyle.Render(key) + " " + headerStyle.Render(label)
}

func footerToggle(key, label string, active bool) string {
	if active {
		return mutedStyle.Render(key) + " " + infoStyle.Render(label)
	}
	return footerCommand(key, label)
}

func (m dashboardModel) renderHelp(width int) string {
	lines := []string{
		"↑/k, ↓/j     Move selection",
		"Enter, 1–9   Open selected row",
		"Click         Select row",
		"Double-click  Open row",
		"Mouse wheel   Scroll table or preview",
		"Tab           Switch tabs",
		"/             Filter active tab",
		"Esc           Clear filter",
		"d             Open Git diff",
		"a             Add worktree (Worktrees)",
		"r             Remove worktree (Worktrees)",
		"o             Open pull request",
		"Ctrl+u/d      Page preview or diff",
		"h/l           Pan long lines",
		"+/-           Resize preview by 10%",
		"s             Toggle agent scope",
		"t             Cycle color scheme",
		"q             Quit",
	}
	body := renderPanel("Help", lines, width, m.height-1, 0, 0, false)
	return body + "\n" + padANSI("  "+footerCommand("? / q", "Close help"), width)
}

func (m dashboardModel) renderDiff(width int) string {
	height := max(3, m.height-1)
	title := "WIP"
	lines := []string{"Loading diff…"}
	files := []string{}
	if !m.loading && m.preview.target != "" {
		title, lines, files = m.preview.title, m.preview.lines, m.preview.rightLines
	}
	rightWidth := max(24, width/4)
	if rightWidth >= width-20 {
		rightWidth = 0
	}
	leftWidth := width - rightWidth
	body := renderPanel(title, lines, leftWidth, height, m.diffOff, m.xOffset, true)
	if rightWidth > 0 {
		body = lipgloss.JoinHorizontal(lipgloss.Top, body, renderPanel(fmt.Sprintf("Files (%d)", len(files)), files, rightWidth, height, m.diffOff, m.xOffset, false))
	}
	footer := "  " + keycapStyle.Render("[j/k]") + textStyle.Render(" scroll  ") + keycapStyle.Render("[h/l]") + textStyle.Render(" pan  ") + keycapStyle.Render("[q]") + textStyle.Render(" close")
	return body + "\n" + padANSI(footer, width)
}

func renderPanel(title string, lines []string, width, height, offset, xOffset int, diff bool) string {
	width, height = max(4, width), max(3, height)
	innerWidth, innerHeight := width-2, height-2
	offset = min(max(0, offset), max(0, len(lines)-innerHeight))

	title = " " + safeText(title) + " "
	title = ansi.Truncate(title, max(0, innerWidth-1), "")
	topLeft := borderStyle.Render("┌─") + headerStyle.Render(title)
	top := topLeft + borderStyle.Render(strings.Repeat("─", max(0, width-ansi.StringWidth(topLeft)-1))+"┐")
	out := []string{top}
	for index := 0; index < innerHeight; index++ {
		line := ""
		lineIndex := offset + index
		if lineIndex < len(lines) {
			line = lines[lineIndex]
			if diff {
				line = colorDiff(safeLine(line))
			}
			line = clipLine(line, innerWidth, xOffset)
		}
		out = append(out, borderStyle.Render("│")+padANSI(line, innerWidth)+borderStyle.Render("│"))
	}
	out = append(out, borderStyle.Render("└"+strings.Repeat("─", innerWidth)+"┘"))
	return strings.Join(out, "\n")
}

func paneHistoryLines(output string) []string {
	metadata, capture, ok := strings.Cut(output, "\n")
	if !ok {
		return nil
	}
	fields := strings.Split(strings.TrimSuffix(metadata, "\r"), "\t")
	if len(fields) != 2 {
		return nil
	}
	cursor, cursorErr := strconv.Atoi(fields[0])
	height, heightErr := strconv.Atoi(fields[1])
	if cursorErr != nil || heightErr != nil || cursor < 0 || height < 1 {
		return nil
	}
	capture = strings.TrimSuffix(capture, "\n")
	lines := strings.Split(capture, "\n")
	visibleStart := max(0, len(lines)-height)
	prompt := visibleStart + cursor
	if prompt < visibleStart || prompt >= len(lines) {
		return nil
	}
	return lines[:max(visibleStart, prompt-1)]
}

func sanitizePaneLine(line string) string {
	var output strings.Builder
	hasStyle := false
	for index := 0; index < len(line); {
		if line[index] == '\x1b' {
			next, sgr := paneEscape(line, index)
			if sgr {
				output.WriteString(line[index:next])
				hasStyle = true
			}
			index = next
			continue
		}
		r, size := utf8.DecodeRuneInString(line[index:])
		if r == '\t' || unicode.IsControl(r) {
			output.WriteByte(' ')
		} else {
			output.WriteString(line[index : index+size])
		}
		index += size
	}
	if hasStyle {
		output.WriteString("\x1b[0m")
	}
	return output.String()
}

func paneEscape(line string, start int) (next int, sgr bool) {
	if start+1 >= len(line) {
		return len(line), false
	}
	switch line[start+1] {
	case '[':
		for index := start + 2; index < len(line); index++ {
			if line[index] < 0x40 || line[index] > 0x7e {
				continue
			}
			if line[index] != 'm' {
				return index + 1, false
			}
			for _, parameter := range line[start+2 : index] {
				if (parameter < '0' || parameter > '9') && parameter != ';' && parameter != ':' {
					return index + 1, false
				}
			}
			return index + 1, true
		}
		return len(line), false
	case ']':
		for index := start + 2; index < len(line); index++ {
			if line[index] == '\a' {
				return index + 1, false
			}
			if line[index] == '\x1b' && index+1 < len(line) && line[index+1] == '\\' {
				return index + 2, false
			}
		}
		return len(line), false
	case 'P', 'X', '^', '_':
		for index := start + 2; index+1 < len(line); index++ {
			if line[index] == '\x1b' && line[index+1] == '\\' {
				return index + 2, false
			}
		}
		return len(line), false
	default:
		next := start + 2
		if strings.ContainsRune("()#%*+-./", rune(line[start+1])) && next < len(line) {
			next++
		}
		return next, false
	}
}

func clipLine(value string, width, offset int) string {
	if width <= 0 {
		return ""
	}
	total := ansi.StringWidth(value)
	offset = min(max(0, offset), max(0, total-1))
	left := ""
	available := width
	if offset > 0 {
		left, available = mutedStyle.Render("‹"), max(0, available-1)
	}
	right := ""
	if total > offset+available {
		right, available = mutedStyle.Render("…"), max(0, available-1)
	}
	return left + ansi.Cut(value, offset, offset+available) + right
}

func styleGitLog(lines []string) []string {
	styled := make([]string, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) == 3 {
			styled = append(styled, accentStyle.Render(parts[0])+"  "+mutedStyle.Render(parts[1])+"  "+textStyle.Render(safeText(parts[2])))
		} else {
			styled = append(styled, textStyle.Render(safeText(line)))
		}
	}
	return styled
}

func cell(value string, width int) string {
	return padANSI(ansi.Truncate(safeText(value), max(0, width-1), ""), width)
}

func padANSI(value string, width int) string {
	return padANSIBackground(value, width, nil)
}

func withBackground(style lipgloss.Style, background *lipgloss.AdaptiveColor) lipgloss.Style {
	if background != nil {
		return style.Background(*background)
	}
	return style
}

func renderCell(style lipgloss.Style, value string, width int, background *lipgloss.AdaptiveColor) string {
	return withBackground(style, background).Render(cell(value, width))
}

func padANSIBackground(value string, width int, background *lipgloss.AdaptiveColor) string {
	value = ansi.Truncate(value, max(0, width), "")
	padding := strings.Repeat(" ", max(0, width-ansi.StringWidth(value)))
	if padding != "" && background != nil {
		padding = withBackground(lipgloss.NewStyle(), background).Render(padding)
	}
	return value + padding
}

func projectName(path string) string {
	path = filepath.Clean(path)
	parent := filepath.Base(filepath.Dir(path))
	if strings.HasSuffix(parent, "__worktrees") {
		return strings.TrimSuffix(parent, "__worktrees")
	}
	return filepath.Base(path)
}

func worktreeName(path string) string { return filepath.Base(filepath.Clean(path)) }

type prStatusSpan struct {
	text  string
	style lipgloss.Style
}

func prStatusSpans(item item, now time.Time) []prStatusSpan {
	if item.prNumber == 0 {
		return []prStatusSpan{{text: "-", style: mutedStyle}}
	}
	icon, style := prOpenIcon, successStyle
	if item.prDraft {
		icon, style = prDraftIcon, mutedStyle
	} else {
		switch item.prState {
		case "MERGED":
			icon, style = prMergedIcon, accentStyle
		case "CLOSED":
			icon, style = prClosedIcon, dangerStyle
		}
	}
	spans := []prStatusSpan{{text: fmt.Sprintf("#%d", item.prNumber), style: style}, {text: icon, style: style}}
	switch item.prCheck {
	case checkSuccess:
		spans = append(spans, prStatusSpan{text: checkSuccessIcon, style: successStyle})
	case checkFailure:
		spans = append(spans, prStatusSpan{text: checkFailureIcon, style: dangerStyle})
	case checkPending:
		spans = append(spans, prStatusSpan{text: spinnerFrame(now), style: accentStyle})
	}
	return spans
}

func prText(item item, now time.Time) string {
	spans := prStatusSpans(item, now)
	parts := make([]string, len(spans))
	for index, span := range spans {
		parts[index] = span.text
	}
	return strings.Join(parts, " ")
}

func prCell(item item, width int, now time.Time, background *lipgloss.AdaptiveColor) string {
	spans := prStatusSpans(item, now)
	parts := make([]string, len(spans))
	for index, span := range spans {
		parts[index] = withBackground(span.style, background).Render(span.text)
	}
	return padANSIBackground(strings.Join(parts, withBackground(textStyle, background).Render(" ")), width, background)
}

type gitStatusSpan struct {
	text  string
	style lipgloss.Style
}

func gitStatusSpans(item item, now time.Time) []gitStatusSpan {
	if !item.gitLoaded {
		return []gitStatusSpan{{text: spinnerFrame(now), style: mutedStyle}}
	}
	var spans []gitStatusSpan
	add := func(text string, style lipgloss.Style) {
		spans = append(spans, gitStatusSpan{text: text, style: style})
	}
	if item.isRebasing {
		add(gitRebaseIcon, warningStyle)
	}
	if item.baseBranch != "" && item.baseBranch != "main" && item.baseBranch != "master" {
		add("→"+item.baseBranch, mutedStyle)
	}

	hasUncommitted := item.dirty || item.added > 0 || item.removed > 0
	allUncommitted := item.added == item.committedAdded && item.removed == item.committedRemoved
	addUncommitted := func() {
		add(gitDiffIcon, accentStyle)
		if item.added > 0 {
			add(fmt.Sprintf("+%d", item.added), successStyle)
		}
		if item.removed > 0 {
			add(fmt.Sprintf("-%d", item.removed), dangerStyle)
		}
	}
	if hasUncommitted && allUncommitted {
		addUncommitted()
	} else {
		if item.committedAdded > 0 {
			add(fmt.Sprintf("+%d", item.committedAdded), successStyle.Faint(true))
		}
		if item.committedRemoved > 0 {
			add(fmt.Sprintf("-%d", item.committedRemoved), dangerStyle.Faint(true))
		}
		if hasUncommitted {
			addUncommitted()
		}
	}
	if item.hasConflict {
		add(gitConflictIcon, dangerStyle)
	}
	if item.ahead > 0 {
		add(fmt.Sprintf("↑%d", item.ahead), infoStyle)
	}
	if item.behind > 0 {
		add(fmt.Sprintf("↓%d", item.behind), warningStyle)
	}
	if len(spans) == 0 {
		add("-", mutedStyle)
	}
	return spans
}

func gitStatusText(item item, now time.Time) string {
	spans := gitStatusSpans(item, now)
	parts := make([]string, len(spans))
	for index, span := range spans {
		parts[index] = span.text
	}
	return strings.Join(parts, " ")
}

func gitStatusCell(item item, width int, now time.Time, background *lipgloss.AdaptiveColor) string {
	spans := gitStatusSpans(item, now)
	parts := make([]string, len(spans))
	for index, span := range spans {
		parts[index] = withBackground(span.style, background).Render(span.text)
	}
	return padANSIBackground(strings.Join(parts, withBackground(textStyle, background).Render(" ")), width, background)
}

func elapsedCell(value time.Time, width int, now time.Time, background *lipgloss.AdaptiveColor) string {
	if value.IsZero() {
		return renderCell(mutedStyle, "-", width, background)
	}
	base := elapsedStyle(now.Sub(value))
	inactive := base.Faint(true)
	parts := strings.Split(elapsedClock(value, now), ":")
	if len(parts) != 3 {
		return renderCell(inactive, strings.Join(parts, ":"), width, background)
	}
	hoursStyle, minutesStyle := base, base
	if parts[0] == "00" {
		hoursStyle = inactive
	}
	if parts[1] == "00" {
		minutesStyle = inactive
	}
	rendered := withBackground(hoursStyle, background).Render(parts[0]) +
		withBackground(inactive, background).Render(":") +
		withBackground(minutesStyle, background).Render(parts[1]) +
		withBackground(inactive, background).Render(":") +
		withBackground(base, background).Render(parts[2])
	return padANSIBackground(rendered, width, background)
}

func elapsedStyle(age time.Duration) lipgloss.Style {
	if age < 5*time.Minute {
		return successStyle
	}
	if age < time.Hour {
		return warningStyle
	}
	return accentStyle.Faint(true)
}

func gitStatusView(item item) string {
	spans := gitStatusSpans(item, time.Now())
	parts := make([]string, len(spans))
	for index, span := range spans {
		parts[index] = span.style.Render(span.text)
	}
	return strings.Join(parts, " ")
}

func muxCell(item item, width int, background *lipgloss.AdaptiveColor) string {
	if item.muxWindowID == "" {
		return renderCell(mutedStyle, "-", width, background)
	}
	return renderCell(infoStyle, "●", width, background)
}

func muxView(item item) string {
	if item.muxWindowID == "" {
		return mutedStyle.Render("- none")
	}
	return infoStyle.Render("● ") + textStyle.Render(safeText(item.muxSessionName+":"+item.muxWindowName))
}

func statusText(item item, now time.Time) string {
	icon := doneIcon
	if item.status == "working" {
		icon = workingIcon
	}
	if isStale(item, now) {
		return icon + " " + staleAgentIcon
	}
	if item.status == "working" {
		return icon + " " + spinnerFrame(now)
	}
	return icon
}

func statusCell(item item, width int, now time.Time, background *lipgloss.AdaptiveColor) string {
	style := successStyle
	if isStale(item, now) {
		style = mutedStyle
	} else if item.status == "working" {
		style = infoStyle
	}
	return renderCell(style, statusText(item, now), width, background)
}

func statusView(item item, now time.Time) string {
	style := successStyle
	if isStale(item, now) {
		style = mutedStyle
	} else if item.status == "working" {
		style = infoStyle
	}
	return style.Render(statusText(item, now))
}

func agentSummaryCell(item item, width int, now time.Time, background *lipgloss.AdaptiveColor) string {
	if item.sessionTitle == "" {
		return renderCell(mutedStyle, "-", width, background)
	}
	style := successStyle
	if isStale(item, now) {
		style = mutedStyle
	} else if item.status == "working" {
		style = infoStyle
	}
	value := withBackground(style, background).Render(statusText(item, now)) + withBackground(textStyle, background).Render(" "+truncate(safeText(item.sessionTitle), 24))
	return padANSIBackground(value, width, background)
}

func agentSummary(item item, now time.Time) string {
	if item.sessionTitle == "" {
		return mutedStyle.Render("-")
	}
	return statusView(item, now) + " " + textStyle.Render(truncate(safeText(item.sessionTitle), 24))
}

func isStale(item item, now time.Time) bool {
	return !item.updated.IsZero() && now.Sub(item.updated) > staleThreshold
}

func spinnerFrame(now time.Time) string {
	index := int(now.UnixNano()/int64(clockInterval)) % len(spinnerFrames)
	if index < 0 {
		index += len(spinnerFrames)
	}
	return spinnerFrames[index]
}

func elapsedClock(value, now time.Time) string {
	if value.IsZero() {
		return "-"
	}
	d := now.Sub(value)
	if d < 0 {
		d = 0
	}
	return fmt.Sprintf("%02d:%02d:%02d", int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60)
}

func colorDiff(line string) string {
	switch {
	case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
		return addedStyle.Render(line)
	case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
		return removedStyle.Render(line)
	case strings.HasPrefix(line, "diff "), strings.HasPrefix(line, "@@"), strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"), strings.HasPrefix(line, "Untracked"):
		return diffHeadStyle.Render(line)
	default:
		return textStyle.Render(line)
	}
}
