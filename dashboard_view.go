package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

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
		return paintDashboardBackground(strings.Join(lines, "\n"))
	}
	if m.help {
		return paintDashboardBackground(m.renderHelp(width))
	}
	if m.diff {
		return paintDashboardBackground(m.renderDiff(width))
	}
	parts := []string{m.renderHeader(width)}
	if m.wideLayout() {
		leftWidth := m.wideTableWidth()
		parts = append(parts, lipgloss.JoinHorizontal(lipgloss.Top, m.renderTable(leftWidth), m.renderPreview(width-leftWidth)))
	} else {
		parts = append(parts, m.renderTable(width))
		if m.previewShown() {
			parts = append(parts, m.renderPreview(width))
		}
	}
	parts = append(parts, m.renderFooter(width))
	return paintDashboardBackground(strings.Join(parts, "\n"))
}

func (m dashboardModel) renderHeader(width int) string {
	labels := m.tabLabels()
	parts := make([]string, tabCount)
	for tab, label := range labels {
		parts[tab] = tabView(label, m.tab == tab)
	}
	header := "  " + strings.Join(parts, borderStyle.Render(" │ "))
	mode, state := m.modeLabel(), m.refreshState()
	if mode != "" {
		status := infoStyle.Render(mode)
		if state != "" {
			fullStatus := status + mutedStyle.Render(" · "+state)
			if ansi.StringWidth(header+"  "+fullStatus) <= width {
				status = fullStatus
			}
		}
		header = ansi.Truncate(header, max(0, width-ansi.StringWidth(status)-2), "")
		header += strings.Repeat(" ", max(2, width-ansi.StringWidth(header)-ansi.StringWidth(status))) + status
	} else if state != "" && ansi.StringWidth(header+"  "+state) <= width {
		header += strings.Repeat(" ", max(2, width-ansi.StringWidth(header)-ansi.StringWidth(state))) + mutedStyle.Render(state)
	}
	return padANSI(header, width) + "\n" + borderStyle.Render(strings.Repeat("─", width))
}

func (m dashboardModel) refreshState() string {
	inFlight := [...]bool{m.agentsInFlight, m.worktreesInFlight, m.sessionsInFlight}[m.tab]
	if inFlight {
		return "refreshing"
	}
	viewErr := [...]error{m.agentErr, m.worktreeErr, m.sessionsErr}[m.tab]
	updated := m.lastRefresh[m.tab]
	if viewErr != nil {
		return "stale"
	}
	if updated.IsZero() {
		if (m.tab == tabAgents || m.tab == tabWorktrees) && len(m.gitCache) > 0 {
			return "cached"
		}
		return "waiting"
	}
	if m.now.Sub(updated) > staleThreshold {
		return "stale"
	}
	return ""
}

func (m dashboardModel) modeLabel() string {
	switch {
	case m.themePicker:
		return "THEME"
	case m.actionMenu:
		return "CONFIRM"
	case m.action != actionNone:
		if m.action == actionRunning {
			return "WORKING"
		}
		return "CONFIRM"
	case m.filter:
		return "SEARCH"
	case m.previewFocused:
		return "PREVIEW"
	default:
		return ""
	}
}

func (m dashboardModel) tabLabels() [tabCount]string {
	labels := [tabCount]string{
		fmt.Sprintf("Agents %d", len(m.agents)),
		fmt.Sprintf("Worktrees %d", len(m.worktrees)),
		fmt.Sprintf("Sessions %d", len(m.sessions)),
	}
	switch m.tab {
	case tabAgents:
		labels[tabAgents] = fmt.Sprintf("[Agents %d · %s]", len(m.agents), m.scope.label())
	case tabSessions:
		labels[tabSessions] = fmt.Sprintf("[Sessions %d/%d · %s]", len(m.rows()), len(m.sessions), m.sessionFilter.label())
	default:
		labels[m.tab] = "[" + labels[m.tab] + "]"
	}
	if 2+ansi.StringWidth(strings.Join(labels[:], " │ ")) > m.width {
		labels[tabAgents] = fmt.Sprintf("Agents %d", len(m.agents))
		if m.tab == tabAgents {
			labels[tabAgents] = "[" + labels[tabAgents] + "]"
		}
	}
	if m.tab == tabSessions && 2+ansi.StringWidth(strings.Join(labels[:], " │ ")) > m.width {
		labels[tabAgents] = fmt.Sprintf("A %d", len(m.agents))
		labels[tabWorktrees] = fmt.Sprintf("W %d", len(m.worktrees))
		labels[tabSessions] = fmt.Sprintf("[S %d/%d %s]", len(m.rows()), len(m.sessions), m.sessionFilter.label())
	}
	return labels
}

func (m dashboardModel) tabHitboxes() [tabCount][2]int {
	labels := m.tabLabels()
	var hitboxes [tabCount][2]int
	start := 2
	for tab, label := range labels {
		end := start + ansi.StringWidth(label)
		hitboxes[tab] = [2]int{start, end}
		start = end + ansi.StringWidth(" │ ")
	}
	return hitboxes
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
		switch m.tab {
		case tabWorktrees:
			label, loaded = "worktrees", m.worktreesLoaded
		case tabSessions:
			label, loaded = "sessions", m.sessionsLoaded
		}
		message := "No " + label + "."
		if !loaded {
			message = "Loading " + label + "…"
		} else if m.query != "" {
			if m.tab == tabSessions {
				message = "No sessions match “" + safeText(m.query) + "”. Esc clears search."
			} else {
				message = "No matches for /" + safeText(m.query) + ". Esc clears search."
			}
		} else if m.tab == tabSessions && m.sessionFilter != sessionFilterAll {
			message = "No " + strings.ToLower(m.sessionFilter.label()) + " sessions. Ctrl+f changes filter."
		} else {
			switch m.tab {
			case tabSessions:
				message += " Add [sessions.entries] or start tmux."
			case tabWorktrees:
				message += " Press a to add one."
			default:
				message += " Start Pi in tmux."
			}
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
	name, path, windows, last                         int
}

func (m dashboardModel) columns(width int, rows []item) tableColumns {
	if m.tab == tabSessions {
		name, windows, last := 9, 5, 11
		for _, item := range rows {
			name = min(28, max(name, ansi.StringWidth(item.title)+8))
		}
		return tableColumns{name: name, path: max(1, width-2-name-windows-last), windows: windows, last: last}
	}
	project, worktree, git, pr := 8, 9, 5, 4
	for _, item := range rows {
		gitItem := m.gitItem(item)
		gitText, prStatus := gitStatusText(gitItem, m.now), prText(gitItem, m.now)
		if m.tab == tabAgents {
			gitText, prStatus = compactGitStatusText(gitItem, m.now), compactPRText(gitItem, m.now)
		}
		project = max(project, ansi.StringWidth(projectName(item.cwd))+2)
		worktree = max(worktree, ansi.StringWidth(m.displayWorktree(item))+1)
		git = max(git, ansi.StringWidth(gitText)+1)
		pr = max(pr, ansi.StringWidth(prStatus))
	}
	project = min(project, 22)
	worktree = min(worktree, 26)
	git = min(git, 30)
	pr = min(pr, 20) + 1
	minimumFullWidth := 4 + 22 + pr + 4 + 5 + 8
	if m.tab == tabAgents {
		minimumFullWidth = 4 + 22 + pr + 7 + 10 + 8
	}
	if width < max(60, minimumFullWidth) {
		worktree = min(worktree, 12)
		git = min(git, 12)
		if m.tab == tabAgents {
			return tableColumns{worktree: worktree, git: git, status: 7, tail: max(1, width-4-worktree-git-7)}
		}
		return tableColumns{worktree: worktree, git: git, status: 4, tail: max(1, width-4-worktree-git-4)}
	}
	reserve := 4 + pr + 4 + 5 + 8
	if m.tab == tabAgents {
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
		}
	}
	fixed := 4 + project + worktree + git + pr
	if m.tab == tabAgents {
		fixed += 7 + 10
		return tableColumns{project: project, worktree: worktree, git: git, pr: pr, status: 7, elapsed: 10, tail: max(1, width-fixed)}
	}
	fixed += 4 + 5
	return tableColumns{project: project, worktree: worktree, git: git, pr: pr, status: 4, elapsed: 5, tail: max(1, width-fixed)}
}

func (m dashboardModel) tableHeader(width int, c tableColumns) string {
	cells := []string{}
	if m.tab == tabSessions {
		cells = append(cells, cell("Session", c.name), cell("Path", c.path), cell("Win", c.windows), cell("Last", c.last))
		return headerStyle.Render("  " + ansi.Truncate(strings.Join(cells, ""), width-2, ""))
	}
	cells = append(cells, cell("#", 2), cell("Project", c.project), cell("Worktree", c.worktree), cell("Git", c.git), cell("PR", c.pr))
	if m.tab == tabAgents {
		cells = append(cells, cell("Status", c.status), cell("Time", c.elapsed), cell("Title", c.tail))
	} else {
		cells = append(cells, cell("Mux", c.status), cell("Age", c.elapsed), cell("Agent", c.tail))
	}
	return headerStyle.Render("  " + ansi.Truncate(strings.Join(cells, ""), width-2, ""))
}

func (m dashboardModel) tableRow(item item, index, width int, c tableColumns) string {
	gitItem := m.gitItem(item)
	var background *lipgloss.AdaptiveColor
	if index == m.index {
		background = &selectedColor
	}
	worktreeStyle := textStyle
	prefix := renderCell(textStyle, "  ", 2, background)
	if index == m.index {
		prefix = renderCell(infoStyle, "▌ ", 2, background)
	} else if item.current {
		prefix = renderCell(textStyle, "▏ ", 2, background)
	}

	line := prefix
	if m.tab == tabSessions {
		icon, style := sessionIcon(item)
		windows, last := "-", "-"
		if item.muxSessionID != "" {
			windows = strconv.Itoa(item.tmuxWindows)
		}
		if !item.lastAttached.IsZero() {
			last = relativeAge(item.lastAttached)
		}
		name := icon + " " + item.title
		line += renderCell(style, name, c.name, background) + renderCell(textStyle, compactHome(item.cwd), c.path, background) + renderCell(mutedStyle, windows, c.windows, background) + renderCell(mutedStyle, last, c.last, background)
		return padANSIBackground(line, width, background)
	}
	jumpKey := ""
	if index < 9 {
		jumpKey = strconv.Itoa(index + 1)
	}
	line += renderCell(keycapStyle, jumpKey, 2, background) + renderCell(worktreeStyle, projectName(item.cwd), c.project, background) + renderCell(worktreeStyle, m.displayWorktree(item), c.worktree, background)
	if m.tab == tabAgents {
		line += compactGitStatusCell(gitItem, c.git, m.now, background) + compactPRCell(gitItem, c.pr, m.now, background)
		line += statusCell(item, c.status, m.now, background) + elapsedCell(item.updated, c.elapsed, m.now, background) + renderCell(worktreeStyle, item.title, c.tail, background)
	} else {
		line += gitStatusCell(gitItem, c.git, m.now, background) + prCell(gitItem, c.pr, m.now, background)
		age := "-"
		if !item.updated.IsZero() {
			age = relativeAge(item.updated)
		}
		line += muxCell(item, c.status, background) + renderCell(mutedStyle, age, c.elapsed, background) + agentSummaryCell(item, c.tail, m.now, background, m.agentsLoaded, m.agentErr)
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
	if m.themePicker {
		return m.renderThemePicker(width, height)
	}
	if m.actionMenu {
		return m.renderActionMenu(width, height)
	}
	if m.action == actionRemoveWorktree || m.action == actionRemoveSession || m.action == actionCleanupWorktree {
		title := "Remove worktree"
		switch m.action {
		case actionRemoveSession:
			title = "Remove session"
		case actionCleanupWorktree:
			title = "Clean up stale record"
		}
		return renderPanel(title, m.removePreviewLines(), width, height, 0, 0, false, true)
	}
	if m.action == actionRenameSession {
		return renderPanel("Rename session", []string{"Session: " + safeText(m.actionTarget.title), "", "Enter a new live tmux session name."}, width, height, 0, 0, false, true)
	}
	selected, hasSelection := m.selected()
	if !hasSelection {
		loaded := m.agentsLoaded
		switch m.tab {
		case tabWorktrees:
			loaded = m.worktreesLoaded
		case tabSessions:
			loaded = m.sessionsLoaded
		}
		message := "No selection."
		if !loaded {
			message = "Loading…"
		}
		return renderPanel("Preview", []string{message}, width, height, 0, 0, false, true)
	}
	if m.loading || m.preview.target == "" || m.preview.target != selected.target {
		return renderPanel(m.previewTitle(selected, "Preview: "+m.displayWorktree(selected)), []string{"Loading…"}, width, height, 0, 0, false, true)
	}
	title := m.previewTitle(selected, m.preview.title)
	if m.tab != tabWorktrees || m.preview.rightTitle == "" || width < 60 {
		return renderPanel(title, m.preview.lines, width, height, m.previewOffset, m.xOffset, false, true)
	}
	leftWidth := min(40, width/2)
	left := renderPanel(title, m.preview.lines, leftWidth, height, m.previewOffset, m.xOffset, false, m.panelFocus == panelLeft)
	right := renderPanel(m.preview.rightTitle, styleGitLog(m.preview.rightLines), width-leftWidth, height, m.rightOffset, m.xOffset, false, m.panelFocus == panelRight)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m dashboardModel) previewTitle(selected item, title string) string {
	identity := selected.title
	if identity == "" {
		identity = m.displayWorktree(selected)
	}
	state := "cached"
	switch selected.kind {
	case "tmux-session":
		if selected.muxSessionID != "" {
			state = "live"
		} else if selected.sessionSource == "config" {
			state = "configured"
		} else {
			state = "discovered"
		}
	case "session":
		if selected.status != "" {
			state = selected.status
		}
	case "worktree":
		if selected.gitLoaded {
			state = "clean"
			if selected.dirty {
				state = "dirty"
			}
		}
	}
	feedback := m.previewFollowLabel()
	if feedback != "" {
		return title + " · " + safeText(identity) + " · " + state + " · " + feedback
	}
	return title + " · " + safeText(identity) + " · " + state
}

func (m dashboardModel) renderActionMenu(width, height int) string {
	entries := m.actionMenuEntries()
	lines := make([]string, 0, len(entries))
	selected := 0
	if len(entries) > 0 {
		selected = m.actionMenuIndex % len(entries)
	}
	for index, entry := range entries {
		prefix, style := "  ", mutedStyle
		if index == selected {
			prefix, style = "▌ ", infoStyle
		}
		lines = append(lines, style.Render(prefix+entry.label))
	}
	if len(lines) == 0 {
		lines = []string{mutedStyle.Render("No actions available.")}
	}
	offset := max(0, selected-max(1, height-2)+1)
	return renderPanel("Actions", lines, width, height, offset, 0, false, true)
}

func (m dashboardModel) removePreviewLines() []string {
	if m.action == actionCleanupWorktree {
		return []string{
			"Selected: " + safeText(compactHome(m.actionTarget.cwd)),
			"",
			"This runs git worktree prune --expire now.",
			"Git removes all stale unlocked worktree records.",
			"It does not remove a live worktree or unlock a locked one.",
			"",
			"Enter Prune    Esc Cancel",
		}
	}
	if m.action == actionRemoveSession {
		return []string{
			"Session: " + safeText(m.actionTarget.title),
			"",
			"This kills the live tmux session. Configured entries stay in config.toml.",
			"",
			"Enter Remove    Esc Cancel",
		}
	}
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
		"Enter Remove    Esc Cancel",
	}
}

func (m dashboardModel) renderFooter(width int) string {
	if m.themePicker {
		return m.renderThemePickerFooter(width)
	}
	if m.actionMenu {
		return padANSI("  "+footerCommand("j/k", "Move")+" "+footerCommand("Enter", "Run")+" "+footerCommand("Esc", "Cancel"), width)
	}
	switch m.action {
	case actionAddWorktree, actionRenameSession:
		input := m.actionTextInput
		styleTextInput(&input)
		label, actionLabel := "branch: ", "Create"
		if m.action == actionRenameSession {
			label, actionLabel = "name: ", "Rename"
		}
		prefix := "  " + textStyle.Render(label)
		create := footerCommand("Enter", actionLabel)
		if width < 60 {
			create = footerCommand("↵", actionLabel)
		}
		suffix := "  " + create + " " + footerCommand("Esc", "Cancel")
		input.Width = max(0, width-ansi.StringWidth(prefix)-ansi.StringWidth(suffix)-1)
		input.SetCursor(input.Position())
		return padANSI(prefix+input.View()+suffix, width)
	case actionRemoveWorktree, actionRemoveSession, actionCleanupWorktree:
		name, verb := m.displayWorktree(m.actionTarget), "Remove"
		if m.action == actionRemoveSession {
			name = m.actionTarget.title
		}
		if m.action == actionCleanupWorktree {
			name, verb = "stale record", "Clean up"
		}
		return padANSI("  "+warningStyle.Render(verb+" "+safeText(name)+"?")+"  "+footerCommand("Enter", verb)+" "+footerCommand("Esc", "Cancel"), width)
	case actionRunning:
		return padANSI("  "+infoStyle.Render("Working…"), width)
	}

	viewErr := m.agentErr
	switch m.tab {
	case tabWorktrees:
		viewErr = m.worktreeErr
	case tabSessions:
		viewErr = m.sessionsErr
	}
	if m.err != nil {
		viewErr = m.err
	}
	if viewErr != nil {
		return m.renderFooterError(width, viewErr)
	}
	if m.notice != "" && m.now.Before(m.noticeUntil) {
		return padANSI("  "+successStyle.Render(dashboardIcon("󰄬", "OK")+" "+safeText(m.notice)), width)
	}
	if m.filter {
		input := m.filterInputs[m.tab]
		styleTextInput(&input)
		if m.tab == tabSessions {
			separator := borderStyle.Render(" │ ")
			base := []string{footerCommand("Esc", "Clear"), footerCommand("^c", "Quit")}
			optional := []string{footerCommand("^j/k/n/p", "Move"), footerCommand("↵", "Open")}
			if selected, ok := m.selected(); ok && selected.muxSessionID != "" {
				optional = append(optional, footerCommand("^d", "Remove"))
			}
			controls := []string{}
			for _, candidate := range optional {
				next := append(append([]string{}, controls...), candidate)
				suffix := "  " + strings.Join(append(next, base...), separator)
				if 2+ansi.StringWidth(input.Prompt)+2+ansi.StringWidth(suffix) <= width {
					controls = next
				}
			}
			controls = append(controls, base...)
			suffix := "  " + strings.Join(controls, separator)
			input.Width = max(1, width-ansi.StringWidth(suffix)-ansi.StringWidth(input.Prompt)-3)
			input.SetCursor(input.Position())
			return padANSI("  "+input.View()+suffix, width)
		}
		input.Width = max(1, width-30)
		input.SetCursor(input.Position())
		return padANSI("  "+input.View()+"  "+footerCommand("Enter", "Accept")+" "+footerCommand("Esc", "Clear"), width)
	}
	filterLabel := "Filter"
	if m.query != "" {
		filterLabel = "Filter:" + truncate(safeText(m.query), 12)
	}
	nextTab := [...]string{"Worktrees", "Sessions", "Agents"}[m.tab]
	if m.tab == tabSessions {
		sortLabel := "Grouped"
		if m.sessionSortRecent {
			sortLabel = "Recent"
		}
		candidates := []string{footerCommand("^j/k/n/p", "Move"), footerCommand("↵", "Open"), footerCommand("^f", m.sessionFilter.label()), footerCommand("^r", sortLabel)}
		if m.query == "" {
			candidates = append(candidates, footerCommand("type", "Search"))
		} else {
			candidates = append(candidates, footerCommand("Esc", "Clear"))
		}
		candidates = append(candidates, footerCommand("?", "Help"), footerCommand("Tab", "Next"))
		if selected, ok := m.selected(); ok && selected.muxSessionID != "" {
			candidates = append(candidates, footerCommand("^d", "Remove"))
		}
		candidates = append(candidates, footerCommand("Space", "Actions"))
		return m.prioritizedFooterWithBase(width, candidates, []string{footerCommand("^c", "Quit")})
	}
	candidates := []string{footerCommand("↵", "Open"), footerCommand("d", "Diff")}
	switch m.tab {
	case tabWorktrees:
		candidates = append(candidates, footerCommand("a", "Add"), footerCommand("r", "Remove"))
	}
	if selected, ok := m.selected(); ok && m.gitItem(selected).prNumber != 0 {
		candidates = append(candidates, footerCommand("o", "PR"))
	}
	if m.tab == tabAgents {
		candidates = append(candidates, footerToggle("s", "Scope ("+m.scope.label()+")", m.scope == scopeSession))
	}
	candidates = append(candidates, footerCommand("/", filterLabel), footerCommand("Tab", nextTab), footerCommand("t", "Theme"), footerCommand("Space", "Actions"))
	return m.prioritizedFooter(width, candidates)
}

func (m dashboardModel) prioritizedFooter(width int, candidates []string) string {
	return m.prioritizedFooterWithBase(width, candidates, []string{footerCommand("?", "Help"), footerCommand("q", "Quit")})
}

func (m dashboardModel) prioritizedFooterWithBase(width int, candidates, base []string) string {
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
	helpQuit := []string{footerCommand("Esc", "Dismiss"), footerCommand("?", "Help"), footerCommand("q", "Quit")}
	if m.tab == tabSessions {
		helpQuit = []string{footerCommand("Esc", "Dismiss"), footerCommand("^c", "Quit")}
	}
	separator := borderStyle.Render(" │ ")
	available := max(1, width-2-ansi.StringWidth(strings.Join(helpQuit, separator))-ansi.StringWidth(separator))
	prefix := dashboardIcon("󰅙", "!") + " Error: "
	message := dangerStyle.Render(ansi.Truncate(prefix+safeText(err.Error()), available, "…"))
	return padANSI("  "+strings.Join(append([]string{message}, helpQuit...), separator), width)
}

func dashboardIcon(nerd, plain string) string {
	if !nerdFontEnabled || os.Getenv("JUMPMUX_PLAIN") != "" {
		return plain
	}
	return nerd
}

func sessionIcon(session item) (string, lipgloss.Style) {
	switch {
	case session.muxSessionID != "":
		return dashboardIcon("", "L"), infoStyle
	case session.sessionSource == "config":
		return dashboardIcon("", "C"), mutedStyle
	default:
		return dashboardIcon("", "R"), mutedStyle
	}
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

func (m dashboardModel) themePickerLines() ([]string, int) {
	themes := m.themeOptions()
	if len(themes) == 0 {
		return []string{mutedStyle.Render("No themes match \"" + safeText(m.themePickerInput.Value()) + "\".")}, 0
	}
	lines, selectedLine := make([]string, 0, len(themes)), 0
	for index, scheme := range themes {
		if index == m.themePickerIndex {
			selectedLine = len(lines)
			lines = append(lines, infoStyle.Render("▌ ")+textStyle.Render(scheme.slug()))
			continue
		}
		lines = append(lines, "  "+mutedStyle.Render(scheme.slug()))
	}
	return lines, selectedLine
}

func (m dashboardModel) renderThemePicker(width, height int) string {
	lines, _ := m.themePickerLines()
	themes := m.themeOptions()
	title := "Theme"
	if len(themes) > 0 {
		title = fmt.Sprintf("Theme: %s (%d/%d)", m.scheme.slug(), m.themePickerIndex+1, len(themes))
	}
	return renderPanel(title, lines, width, height, m.themePickerOffset, 0, false, true)
}

func (m dashboardModel) renderThemePickerFooter(width int) string {
	input := m.themePickerInput
	styleTextInput(&input)
	apply := footerCommand("Enter", "Apply")
	if width < 60 {
		apply = footerCommand("↵", "Apply")
	}
	suffix := "  " + apply + " " + footerCommand("Esc", "Cancel")
	input.Width = max(0, width-3-ansi.StringWidth(input.Prompt)-ansi.StringWidth(suffix))
	input.SetCursor(input.Position())
	return padANSI("  "+input.View()+suffix, width)
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
	footer := m.prioritizedFooterWithBase(width,
		[]string{footerCommand("j/k", "Scroll"), footerCommand("PgUp/Dn", "Page"), footerCommand("g/G", "Ends"), footerCommand("Shift+←/→", "Focus")},
		[]string{footerCommand("q/Esc", "Close")},
	)
	return body + "\n" + footer
}

func renderPanel(title string, lines []string, width, height, offset, xOffset int, diff, focused bool) string {
	width, height = max(4, width), max(3, height)
	innerWidth, innerHeight := width-2, height-2
	offset = min(max(0, offset), max(0, len(lines)-innerHeight))

	panelBorderStyle := borderStyle
	if focused {
		title = "▶ " + title
		panelBorderStyle = activeBorderStyle
	}
	title = " " + safeText(title) + " "
	title = ansi.Truncate(title, max(0, innerWidth-1), "")
	topLeft := panelBorderStyle.Render("┌─") + headerStyle.Render(title)
	top := topLeft + panelBorderStyle.Render(strings.Repeat("─", max(0, width-ansi.StringWidth(topLeft)-1))+"┐")
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
		out = append(out, panelBorderStyle.Render("│")+padANSI(line, innerWidth)+panelBorderStyle.Render("│"))
	}
	out = append(out, panelBorderStyle.Render("└"+strings.Repeat("─", innerWidth)+"┘"))
	return strings.Join(out, "\n")
}
