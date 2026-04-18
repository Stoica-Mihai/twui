package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/rivo/uniseg"
)

// renderHelpOverlay lists every labelled Binding so the overlay stays in sync
// with the real keymap automatically (B11). Continuation entries with an empty
// Display or Desc are skipped so a parent binding can group siblings under a
// single row (e.g. "j / k / ↑ / ↓").
func (m Model) renderHelpOverlay() string {
	title := " Keyboard Shortcuts"

	rows := make([][]string, 0)
	maxKey := 0
	maxDesc := 0
	for _, b := range m.bindings() {
		if b.Display == "" || b.Desc == "" {
			continue
		}
		rows = append(rows, []string{b.Display, b.Desc})
		if kw := uniseg.StringWidth(b.Display); kw > maxKey {
			maxKey = kw
		}
		if dw := uniseg.StringWidth(b.Desc); dw > maxDesc {
			maxDesc = dw
		}
	}

	// Column padding: 2 spaces on each side of every cell.
	const colPad = 2
	bodyWidth := maxKey + maxDesc + colPad*4
	w := overlayWidth(title, bodyWidth)

	keyStyle := lipgloss.NewStyle().Padding(0, colPad).Inherit(m.styles.title)
	descStyle := lipgloss.NewStyle().Padding(0, colPad).Inherit(m.styles.text)
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderTop(false).BorderBottom(false).
		BorderLeft(false).BorderRight(false).
		BorderRow(false).BorderColumn(false).
		BorderHeader(false).
		Width(w).
		StyleFunc(func(_, col int) lipgloss.Style {
			if col == 0 {
				return keyStyle
			}
			return descStyle
		}).
		Rows(rows...)

	lines := m.overlayHeader(title, w)
	for _, row := range strings.Split(strings.TrimRight(t.String(), "\n"), "\n") {
		lines = append(lines, m.overlayRow(row))
	}
	lines = append(lines, m.overlayFooter(w))
	return strings.Join(lines, "\n")
}
