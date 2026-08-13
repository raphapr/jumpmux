package main

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"
)

const (
	tabAgents = iota
	tabWorktrees
	tabSessions
	tabCount
)

type sessionFilter uint8

const (
	sessionFilterAll sessionFilter = iota
	sessionFilterLive
	sessionFilterInactive
	sessionFilterConfigured
	sessionFilterDiscovered
)

var sessionFilters = [...]sessionFilter{sessionFilterAll, sessionFilterLive, sessionFilterInactive, sessionFilterConfigured, sessionFilterDiscovered}

func (filter sessionFilter) label() string {
	return [...]string{"All", "Live", "Inactive", "Configured", "Discovered"}[filter]
}

const (
	refreshInterval      = 2 * time.Second
	gitRefreshPeriod     = 5 * time.Second
	previewRefreshPeriod = 500 * time.Millisecond
	clockInterval        = 250 * time.Millisecond
	staleThreshold       = time.Hour
	maxDiffOutput        = 1 << 20
	worktreeMetadataRows = 7
)

var spinnerFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

var nerdFontEnabled = true

var (
	currentRowColor, currentWorktreeColor, selectedColor lipgloss.AdaptiveColor
	textColor, dimmedColor, borderColor, headerColor     lipgloss.AdaptiveColor
	keycapColor, infoColor, successColor, warningColor   lipgloss.AdaptiveColor
	dangerColor, accentColor                             lipgloss.AdaptiveColor
	dashboardBackgroundColor                             lipgloss.AdaptiveColor
	dashboardBackgroundEnabled                           bool

	textStyle, headerStyle, mutedStyle, borderStyle   lipgloss.Style
	activeBorderStyle, keycapStyle, cursorStyle       lipgloss.Style
	infoStyle, successStyle                           lipgloss.Style
	warningStyle, dangerStyle, accentStyle            lipgloss.Style
	selectedStyle, currentStyle, currentWorktreeStyle lipgloss.Style
	addedStyle, removedStyle, diffHeadStyle           lipgloss.Style
)

type dashboardModel struct {
	cwd                                                                   string
	now                                                                   time.Time
	tab, index                                                            int
	width, height                                                         int
	agents, allAgents                                                     []item
	worktrees, sessions                                                   []item
	agentGit, gitCache, prCache                                           map[string]item
	scope                                                                 scopeMode
	scheme                                                                colorScheme
	launchSession                                                         string
	worktreeGeneration, sessionGeneration                                 uint64
	previewRequest, diffRequest                                           uint64
	filter, diff, help, themePicker                                       bool
	query                                                                 string
	queries, tabTargets                                                   [tabCount]string
	filterInputs                                                          [tabCount]textinput.Model
	sessionSortRecent                                                     bool
	themePickerInput                                                      textinput.Model
	themePickerIndex, themePickerOffset                                   int
	themePickerPrevious                                                   colorScheme
	sessionFilter                                                         sessionFilter
	actionMenu                                                            bool
	actionMenuIndex                                                       int
	actionTextInput                                                       textinput.Model
	preview                                                               previewData
	previewOffset, diffOff, rightOffset, xOffset, previewSize             int
	previewEnabled                                                        [tabCount]bool
	previewOverride                                                       [tabCount]int8
	panelFocus, helpOffset                                                int
	previewFocused, focused                                               bool
	loading                                                               bool
	agentsLoaded, worktreesLoaded, sessionsLoaded                         bool
	lastRefresh                                                           [tabCount]time.Time
	agentsInFlight, agentGitInFlight, worktreesInFlight, sessionsInFlight bool
	agentGitRefreshed                                                     time.Time
	worktreePending                                                       int
	err, agentErr, worktreeErr, sessionsErr                               error
	notice                                                                string
	noticeUntil                                                           time.Time
	chosen                                                                bool
	selection                                                             item
	lastClickTarget                                                       string
	lastClickAt                                                           time.Time
	action                                                                dashboardAction
	actionTarget                                                          item
	actionBackend                                                         worktreeBackend
	sessionSelectionAfterRemove                                           string
	restoreSessionSelection                                               bool
}

type dashboardData struct {
	agents []item
	err    error
}

type dashboardDataMsg dashboardData

type agentGitMsg []item

type sessionDataMsg struct {
	generation uint64
	sessions   []item
	err        error
}

type dashboardAction uint8
type menuAction uint8

const (
	panelLeft = iota
	panelRight
)

const (
	actionNone dashboardAction = iota
	actionAddWorktree
	actionRemoveWorktree
	actionRemoveSession
	actionRenameSession
	actionCleanupWorktree
	actionRunning
)

const (
	menuOpen menuAction = iota
	menuDiff
	menuPR
	menuCopyPath
	menuCopyName
	menuCopyPRURL
	menuRename
	menuCleanup
	menuRemove
)

type actionMenuEntry struct {
	action menuAction
	label  string
}

type worktreeActionMsg struct {
	action       dashboardAction
	notice       string
	selectTarget string
	err          error
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
	kind         string
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
	return dashboardModel{cwd: cwd, now: time.Now(), width: 80, height: 24, previewSize: defaultPreviewSize, previewEnabled: [tabCount]bool{true, true, true}, scheme: schemeDefault, focused: true, worktreeGeneration: 1, agentGit: map[string]item{}, gitCache: map[string]item{}, prCache: map[string]item{}, filterInputs: [tabCount]textinput.Model{newTextInput("/"), newTextInput("/"), newTextInput("search: ")}, themePickerInput: newTextInput("filter: "), actionTextInput: newTextInput(""), agentsInFlight: true, worktreesInFlight: true, sessionsInFlight: true}
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
	input.Cursor.Style = cursorStyle
}

func (m dashboardModel) themeOptions() []colorScheme {
	query := strings.ToLower(strings.TrimSpace(m.themePickerInput.Value()))
	themes := make([]colorScheme, 0, len(colorSchemes))
	for _, scheme := range colorSchemes {
		if query == "" || strings.Contains(scheme.slug(), query) {
			themes = append(themes, scheme)
		}
	}
	return themes
}

func (m *dashboardModel) selectTheme(index int) {
	themes := m.themeOptions()
	if len(themes) == 0 {
		m.themePickerIndex, m.themePickerOffset = 0, 0
		return
	}
	m.themePickerIndex = (index%len(themes) + len(themes)) % len(themes)
	m.scheme = themes[m.themePickerIndex]
	applyColorScheme(m.scheme)
	m.revealThemeSelection()
}

func (m *dashboardModel) revealThemeSelection() {
	lines, selectedLine := m.themePickerLines()
	visible := max(1, m.previewHeight()-2)
	if selectedLine < m.themePickerOffset {
		m.themePickerOffset = selectedLine
	}
	if selectedLine >= m.themePickerOffset+visible {
		m.themePickerOffset = selectedLine - visible + 1
	}
	m.themePickerOffset = clampLineOffset(m.themePickerOffset, lines, visible)
}

func (m *dashboardModel) openThemePicker() tea.Cmd {
	m.themePicker, m.themePickerPrevious, m.themePickerOffset = true, m.scheme, 0
	m.themePickerInput.SetValue("")
	m.themePickerIndex = max(0, slices.Index(colorSchemes[:], m.scheme))
	m.lastClickTarget, m.lastClickAt = "", time.Time{}
	m.revealThemeSelection()
	return m.themePickerInput.Focus()
}

func (m *dashboardModel) closeThemePicker(save bool) tea.Cmd {
	m.themePicker = false
	m.themePickerInput.Blur()
	if !save {
		m.scheme = m.themePickerPrevious
		applyColorScheme(m.scheme)
	} else {
		m.err = saveColorScheme(m.scheme)
		if m.err == nil {
			m.setNotice("Theme: " + m.scheme.slug())
		}
	}
	if selected, ok := m.selected(); ok {
		return m.requestPreview(selected)
	}
	return nil
}

func (m *dashboardModel) cycleSessionFilter() tea.Cmd {
	target := m.selectedTarget()
	m.sessionFilter = sessionFilters[(int(m.sessionFilter)+1)%len(sessionFilters)]
	m.setNotice("Sessions: " + m.sessionFilter.label())
	m.restoreSelection(target)
	if selected, ok := m.selected(); ok {
		return m.requestPreview(selected)
	}
	m.preview, m.loading = previewData{}, false
	return nil
}

func (m *dashboardModel) setNotice(value string) {
	m.notice, m.noticeUntil = value, m.now.Add(2*time.Second)
}

func (m *dashboardModel) clearActionError() { m.err = nil }

func (m *dashboardModel) togglePreview() tea.Cmd {
	if m.previewShown() {
		m.previewOverride[m.tab] = -1
		m.preview, m.loading = previewData{}, false
		m.setNotice("Preview hidden")
		return nil
	}
	m.previewOverride[m.tab] = 1
	m.setNotice("Preview shown")
	if selected, ok := m.selected(); ok {
		return m.requestPreview(selected)
	}
	return nil
}

func (m dashboardModel) actionMenuEntries() []actionMenuEntry {
	selected, ok := m.selected()
	if !ok {
		return nil
	}
	entries := []actionMenuEntry{{menuOpen, "Open"}}
	if selected.cwd != "" && m.tab != tabSessions {
		entries = append(entries, actionMenuEntry{menuCopyPath, "Copy path"})
	}
	if m.tab == tabSessions {
		if selected.muxSessionID != "" {
			entries = append(entries, actionMenuEntry{menuRename, "Rename session"}, actionMenuEntry{menuRemove, "Remove"})
		}
		if selected.cwd != "" {
			entries = append(entries, actionMenuEntry{menuCopyPath, "Copy path"})
		}
		entries = append(entries, actionMenuEntry{menuCopyName, "Copy session name"})
		return entries
	}
	gitItem := m.gitItem(selected)
	if gitItem.branch != "" {
		entries = append(entries, actionMenuEntry{menuCopyName, "Copy branch"})
	}
	entries = append(entries, actionMenuEntry{menuDiff, "Diff"})
	if gitItem.prNumber != 0 {
		entries = append(entries, actionMenuEntry{menuPR, "Open PR"})
	}
	if gitItem.prURL != "" {
		entries = append(entries, actionMenuEntry{menuCopyPRURL, "Copy PR URL"})
	}
	if m.tab == tabWorktrees && selected.prunable && !selected.locked && !selected.current {
		entries = append(entries, actionMenuEntry{menuCleanup, "Clean up stale record"})
	}
	if m.tab == tabWorktrees && !selected.current && (len(m.worktrees) == 0 || !samePath(selected.cwd, m.worktrees[0].cwd)) {
		entries = append(entries, actionMenuEntry{menuRemove, "Remove"})
	}
	return entries
}

func (m *dashboardModel) openActionMenu() tea.Cmd {
	if _, ok := m.selected(); !ok {
		return nil
	}
	m.clearActionError()
	m.actionMenu, m.actionMenuIndex = true, 0
	m.lastClickTarget, m.lastClickAt = "", time.Time{}
	return nil
}

func (m *dashboardModel) beginRename(selected item) tea.Cmd {
	if selected.muxSessionID == "" {
		m.err = errors.New("the selected session is not running")
		return nil
	}
	m.action, m.actionTarget, m.err = actionRenameSession, selected, nil
	m.actionTextInput.SetValue(selected.title)
	m.lastClickTarget, m.lastClickAt = "", time.Time{}
	return m.actionTextInput.Focus()
}

func (m *dashboardModel) beginCleanup(selected item) {
	if !selected.prunable || selected.locked || selected.current {
		m.err = errors.New("the selected worktree is not safe to clean up")
		return
	}
	m.action, m.actionTarget, m.err = actionCleanupWorktree, selected, nil
}

func (m *dashboardModel) beginRemove(selected item) {
	if m.tab == tabSessions {
		if selected.muxSessionID == "" {
			m.err = errors.New("the selected session is not running")
			return
		}
		rows := m.rows()
		m.sessionSelectionAfterRemove = ""
		switch {
		case m.index+1 < len(rows):
			m.sessionSelectionAfterRemove = rows[m.index+1].target
		case m.index > 0:
			m.sessionSelectionAfterRemove = rows[m.index-1].target
		}
		m.action, m.actionTarget = actionRemoveSession, selected
		return
	}
	if m.tab != tabWorktrees {
		return
	}
	if selected.current {
		m.err = errors.New("cannot remove the current worktree")
		return
	}
	if len(m.worktrees) > 0 && samePath(selected.cwd, m.worktrees[0].cwd) {
		m.err = errors.New("cannot remove the primary worktree")
		return
	}
	backend, err := loadWorktreeBackend()
	if err != nil {
		m.err = err
		return
	}
	backend, err = resolvedWorktreeBackend(backend)
	if err != nil {
		m.err = err
		return
	}
	m.action, m.actionTarget, m.actionBackend, m.err = actionRemoveWorktree, selected, backend, nil
}

func (m *dashboardModel) resizeInputs() {
	width := max(1, m.width-18)
	for index := range m.filterInputs {
		m.filterInputs[index].Width = width
	}
	m.themePickerInput.Width = width
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
	if configErr == nil {
		model.previewEnabled = config.preview
	}
	model.previewSize = loadPreviewSize()
	applyColorScheme(model.scheme)
	model.launchSession = launchSession
	if forceSession {
		model.scope = scopeSession
	}
	return model
}

func (m dashboardModel) Init() tea.Cmd {
	return tea.Batch(refreshAgents(), refreshWorktreeList(m.cwd, m.worktreeGeneration), refreshSessions(m.sessionGeneration), nextTick(), nextClock(), func() tea.Msg { return tea.EnableReportFocus() })
}

func nextTick() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func nextClock() tea.Cmd {
	interval := clockInterval
	if reducedMotion() {
		interval = time.Second
	}
	return tea.Tick(interval, func(t time.Time) tea.Msg { return clockMsg(t) })
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

func refreshSessions(generation uint64) tea.Cmd {
	return func() tea.Msg {
		sessions, err := listSessions(false)
		return sessionDataMsg{generation: generation, sessions: sessions, err: err}
	}
}

func refreshWorktreeList(cwd string, generation uint64) tea.Cmd {
	return func() tea.Msg {
		items, err := listWorktreeItems(cwd)
		return worktreeDataMsg{stage: worktreeListStage, generation: generation, worktrees: items, err: err}
	}
}

func (m *dashboardModel) queueRefreshes() []tea.Cmd {
	commands := []tea.Cmd{}
	if !m.agentsInFlight {
		m.agentsInFlight = true
		commands = append(commands, refreshAgents())
	}
	if !m.worktreesInFlight {
		m.worktreesInFlight = true
		m.worktreeGeneration++
		commands = append(commands, refreshWorktreeList(m.cwd, m.worktreeGeneration))
	}
	if !m.sessionsInFlight {
		m.sessionsInFlight = true
		m.sessionGeneration++
		commands = append(commands, refreshSessions(m.sessionGeneration))
	}
	return commands
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
		preview := previewData{request: request, scheme: scheme, target: item.target, kind: item.kind, updated: item.updated, title: "Preview: " + worktreeName(item.cwd), followBottom: true}
		if item.prCheck == checkFailure {
			preview.lines = append(preview.lines, failedChecksPreview(item.prFailedChecks))
		}
		output, err := tmuxOutput(
			"display-message", "-p", "-t", item.pane, "#{cursor_y}\t#{pane_height}",
			";", "capture-pane", "-p", "-e", "-S", "-200", "-t", item.pane,
		)
		if err != nil {
			preview.lines = append(preview.lines, "(pane not available)")
			return previewMsg(preview)
		}
		lines := paneHistoryLines(output)
		if len(lines) == 0 {
			preview.lines = append(preview.lines, "(empty output)")
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
	state := "-"
	if item.locked || item.prunable {
		state = ""
	}
	if item.locked {
		state += dashboardIcon("󰌾", "LOCK") + " "
	}
	if item.prunable {
		state += dashboardIcon("󰆴", "PRUNE")
	}
	return previewData{
		request: request,
		scheme:  scheme,
		target:  item.target,
		kind:    item.kind,
		updated: item.updated,
		title:   worktreeName(item.cwd),
		lines: []string{
			mutedStyle.Render("Branch  ") + textStyle.Render(safeText(item.branch)),
			mutedStyle.Render("Path    ") + textStyle.Render(safeText(compactHome(item.cwd))),
			mutedStyle.Render("Diff    ") + gitStatusView(item),
			mutedStyle.Render("Agent   ") + agentSummary(item, time.Now()),
			mutedStyle.Render("Mux     ") + muxView(item),
			mutedStyle.Render("State   ") + textStyle.Render(state),
			failedChecksPreview(item.prFailedChecks),
		},
		rightTitle: "Git Log",
	}
}

func failedChecksPreview(names []string) string {
	if len(names) == 0 {
		return ""
	}
	limit := min(3, len(names))
	value := strings.Join(names[:limit], ", ")
	if len(names) > limit {
		value += fmt.Sprintf(" +%d", len(names)-limit)
	}
	return dangerStyle.Render("Failed checks: ") + textStyle.Render(safeText(value))
}

func sessionPreview(item item, scheme colorScheme, request uint64) previewData {
	lines := []string{mutedStyle.Render("Inactive (Enter creates it)")}
	if item.muxSessionID != "" {
		lines = []string{mutedStyle.Render("Loading pane…")}
	}
	return previewData{
		request: request, scheme: scheme, target: item.target, kind: item.kind,
		title: "Active pane: " + item.title, lines: lines,
	}
}

func loadSessionPreview(item item, scheme colorScheme, request uint64) tea.Cmd {
	return func() tea.Msg {
		preview := sessionPreview(item, scheme, request)
		if item.muxSessionID == "" || item.pane == "" {
			return previewMsg(preview)
		}
		preview.lines, preview.followBottom = nil, true
		output, err := tmuxOutput("capture-pane", "-p", "-e", "-t", item.pane)
		if err != nil {
			preview.lines = []string{"(pane not available)"}
			return previewMsg(preview)
		}
		for _, line := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
			preview.lines = append(preview.lines, sanitizePaneLine(strings.TrimSuffix(line, "\r")))
		}
		if len(preview.lines) == 0 {
			preview.lines = []string{"(empty output)"}
		}
		return previewMsg(preview)
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
	if !m.focused {
		return nil
	}
	if !m.previewShown() {
		m.preview, m.loading = previewData{}, false
		return nil
	}
	m.previewRequest++
	request, scheme := m.previewRequest, m.scheme
	changed := m.preview.target != item.target
	if changed {
		m.previewOffset, m.rightOffset, m.xOffset, m.panelFocus = 0, 0, 0, panelLeft
	}
	if item.kind == "session" {
		m.loading = changed
		details := m.gitItem(item)
		item.prCheck, item.prFailedChecks = details.prCheck, details.prFailedChecks
		return loadAgentPreview(item, scheme, request)
	}
	if item.kind == "tmux-session" {
		if changed || m.preview.target == "" {
			m.preview = sessionPreview(item, scheme, request)
		}
		m.loading = false
		return loadSessionPreview(item, scheme, request)
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
		wasShown := m.previewShown()
		wasPreviewAtBottom := m.previewOffset == m.previewBottomOffset(m.preview.lines)
		m.width, m.height = msg.Width, msg.Height
		m.resizeInputs()
		if wasPreviewAtBottom {
			m.previewOffset = m.previewBottomOffset(m.preview.lines)
		}
		if !m.hasRightPanel() {
			m.panelFocus = panelLeft
		}
		if !wasShown && m.previewShown() && !m.diff {
			if selected, ok := m.selected(); ok {
				return m, m.requestPreview(selected)
			}
		}
		return m, nil
	case tea.FocusMsg:
		m.focused = true
		commands := m.queueRefreshes()
		if selected, ok := m.selected(); ok && !m.diff {
			commands = append(commands, m.requestPreview(selected))
		}
		return m, tea.Batch(commands...)
	case tea.BlurMsg:
		m.focused = false
		return m, nil
	case tickMsg:
		commands := []tea.Cmd{nextTick()}
		if m.focused {
			commands = append(commands, m.queueRefreshes()...)
		}
		return m, tea.Batch(commands...)
	case clockMsg:
		m.now = time.Time(msg)
		if !m.noticeUntil.IsZero() && !m.now.Before(m.noticeUntil) {
			m.notice, m.noticeUntil = "", time.Time{}
		}
		return m, nextClock()
	case previewTickMsg:
		if !m.focused || uint64(msg) != m.previewRequest || m.diff {
			return m, nil
		}
		if selected, ok := m.selected(); ok && (selected.kind == "session" || (selected.kind == "tmux-session" && selected.muxSessionID != "")) {
			return m, m.requestPreview(selected)
		}
		return m, nil
	case sessionDataMsg:
		if msg.generation != m.sessionGeneration {
			return m, nil
		}
		m.sessionsInFlight, m.sessionsLoaded, m.sessionsErr = false, true, msg.err
		if msg.err == nil {
			m.lastRefresh[tabSessions] = m.now
		}
		if msg.err != nil {
			return m, nil
		}
		before, hadBefore := m.selected()
		target, index := m.selectedTarget(), m.index
		if m.restoreSessionSelection {
			target = m.sessionSelectionAfterRemove
			m.restoreSessionSelection, m.sessionSelectionAfterRemove = false, ""
		}
		m.sessions = msg.sessions
		m.restoreSelection(target)
		if selected, ok := m.selected(); hadBefore && target != "" && (!ok || selected.target != target) {
			m.index = min(index, max(0, len(m.rows())-1))
		}
		after, hasAfter := m.selected()
		if hasAfter && after.kind == "tmux-session" && !m.diff {
			if !hadBefore || before.target != after.target || before.kind != after.kind || m.preview.target != after.target || m.preview.kind != after.kind {
				return m, m.requestPreview(after)
			}
		} else if !m.diff && !hasAfter {
			m.preview, m.loading = previewData{}, false
		}
		return m, nil
	case dashboardDataMsg:
		m.agentsInFlight, m.agentsLoaded, m.agentErr = false, true, msg.err
		if msg.err == nil {
			m.lastRefresh[tabAgents] = m.now
		}
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
		if m.focused && len(m.allAgents) > 0 && !m.agentGitInFlight && time.Since(m.agentGitRefreshed) >= gitRefreshPeriod {
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
		selectedAgentUpdated := false
		selected, hasSelected := m.selected()
		for _, detail := range msg {
			if hasSelected && selected.kind == "session" && detail.cwd == selected.cwd {
				selectedAgentUpdated = true
			}
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
		if selectedAgentUpdated && !m.diff {
			return m, m.requestPreview(selected)
		}
		return m, nil
	case worktreeDataMsg:
		if msg.generation != m.worktreeGeneration {
			return m, nil
		}
		if msg.stage == worktreeListStage {
			m.worktreesLoaded, m.worktreeErr = true, msg.err
			if msg.err == nil {
				m.lastRefresh[tabWorktrees] = m.now
			}
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
			if !m.focused {
				m.worktreesInFlight = false
				return m, nil
			}
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
		changed := !hadBefore || before.target != after.target || before.kind != after.kind
		selectedGitUpdated := hasAfter && msg.stage == worktreeGitStage && slices.ContainsFunc(msg.worktrees, func(item item) bool { return item.target == after.target })
		selectedPRUpdated := hasAfter && msg.stage == worktreePRStage && slices.ContainsFunc(msg.worktrees, func(item item) bool { return item.target == after.target })
		refreshPreview := selectedGitUpdated || selectedPRUpdated || (msg.stage == worktreeMuxStage && changed)
		if msg.err == nil && !m.diff && hasAfter && after.kind == "worktree" && refreshPreview {
			return m, m.requestPreview(after)
		}
		return m, nil
	case previewMsg:
		if !m.diff && msg.request == m.previewRequest && msg.scheme == m.scheme {
			if selected, ok := m.selected(); ok && (msg.kind == "" || selected.kind == msg.kind) && selected.target == msg.target {
				reset := m.preview.target != msg.target
				wasAtBottom := m.previewOffset == m.previewBottomOffset(m.preview.lines)
				m.preview = previewData(msg)
				if reset {
					m.previewOffset, m.rightOffset, m.xOffset = 0, 0, 0
				}
				if msg.followBottom && (reset || wasAtBottom) {
					m.previewOffset = m.previewBottomOffset(msg.lines)
				} else {
					m.previewOffset = m.clampPreviewOffset(m.previewOffset, msg.lines)
				}
				m.loading = false
				if selected.kind == "session" || (selected.kind == "tmux-session" && selected.muxSessionID != "") {
					return m, nextPreviewTick(msg.request)
				}
				return m, nil
			}
		}
		return m, nil
	case worktreeStatusMsg:
		if !m.diff && msg.request == m.previewRequest && msg.scheme == m.scheme && m.preview.target == msg.target && m.preview.kind == "worktree" {
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
		if !m.diff && msg.request == m.previewRequest && msg.scheme == m.scheme && m.preview.target == msg.target && m.preview.kind == "worktree" {
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
				m.preview = previewData{target: msg.target, kind: selected.kind, title: msg.title, lines: msg.lines, rightTitle: fmt.Sprintf("Files (%d)", len(msg.files)), rightLines: msg.files}
				m.diffOff, m.rightOffset, m.xOffset, m.panelFocus, m.loading = 0, 0, 0, panelLeft, false
			}
		}
		return m, nil
	case worktreeActionMsg:
		m.action, m.actionTarget, m.actionBackend, m.err = actionNone, item{}, "", msg.err
		if msg.action == actionRemoveSession || msg.action == actionRenameSession {
			m.restoreSessionSelection = msg.err == nil
			if msg.action == actionRenameSession && msg.err == nil {
				m.sessionSelectionAfterRemove = msg.selectTarget
			}
			if msg.err != nil {
				m.sessionSelectionAfterRemove = ""
			}
		}
		if msg.err == nil && msg.notice != "" {
			m.setNotice(msg.notice)
		}
		if msg.err == nil && (msg.action == actionAddWorktree || msg.action == actionRemoveWorktree || msg.action == actionCleanupWorktree) {
			m.worktreesInFlight = true
			m.worktreeGeneration++
			return m, refreshWorktreeList(m.cwd, m.worktreeGeneration)
		}
		if msg.err == nil && (msg.action == actionRemoveSession || msg.action == actionRenameSession) {
			m.sessionsInFlight = true
			m.sessionGeneration++
			return m, refreshSessions(m.sessionGeneration)
		}
		return m, nil
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	if m.themePicker {
		previous := m.themePickerInput.Value()
		var command tea.Cmd
		m.themePickerInput, command = m.themePickerInput.Update(msg)
		if m.themePickerInput.Value() != previous {
			m.selectTheme(0)
		}
		return m, command
	}
	if m.filter {
		previous := m.filterInputs[m.tab].Value()
		var command tea.Cmd
		m.filterInputs[m.tab], command = m.filterInputs[m.tab].Update(msg)
		m.query, m.queries[m.tab] = m.filterInputs[m.tab].Value(), m.filterInputs[m.tab].Value()
		if m.query == previous {
			return m, command
		}
		m.clearActionError()
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
	if m.themePicker || m.actionMenu || m.action != actionNone {
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
		} else if m.wideLayout() && msg.X >= m.wideTableWidth() {
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
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}
	if m.diff {
		m.focusPanelAt(msg.X)
		return m, nil
	}
	if m.wideLayout() && msg.X >= m.wideTableWidth() {
		m.focusPanelAt(msg.X)
		return m, nil
	}
	if msg.Y >= 2+m.tableHeight() {
		m.focusPanelAt(msg.X)
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
		case "pgup":
			m.helpOffset = m.clampHelpOffset(m.helpOffset - m.helpHeight())
		case "pgdown":
			m.helpOffset = m.clampHelpOffset(m.helpOffset + m.helpHeight())
		case "g", "home":
			m.helpOffset = 0
		case "G", "end":
			m.helpOffset = m.clampHelpOffset(len(m.helpLines()))
		}
		return m, nil
	}
	if m.themePicker {
		return m.handleThemePickerKey(msg)
	}
	if m.actionMenu {
		return m.handleActionMenuKey(msg)
	}
	if m.action != actionNone {
		return m.handleActionKey(msg)
	}
	if key == "?" {
		m.clearActionError()
		m.help, m.helpOffset = true, 0
		return m, nil
	}
	if key == " " && !m.filter {
		return m, m.openActionMenu()
	}
	if m.livePreviewPaused() && (key == "G" || key == "end") {
		m.clearActionError()
		m.previewOffset = m.previewBottomOffset(m.preview.lines)
		m.previewFocused = true
		return m, nil
	}
	if m.tab == tabSessions && key == "ctrl+r" {
		m.clearActionError()
		target := m.selectedTarget()
		m.sessionSortRecent = !m.sessionSortRecent
		m.setNotice("Sessions: " + map[bool]string{true: "Recent", false: "Grouped"}[m.sessionSortRecent])
		m.restoreSelection(target)
		return m, nil
	}
	if m.tab == tabSessions && (key == "ctrl+j" || key == "ctrl+k" || key == "ctrl+n" || key == "ctrl+p") {
		delta := 1
		if key == "ctrl+k" || key == "ctrl+p" {
			delta = -1
		}
		m.move(delta)
		if selected, ok := m.selected(); ok {
			return m, m.requestPreview(selected)
		}
		return m, nil
	}
	if m.tab == tabSessions && key == "ctrl+d" {
		m.clearActionError()
		if selected, ok := m.selected(); ok {
			m.beginRemove(selected)
		} else {
			m.err = errors.New("the selected session is not running")
		}
		m.lastClickTarget, m.lastClickAt = "", time.Time{}
		return m, nil
	}
	if m.tab == tabSessions && (key == "pgup" || key == "pgdown") {
		delta := m.previewVisibleHeight()
		if key == "pgup" {
			delta = -delta
		}
		m.previewFocused = true
		m.scrollFocusedPanel(delta)
		return m, nil
	}
	if m.tab == tabSessions && (key == "home" || key == "end") {
		if key == "home" {
			m.index = 0
		} else {
			m.index = max(0, len(m.rows())-1)
		}
		if selected, ok := m.selected(); ok {
			return m, m.requestPreview(selected)
		}
		return m, nil
	}
	if m.filter {
		switch key {
		case "shift+tab":
			return m.switchTab((m.tab - 1 + tabCount) % tabCount)
		case "tab":
			if m.tab == tabSessions {
				return m.switchTab((m.tab + 1) % tabCount)
			}
			var command tea.Cmd
			m.filterInputs[m.tab], command = m.filterInputs[m.tab].Update(msg)
			return m, command
		case "enter":
			if m.tab == tabSessions {
				if selected, ok := m.selected(); ok {
					m.selection, m.chosen = selected, true
					return m, tea.Quit
				}
				return m, nil
			}
			m.filter = false
			m.filterInputs[m.tab].Blur()
		case "up", "down":
			if m.tab == tabSessions {
				delta := 1
				if key == "up" {
					delta = -1
				}
				m.move(delta)
				if selected, ok := m.selected(); ok {
					return m, m.requestPreview(selected)
				}
				return m, nil
			}
			var command tea.Cmd
			m.filterInputs[m.tab], command = m.filterInputs[m.tab].Update(msg)
			return m, command
		case "esc":
			m.filter, m.query, m.index = false, "", 0
			m.filterInputs[m.tab].SetValue("")
			m.filterInputs[m.tab].Blur()
		default:
			previous := m.filterInputs[m.tab].Value()
			var command tea.Cmd
			m.filterInputs[m.tab], command = m.filterInputs[m.tab].Update(msg)
			m.query, m.index = m.filterInputs[m.tab].Value(), 0
			m.queries[m.tab] = m.query
			if m.query != previous {
				m.clearActionError()
			}
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
		if !m.previewEnabled[m.tab] {
			return m, nil
		}
		delta := previewSizeStep
		if key == "-" || key == "_" {
			delta = -delta
		}
		m.resizePreview(delta)
		return m, nil
	}
	if key == "shift+left" || key == "shift+right" {
		if m.hasRightPanel() {
			m.previewFocused = true
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
		case "pgup":
			m.scrollFocusedPanel(-m.diffVisibleHeight())
		case "pgdown":
			m.scrollFocusedPanel(m.diffVisibleHeight())
		case "g", "home":
			m.diffOff, m.rightOffset = 0, 0
		case "G", "end":
			if m.panelFocus == panelRight && m.hasRightPanel() {
				m.rightOffset = m.clampDiffOffset(len(m.preview.rightLines), m.preview.rightLines)
			} else {
				m.diffOff = m.clampDiffOffset(len(m.preview.lines), m.preview.lines)
			}
		case "h", "left":
			m.xOffset = max(0, m.xOffset-4)
		case "l", "right":
			m.xOffset = m.clampX(m.xOffset + 4)
		}
		return m, nil
	}

	if m.tab == tabSessions && msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
		m.clearActionError()
		m.filter = true
		m.filterInputs[m.tab].SetValue(m.query)
		m.filterInputs[m.tab].CursorEnd()
		m.filterInputs[m.tab].Focus()
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
	if m.tab != tabSessions && len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
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
		if m.err != nil {
			m.err = nil
			return m, nil
		}
		if m.query != "" {
			m.query, m.queries[m.tab], m.index = "", "", 0
			m.filterInputs[m.tab].SetValue("")
			if selected, ok := m.selected(); ok {
				return m, m.requestPreview(selected)
			}
			return m, nil
		}
		return m, tea.Quit
	case "tab":
		m.clearActionError()
		return m.switchTab((m.tab + 1) % tabCount)
	case "shift+tab":
		m.clearActionError()
		return m.switchTab((m.tab - 1 + tabCount) % tabCount)
	case "ctrl+v":
		m.clearActionError()
		return m, m.togglePreview()
	case "t":
		m.clearActionError()
		return m, m.openThemePicker()
	case "ctrl+f":
		if m.tab == tabSessions {
			m.clearActionError()
			return m, m.cycleSessionFilter()
		}
	case "s":
		if m.tab != tabAgents {
			return m, nil
		}
		m.clearActionError()
		target := m.selectedTarget()
		m.scope = m.scope.toggle()
		m.err = saveScopeMode(m.scope)
		if m.err == nil {
			m.setNotice("Scope: " + m.scope.label())
		}
		m.applyAgentScope()
		m.restoreSelection(target)
		if selected, ok := m.selected(); ok {
			return m, m.requestPreview(selected)
		}
		m.preview, m.loading = previewData{}, false
	case "/":
		m.clearActionError()
		m.filter = true
		m.filterInputs[m.tab].SetValue(m.query)
		return m, m.filterInputs[m.tab].Focus()
	case "a":
		if m.tab == tabWorktrees {
			m.clearActionError()
			m.action = actionAddWorktree
			m.actionTextInput.SetValue("")
			m.lastClickTarget, m.lastClickAt = "", time.Time{}
			return m, m.actionTextInput.Focus()
		}
	case "r":
		m.clearActionError()
		if selected, ok := m.selected(); ok {
			m.beginRemove(selected)
		}
		m.lastClickTarget, m.lastClickAt = "", time.Time{}
	case "o":
		if m.tab != tabSessions {
			m.clearActionError()
			if selected, ok := m.selected(); ok {
				pr := m.gitItem(selected)
				m.err = nil
				return m, func() tea.Msg {
					return worktreeActionMsg{notice: fmt.Sprintf("Opened PR #%d", pr.prNumber), err: openPullRequest(selected.cwd, pr.prNumber)}
				}
			}
		}
	case "d":
		if m.tab != tabSessions {
			m.clearActionError()
			if selected, ok := m.selected(); ok {
				m.diff, m.loading, m.panelFocus = true, true, panelLeft
				m.diffRequest++
				return m, loadDiff(selected, m.diffRequest)
			}
		}
	case "enter":
		if selected, ok := m.selected(); ok {
			m.clearActionError()
			m.selection, m.chosen = selected, true
			return m, tea.Quit
		}
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "g", "home":
		if m.tab != tabSessions {
			m.index = 0
		}
	case "G", "end":
		if m.tab != tabSessions {
			m.index = max(0, len(m.rows())-1)
		}
	case "ctrl+u":
		m.scrollFocusedPanel(-m.previewVisibleHeight() / 2)
	case "ctrl+d":
		m.scrollFocusedPanel(m.previewVisibleHeight() / 2)
	case "pgup":
		m.scrollFocusedPanel(-m.previewVisibleHeight())
	case "pgdown":
		m.scrollFocusedPanel(m.previewVisibleHeight())
	case "h", "left":
		m.xOffset = max(0, m.xOffset-4)
	case "l", "right":
		m.xOffset = m.clampX(m.xOffset + 4)
	default:
		return m, nil
	}
	if key == "j" || key == "down" || key == "k" || key == "up" || (m.tab != tabSessions && (key == "g" || key == "G" || key == "home" || key == "end")) {
		if selected, ok := m.selected(); ok {
			return m, m.requestPreview(selected)
		}
	}
	return m, nil
}

func (m dashboardModel) handleThemePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m, m.closeThemePicker(false)
	case "enter":
		if len(m.themeOptions()) == 0 {
			return m, nil
		}
		return m, m.closeThemePicker(true)
	case "j", "down":
		m.selectTheme(m.themePickerIndex + 1)
	case "k", "up":
		m.selectTheme(m.themePickerIndex - 1)
	default:
		previous := m.themePickerInput.Value()
		var command tea.Cmd
		m.themePickerInput, command = m.themePickerInput.Update(msg)
		if m.themePickerInput.Value() != previous {
			m.selectTheme(0)
		}
		return m, command
	}
	return m, nil
}

func (m dashboardModel) handleActionMenuKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	entries := m.actionMenuEntries()
	switch key {
	case "esc":
		m.actionMenu = false
		return m, nil
	case "j", "down":
		if len(entries) > 0 {
			m.actionMenuIndex = (m.actionMenuIndex + 1) % len(entries)
		}
		return m, nil
	case "k", "up":
		if len(entries) > 0 {
			m.actionMenuIndex = (m.actionMenuIndex - 1 + len(entries)) % len(entries)
		}
		return m, nil
	case "enter":
		if len(entries) == 0 {
			return m, nil
		}
		entry := entries[m.actionMenuIndex%len(entries)]
		m.actionMenu = false
		m.clearActionError()
		switch entry.action {
		case menuOpen:
			if selected, ok := m.selected(); ok {
				m.selection, m.chosen = selected, true
				return m, tea.Quit
			}
		case menuDiff:
			if selected, ok := m.selected(); ok {
				m.diff, m.loading, m.panelFocus = true, true, panelLeft
				m.diffRequest++
				return m, loadDiff(selected, m.diffRequest)
			}
		case menuPR:
			if selected, ok := m.selected(); ok {
				pr := m.gitItem(selected)
				return m, func() tea.Msg {
					return worktreeActionMsg{notice: fmt.Sprintf("Opened PR #%d", pr.prNumber), err: openPullRequest(selected.cwd, pr.prNumber)}
				}
			}
		case menuCopyPath, menuCopyName, menuCopyPRURL:
			if selected, ok := m.selected(); ok {
				value, label := selected.cwd, "path"
				switch entry.action {
				case menuCopyName:
					if m.tab == tabSessions {
						value, label = selected.title, "session name"
					} else {
						value, label = m.gitItem(selected).branch, "branch"
					}
				case menuCopyPRURL:
					value, label = m.gitItem(selected).prURL, "PR URL"
				}
				return m, func() tea.Msg {
					return worktreeActionMsg{notice: "Copied " + label, err: clipboard.WriteAll(value)}
				}
			}
		case menuRename:
			if selected, ok := m.selected(); ok {
				return m, m.beginRename(selected)
			}
		case menuCleanup:
			if selected, ok := m.selected(); ok {
				m.beginCleanup(selected)
			}
		case menuRemove:
			if selected, ok := m.selected(); ok {
				m.beginRemove(selected)
			}
		}
		return m, nil
	default:
		return m, nil
	}
}

func (m dashboardModel) handleActionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch m.action {
	case actionAddWorktree, actionRenameSession:
		switch key {
		case "esc":
			m.action = actionNone
			m.actionTextInput.SetValue("")
			m.actionTextInput.Blur()
		case "enter":
			value := strings.TrimSpace(m.actionTextInput.Value())
			if value == "" {
				return m, nil
			}
			action, target := m.action, m.actionTarget
			m.action, m.err = actionRunning, nil
			m.actionTextInput.Blur()
			return m, func() tea.Msg {
				if action == actionRenameSession {
					return worktreeActionMsg{action: action, notice: "Renamed session to " + value, selectTarget: value, err: renameTmuxSession(target, value)}
				}
				return worktreeActionMsg{action: action, notice: "Created worktree " + value, err: addWorktree(m.cwd, value, backendAuto)}
			}
		default:
			var command tea.Cmd
			m.actionTextInput, command = m.actionTextInput.Update(msg)
			return m, command
		}
	case actionRemoveWorktree, actionRemoveSession, actionCleanupWorktree:
		switch key {
		case "enter":
			action, target, backend := m.action, m.actionTarget, m.actionBackend
			m.action, m.err = actionRunning, nil
			return m, func() tea.Msg {
				if action == actionRemoveSession {
					return worktreeActionMsg{action: action, notice: "Removed session " + target.title, err: removeTmuxSession(target)}
				}
				if action == actionCleanupWorktree {
					return worktreeActionMsg{action: action, notice: "Cleaned up stale worktree record", err: cleanupPrunableWorktree(m.cwd, target)}
				}
				return worktreeActionMsg{action: action, notice: "Removed worktree " + m.displayWorktree(target), err: removeWorktree(m.cwd, target.cwd, backend)}
			}
		case "esc":
			if m.action == actionRemoveSession {
				m.sessionSelectionAfterRemove = ""
			}
			m.action, m.actionTarget, m.actionBackend = actionNone, item{}, ""
		}
	}
	return m, nil
}

func (m dashboardModel) switchTab(tab int) (tea.Model, tea.Cmd) {
	if tab == m.tab || tab < 0 || tab >= tabCount {
		return m, nil
	}
	m.queries[m.tab], m.tabTargets[m.tab] = m.query, m.selectedTarget()
	m.filterInputs[m.tab].Blur()
	m.filter = false
	m.tab, m.index, m.previewOffset, m.rightOffset, m.xOffset, m.panelFocus, m.previewFocused = tab, 0, 0, 0, 0, panelLeft, false
	m.query = m.queries[m.tab]
	m.filterInputs[m.tab].SetValue(m.query)
	m.restoreSelection(m.tabTargets[m.tab])
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
func (m dashboardModel) previewBottomOffset(lines []string) int {
	return clampLineOffset(len(lines), lines, m.previewVisibleHeight())
}

func (m dashboardModel) livePreviewPaused() bool {
	if !m.preview.followBottom || m.preview.target == "" || m.previewOffset == m.previewBottomOffset(m.preview.lines) {
		return false
	}
	selected, ok := m.selected()
	return ok && (selected.kind == "session" || (selected.kind == "tmux-session" && selected.muxSessionID != ""))
}

func (m dashboardModel) previewFollowLabel() string {
	if !m.livePreviewPaused() {
		return ""
	}
	return fmt.Sprintf("PAUSED %d/%d", min(len(m.preview.lines), m.previewOffset+m.previewVisibleHeight()), len(m.preview.lines))
}
func (m dashboardModel) clampDiffOffset(offset int, lines []string) int {
	return clampLineOffset(offset, lines, m.diffVisibleHeight())
}

func (m dashboardModel) previewRenderWidth() int {
	if m.wideLayout() {
		return m.width - m.wideTableWidth()
	}
	return m.width
}

func (m dashboardModel) hasRightPanel() bool {
	if m.diff {
		rightWidth := max(24, m.width/4)
		return rightWidth < m.width-20
	}
	return m.tab == tabWorktrees && m.preview.rightTitle != "" && m.previewRenderWidth() >= 60
}

func (m *dashboardModel) focusPanelAt(x int) {
	m.previewFocused = true
	m.panelFocus = panelLeft
	if m.hasRightPanel() {
		leftWidth := m.width
		if m.diff {
			leftWidth -= max(24, m.width/4)
		} else {
			if m.wideLayout() {
				x -= m.wideTableWidth()
			}
			leftWidth = min(40, m.previewRenderWidth()/2)
		}
		if x >= leftWidth {
			m.panelFocus = panelRight
		}
	}
}

func (m *dashboardModel) scrollFocusedPanel(delta int) {
	m.previewFocused = true
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
		"↑/k, ↓/j     Move (Agents/Worktrees)",
		"g/Home, G/End First/last (Agents/Worktrees)",
		"Ctrl+j/k/n/p  Move (Sessions)",
		"Home/End      First/last (Sessions)",
		"Session icons  " + dashboardIcon(" live,  configured,  discovered", "L live, C configured, R discovered"),
		"Enter         Open selected row",
		"1–9           Open row (Agents/Worktrees)",
		"Click         Select row",
		"Double-click  Open row",
		"Mouse wheel   Scroll table or preview",
		"Tab/Shift+Tab Switch tabs",
		"/             Filter (Agents/Worktrees)",
		"Type          Search Sessions (g/G stay searchable)",
		"Space         Selected-item actions",
		"Ctrl+f        Cycle Session filter",
		"Ctrl+r        Toggle grouped/recent Session order",
		"Enter / Esc   Open / clear session search",
		"d             Open Git diff (Agents/Worktrees)",
		"Shift+←/→     Focus split panel",
		"a             Add worktree (Worktrees)",
		"r             Remove worktree (Worktrees)",
		"Ctrl+d        Remove live session (Sessions)",
		"o             Open pull request (Agents/Worktrees)",
		"PgUp/PgDn     Page diff/help; preview in Sessions",
		"Ctrl+u/d      Page preview, diff, or help",
		"              (Ctrl+d only removes in Sessions)",
		"G/End         Resume a paused live preview",
		"g/G, Home/End Top/bottom in diff or help",
		"h/l           Pan long lines",
		"Ctrl+v        Toggle this tab's runtime preview",
		"+/-           Resize preview by 10%",
		"s             Toggle agent scope",
		"t             Open theme picker",
		"Theme picker  Type filters; j/k browses",
		"              Enter applies; Esc cancels",
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
	m.previewOffset, m.rightOffset, m.xOffset, m.panelFocus, m.previewFocused = 0, 0, 0, panelLeft, false
}

func (m dashboardModel) rows() []item {
	var rows []item
	switch m.tab {
	case tabAgents:
		rows = m.agents
	case tabWorktrees:
		rows = m.worktrees
	case tabSessions:
		rows = slices.DeleteFunc(slices.Clone(m.sessions), func(item item) bool {
			switch m.sessionFilter {
			case sessionFilterLive:
				return item.muxSessionID == ""
			case sessionFilterInactive:
				return item.muxSessionID != ""
			case sessionFilterConfigured:
				return item.sessionSource != "config"
			case sessionFilterDiscovered:
				return item.sessionSource != "discovered"
			default:
				return false
			}
		})
	}
	if m.query == "" {
		if m.tab == tabSessions && m.sessionSortRecent {
			sort.SliceStable(rows, func(i, j int) bool {
				if !rows[i].lastAttached.Equal(rows[j].lastAttached) {
					return rows[i].lastAttached.After(rows[j].lastAttached)
				}
				left, right := sessionSortRank(rows[i]), sessionSortRank(rows[j])
				return left < right || (left == right && rows[i].title < rows[j].title)
			})
		}
		return rows
	}
	if m.tab == tabSessions {
		targets := make([]string, len(rows))
		for index, item := range rows {
			targets[index] = item.title + " " + compactHome(item.cwd) + " " + item.cwd
		}
		matches := fuzzy.Find(m.query, targets)
		filtered := make([]item, 0, len(matches))
		for _, match := range matches {
			filtered = append(filtered, rows[match.Index])
		}
		return filtered
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
				old.current, old.locked, old.prunable = old.current || fresh.current, fresh.locked, fresh.prunable
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
	dst.prNumber, dst.prState, dst.prDraft, dst.prCheck, dst.prFailedChecks, dst.prURL, dst.prLoaded = src.prNumber, src.prState, src.prDraft, src.prCheck, src.prFailedChecks, src.prURL, src.prLoaded
}

func (m dashboardModel) contentHeight() int { return max(0, m.height-3) }

func (m dashboardModel) previewShown() bool {
	return m.themePicker || m.actionMenu || m.action != actionNone || ((m.previewOverride[m.tab] == 1 || (m.previewOverride[m.tab] != -1 && m.previewEnabled[m.tab])) && (m.width >= 60 || m.previewOverride[m.tab] == 1))
}

func (m dashboardModel) wideLayout() bool {
	return !m.diff && !m.help && !m.themePicker && !m.actionMenu && m.width >= 140 && m.previewShown()
}

func (m dashboardModel) wideTableWidth() int { return max(60, m.width*11/20) }

func (m dashboardModel) tableHeight() int {
	content := m.contentHeight()
	if !m.previewShown() || m.wideLayout() {
		return content
	}
	return max(2, min(content-3, content*(100-m.previewSize)/100))
}
func (m dashboardModel) previewHeight() int {
	if !m.previewShown() {
		return 0
	}
	if m.wideLayout() {
		return m.contentHeight()
	}
	return m.contentHeight() - m.tableHeight()
}

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
