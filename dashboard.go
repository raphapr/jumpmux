package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
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
	cwd                                                       string
	now                                                       time.Time
	tab, index                                                int
	width, height                                             int
	agents, allAgents                                         []item
	worktrees                                                 []item
	agentGit, gitCache, prCache                               map[string]item
	scope                                                     scopeMode
	scheme                                                    colorScheme
	launchSession                                             string
	worktreeGeneration                                        uint64
	previewRequest, diffRequest                               uint64
	filter, diff, help                                        bool
	query                                                     string
	queries                                                   [2]string
	filterInputs                                              [2]textinput.Model
	actionTextInput                                           textinput.Model
	preview                                                   previewData
	previewOffset, diffOff, rightOffset, xOffset, previewSize int
	panelFocus, helpOffset                                    int
	loading                                                   bool
	agentsLoaded, worktreesLoaded                             bool
	agentsInFlight, agentGitInFlight, worktreesInFlight       bool
	agentGitRefreshed, previewRefreshed                       time.Time
	worktreePending                                           int
	err, agentErr, worktreeErr                                error
	chosen                                                    bool
	selection                                                 item
	lastClickTarget                                           string
	lastClickAt                                               time.Time
	action                                                    dashboardAction
	actionTarget                                              item
	actionBackend                                             worktreeBackend
}

type dashboardData struct {
	agents []item
	err    error
}

type dashboardDataMsg dashboardData

type agentGitMsg []item

type dashboardAction uint8

const (
	panelLeft = iota
	panelRight
)

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
	return dashboardModel{cwd: cwd, now: time.Now(), width: 80, height: 24, previewSize: defaultPreviewSize, scheme: schemeDefault, worktreeGeneration: 1, agentGit: map[string]item{}, gitCache: map[string]item{}, prCache: map[string]item{}, filterInputs: [2]textinput.Model{newTextInput("/"), newTextInput("/")}, actionTextInput: newTextInput(""), agentsInFlight: true, worktreesInFlight: true}
}

func newTextInput(prompt string) textinput.Model {
	input := textinput.New()
	input.Prompt = prompt
	input.Width = 40
	return input
}

func styleTextInput(input *textinput.Model) {
	input.PromptStyle = mutedStyle
	input.TextStyle = textStyle
	input.PlaceholderStyle = mutedStyle
	input.CompletionStyle = mutedStyle
	input.Cursor.Style = keycapStyle
}

func (m *dashboardModel) resizeInputs() {
	width := max(1, m.width-18)
	for index := range m.filterInputs {
		m.filterInputs[index].Width = width
	}
	m.actionTextInput.Width = width
}

func newDashboardForLaunch(cwd, launchSession string, forceSession bool) dashboardModel {
	model := newDashboard(cwd)
	model.gitCache = loadGitStatusCache()
	model.prCache = loadPRStatusCache()
	model.agentGit = maps.Clone(model.gitCache)
	for path, cached := range model.prCache {
		detail := model.agentGit[path]
		detail.kind, detail.target, detail.cwd = "worktree", path, path
		if cached.branch == "" && detail.branch != "" {
			cached.branch = detail.branch
			model.prCache[path] = cached
		}
		if detail.branch != "" && detail.branch == cached.branch {
			copyPRStatus(&detail, cached)
		}
		model.agentGit[path] = detail
	}
	model.scope = loadScopeMode()
	model.previewSize = loadPreviewSize()
	_, model.err = loadWorktreeBackend()
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

		status, statusErr := gitOutput(item.cwd, "status", "--short")
		log, logErr := gitOutput(item.cwd, "log", "--pretty=format:%h%x09%ar%x09%s", "-20")
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
		if statusErr != nil {
			preview.lines = append(preview.lines, gitErrorLine(statusErr))
		} else if strings.TrimSpace(status) == "" {
			preview.lines = append(preview.lines, mutedStyle.Render("clean"))
		} else {
			for _, line := range strings.Split(strings.TrimSpace(status), "\n") {
				preview.lines = append(preview.lines, safeLine(line))
			}
		}
		preview.rightTitle = "Git Log"
		if logErr != nil {
			preview.rightLines = []string{gitErrorLine(logErr)}
		} else if strings.TrimSpace(log) == "" {
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
			return diffMsg{request: request, target: item.target, title: "WIP", lines: []string{gitErrorLine(err)}}
		}
		unstaged, unstagedTruncated, unstagedErr := gitOutputLimited(item.cwd, maxDiffOutput, "diff", "--no-ext-diff", "--no-color")
		staged, stagedTruncated, stagedErr := gitOutputLimited(item.cwd, maxDiffOutput, "diff", "--no-ext-diff", "--no-color", "--cached")
		untracked, untrackedErr := gitOutput(item.cwd, "ls-files", "--others", "--exclude-standard")
		status, statusErr := gitOutput(item.cwd, "status", "--porcelain")
		branch, branchErr := gitOutput(item.cwd, "branch", "--show-current")
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
		for _, err := range []error{unstagedErr, stagedErr} {
			if err != nil {
				lines = append(lines, gitErrorLine(err))
			}
		}
		if untrackedErr != nil {
			lines = append(lines, gitErrorLine(untrackedErr))
		} else if strings.TrimSpace(untracked) != "" {
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
		if statusErr != nil {
			files = append(files, gitErrorLine(statusErr))
		} else {
			for _, line := range strings.Split(strings.TrimSpace(status), "\n") {
				if len(line) >= 4 {
					files = append(files, safeLine(line[:2]+" "+line[3:]))
				}
			}
		}
		title := "WIP: " + strings.TrimSpace(branch)
		if branchErr != nil || strings.TrimSpace(branch) == "" {
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
		m.previewOffset, m.rightOffset, m.xOffset, m.panelFocus, m.loading = 0, 0, 0, panelLeft, true
	}
	return tea.Tick(75*time.Millisecond, func(time.Time) tea.Msg {
		return previewRequestMsg{request: request, item: item, scheme: scheme}
	})
}

func (m dashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizeInputs()
		if !m.hasRightPanel() {
			m.panelFocus = panelLeft
		}
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
				cached := m.agentGit[detail.cwd]
				if cached.branch == detail.branch {
					copyPRStatus(&detail, cached)
				}
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
				wasAtBottom := m.previewOffset == m.clampPreviewOffset(len(m.preview.lines), m.preview.lines)
				m.preview = previewData(msg)
				if reset {
					m.previewOffset, m.xOffset = 0, 0
				}
				if msg.followBottom && (reset || wasAtBottom) {
					m.previewOffset = m.clampPreviewOffset(len(msg.lines), msg.lines)
				} else {
					m.previewOffset = m.clampPreviewOffset(m.previewOffset, msg.lines)
				}
				m.loading = false
			}
		}
		return m, nil
	case diffMsg:
		if m.diff && msg.request == m.diffRequest {
			if selected, ok := m.selected(); ok && selected.target == msg.target {
				m.preview = previewData{target: msg.target, title: msg.title, lines: msg.lines, rightTitle: fmt.Sprintf("Files (%d)", len(msg.files)), rightLines: msg.files}
				m.diffOff, m.rightOffset, m.xOffset, m.panelFocus, m.loading = 0, 0, 0, panelLeft, false
			}
		}
		return m, nil
	case worktreeActionMsg:
		m.action, m.actionTarget, m.actionBackend, m.err = actionNone, item{}, "", msg.err
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
	if m.filter {
		previous := m.filterInputs[m.tab].Value()
		var command tea.Cmd
		m.filterInputs[m.tab], command = m.filterInputs[m.tab].Update(msg)
		m.query, m.queries[m.tab] = m.filterInputs[m.tab].Value(), m.filterInputs[m.tab].Value()
		if m.query == previous {
			return m, command
		}
		m.index = 0
		if selected, ok := m.selected(); ok {
			return m, tea.Batch(command, m.requestPreview(selected))
		}
		m.preview, m.loading = previewData{}, false
		return m, command
	}
	if m.action == actionAddWorktree {
		var command tea.Cmd
		m.actionTextInput, command = m.actionTextInput.Update(msg)
		return m, command
	}
	return m, nil
}

func (m dashboardModel) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.width < 40 || m.height < 10 || msg.X < 0 || msg.X >= m.width {
		return m, nil
	}
	if m.help {
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			delta := 3
			if msg.Button == tea.MouseButtonWheelUp {
				delta = -delta
			}
			m.helpOffset = m.clampHelpOffset(m.helpOffset + delta)
		}
		return m, nil
	}
	if m.filter || m.action != actionNone {
		return m, nil
	}
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		delta := 3
		if msg.Button == tea.MouseButtonWheelUp {
			delta = -delta
		}
		if m.diff {
			m.focusPanelAt(msg.X)
			m.scrollFocusedPanel(delta)
		} else if msg.Y >= 2+m.tableHeight() {
			m.focusPanelAt(msg.X)
			m.scrollFocusedPanel(delta)
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
		for tab, hitbox := range m.tabHitboxes() {
			if msg.X >= hitbox[0] && msg.X < hitbox[1] {
				return m.switchTab(tab)
			}
		}
		return m, nil
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
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	if m.width < 40 || m.height < 10 {
		if key == "q" || key == "esc" {
			return m, tea.Quit
		}
		return m, nil
	}
	if m.help {
		switch key {
		case "q":
			return m, tea.Quit
		case "?", "esc":
			m.help, m.helpOffset = false, 0
		case "k", "up":
			m.helpOffset = m.clampHelpOffset(m.helpOffset - 1)
		case "j", "down":
			m.helpOffset = m.clampHelpOffset(m.helpOffset + 1)
		case "ctrl+u":
			m.helpOffset = m.clampHelpOffset(m.helpOffset - m.helpHeight()/2)
		case "ctrl+d":
			m.helpOffset = m.clampHelpOffset(m.helpOffset + m.helpHeight()/2)
		}
		return m, nil
	}
	if m.action != actionNone {
		return m.handleActionKey(msg)
	}
	if m.filter {
		if key == "enter" {
			m.filter = false
			m.filterInputs[m.tab].Blur()
		} else if key == "esc" {
			m.filter, m.query, m.index = false, "", 0
			m.filterInputs[m.tab].SetValue("")
			m.filterInputs[m.tab].Blur()
		} else {
			var command tea.Cmd
			m.filterInputs[m.tab], command = m.filterInputs[m.tab].Update(msg)
			m.query, m.index = m.filterInputs[m.tab].Value(), 0
			m.queries[m.tab] = m.query
			if selected, ok := m.selected(); ok {
				return m, tea.Batch(command, m.requestPreview(selected))
			}
			m.preview, m.loading = previewData{}, false
			return m, command
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
	if key == "shift+left" || key == "shift+right" {
		if m.hasRightPanel() {
			m.panelFocus = panelLeft
			if key == "shift+right" {
				m.panelFocus = panelRight
			}
		}
		return m, nil
	}
	if m.diff {
		switch key {
		case "esc", "q":
			m.diff, m.preview, m.panelFocus = false, previewData{}, panelLeft
			if selected, ok := m.selected(); ok {
				return m, m.requestPreview(selected)
			}
		case "k", "up":
			m.scrollFocusedPanel(-1)
		case "j", "down":
			m.scrollFocusedPanel(1)
		case "ctrl+u":
			m.scrollFocusedPanel(-m.diffVisibleHeight() / 2)
		case "ctrl+d":
			m.scrollFocusedPanel(m.diffVisibleHeight() / 2)
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
	case "q":
		return m, tea.Quit
	case "esc":
		if m.query != "" {
			m.query, m.queries[m.tab], m.index = "", "", 0
			m.filterInputs[m.tab].SetValue("")
			if selected, ok := m.selected(); ok {
				return m, m.requestPreview(selected)
			}
			return m, nil
		}
		return m, tea.Quit
	case "?":
		m.help, m.helpOffset = true, 0
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
		m.filterInputs[m.tab].SetValue(m.query)
		return m, m.filterInputs[m.tab].Focus()
	case "a":
		if m.tab == 1 {
			m.action, m.err = actionAddWorktree, nil
			m.actionTextInput.SetValue("")
			m.lastClickTarget, m.lastClickAt = "", time.Time{}
			return m, m.actionTextInput.Focus()
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
			backend, err := loadWorktreeBackend()
			if err != nil {
				m.err = err
				return m, nil
			}
			backend, err = resolvedWorktreeBackend(backend)
			if err != nil {
				m.err = err
				return m, nil
			}
			m.action, m.actionTarget, m.actionBackend, m.err = actionRemoveWorktree, selected, backend, nil
			m.lastClickTarget, m.lastClickAt = "", time.Time{}
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
			m.diff, m.loading, m.panelFocus = true, true, panelLeft
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
		m.scrollFocusedPanel(-m.previewVisibleHeight() / 2)
	case "ctrl+d":
		m.scrollFocusedPanel(m.previewVisibleHeight() / 2)
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
			m.action = actionNone
			m.actionTextInput.SetValue("")
			m.actionTextInput.Blur()
		case "enter":
			branch := strings.TrimSpace(m.actionTextInput.Value())
			if branch == "" {
				return m, nil
			}
			m.action, m.err = actionRunning, nil
			m.actionTextInput.Blur()
			return m, func() tea.Msg {
				return worktreeActionMsg{action: actionAddWorktree, err: addWorktree(m.cwd, branch, backendAuto)}
			}
		default:
			var command tea.Cmd
			m.actionTextInput, command = m.actionTextInput.Update(msg)
			return m, command
		}
	case actionRemoveWorktree:
		switch key {
		case "y", "Y":
			m.action, m.err = actionRunning, nil
			return m, func() tea.Msg {
				return worktreeActionMsg{action: actionRemoveWorktree, err: removeWorktree(m.cwd, m.actionTarget.cwd, m.actionBackend)}
			}
		case "n", "N", "esc":
			m.action, m.actionTarget, m.actionBackend = actionNone, item{}, ""
		}
	}
	return m, nil
}

func (m dashboardModel) switchTab(tab int) (tea.Model, tea.Cmd) {
	if tab == m.tab {
		return m, nil
	}
	m.queries[m.tab] = m.query
	m.tab, m.index, m.previewOffset, m.rightOffset, m.xOffset, m.panelFocus = tab, 0, 0, 0, 0, panelLeft
	m.query = m.queries[m.tab]
	m.filterInputs[m.tab].SetValue(m.query)
	m.preview = previewData{}
	if selected, ok := m.selected(); ok {
		return m, m.requestPreview(selected)
	}
	m.loading = false
	return m, nil
}

func clampLineOffset(offset int, lines []string, visible int) int {
	return max(0, min(offset, max(0, len(lines)-max(1, visible))))
}

func (m dashboardModel) previewVisibleHeight() int { return max(1, m.previewHeight()-2) }
func (m dashboardModel) diffVisibleHeight() int    { return max(1, m.height-3) }
func (m dashboardModel) clampPreviewOffset(offset int, lines []string) int {
	return clampLineOffset(offset, lines, m.previewVisibleHeight())
}
func (m dashboardModel) clampDiffOffset(offset int, lines []string) int {
	return clampLineOffset(offset, lines, m.diffVisibleHeight())
}

func (m dashboardModel) hasRightPanel() bool {
	if m.diff {
		rightWidth := max(24, m.width/4)
		return rightWidth < m.width-20
	}
	return m.tab == 1 && m.preview.rightTitle != "" && m.width >= 60
}

func (m *dashboardModel) focusPanelAt(x int) {
	m.panelFocus = panelLeft
	if m.hasRightPanel() {
		leftWidth := m.width
		if m.diff {
			leftWidth -= max(24, m.width/4)
		} else {
			leftWidth = min(40, m.width/2)
		}
		if x >= leftWidth {
			m.panelFocus = panelRight
		}
	}
}

func (m *dashboardModel) scrollFocusedPanel(delta int) {
	if m.panelFocus == panelRight && m.hasRightPanel() {
		if m.diff {
			m.rightOffset = m.clampDiffOffset(m.rightOffset+delta, m.preview.rightLines)
		} else {
			m.rightOffset = m.clampPreviewOffset(m.rightOffset+delta, m.preview.rightLines)
		}
		return
	}
	if m.diff {
		m.diffOff = m.clampDiffOffset(m.diffOff+delta, m.preview.lines)
		return
	}
	m.previewOffset = m.clampPreviewOffset(m.previewOffset+delta, m.preview.lines)
}

func (m dashboardModel) helpHeight() int { return max(1, m.height-3) }

func (m dashboardModel) clampHelpOffset(offset int) int {
	return max(0, min(offset, max(0, len(m.helpLines())-m.helpHeight())))
}

func (m dashboardModel) helpLines() []string {
	return []string{
		"↑/k, ↓/j     Move selection",
		"Enter, 1–9   Open selected row",
		"Click         Select row",
		"Double-click  Open row",
		"Mouse wheel   Scroll table or preview",
		"Tab           Switch tabs",
		"/             Filter active tab",
		"Enter / Esc   Accept / clear filter",
		"d             Open Git diff",
		"Shift+←/→     Focus split panel",
		"a             Add worktree (Worktrees)",
		"r             Remove worktree (Worktrees)",
		"o             Open pull request",
		"Ctrl+u/d      Page preview, diff, or help",
		"h/l           Pan long lines",
		"+/-           Resize preview by 10%",
		"s             Toggle agent scope",
		"t             Cycle color scheme",
	}
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
	m.previewOffset, m.rightOffset, m.xOffset, m.panelFocus = 0, 0, 0, panelLeft
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
		if !ok || (stage == worktreePRStage && detail.branch != current[index].branch) || (stage != worktreePRStage && detail.branch != "" && detail.branch != current[index].branch) {
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
	content := m.contentHeight()
	return max(2, min(content-3, content*(100-m.previewSize)/100))
}
func (m dashboardModel) previewHeight() int { return m.contentHeight() - m.tableHeight() }

func (m *dashboardModel) resizePreview(delta int) {
	m.previewSize = max(minPreviewSize, min(maxPreviewSize, m.previewSize+delta))
	m.err = savePreviewSize(m.previewSize)
	m.previewOffset = m.clampPreviewOffset(m.previewOffset, m.preview.lines)
	m.diffOff = m.clampDiffOffset(m.diffOff, m.preview.lines)
	if m.diff {
		m.rightOffset = m.clampDiffOffset(m.rightOffset, m.preview.rightLines)
	} else {
		m.rightOffset = m.clampPreviewOffset(m.rightOffset, m.preview.rightLines)
	}
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
	labels := m.tabLabels()
	line := "  " + tabView(labels[0], m.tab == 0) + borderStyle.Render(" │ ") + tabView(labels[1], m.tab == 1)
	return padANSI(line, width) + "\n" + borderStyle.Render(strings.Repeat("─", width))
}

func (m dashboardModel) tabLabels() [2]string {
	agents := fmt.Sprintf("Agents %d", len(m.agents))
	if m.tab == 0 {
		agents = fmt.Sprintf("[Agents %d · %s]", len(m.agents), m.scope.label())
	}
	worktrees := fmt.Sprintf("Worktrees %d", len(m.worktrees))
	if m.tab == 1 {
		worktrees = fmt.Sprintf("[Worktrees %d]", len(m.worktrees))
	}
	return [2]string{agents, worktrees}
}

func (m dashboardModel) tabHitboxes() [2][2]int {
	labels := m.tabLabels()
	start := 2
	firstEnd := start + ansi.StringWidth(labels[0])
	secondStart := firstEnd + ansi.StringWidth(" │ ")
	return [2][2]int{{start, firstEnd}, {secondStart, secondStart + ansi.StringWidth(labels[1])}}
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
	if m.action == actionRemoveWorktree {
		return renderPanel("Remove worktree", m.removePreviewLines(), width, height, 0, 0, false, true)
	}
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
		return renderPanel("Preview", []string{message}, width, height, 0, 0, false, true)
	}
	if m.loading || m.preview.target == "" || m.preview.target != selected.target {
		return renderPanel("Preview: "+m.displayWorktree(selected), []string{"Loading…"}, width, height, 0, 0, false, true)
	}
	if m.tab == 0 || m.preview.rightTitle == "" || width < 60 {
		return renderPanel(m.preview.title, m.preview.lines, width, height, m.previewOffset, m.xOffset, false, true)
	}
	leftWidth := min(40, width/2)
	left := renderPanel(m.preview.title, m.preview.lines, leftWidth, height, m.previewOffset, m.xOffset, false, m.panelFocus == panelLeft)
	right := renderPanel(m.preview.rightTitle, styleGitLog(m.preview.rightLines), width-leftWidth, height, m.rightOffset, m.xOffset, false, m.panelFocus == panelRight)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m dashboardModel) removePreviewLines() []string {
	behavior := "Native Git removes the worktree and keeps its branch."
	if m.actionBackend == backendWT {
		behavior = "Worktrunk removal follows wt remove semantics."
	}
	return []string{
		"Branch: " + safeText(m.actionTarget.branch),
		"Path:   " + safeText(compactHome(m.actionTarget.cwd)),
		"",
		behavior,
		"",
		"y Remove    n or Esc Cancel",
	}
}

func (m dashboardModel) renderFooter(width int) string {
	switch m.action {
	case actionAddWorktree:
		input := m.actionTextInput
		styleTextInput(&input)
		prefix := "  " + textStyle.Render("branch: ")
		create := footerCommand("Enter", "Create")
		if width < 60 {
			create = footerCommand("↵", "Create")
		}
		suffix := "  " + create + " " + footerCommand("Esc", "Cancel")
		input.Width = max(1, width-ansi.StringWidth(prefix)-ansi.StringWidth(suffix)-1)
		input.SetCursor(input.Position())
		return padANSI(prefix+input.View()+suffix, width)
	case actionRemoveWorktree:
		return padANSI("  "+warningStyle.Render("Remove "+safeText(m.displayWorktree(m.actionTarget))+"?")+"  "+footerCommand("y", "Remove")+" "+footerCommand("n/Esc", "Cancel"), width)
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
		return m.renderFooterError(width, viewErr)
	}
	if m.filter {
		input := m.filterInputs[m.tab]
		styleTextInput(&input)
		input.Width = max(1, width-30)
		input.SetCursor(input.Position())
		return padANSI("  "+input.View()+"  "+footerCommand("Enter", "Accept")+" "+footerCommand("Esc", "Clear"), width)
	}
	filterLabel := "Filter"
	if m.query != "" {
		filterLabel = "Filter:" + truncate(safeText(m.query), 12)
	}
	tab := "Worktrees"
	if m.tab == 1 {
		tab = "Agents"
	}
	candidates := []string{}
	if m.tab == 1 {
		candidates = append(candidates, footerCommand("a", "Add"), footerCommand("r", "Remove"))
	}
	candidates = append(candidates, footerCommand("↵", "Open"), footerCommand("d", "Diff"))
	if selected, ok := m.selected(); ok && m.gitItem(selected).prNumber != 0 {
		candidates = append(candidates, footerCommand("o", "PR"))
	}
	if m.tab == 0 {
		candidates = append(candidates, footerToggle("s", "Scope ("+m.scope.label()+")", m.scope == scopeSession))
	}
	candidates = append(candidates, footerCommand("/", filterLabel), footerCommand("Tab", tab), footerCommand("t", "Theme"))
	return m.prioritizedFooter(width, candidates)
}

func (m dashboardModel) prioritizedFooter(width int, candidates []string) string {
	base := []string{footerCommand("?", "Help"), footerCommand("q", "Quit")}
	parts := []string{}
	separator := borderStyle.Render(" │ ")
	for _, candidate := range candidates {
		withCandidate := append(append([]string{}, parts...), candidate)
		if ansi.StringWidth("  "+strings.Join(append(withCandidate, base...), separator)) > width {
			continue
		}
		parts = withCandidate
	}
	return padANSI("  "+strings.Join(append(parts, base...), separator), width)
}

func (m dashboardModel) renderFooterError(width int, err error) string {
	helpQuit := []string{footerCommand("?", "Help"), footerCommand("q", "Quit")}
	separator := borderStyle.Render(" │ ")
	available := max(1, width-2-ansi.StringWidth(strings.Join(helpQuit, separator))-ansi.StringWidth(separator))
	message := dangerStyle.Render("Error: " + ansi.Truncate(safeText(err.Error()), max(0, available-7), "…"))
	return padANSI("  "+strings.Join(append([]string{message}, helpQuit...), separator), width)
}

func dashboardIcon(nerd, plain string) string {
	if os.Getenv("JUMPMUX_PLAIN") != "" {
		return plain
	}
	return nerd
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
	lines := m.helpLines()
	start := m.helpOffset + 1
	end := min(len(lines), m.helpOffset+m.helpHeight())
	title := fmt.Sprintf("Help (%d-%d/%d)", start, end, len(lines))
	body := renderPanel(title, lines, width, m.height-1, m.helpOffset, 0, false, true)
	return body + "\n" + padANSI("  "+footerCommand("? / Esc", "Close")+" "+footerCommand("q", "Quit"), width)
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
	body := renderPanel(title, lines, leftWidth, height, m.diffOff, m.xOffset, true, m.panelFocus == panelLeft)
	if rightWidth > 0 {
		body = lipgloss.JoinHorizontal(lipgloss.Top, body, renderPanel(fmt.Sprintf("Files (%d)", len(files)), files, rightWidth, height, m.rightOffset, m.xOffset, false, m.panelFocus == panelRight))
	}
	footer := "  " + keycapStyle.Render("[j/k]") + textStyle.Render(" scroll focused  ") + keycapStyle.Render("[Shift+←/→]") + textStyle.Render(" focus  ") + keycapStyle.Render("[q]") + textStyle.Render(" close")
	return body + "\n" + padANSI(footer, width)
}

func renderPanel(title string, lines []string, width, height, offset, xOffset int, diff, focused bool) string {
	width, height = max(4, width), max(3, height)
	innerWidth, innerHeight := width-2, height-2
	offset = min(max(0, offset), max(0, len(lines)-innerHeight))

	if focused {
		title = "▶ " + title
	}
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
	icon, style := dashboardIcon(prOpenIcon, "O"), successStyle
	if item.prDraft {
		icon, style = dashboardIcon(prDraftIcon, "D"), mutedStyle
	} else {
		switch item.prState {
		case "MERGED":
			icon, style = dashboardIcon(prMergedIcon, "M"), accentStyle
		case "CLOSED":
			icon, style = dashboardIcon(prClosedIcon, "X"), dangerStyle
		}
	}
	spans := []prStatusSpan{{text: fmt.Sprintf("#%d", item.prNumber), style: style}, {text: icon, style: style}}
	switch item.prCheck {
	case checkSuccess:
		spans = append(spans, prStatusSpan{text: dashboardIcon(checkSuccessIcon, "+"), style: successStyle})
	case checkFailure:
		spans = append(spans, prStatusSpan{text: dashboardIcon(checkFailureIcon, "x"), style: dangerStyle})
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
		add(dashboardIcon(gitRebaseIcon, "R"), warningStyle)
	}
	if item.baseBranch != "" && item.baseBranch != "main" && item.baseBranch != "master" {
		add("→"+item.baseBranch, mutedStyle)
	}

	hasUncommitted := item.dirty || item.added > 0 || item.removed > 0
	allUncommitted := item.added == item.committedAdded && item.removed == item.committedRemoved
	addUncommitted := func() {
		add(dashboardIcon(gitDiffIcon, "*"), accentStyle)
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
		add(dashboardIcon(gitConflictIcon, "!"), dangerStyle)
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
		return icon + " " + dashboardIcon(staleAgentIcon, "old")
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
