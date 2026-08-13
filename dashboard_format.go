package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

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

func reducedMotion() bool { return os.Getenv("JUMPMUX_REDUCED_MOTION") == "1" }

func spinnerFrame(now time.Time) string {
	if reducedMotion() {
		return "·"
	}
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
