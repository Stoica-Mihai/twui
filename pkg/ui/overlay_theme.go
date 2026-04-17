package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m Model) renderThemeOverlay() string {
	title := " Theme Picker "
	w := overlayWidth(title, 30)
	lines := m.overlayHeader(title, w)
	for i, p := range Presets {
		row := fmt.Sprintf("  %-*s  ", w-4, p.Name)
		if i == m.themeIdx {
			row = m.styles.selected.Render(row)
		}
		lines = append(lines, m.overlayRow(row))
	}
	lines = append(lines, m.overlayFooter(w))
	return strings.Join(lines, "\n")
}

// handleThemeKey handles key presses while the theme overlay is open.
// Navigation live-previews by rebuilding styles; Enter persists.
func (m Model) handleThemeKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.overlay = overlayNone
	case "j", "down":
		if m.themeIdx < len(Presets)-1 {
			m.themeIdx++
			m.styles = buildStyles(Presets[m.themeIdx].Theme)
		}
	case "k", "up":
		if m.themeIdx > 0 {
			m.themeIdx--
			m.styles = buildStyles(Presets[m.themeIdx].Theme)
		}
	case "enter":
		m.overlay = overlayNone
		if m.fns.WriteTheme != nil {
			m.fns.WriteTheme(Presets[m.themeIdx].Name)
		}
	}
	return m, nil
}
