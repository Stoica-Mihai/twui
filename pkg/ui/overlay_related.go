package ui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m Model) renderRelatedOverlay() string {
	title := fmt.Sprintf(" Hosting — %s ", m.overlayChannel)
	w := 40
	if len(title) > w {
		w = len(title)
	}
	lines := m.overlayHeader(title, w)
	if m.relatedLoading {
		lines = append(lines, m.overlayRow(m.styles.text.Render(pad("  Loading...", w))))
	} else if len(m.relatedHosts) == 0 {
		lines = append(lines, m.overlayRow(m.styles.text.Render(pad("  Not hosting anyone.", w))))
	} else {
		for _, h := range m.relatedHosts {
			name := h.DisplayName
			if name == "" {
				name = h.Login
			}
			lines = append(lines, m.overlayRow(m.styles.live.Render(pad("  "+name, w))))
		}
	}
	lines = append(lines, m.overlayFooter(w))
	return strings.Join(lines, "\n")
}

// handleRelated opens the related/host channels overlay for the current
// cursor entry and kicks off the lookup asynchronously.
func (m Model) handleRelated() (tea.Model, tea.Cmd) {
	e := m.currentEntry()
	if e == nil || e.Kind != EntryChannel {
		return m, nil
	}
	ch := e.Login
	m.overlay = overlayRelated
	m.overlayChannel = ch
	m.relatedHosts = nil
	m.relatedLoading = true
	ctx := m.ctx
	fns := m.fns
	return m, func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, timeoutRelated)
		defer cancel()
		hosts, err := fns.HostingChannels(c, ch)
		return relatedResultMsg{channel: ch, hosts: hosts, err: err}
	}
}

// handleRelatedKey handles key presses while the related overlay is open.
func (m Model) handleRelatedKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "r", "q", "Q":
		m.overlay = overlayNone
	}
	return m, nil
}
