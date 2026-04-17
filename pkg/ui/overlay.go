package ui

import (
	"strings"

	"github.com/rivo/uniseg"
)

// renderOverlay dispatches to the currently active overlay's renderer.
// Returns "" when no overlay is active.
func (m Model) renderOverlay() string {
	switch m.overlay {
	case overlayQuality:
		return m.renderQualityOverlay()
	case overlayHelp:
		return m.renderHelpOverlay()
	case overlayTheme:
		return m.renderThemeOverlay()
	case overlayRelated:
		return m.renderRelatedOverlay()
	}
	return ""
}

// --- Shared overlay chrome ---

// overlayWidth returns max(display width of title, min), measuring title's
// visible width after stripping ANSI so styled or non-ASCII titles don't
// desync the box frame.
func overlayWidth(title string, min int) int {
	w := uniseg.StringWidth(stripANSI(title))
	if w < min {
		return min
	}
	return w
}

// overlayHeader returns the top three rows of an overlay box: top border,
// titled row, and the separator between title and body.
func (m Model) overlayHeader(title string, w int) []string {
	return []string{
		m.styles.border.Render("┌" + strings.Repeat("─", w) + "┐"),
		m.styles.border.Render("│") + m.styles.title.Render(pad(title, w)) + m.styles.border.Render("│"),
		m.styles.border.Render("├" + strings.Repeat("─", w) + "┤"),
	}
}

// overlayFooter returns the bottom-border row of an overlay box.
func (m Model) overlayFooter(w int) string {
	return m.styles.border.Render("└" + strings.Repeat("─", w) + "┘")
}

// overlayRow wraps content with side borders to form a body row.
func (m Model) overlayRow(content string) string {
	return m.styles.border.Render("│") + content + m.styles.border.Render("│")
}

// overlayOn centers overlay content on top of the base string. Visible cells
// are measured after stripping ANSI so styled content doesn't inflate width.
func overlayOn(base, overlay string, width, height int) string {
	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")

	oHeight := len(overlayLines)
	oWidth := 0
	for _, l := range overlayLines {
		plain := stripANSI(l)
		if w := uniseg.StringWidth(plain); w > oWidth {
			oWidth = w
		}
	}

	topPad := (height - oHeight) / 2
	leftPad := (width - oWidth) / 2
	if leftPad < 0 {
		leftPad = 0
	}

	result := make([]string, len(baseLines))
	copy(result, baseLines)

	pad := strings.Repeat(" ", leftPad)
	for i, ol := range overlayLines {
		row := topPad + i
		if row < 0 || row >= len(result) {
			continue
		}
		result[row] = pad + ol
	}

	return strings.Join(result, "\n")
}
