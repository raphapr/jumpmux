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
	if !nerdFontEnabled || os.Getenv("JUMPMUX_PLAIN") != "" {
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
