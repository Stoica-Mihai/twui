package ui

import (
	"fmt"
	"strings"
)

// renderHelpOverlay lists every labelled Binding so the overlay stays in sync
// with the real keymap automatically (B11). Continuation entries with an empty
// Display or Desc are skipped so a parent binding can group siblings under a
// single row (e.g. "j / k / ↑ / ↓").
func (m Model) renderHelpOverlay() string {
	w := 50
	lines := m.overlayHeader(" Keyboard Shortcuts", w)
	for _, b := range m.bindings() {
		if b.Display == "" || b.Desc == "" {
			continue
		}
		lines = append(lines, m.overlayRow(pad(fmt.Sprintf("  %-22s %s", b.Display, b.Desc), w)))
	}
	lines = append(lines, m.overlayFooter(w))
	return strings.Join(lines, "\n")
}
