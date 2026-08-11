package main

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

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
	worktreeMetadataRows = 6
)

var spinnerFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

var nerdFontEnabled = true

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
	agentGitRefreshed                                         time.Time
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

type worktreeStatusMsg struct {
	request uint64
	scheme  colorScheme
	target  string
	status  string
	err     error
}

type worktreeLogMsg struct {
	request uint64
	scheme  colorScheme
	target  string
	log     string
	err     error
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
type previewTickMsg uint64

func newDashboard(cwd string) dashboardModel {
	nerdFontEnabled = true
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
	config, configErr := loadConfig()
	if configErr != nil {
		model.err = configErr
	}
	if configErr == nil && config.hasDefaultScope {
		model.scope = config.defaultScope
	} else {
		model.scope = loadLegacyScopeMode()
	}
	if configErr == nil && config.hasTheme {
		model.scheme = config.theme
	} else {
		model.scheme = loadLegacyColorScheme()
	}
	nerdFontEnabled = configErr != nil || !config.hasNerdFont || config.nerdFont
	model.previewSize = loadPreviewSize()
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

func nextPreviewTick(request uint64) tea.Cmd {
	return tea.Tick(previewRefreshPeriod, func(time.Time) tea.Msg { return previewTickMsg(request) })
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
	if len(items) == 0 {
		return nil
	}
	limit := make(chan struct{}, min(4, len(items)))
	var baseOnce sync.Once
	var baseBranch string
	commands := make([]tea.Cmd, 0, len(items))
	for _, worktree := range items {
		worktree := worktree
		commands = append(commands, func() tea.Msg {
			limit <- struct{}{}
			defer func() { <-limit }()
			baseOnce.Do(func() { baseBranch = worktrunkDefaultBranch(items[0].cwd) })
			return worktreeDataMsg{stage: worktreeGitStage, generation: generation, worktrees: []item{loadGitDetails(worktree, baseBranch)}}
		})
	}
	return tea.Batch(commands...)
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

func loadAgentPreview(item item, scheme colorScheme, request uint64) tea.Cmd {
	return func() tea.Msg {
		preview := previewData{request: request, scheme: scheme, target: item.target, updated: item.updated, title: "Preview: " + worktreeName(item.cwd), followBottom: true}
		output, err := tmuxOutput(
			"display-message", "-p", "-t", item.pane, "#{cursor_y}\t#{pane_height}",
			";", "capture-pane", "-p", "-e", "-S", "-200", "-t", item.pane,
		)
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
}

func worktreePreview(item item, scheme colorScheme, request uint64) previewData {
	return previewData{
		request: request,
		scheme:  scheme,
		target:  item.target,
		updated: item.updated,
		title:   worktreeName(item.cwd),
		lines: []string{
			mutedStyle.Render("Branch  ") + textStyle.Render(safeText(item.branch)),
			mutedStyle.Render("Path    ") + textStyle.Render(safeText(compactHome(item.cwd))),
			mutedStyle.Render("Diff    ") + gitStatusView(item),
			mutedStyle.Render("Agent   ") + agentSummary(item, time.Now()),
			mutedStyle.Render("Mux     ") + muxView(item),
			"",
		},
		rightTitle: "Git Log",
	}
}

func loadWorktreeStatus(item item, scheme colorScheme, request uint64) tea.Cmd {
	return func() tea.Msg {
		status, err := gitOutput(item.cwd, "status", "--short")
		return worktreeStatusMsg{request: request, scheme: scheme, target: item.target, status: status, err: err}
	}
}

func loadWorktreeLog(item item, scheme colorScheme, request uint64) tea.Cmd {
	return func() tea.Msg {
		log, err := gitOutput(item.cwd, "log", "--pretty=format:%h%x09%ar%x09%s", "-20")
		return worktreeLogMsg{request: request, scheme: scheme, target: item.target, log: log, err: err}
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
	request, scheme := m.previewRequest, m.scheme
	changed := m.preview.target != item.target
	if changed {
		m.previewOffset, m.rightOffset, m.xOffset, m.panelFocus = 0, 0, 0, panelLeft
	}
	if item.kind == "session" {
		m.loading = changed
		return loadAgentPreview(item, scheme, request)
	}

	preview := worktreePreview(item, scheme, request)
	if !changed {
		if len(m.preview.lines) > len(preview.lines) {
			preview.lines = append(preview.lines, m.preview.lines[len(preview.lines):]...)
		}
		preview.rightLines = m.preview.rightLines
	}
	m.preview, m.loading = preview, false
	return tea.Batch(loadWorktreeStatus(item, scheme, request), loadWorktreeLog(item, scheme, request))
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
		return m, nextClock()
	case previewTickMsg:
		if uint64(msg) != m.previewRequest || m.diff {
			return m, nil
		}
		if selected, ok := m.selected(); ok && selected.kind == "session" {
			return m, m.requestPreview(selected)
		}
		return m, nil
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
			m.worktreePending = len(msg.worktrees) + 2
			commands := []tea.Cmd{
				refreshWorktreeGit(msg.worktrees, msg.generation),
				refreshWorktreePR(m.cwd, msg.worktrees, msg.generation),
				refreshWorktreeMux(msg.worktrees, msg.generation),
			}
			if selected, ok := m.selected(); ok && selected.kind == "worktree" && !m.diff {
				commands = append(commands, m.requestPreview(selected))
			}
			return m, tea.Batch(commands...)
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
		selectedGitUpdated := hasAfter && msg.stage == worktreeGitStage && slices.ContainsFunc(msg.worktrees, func(item item) bool { return item.target == after.target })
		refreshPreview := selectedGitUpdated || (msg.stage == worktreeMuxStage && changed)
		if msg.err == nil && !m.diff && hasAfter && after.kind == "worktree" && refreshPreview {
			return m, m.requestPreview(after)
		}
		return m, nil
	case previewMsg:
		if !m.diff && msg.request == m.previewRequest && msg.scheme == m.scheme {
			if selected, ok := m.selected(); ok && selected.kind == "session" && selected.target == msg.target {
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
				return m, nextPreviewTick(msg.request)
			}
		}
		return m, nil
	case worktreeStatusMsg:
		if !m.diff && msg.request == m.previewRequest && msg.scheme == m.scheme && m.preview.target == msg.target {
			if selected, ok := m.selected(); ok && selected.kind == "worktree" && selected.target == msg.target {
				m.preview.lines = m.preview.lines[:min(worktreeMetadataRows, len(m.preview.lines))]
				if msg.err != nil {
					m.preview.lines = append(m.preview.lines, gitErrorLine(msg.err))
				} else if strings.TrimSpace(msg.status) == "" {
					m.preview.lines = append(m.preview.lines, mutedStyle.Render("clean"))
				} else {
					for _, line := range strings.Split(strings.TrimSpace(msg.status), "\n") {
						m.preview.lines = append(m.preview.lines, safeLine(line))
					}
				}
			}
		}
		return m, nil
	case worktreeLogMsg:
		if !m.diff && msg.request == m.previewRequest && msg.scheme == m.scheme && m.preview.target == msg.target {
			if selected, ok := m.selected(); ok && selected.kind == "worktree" && selected.target == msg.target {
				switch {
				case msg.err != nil:
					m.preview.rightLines = []string{gitErrorLine(msg.err)}
				case strings.TrimSpace(msg.log) == "":
					m.preview.rightLines = []string{"(no commits)"}
				default:
					m.preview.rightLines = strings.Split(strings.TrimSpace(msg.log), "\n")
				}
				m.rightOffset = m.clampPreviewOffset(m.rightOffset, m.preview.rightLines)
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
		switch key {
		case "enter":
			m.filter = false
			m.filterInputs[m.tab].Blur()
		case "esc":
			m.filter, m.query, m.index = false, "", 0
			m.filterInputs[m.tab].SetValue("")
			m.filterInputs[m.tab].Blur()
		default:
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
