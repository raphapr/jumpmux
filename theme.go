package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type colorScheme uint8

const (
	schemeDefault colorScheme = iota
	schemeEmberforge
	schemeGlacierSignal
	schemeObsidianPop
	schemeSlateGarden
	schemePhosphorArcade
	schemeLasergrid
	schemeMossfire
	schemeNightSorbet
	schemeGraphiteCode
	schemeFestivalCircuit
	schemeTealDrift
	schemeCatppuccinLatte
	schemeCatppuccinFrappe
	schemeCatppuccinMacchiato
	schemeCatppuccinMocha
)

var colorSchemes = [...]colorScheme{
	schemeDefault,
	schemeEmberforge,
	schemeGlacierSignal,
	schemeObsidianPop,
	schemeSlateGarden,
	schemePhosphorArcade,
	schemeLasergrid,
	schemeMossfire,
	schemeNightSorbet,
	schemeGraphiteCode,
	schemeFestivalCircuit,
	schemeTealDrift,
	schemeCatppuccinLatte,
	schemeCatppuccinFrappe,
	schemeCatppuccinMacchiato,
	schemeCatppuccinMocha,
}

func (scheme colorScheme) slug() string {
	switch scheme {
	case schemeDefault:
		return "default"
	case schemeEmberforge:
		return "emberforge"
	case schemeGlacierSignal:
		return "glacier-signal"
	case schemeObsidianPop:
		return "obsidian-pop"
	case schemeSlateGarden:
		return "slate-garden"
	case schemePhosphorArcade:
		return "phosphor-arcade"
	case schemeLasergrid:
		return "lasergrid"
	case schemeMossfire:
		return "mossfire"
	case schemeNightSorbet:
		return "night-sorbet"
	case schemeGraphiteCode:
		return "graphite-code"
	case schemeFestivalCircuit:
		return "festival-circuit"
	case schemeTealDrift:
		return "teal-drift"
	case schemeCatppuccinLatte:
		return "catppuccin-latte"
	case schemeCatppuccinFrappe:
		return "catppuccin-frappe"
	case schemeCatppuccinMacchiato:
		return "catppuccin-macchiato"
	case schemeCatppuccinMocha:
		return "catppuccin-mocha"
	default:
		return "default"
	}
}

func colorSchemeFromSlug(value string) colorScheme {
	for _, scheme := range colorSchemes {
		if scheme.slug() == strings.TrimSpace(strings.ToLower(value)) {
			return scheme
		}
	}
	return schemeDefault
}

type themePalette struct {
	background, currentRow, currentWorktree, selected lipgloss.AdaptiveColor
	text, dimmed, border, activeBorder, header        lipgloss.AdaptiveColor
	keycap, cursor, info, diff, success, warning      lipgloss.AdaptiveColor
	danger, accent                                    lipgloss.AdaptiveColor
}

func adaptive(light, dark string) lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: light, Dark: dark}
}

func fixed(color string) lipgloss.AdaptiveColor {
	return adaptive(color, color)
}

var themePalettes = [...]themePalette{
	schemeDefault: {
		currentRow:      adaptive("#D7E6D7", "#18222E"),
		selected:        adaptive("#C8C8D2", "#28303E"),
		currentWorktree: adaptive("#4C4F69", "#F4F8FF"),
		dimmed:          adaptive("#8C8FA1", "#6C7086"),
		text:            adaptive("#4C4F69", "#CDD6F4"),
		border:          adaptive("#A0A0AF", "#3A4A5E"),
		header:          adaptive("#3C4B5F", "#B4C8DC"),
		keycap:          adaptive("#DF8E1D", "#F9E2AF"),
		info:            adaptive("#179299", "#78E1D5"),
		success:         adaptive("#40A02B", "#A6DA95"),
		warning:         adaptive("#DF8E1D", "#F9E2AF"),
		danger:          adaptive("#D20F39", "#ED8796"),
		accent:          adaptive("#8839EF", "#CBA6F7"),
	},
	schemeEmberforge: {
		currentRow:      adaptive("#F8EBE1", "#201612"),
		selected:        adaptive("#EEDED2", "#32221B"),
		currentWorktree: adaptive("#3C281E", "#FFF5E6"),
		dimmed:          adaptive("#9B8778", "#967C6C"),
		text:            adaptive("#463226", "#E7D6C6"),
		border:          adaptive("#C3AFA0", "#684E3F"),
		header:          adaptive("#AA641E", "#FFB05C"),
		keycap:          adaptive("#B48228", "#FFDD8A"),
		info:            adaptive("#1E8291", "#67C4CF"),
		success:         adaptive("#378C28", "#91D376"),
		warning:         adaptive("#B47814", "#FFBD5C"),
		danger:          adaptive("#BE3C2D", "#E46C5C"),
		accent:          adaptive("#AF5537", "#DC886C"),
	},
	schemeGlacierSignal: {
		currentRow:      adaptive("#E6F0F8", "#101B29"),
		selected:        adaptive("#D7E4F0", "#19293C"),
		currentWorktree: adaptive("#142841", "#EDF7FF"),
		dimmed:          adaptive("#788CA0", "#6F8195"),
		text:            adaptive("#1E324B", "#C6D8E8"),
		border:          adaptive("#B4C3D2", "#405870"),
		header:          adaptive("#286EBE", "#76BBFF"),
		keycap:          adaptive("#1E82B4", "#A6E8FF"),
		info:            adaptive("#148CA0", "#68DCE9"),
		success:         adaptive("#28916E", "#80D6B8"),
		warning:         adaptive("#B9821E", "#FFC56C"),
		danger:          adaptive("#C83737", "#FF7A7A"),
		accent:          adaptive("#505AC8", "#99AAFF"),
	},
	schemeObsidianPop: {
		currentRow:      adaptive("#F5F5FA", "#0A0A0E"),
		selected:        adaptive("#E8E8F0", "#1A1A22"),
		currentWorktree: adaptive("#0F0F19", "#FAFAFF"),
		dimmed:          adaptive("#8C8C9B", "#767684"),
		text:            adaptive("#1E1E2D", "#E2E2F0"),
		border:          adaptive("#BEBECD", "#4E4E5E"),
		header:          adaptive("#BE148C", "#FF40C4"),
		keycap:          adaptive("#AA9600", "#FFEA00"),
		info:            adaptive("#0096BE", "#00D6FF"),
		success:         adaptive("#32A000", "#5EFF00"),
		warning:         adaptive("#BE7D00", "#FFB300"),
		danger:          adaptive("#D22323", "#FF4B4B"),
		accent:          adaptive("#6E32D2", "#A661FF"),
	},
	schemeSlateGarden: {
		currentRow:      adaptive("#F0F2F5", "#1B1F26"),
		selected:        adaptive("#E4E8EC", "#272E36"),
		currentWorktree: adaptive("#282D37", "#ECECE4"),
		dimmed:          adaptive("#8C919B", "#7E818A"),
		text:            adaptive("#373E46", "#CBCFD2"),
		border:          adaptive("#BEC3CA", "#525A63"),
		header:          adaptive("#465F78", "#8AA7BE"),
		keycap:          adaptive("#8C783C", "#C4B076"),
		info:            adaptive("#3C7876", "#78AAA8"),
		success:         adaptive("#4B7D41", "#96B68A"),
		warning:         adaptive("#9B7D32", "#CEB173"),
		danger:          adaptive("#964B50", "#BA8487"),
		accent:          adaptive("#695A91", "#A195BC"),
	},
	schemePhosphorArcade: {
		currentRow:      adaptive("#EBF8EE", "#0C1712"),
		selected:        adaptive("#DCEEE0", "#14251D"),
		currentWorktree: adaptive("#14321E", "#CDFFBE"),
		dimmed:          adaptive("#789176", "#6A8366"),
		text:            adaptive("#1E3C26", "#AAE09E"),
		border:          adaptive("#B4CDB9", "#385C43"),
		header:          adaptive("#AA7819", "#FFC05C"),
		keycap:          adaptive("#9B8228", "#FFE48A"),
		info:            adaptive("#1E8C73", "#68DCBB"),
		success:         adaptive("#32962A", "#8FF084"),
		warning:         adaptive("#AF7D14", "#F8BF59"),
		danger:          adaptive("#C83723", "#FF6E57"),
		accent:          adaptive("#2D82C3", "#84D1FF"),
	},
	schemeLasergrid: {
		currentRow:      adaptive("#F2EEFC", "#0E0A19"),
		selected:        adaptive("#E4DEF5", "#1B132E"),
		currentWorktree: adaptive("#191232", "#F4F0FF"),
		dimmed:          adaptive("#8C80A5", "#7F7199"),
		text:            adaptive("#231C41", "#D9CFF0"),
		border:          adaptive("#C3B9D7", "#574485"),
		header:          adaptive("#009BAA", "#2EF3FF"),
		keycap:          adaptive("#829100", "#EAFF47"),
		info:            adaptive("#008CAF", "#00D8FF"),
		success:         adaptive("#23A537", "#66FF78"),
		warning:         adaptive("#B9730A", "#FFA72E"),
		danger:          adaptive("#CD1E55", "#FF4A8A"),
		accent:          adaptive("#8C23D2", "#CA4FFF"),
	},
	schemeMossfire: {
		currentRow:      adaptive("#F2F0E8", "#161812"),
		selected:        adaptive("#E6E4DA", "#24281E"),
		currentWorktree: adaptive("#23261C", "#EEE9D6"),
		dimmed:          adaptive("#8A8C78", "#7E8068"),
		text:            adaptive("#303426", "#D0CAB7"),
		border:          adaptive("#C3C0B2", "#565943"),
		header:          adaptive("#5F762D", "#ACC476"),
		keycap:          adaptive("#A08232", "#E4C179"),
		info:            adaptive("#287380", "#67ABB6"),
		success:         adaptive("#37822D", "#7EBA6C"),
		warning:         adaptive("#A5761E", "#DAA958"),
		danger:          adaptive("#A0412D", "#BF6F58"),
		accent:          adaptive("#734B7D", "#A683AD"),
	},
	schemeNightSorbet: {
		currentRow:      adaptive("#F5F0F8", "#1B1826"),
		selected:        adaptive("#E8E2F0", "#2A253A"),
		currentWorktree: adaptive("#282337", "#FAF5F8"),
		dimmed:          adaptive("#948CA2", "#91899D"),
		text:            adaptive("#322A41", "#DFD6E2"),
		border:          adaptive("#C8C0D7", "#5D5470"),
		header:          adaptive("#3778B9", "#97D2FF"),
		keycap:          adaptive("#A5872D", "#FFEBB2"),
		info:            adaptive("#1E918C", "#99ECE5"),
		success:         adaptive("#419637", "#B6EAAB"),
		warning:         adaptive("#B97D37", "#FFC4A0"),
		danger:          adaptive("#C84155", "#FF9BAD"),
		accent:          adaptive("#825FC8", "#CDB4FF"),
	},
	schemeGraphiteCode: {
		currentRow:      adaptive("#F5F6F8", "#13161A"),
		selected:        adaptive("#EAECF0", "#20242A"),
		currentWorktree: adaptive("#191C23", "#F2F4F7"),
		dimmed:          adaptive("#8C949E", "#747C84"),
		text:            adaptive("#282E37", "#CDD2D8"),
		border:          adaptive("#C3C8D0", "#4A525A"),
		header:          adaptive("#373E48", "#E2E6EB"),
		keycap:          adaptive("#282D34", "#F6F8FA"),
		info:            adaptive("#555F6C", "#BCC4CC"),
		success:         adaptive("#464E58", "#D6DCE2"),
		warning:         adaptive("#646C76", "#ABB3BB"),
		danger:          adaptive("#78808A", "#9299A1"),
		accent:          adaptive("#3C424C", "#E8ECF0"),
	},
	schemeFestivalCircuit: {
		currentRow:      adaptive("#F0EEFC", "#13122C"),
		selected:        adaptive("#E2E0F5", "#211F46"),
		currentWorktree: adaptive("#191637", "#F8F4FF"),
		dimmed:          adaptive("#8784AC", "#817EAA"),
		text:            adaptive("#231E44", "#DBD6F1"),
		border:          adaptive("#BEC3DC", "#4A568F"),
		header:          adaptive("#0F87B4", "#45D3FF"),
		keycap:          adaptive("#A58C00", "#FFDB46"),
		info:            adaptive("#149687", "#47E7CC"),
		success:         adaptive("#2D961E", "#75E260"),
		warning:         adaptive("#BE690F", "#FF9C3F"),
		danger:          adaptive("#CD284B", "#FF5C84"),
		accent:          adaptive("#7832D2", "#B265FF"),
	},
	schemeTealDrift: {
		currentRow:      adaptive("#F2F4F6", "#19191E"),
		selected:        adaptive("#E4E8EC", "#2D2D37"),
		currentWorktree: adaptive("#242D35", "#FFFFFF"),
		dimmed:          adaptive("#828C94", "#646464"),
		text:            adaptive("#242D35", "#C8C8C8"),
		border:          adaptive("#BCC4C8", "#3C3C3C"),
		header:          adaptive("#344664", "#B4BEC8"),
		keycap:          adaptive("#8C691E", "#C8B478"),
		info:            adaptive("#0D8076", "#4EC9B0"),
		success:         adaptive("#28783C", "#78C878"),
		warning:         adaptive("#8C691E", "#C8B478"),
		danger:          adaptive("#B43232", "#DC7878"),
		accent:          adaptive("#734B91", "#B48CC8"),
	},
	// Terminals cannot render Catppuccin's translucent selection color, so
	// selected preblends Overlay 2 at 25% over Base. Latte semantic colors
	// are darkened enough to remain readable on its light background.
	schemeCatppuccinLatte: {
		background:      fixed("#EFF1F5"),
		currentRow:      fixed("#CCD0DA"),
		selected:        fixed("#D2D4DC"),
		currentWorktree: fixed("#7287FD"),
		dimmed:          fixed("#8C8FA1"),
		text:            fixed("#4C4F69"),
		border:          fixed("#9CA0B0"),
		activeBorder:    fixed("#7287FD"),
		header:          fixed("#7287FD"),
		keycap:          fixed("#DF8E1D"),
		cursor:          fixed("#DC8A78"),
		info:            fixed("#0F6F69"),
		diff:            fixed("#1450B8"),
		success:         fixed("#2A6E1B"),
		warning:         fixed("#8A5500"),
		danger:          fixed("#D20F39"),
		accent:          fixed("#8839EF"),
	},
	schemeCatppuccinFrappe: {
		background:      fixed("#303446"),
		currentRow:      fixed("#414559"),
		selected:        fixed("#494E63"),
		currentWorktree: fixed("#BABBF1"),
		dimmed:          fixed("#838BA7"),
		text:            fixed("#C6D0F5"),
		border:          fixed("#737994"),
		activeBorder:    fixed("#BABBF1"),
		header:          fixed("#BABBF1"),
		keycap:          fixed("#E5C890"),
		cursor:          fixed("#F2D5CF"),
		info:            fixed("#81C8BE"),
		diff:            fixed("#8CAAEE"),
		success:         fixed("#A6D189"),
		warning:         fixed("#E5C890"),
		danger:          fixed("#E78284"),
		accent:          fixed("#CA9EE6"),
	},
	schemeCatppuccinMacchiato: {
		background:      fixed("#24273A"),
		currentRow:      fixed("#363A4F"),
		selected:        fixed("#404459"),
		currentWorktree: fixed("#B7BDF8"),
		dimmed:          fixed("#8087A2"),
		text:            fixed("#CAD3F5"),
		border:          fixed("#6E738D"),
		activeBorder:    fixed("#B7BDF8"),
		header:          fixed("#B7BDF8"),
		keycap:          fixed("#EED49F"),
		cursor:          fixed("#F4DBD6"),
		info:            fixed("#8BD5CA"),
		diff:            fixed("#8AADF4"),
		success:         fixed("#A6DA95"),
		warning:         fixed("#EED49F"),
		danger:          fixed("#ED8796"),
		accent:          fixed("#C6A0F6"),
	},
	schemeCatppuccinMocha: {
		background:      fixed("#1E1E2E"),
		currentRow:      fixed("#313244"),
		selected:        fixed("#3B3D4F"),
		currentWorktree: fixed("#B4BEFE"),
		dimmed:          fixed("#7F849C"),
		text:            fixed("#CDD6F4"),
		border:          fixed("#6C7086"),
		activeBorder:    fixed("#B4BEFE"),
		header:          fixed("#B4BEFE"),
		keycap:          fixed("#F9E2AF"),
		cursor:          fixed("#F5E0DC"),
		info:            fixed("#94E2D5"),
		diff:            fixed("#89B4FA"),
		success:         fixed("#A6E3A1"),
		warning:         fixed("#F9E2AF"),
		danger:          fixed("#F38BA8"),
		accent:          fixed("#CBA6F7"),
	},
}

func applyColorScheme(scheme colorScheme) {
	if int(scheme) >= len(themePalettes) {
		scheme = schemeDefault
	}
	palette := themePalettes[scheme]
	dashboardBackgroundColor = palette.background
	dashboardBackgroundEnabled = palette.background.Light != "" || palette.background.Dark != ""
	selectedColor = palette.selected
	textColor = palette.text
	dimmedColor = palette.dimmed
	borderColor = palette.border
	headerColor = palette.header
	keycapColor = palette.keycap
	infoColor = palette.info
	successColor = palette.success
	warningColor = palette.warning
	dangerColor = palette.danger
	accentColor = palette.accent

	activeBorder := palette.activeBorder
	if activeBorder.Light == "" && activeBorder.Dark == "" {
		activeBorder = palette.border
	}
	cursor := palette.cursor
	if cursor.Light == "" && cursor.Dark == "" {
		cursor = palette.keycap
	}
	diff := palette.diff
	if diff.Light == "" && diff.Dark == "" {
		diff = palette.info
	}
	textStyle = lipgloss.NewStyle().Foreground(textColor)
	headerStyle = lipgloss.NewStyle().Foreground(headerColor).Bold(true)
	mutedStyle = lipgloss.NewStyle().Foreground(dimmedColor)
	borderStyle = lipgloss.NewStyle().Foreground(borderColor)
	activeBorderStyle = lipgloss.NewStyle().Foreground(activeBorder)
	keycapStyle = lipgloss.NewStyle().Foreground(keycapColor)
	cursorStyle = lipgloss.NewStyle().Foreground(cursor)
	infoStyle = lipgloss.NewStyle().Foreground(infoColor)
	successStyle = lipgloss.NewStyle().Foreground(successColor)
	warningStyle = lipgloss.NewStyle().Foreground(warningColor)
	dangerStyle = lipgloss.NewStyle().Foreground(dangerColor)
	accentStyle = lipgloss.NewStyle().Foreground(accentColor).Bold(true)
	selectedStyle = lipgloss.NewStyle().Background(selectedColor)
	addedStyle = successStyle
	removedStyle = dangerStyle
	diffHeadStyle = lipgloss.NewStyle().Foreground(diff).Bold(true)
}

func paintDashboardBackground(value string) string {
	if !dashboardBackgroundEnabled {
		return value
	}
	sample := lipgloss.NewStyle().Foreground(textColor).Background(dashboardBackgroundColor).Render(" ")
	prefix, _, ok := strings.Cut(sample, " ")
	if !ok || prefix == "" {
		return value
	}
	lines := strings.Split(value, "\n")
	for index, line := range lines {
		lines[index] = prefix + strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+prefix) + "\x1b[0m"
	}
	return strings.Join(lines, "\n")
}

func loadLegacyColorScheme() colorScheme {
	path, err := colorSchemePath()
	if err != nil {
		return schemeDefault
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return schemeDefault
	}
	return colorSchemeFromSlug(string(data))
}

func saveColorScheme(scheme colorScheme) error {
	return saveConfigValue("theme", scheme.slug())
}

func colorSchemePath() (string, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, "jumpmux", "color_scheme"), nil
}

func init() {
	applyColorScheme(schemeDefault)
}
