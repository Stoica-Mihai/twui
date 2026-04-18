package ui

import (
	tea "charm.land/bubbletea/v2"
)

func (m Model) renderThemeOverlay() string {
	return m.renderListOverlay(" Theme Picker ", 30, len(Presets), m.themeIdx, func(i int) string {
		return Presets[i].Name
	})
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
