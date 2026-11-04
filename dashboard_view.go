package main

import (
	"cmp"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode"

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
	if m.confirmationNeedsFullHeight() {
		parts = append(parts, m.renderPreview(width))
	} else if m.actionSidebar() {
		sidebarWidth := m.actionSidebarWidth()
		parts = append(parts, lipgloss.JoinHorizontal(lipgloss.Top, m.renderTable(width-sidebarWidth), m.renderActionMenu(sidebarWidth, m.contentHeight()+1)))
	} else if m.wideLayout() {
		leftWidth := m.wideTableWidth()
		parts = append(parts, lipgloss.JoinHorizontal(lipgloss.Top, m.renderTable(leftWidth), m.renderPreview(width-leftWidth)))
	} else {
		parts = append(parts, m.renderTable(width))
		if m.previewShown() {
			parts = append(parts, m.renderPreview(width))
		}
	}
	if !m.actionSidebar() {
		parts = append(parts, m.renderFooter(width))
	}
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
	if m.refreshPaused() {
		return ""
	}
	inFlight := [...]bool{m.agentsInFlight, m.worktreesInFlight, m.sessionsInFlight}[m.tab]
	loaded := [...]bool{m.agentsLoaded, m.worktreesLoaded, m.sessionsLoaded}[m.tab]
	if inFlight && !loaded {
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
		return "ACTIONS"
	case m.action == actionRunning || m.action == actionAgentNaming:
		return "WORKING"
	case m.action != actionNone:
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
	liveSessions := 0
	for _, session := range m.sessions {
		if session.muxSessionID != "" {
			liveSessions++
		}
	}
	labels := [tabCount]string{
		fmt.Sprintf("Agents %d", len(m.agents)),
		fmt.Sprintf("Worktrees %d", len(m.worktrees)),
		fmt.Sprintf("Sessions %d/%d", liveSessions, len(m.sessions)),
	}
	switch m.tab {
	case tabAgents:
		labels[tabAgents] = fmt.Sprintf("[Agents %d · %s]", len(m.agents), m.scope.displayLabel())
	case tabSessions:
		labels[tabSessions] = fmt.Sprintf("[Sessions %d/%d · %s]", liveSessions, len(m.sessions), m.sessionFilter.label())
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
		labels[tabSessions] = fmt.Sprintf("[S %d/%d %s]", liveSessions, len(m.sessions), m.sessionFilter.label())
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
		} else if m.tab == tabWorktrees && m.worktreeErr != nil && strings.Contains(strings.ToLower(m.worktreeErr.Error()), "not a git repository") {
			message = "Not a Git repo. Run jumpmux in one."
		} else if m.query != "" {
			if m.tab == tabSessions {
				message = "No sessions match “" + safeText(m.query) + "”. Esc clears search."
			} else {
				message = "No matches for /" + safeText(m.query) + ". Esc clears search."
			}
		} else if m.tab == tabSessions && m.sessionFilter != sessionFilterAll {
			message = "No " + strings.ToLower(m.sessionFilter.label()) + " sessions. Ctrl+f changes filter."
		} else if m.tab == tabAgents && m.scope == scopeSession {
			message = "No agents in this tmux session. Press s to show all."
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
	var background *lipgloss.AdaptiveColor
	if index == m.index {
		background = &selectedColor
	}
	worktreeStyle := textStyle
	marker := "  "
	if index == m.index {
		marker = "> "
	} else if item.current {
		marker = "▌ "
	}
	prefix := renderCell(textStyle, marker, 2, background)

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
	gitItem := m.gitItem(item)
	jumpKey := ""
	if index < 9 {
		jumpKey = strconv.Itoa(index + 1)
	}
	line += renderCell(keycapStyle, jumpKey, 2, background) + renderCell(worktreeStyle, projectName(item.cwd), c.project, background) + renderCell(worktreeStyle, m.displayWorktree(item), c.worktree, background)
	if m.tab == tabAgents {
		line += compactGitStatusCell(gitItem, c.git, m.now, background) + compactPRCell(gitItem, c.pr, m.now, background)
		line += statusCell(item, c.status, m.now, background) + elapsedCell(item.updated, c.elapsed, m.now, background) + m.agentTitleCell(item, worktreeStyle, c.tail, background)
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

// agentTitle returns the agent's title and a tmux location suffix for when the
// title alone is not enough: " @session:window" for an agent outside the
// dashboard's session, so a jump is visibly a session switch, and the pane
// locator when another agent would otherwise render an identical row. The title
// is shortened first so the suffix survives truncation in a cell of the given
// width. tag reports whether the suffix is a session tag; a tag that cannot fit
// beside a readable title is dropped rather than cut.
func (m dashboardModel) agentTitle(agent item, width int) (title, suffix string, tag bool) {
	title = safeText(agent.title)
	// cell() reserves one trailing column as the gap.
	available := max(0, width-1)
	locator := ""
	if m.duplicateAgentRow(agent) {
		locator = paneLocator(agent)
	}
	session, window := safeText(agent.muxSessionName), safeText(agent.muxWindowIndex)
	if m.launchSessionID != "" && agent.muxSessionID != "" && agent.muxSessionID != m.launchSessionID && session != "" {
		room := available - min(ansi.StringWidth(title), max(4, available/2))
		suffix = sessionTag(session, cmp.Or(locator, window), room)
		if suffix == "" && locator != "" {
			suffix = sessionTag(session, window, room)
		}
		tag = suffix != ""
	} else if locator != "" {
		suffix = ansi.Truncate(" · "+locator, available, "")
	}
	if suffix != "" {
		title = ansi.Truncate(title, max(0, available-ansi.StringWidth(suffix)), "…")
	}
	return title, suffix, tag
}

// sessionTag formats " @session:where" within room columns, shortening the
// session name with … so the window stays visible. It returns "" when even a
// shortened name does not fit.
func sessionTag(session, where string, room int) string {
	tail := ""
	if where != "" {
		tail = ":" + where
	}
	sessionRoom := room - ansi.StringWidth(" @"+tail)
	if sessionRoom < min(ansi.StringWidth(session), 2) {
		return ""
	}
	return " @" + ansi.Truncate(session, sessionRoom, "…") + tail
}

// agentTitleCell paints a session tag apart from the title so it reads as a
// location; the text itself still marks it for plain and NO_COLOR terminals.
func (m dashboardModel) agentTitleCell(agent item, style lipgloss.Style, width int, background *lipgloss.AdaptiveColor) string {
	title, suffix, tag := m.agentTitle(agent, width)
	if !tag {
		return renderCell(style, title+suffix, width, background)
	}
	return padANSIBackground(paint(withBackground(style, background), title)+paint(withBackground(infoStyle, background), suffix), width, background)
}

// duplicateAgentRow reports whether another agent would render an identical
// row: same project, worktree, and title.
func (m dashboardModel) duplicateAgentRow(agent item) bool {
	title, project, worktree := safeText(agent.title), projectName(agent.cwd), ""
	for _, other := range m.agents {
		// Compare titles first; the worktree lookup scans every worktree, and this
		// runs for each agent row on every frame.
		if other.target == agent.target || safeText(other.title) != title || projectName(other.cwd) != project {
			continue
		}
		if worktree == "" {
			worktree = m.displayWorktree(agent)
		}
		if m.displayWorktree(other) == worktree {
			return true
		}
	}
	return false
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

// prompting reports whether the preview panel shows a prompt instead of a preview.
func (m dashboardModel) prompting() bool {
	return m.confirming() || m.action == actionAddWorktree
}

// confirmationNeedsFullHeight takes the table's rows when the full prompt does
// not fit the preview panel; renderPreview falls back to the compact prompt
// only when even the full height is too short.
func (m dashboardModel) confirmationNeedsFullHeight() bool {
	return m.prompting() && m.previewHeight() < len(m.promptLines(false))+2
}

func (m dashboardModel) renderPreview(width int) string {
	height := m.previewHeight()
	if m.confirmationNeedsFullHeight() {
		height = m.contentHeight()
	}
	if m.themePicker {
		return m.renderThemePicker(width, height)
	}
	if m.actionMenu {
		return m.renderActionMenu(width, height)
	}
	if m.prompting() {
		title := "Remove worktree"
		switch m.action {
		case actionAddWorktree:
			title = "Add worktree"
		case actionRemoveSession:
			title = "Remove session"
		case actionRemoveAgent:
			title = "Remove agent"
		case actionCleanupWorktree:
			title = "Clean up stale record"
		case actionRebaseWorktree:
			title = "Rebase worktree"
		case actionMergeWorktree:
			title = "Merge worktree"
		}
		lines := m.promptLines(false)
		if len(lines) > height-2 {
			lines = m.promptLines(true)
		}
		if width >= 60 {
			padded := make([]string, len(lines))
			for index, line := range lines {
				if line != "" {
					padded[index] = " " + line
				}
			}
			lines = padded
		}
		return renderPanel(title, lines, width, height, 0, 0, false, true)
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
	lines := m.preview.lines
	if selected.kind == "session" {
		lines = append([]string(nil), lines...)
		index := 0
		if location := paneLocation(selected); location != "" && len(lines) > index && lines[index] == "Pane "+location {
			lines[index] = mutedStyle.Render("Pane ") + textStyle.Render(location)
			index++
		}
		git := m.gitItem(selected)
		if failed := failedChecksPreview(git.prFailedChecks); git.prCheck == checkFailure && failed != "" && len(lines) > index && lines[index] == failed {
			lines[index] = failedChecksPreviewStyled(git.prFailedChecks)
		}
	}
	if m.tab != tabWorktrees || m.preview.rightTitle == "" || width < 60 {
		return renderPanel(title, lines, width, height, m.previewOffset, m.xOffset, false, true)
	}
	leftWidth := min(40, width/2)
	left := renderPanel(title, lines, leftWidth, height, m.previewOffset, m.xOffset, false, m.panelFocus == panelLeft)
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
	selected := 0
	if len(entries) > 0 {
		selected = m.actionMenuIndex % len(entries)
	}
	if !m.actionSidebar() {
		lines := make([]string, 0, len(entries))
		for index, entry := range entries {
			style, marker := textStyle, "  "
			if index == selected {
				style, marker = selectedStyle.Inherit(textStyle), "> "
			}
			lines = append(lines, style.Render(marker+"["+entry.key+"] "+entry.label))
		}
		if len(lines) == 0 {
			lines = []string{mutedStyle.Render("No actions available.")}
		}
		offset := max(0, selected-max(1, height-2)+1)
		return renderPanel("Actions", lines, width, height, offset, 0, false, true)
	}
	lines := make([]string, 0, len(entries)+8)
	selectedItem, _ := m.selected()
	identity := selectedItem.title
	if m.tab != tabSessions {
		identity = m.displayWorktree(selectedItem)
	}
	lines = append(lines, headerStyle.Render(truncate(safeText(identity), max(1, width-6))))
	if selectedItem.cwd != "" {
		lines = append(lines, mutedStyle.Render(truncate(safeText(compactHome(selectedItem.cwd)), max(1, width-6))))
	}
	lines = append(lines, "")
	lastGroup, selectedLine := "", 0
	for index, entry := range entries {
		group := actionMenuGroup(entry.action)
		if group != lastGroup {
			if lastGroup != "" {
				lines = append(lines, "")
			}
			lines = append(lines, mutedStyle.Render(strings.ToUpper(group)))
			lastGroup = group
		}
		line := "  [" + entry.key + "] " + entry.label
		if index == selected {
			selectedLine = len(lines)
			line = selectedStyle.Inherit(textStyle).Width(width - 2).Render("> [" + entry.key + "] " + entry.label)
		} else if entry.action == menuRemove {
			line = dangerStyle.Render(line)
		} else {
			line = textStyle.Render(line)
		}
		lines = append(lines, line)
	}
	if len(entries) == 0 {
		lines = append(lines, mutedStyle.Render("No actions available."))
	}
	offset := max(0, selectedLine-max(1, height-2)+1)
	title := [...]string{"AGENT ACTIONS", "WORKTREE ACTIONS", "SESSION ACTIONS"}[m.tab]
	return renderActionSidebar(title, lines, width, height, offset)
}

func renderActionSidebar(title string, lines []string, width, height, offset int) string {
	width, height = max(20, width), max(6, height)
	innerWidth, innerHeight := width-2, height-2
	offset = min(max(0, offset), max(0, len(lines)-innerHeight))
	titleView := headerStyle.Render(" " + title + " ")
	top := activeBorderStyle.Render("┌") + titleView + activeBorderStyle.Render(strings.Repeat("─", max(0, width-ansi.StringWidth(titleView)-2))+"┐")
	out := []string{top}
	for index := 0; index < innerHeight; index++ {
		line := ""
		if lineIndex := offset + index; lineIndex < len(lines) {
			line = clipLine(lines[lineIndex], innerWidth, 0)
		}
		out = append(out, activeBorderStyle.Render("│")+padANSI(line, innerWidth)+activeBorderStyle.Render("│"))
	}
	hint := mutedStyle.Render(" ↑↓ Navigate · key/↵ Run · Esc ")
	out = append(out, activeBorderStyle.Render("└")+hint+activeBorderStyle.Render(strings.Repeat("─", max(0, innerWidth-ansi.StringWidth(hint)))+"┘"))
	return strings.Join(out, "\n")
}

func actionMenuGroup(action menuAction) string {
	switch action {
	case menuOpen, menuDiff, menuPR, menuPreviousSession:
		return "Open"
	case menuAddWorktree:
		return "Create"
	case menuMarkAgentSeen:
		return "Attention"
	case menuRebase, menuMerge, menuCleanup:
		return "Manage"
	case menuRemove:
		return "Danger"
	default:
		return "Actions"
	}
}

// promptOption explains a footer choice; the label matches the footer's, so
// the panel never repeats the keys.
func promptOption(label, note string, noteStyle lipgloss.Style) string {
	return paint(headerStyle, fmt.Sprintf("%-8s", label)) + " " + noteStyle.Render(note)
}

// promptStatus renders a status line whose words carry the meaning; the icon
// and color only reinforce them.
func promptStatus(style lipgloss.Style, nerd, plain, text string) string {
	return style.Render(dashboardIcon(nerd, plain) + " " + text)
}

func promptDetail(text string) string {
	return mutedStyle.Render("  " + text)
}

// worktreePrompt lays out a worktree prompt: branch and path, then the body.
// The keys live in the footer. The compact form drops the heading so the body
// still fits a short panel.
func worktreePrompt(name, path string, body []string, compact bool) []string {
	var lines []string
	if !compact {
		if name != "" {
			lines = append(lines, headerStyle.Render(safeText(name)))
		}
		if path != "" {
			lines = append(lines, mutedStyle.Render(safeText(compactHome(path))))
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
	}
	return append(lines, body...)
}

func (m dashboardModel) promptLines(compact bool) []string {
	target := m.actionTarget
	switch m.action {
	case actionAddWorktree:
		return m.addWorktreeLines(compact)
	case actionRemoveWorktree:
		return m.removeWorktreeLines(compact)
	case actionRebaseWorktree:
		return worktreePrompt(m.displayWorktree(target), target.cwd, []string{
			textStyle.Render("Will rebase " + safeText(target.branch) + " onto the local default branch."),
			promptDetail("Commits may be rewritten. Conflicts leave the rebase open."),
			promptDetail("This does not fetch or remove the worktree."),
		}, compact)
	case actionMergeWorktree:
		return worktreePrompt(m.displayWorktree(target), target.cwd, []string{
			textStyle.Render("Will merge " + safeText(target.branch) + " into the local default branch."),
			promptDetail("Needs clean worktrees; keeps it."),
			promptDetail("Fast-forward only; commits stay intact."),
		}, compact)
	case actionCleanupWorktree:
		return worktreePrompt("", "", []string{
			textStyle.Render("Will remove stale record " + safeText(compactHome(target.cwd)) + "."),
			promptDetail("Other worktrees and the branch stay."),
		}, compact)
	case actionRemoveAgent:
		return []string{
			"Will close Pi in tmux pane " + safeText(target.pane) + ".",
			"Other panes and the worktree stay.",
		}
	case actionRemoveSession:
		return []string{
			"Will kill tmux session " + safeText(target.title) + ".",
			"Configured entry stays in config.toml.",
		}
	}
	return nil
}

func (m dashboardModel) addWorktreeLines(compact bool) []string {
	branch := strings.TrimSpace(m.actionTextInput.Value())
	path := ""
	var body []string
	switch {
	case branch == "":
		body = []string{promptDetail("Type a branch name. Existing branches are reused.")}
	case m.branchRefsErr != nil:
		body = []string{promptStatus(dangerStyle, "󰅙", "X", "Could not read branches: "+safeText(m.branchRefsErr.Error()))}
	case m.branchRefs == nil:
		body = []string{promptDetail("Checking branches…")}
	case !validBranchName(branch):
		body = []string{promptStatus(dangerStyle, "󰅙", "X", "Not a valid branch name")}
	default:
		plan := m.branchRefs.plan(branch)
		if m.branchRefs.root != "" {
			path = worktreePath(m.branchRefs.root, branch)
		}
		arrow := func(text string) string { return promptStatus(infoStyle, "→", "->", text) }
		// Enter would fail in these cases, so say why instead of showing a path.
		problem := ""
		switch {
		case plan.source == branchRemoteName:
			problem = remoteNameError(branch, plan.ref).Error()
		case plan.source == branchNew && plan.ref == "":
			problem = "No local default branch; set origin/HEAD or create main"
		case plan.source == branchNew && !m.branchRefs.local[plan.ref]:
			problem = "Default branch " + plan.ref + " is not available locally"
		}
		if problem != "" {
			path, body = "", []string{promptStatus(dangerStyle, "󰅙", "X", safeText(problem))}
			break
		}
		switch plan.source {
		case branchCheckedOut:
			path = plan.ref
			body = []string{arrow("Already checked out here")}
		case branchLocal:
			body = []string{arrow("Uses the existing local branch")}
		case branchRemote:
			body = []string{arrow("Tracks " + safeText(plan.ref)), promptDetail("Creates a local branch from it.")}
		default:
			text := "New branch"
			if plan.ref != "" {
				text += " from " + safeText(plan.ref)
			}
			body = []string{arrow(text)}
		}
	}
	return worktreePrompt(branch, path, body, compact)
}

func (m dashboardModel) removeWorktreeLines(compact bool) []string {
	branchStays := promptDetail("The branch stays.")
	// The footer offers s Stash and F Discard; the panel says what each does.
	// Short or narrow panels skip this, since a clipped note reads worse than
	// none and the footer labels stand alone.
	var options []string
	if !compact && m.width >= 70 {
		options = []string{"",
			promptOption("Stash", "Keeps changes in git stash list", mutedStyle),
			promptOption("Discard", "Deletes changes; cannot be undone", dangerStyle),
		}
	}
	var body []string
	switch {
	case m.removeChanges == nil:
		body = []string{promptDetail("Checking for changes and open panes…")}
	case len(m.removePanes) > 0:
		// Closing panes kills what runs in them, so jumpmux leaves that to you.
		status, next := "Open in 1 tmux pane", "Close it, then press r again."
		if len(m.removePanes) > 1 {
			status, next = fmt.Sprintf("Open in %d tmux panes", len(m.removePanes)), "Close them, then press r again."
		}
		body = []string{promptStatus(dangerStyle, "󰅙", "X", status)}
		for _, location := range paneLocations(m.removePanes) {
			body = append(body, textStyle.Render("  "+location))
		}
		body = append(body, promptDetail(next))
	case m.removeChangesErr != nil:
		body = append([]string{promptStatus(dangerStyle, "󰅙", "X", "Could not check the worktree: "+safeText(m.removeChangesErr.Error())), branchStays}, options...)
	case m.removeChanges.dirty():
		body = append([]string{promptStatus(warningStyle, "\uf071", "!", "Has "+m.removeChanges.String()), branchStays}, options...)
	default:
		body = []string{promptStatus(successStyle, "󰄬", "OK", "No uncommitted changes"), branchStays}
	}
	return worktreePrompt(m.displayWorktree(m.actionTarget), m.actionTarget.cwd, body, compact)
}

func (m dashboardModel) renderFooter(width int) string {
	if m.themePicker {
		return m.renderThemePickerFooter(width)
	}
	if m.actionMenu {
		return padANSI("  "+footerCommand("j/k", "Move")+" "+footerCommand("key", "Run")+" "+footerCommand("Enter", "Run")+" "+footerCommand("Esc", "Cancel"), width)
	}
	switch m.action {
	case actionAddWorktree, actionAgentPrompt, actionAgentBranch:
		input := m.actionTextInput
		styleTextInput(&input)
		label, verb := "branch: ", "Create"
		switch {
		case m.action == actionAgentPrompt:
			label, verb = "prompt: ", "Name"
		case m.action == actionAgentBranch && m.agentNameFallback:
			label = "naming failed, branch: "
		}
		if m.action == actionAddWorktree && m.branchRefs != nil && m.branchRefs.plan(strings.TrimSpace(m.actionTextInput.Value())).source == branchCheckedOut {
			verb = "Open"
		}
		prefix := "  " + textStyle.Render(label)
		create := footerCommand("Enter", verb)
		if width < 60 {
			create = footerCommand("↵", verb)
		}
		suffix := "  " + create + " " + footerCommand("Esc", "Cancel")
		input.Width = max(0, width-ansi.StringWidth(prefix)-ansi.StringWidth(suffix)-1)
		input.SetCursor(input.Position())
		return padANSI(prefix+input.View()+suffix, width)
	case actionRunning:
		return padANSI("  "+infoStyle.Render("Working…"), width)
	case actionAgentNaming:
		return padANSI("  "+infoStyle.Render("Naming branch…")+"  "+footerCommand("Esc", "Cancel"), width)
	}
	if m.confirming() {
		name, verb, key := m.displayWorktree(m.actionTarget), "Remove", "Enter"
		if m.action == actionRemoveSession || m.action == actionRemoveAgent {
			name = m.actionTarget.title
		}
		switch m.action {
		case actionCleanupWorktree:
			name, verb = "stale record", "Clean up"
		case actionRebaseWorktree:
			verb = "Rebase"
		case actionMergeWorktree:
			verb = "Merge"
		}
		keys := footerCommand(key, verb) + " "
		if m.action == actionRemoveWorktree {
			keys = ""
			_, clean := m.removeMode("enter")
			switch {
			case clean:
				keys = footerCommand(key, verb) + " "
			case m.removeChanges != nil && len(m.removePanes) == 0:
				keys = footerCommand("s", "Stash") + " " + footerCommand("F", "Discard") + " "
			}
		}
		keys += footerCommand("Esc", "Cancel")
		// The footer is the only place the keys appear, so they win over the
		// question when the line is short; the panel already names the target.
		question := warningStyle.Render(verb+" "+safeText(name)+"?") + "  "
		if ansi.StringWidth(question+keys)+2 > width {
			question = ""
		}
		return padANSI("  "+question+keys, width)
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
				optional = append(optional, footerCommand("^r", "Remove"))
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
		candidates := []string{footerCommand("^j/k/n/p", "Move"), footerCommand("↵", "Open"), footerCommand("^f", m.sessionFilter.label()), footerCommand("^g", sortLabel)}
		if m.query == "" {
			candidates = append(candidates, footerCommand("type", "Search"))
		} else {
			candidates = append(candidates, footerCommand("Esc", "Clear"))
		}
		for _, entry := range m.actionMenuEntries() {
			key := entry.key
			if entry.action == menuRemove {
				key = "^r"
			}
			candidates = append(candidates, footerCommand(key, entry.footer))
		}
		candidates = append(candidates, footerCommand("Tab", "Next"), footerCommand("Space", "Actions"))
		return m.prioritizedFooterWithBase(width, candidates, []string{footerCommand("?", "Help"), footerCommand("^c", "Quit")})
	}
	candidates := []string{footerCommand("↵", "Open")}
	for _, entry := range m.actionMenuEntries() {
		// ↵ already shows Open; o stays in Help and the action menu.
		if entry.action != menuOpen {
			candidates = append(candidates, footerCommand(entry.key, entry.footer))
		}
	}
	if m.tab == tabAgents {
		candidates = append(candidates, footerToggle("s", "Scope ("+m.scope.displayLabel()+")", m.scope == scopeSession))
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
	helpQuit := []string{footerCommand("?", "Help"), footerCommand("q", "Quit")}
	if m.tab == tabSessions {
		helpQuit = []string{footerCommand("^c", "Quit")}
	}
	// Esc clears action errors only; refresh errors stay until a refresh succeeds.
	if m.err != nil {
		helpQuit = append([]string{footerCommand("Esc", "Dismiss")}, helpQuit...)
	}
	separator := borderStyle.Render(" │ ")
	available := max(1, width-2-ansi.StringWidth(strings.Join(helpQuit, separator))-ansi.StringWidth(separator))
	prefix := dashboardIcon("󰅙", "!") + " "
	message := dangerStyle.Render(ansi.Truncate(prefix+dashboardErrorText(err), available, "…"))
	return padANSI("  "+strings.Join(append([]string{message}, helpQuit...), separator), width)
}

func dashboardErrorText(err error) string {
	text := safeText(err.Error())
	if text == "" {
		return "Something went wrong"
	}
	// Command errors keep the command name as written.
	if strings.HasPrefix(text, "tmux ") || strings.HasPrefix(text, "git ") || strings.HasPrefix(text, "jumpmux ") {
		return text
	}
	runes := []rune(text)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
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
	return paint(mutedStyle, key) + " " + paint(headerStyle, label)
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
			lines = append(lines, selectedStyle.Inherit(textStyle).Render("> "+scheme.slug()))
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
	title := "Help · Keys"
	commands := []string{footerCommand("? / Esc", "Close"), footerCommand("q", "Quit")}
	if len(lines) > m.helpHeight() {
		title = fmt.Sprintf("Help · Keys (%d-%d/%d)", start, end, len(lines))
		commands = append([]string{footerCommand("j/k", "Scroll"), footerCommand("PgUp/PgDn", "Page")}, commands...)
	}
	body := renderPanel(title, lines, width, m.height-1, m.helpOffset, 0, false, true)
	return body + "\n" + m.prioritizedFooterWithBase(width, commands[:len(commands)-2], commands[len(commands)-2:])
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
		out = append(out, paint(panelBorderStyle, "│")+padANSI(line, innerWidth)+paint(panelBorderStyle, "│"))
	}
	out = append(out, panelBorderStyle.Render("└"+strings.Repeat("─", innerWidth)+"┘"))
	return strings.Join(out, "\n")
}
