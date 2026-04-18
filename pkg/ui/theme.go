package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// PresetTheme pairs a display name with a Theme configuration.
type PresetTheme struct {
	Name  string
	Theme Theme
}

// Presets is the list of built-in themes.
var Presets = []PresetTheme{
	{Name: "default", Theme: Theme{}},
	{Name: "dracula", Theme: Theme{
		Border: "#6272a4", Text: "#f8f8f2", Live: "#50fa7b", Offline: "#6272a4",
		Title: "#bd93f9", SelectedBg: "#44475a", SelectedFg: "#f8f8f2",
		Playing: "#50fa7b", AdBreak: "#f1fa8c", Waiting: "#8be9fd", Reconnecting: "#ff79c6",
		TabActive: "#bd93f9", Category: "#8be9fd", Favorite: "#f1fa8c",
	}},
	{Name: "nord", Theme: Theme{
		Border: "#4c566a", Text: "#d8dee9", Live: "#a3be8c", Offline: "#4c566a",
		Title: "#81a1c1", SelectedBg: "#3b4252", SelectedFg: "#eceff4",
		Playing: "#a3be8c", AdBreak: "#ebcb8b", Waiting: "#88c0d0", Reconnecting: "#b48ead",
		TabActive: "#81a1c1", Category: "#88c0d0", Favorite: "#ebcb8b",
	}},
	{Name: "solarized-dark", Theme: Theme{
		Border: "#586e75", Text: "#839496", Live: "#859900", Offline: "#586e75",
		Title: "#268bd2", SelectedBg: "#073642", SelectedFg: "#fdf6e3",
		Playing: "#859900", AdBreak: "#b58900", Waiting: "#2aa198", Reconnecting: "#d33682",
		TabActive: "#268bd2", Category: "#2aa198", Favorite: "#b58900",
	}},
	{Name: "gruvbox", Theme: Theme{
		Border: "#665c54", Text: "#ebdbb2", Live: "#b8bb26", Offline: "#665c54",
		Title: "#83a598", SelectedBg: "#3c3836", SelectedFg: "#fbf1c7",
		Playing: "#b8bb26", AdBreak: "#fabd2f", Waiting: "#83a598", Reconnecting: "#d3869b",
		TabActive: "#83a598", Category: "#83a598", Favorite: "#fabd2f",
	}},
	{Name: "catppuccin", Theme: Theme{
		Border: "#585b70", Text: "#cdd6f4", Live: "#a6e3a1", Offline: "#585b70",
		Title: "#89b4fa", SelectedBg: "#313244", SelectedFg: "#cdd6f4",
		Playing: "#a6e3a1", AdBreak: "#f9e2af", Waiting: "#89dceb", Reconnecting: "#f5c2e7",
		TabActive: "#cba6f7", Category: "#89dceb", Favorite: "#f9e2af",
	}},
	{Name: "tokyo-night", Theme: Theme{
		Border: "#565f89", Text: "#a9b1d6", Live: "#9ece6a", Offline: "#565f89",
		Title: "#7aa2f7", SelectedBg: "#292e42", SelectedFg: "#c0caf5",
		Playing: "#9ece6a", AdBreak: "#e0af68", Waiting: "#7dcfff", Reconnecting: "#bb9af7",
		TabActive: "#bb9af7", Category: "#7dcfff", Favorite: "#e0af68",
	}},
	{Name: "rose-pine", Theme: Theme{
		Border: "#524f67", Text: "#e0def4", Live: "#31748f", Offline: "#524f67",
		Title: "#c4a7e7", SelectedBg: "#21202e", SelectedFg: "#e0def4",
		Playing: "#31748f", AdBreak: "#f6c177", Waiting: "#9ccfd8", Reconnecting: "#eb6f92",
		TabActive: "#c4a7e7", Category: "#9ccfd8", Favorite: "#f6c177",
	}},
	{Name: "kanagawa", Theme: Theme{
		Border: "#54546d", Text: "#dcd7ba", Live: "#98bb6c", Offline: "#54546d",
		Title: "#7e9cd8", SelectedBg: "#2a2a37", SelectedFg: "#dcd7ba",
		Playing: "#98bb6c", AdBreak: "#e6c384", Waiting: "#7fb4ca", Reconnecting: "#d27e99",
		TabActive: "#957fb8", Category: "#7fb4ca", Favorite: "#e6c384",
	}},
	{Name: "everforest", Theme: Theme{
		Border: "#859289", Text: "#d3c6aa", Live: "#a7c080", Offline: "#859289",
		Title: "#7fbbb3", SelectedBg: "#374145", SelectedFg: "#d3c6aa",
		Playing: "#a7c080", AdBreak: "#dbbc7f", Waiting: "#7fbbb3", Reconnecting: "#d699b6",
		TabActive: "#d699b6", Category: "#7fbbb3", Favorite: "#dbbc7f",
	}},
	{Name: "one-dark", Theme: Theme{
		Border: "#5c6370", Text: "#abb2bf", Live: "#98c379", Offline: "#5c6370",
		Title: "#61afef", SelectedBg: "#2c313c", SelectedFg: "#abb2bf",
		Playing: "#98c379", AdBreak: "#e5c07b", Waiting: "#56b6c2", Reconnecting: "#c678dd",
		TabActive: "#c678dd", Category: "#56b6c2", Favorite: "#e5c07b",
	}},
}

// ThemeByName returns the Theme for the given preset name (case-insensitive).
func ThemeByName(name string) Theme {
	for _, p := range Presets {
		if strings.EqualFold(p.Name, name) {
			return p.Theme
		}
	}
	return DefaultTheme()
}

// Theme holds hex color values for picker TUI visual aspects.
type Theme struct {
	Border       string
	Text         string
	Live         string
	Offline      string
	Title        string
	SelectedBg   string
	SelectedFg   string
	Playing      string
	AdBreak      string
	Waiting      string
	Reconnecting string

	// twui additions
	TabActive string // active tab label color
	Category  string // category name in browse view
	Favorite  string // favorite star color

	// Monochrome suppresses all color output, keeping only text attributes
	// (bold/faint/italic/reverse). Honors the NO_COLOR convention.
	Monochrome bool
}

// DefaultTheme returns a Theme with all fields empty (use hardcoded defaults).
func DefaultTheme() Theme {
	return Theme{}
}

// buildMonochromeStyles returns a palette that uses only text attributes —
// no foreground or background colors — so lipgloss emits no color escape codes.
func buildMonochromeStyles() pickerStyles {
	plain := lipgloss.NewStyle()
	return pickerStyles{
		live:         plain.Bold(true),
		offline:      plain.Faint(true),
		title:        plain.Italic(true),
		selected:     plain.Reverse(true),
		border:       plain,
		text:         plain,
		playing:      plain.Bold(true),
		adBreak:      plain.Italic(true),
		waiting:      plain.Faint(true),
		reconnecting: plain.Underline(true),
		tabActive:    plain.Bold(true).Underline(true),
		category:     plain.Italic(true),
		favorite:     plain.Bold(true),
	}
}

// pickerStyles holds the computed Lipgloss styles for one picker session.
type pickerStyles struct {
	live         lipgloss.Style
	offline      lipgloss.Style
	title        lipgloss.Style
	selected     lipgloss.Style
	border       lipgloss.Style
	text         lipgloss.Style
	playing      lipgloss.Style
	adBreak      lipgloss.Style
	waiting      lipgloss.Style
	reconnecting lipgloss.Style
	tabActive    lipgloss.Style
	category     lipgloss.Style
	favorite     lipgloss.Style
}

// fgOrDefault returns a foreground-only style using hex when non-empty,
// otherwise the given ANSI palette number. Used for the common theme-field
// pattern: "apply the user's hex override, else fall back to an ANSI color."
func fgOrDefault(hex, defaultANSI string) lipgloss.Style {
	c := hex
	if c == "" {
		c = defaultANSI
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(c))
}

// fgOrPlain returns a foreground-only style using hex when non-empty,
// otherwise an unstyled passthrough (no color escapes emitted). Used for
// border/text where the default is "do nothing," not a fallback color.
func fgOrPlain(hex string) lipgloss.Style {
	if hex == "" {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(hex))
}

// buildStyles creates Lipgloss styles from a Theme.
func buildStyles(t Theme) pickerStyles {
	if t.Monochrome {
		return buildMonochromeStyles()
	}

	ps := pickerStyles{
		live:         fgOrDefault(t.Live, "2"),
		playing:      fgOrDefault(t.Playing, "2"),
		adBreak:      fgOrDefault(t.AdBreak, "3"),
		waiting:      fgOrDefault(t.Waiting, "4"),
		reconnecting: fgOrDefault(t.Reconnecting, "6"),
		category:     fgOrDefault(t.Category, "6"),
		favorite:     fgOrDefault(t.Favorite, "3"),
		tabActive:    fgOrDefault(t.TabActive, "5").Bold(true),
		border:       fgOrPlain(t.Border),
		text:         fgOrPlain(t.Text),
	}

	if t.Offline != "" {
		ps.offline = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Offline))
	} else {
		ps.offline = lipgloss.NewStyle().Faint(true)
	}

	if t.Title != "" {
		ps.title = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Title))
	} else {
		ps.title = lipgloss.NewStyle().Faint(true).Italic(true)
	}

	if t.SelectedBg != "" || t.SelectedFg != "" {
		s := lipgloss.NewStyle()
		if t.SelectedBg != "" {
			s = s.Background(lipgloss.Color(t.SelectedBg))
		}
		if t.SelectedFg != "" {
			s = s.Foreground(lipgloss.Color(t.SelectedFg))
		}
		ps.selected = s
	} else {
		ps.selected = lipgloss.NewStyle().Reverse(true)
	}

	return ps
}
