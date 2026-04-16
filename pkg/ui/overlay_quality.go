package ui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m Model) renderQualityOverlay() string {
	title := fmt.Sprintf(" Quality — %s ", m.overlayChannel)
	w := len(title)
	lines := m.overlayHeader(title, w)
	for i, q := range m.overlayList {
		row := fmt.Sprintf("  %-*s  ", w-4, q)
		if i == m.overlayCursor {
			row = m.styles.selected.Render(row)
		}
		lines = append(lines, m.overlayRow(row))
	}
	lines = append(lines, m.overlayFooter(w))
	return strings.Join(lines, "\n")
}

// handleQualityKey handles key presses while the quality overlay is open.
func (m Model) handleQualityKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.overlay = overlayNone
	case "j", "down":
		if m.overlayCursor < len(m.overlayList)-1 {
			m.overlayCursor++
		}
	case "k", "up":
		if m.overlayCursor > 0 {
			m.overlayCursor--
		}
	case "enter":
		quality := m.overlayList[m.overlayCursor]
		ch := m.overlayChannel
		avatar := m.overlayAvatarURL
		m.overlay = overlayNone
		return m, m.launchStream(ch, quality, avatar)
	}
	return m, nil
}

// handleQualityPicker opens the quality overlay for the current entry by
// first fetching the available qualities asynchronously.
func (m Model) handleQualityPicker() (tea.Model, tea.Cmd) {
	e := m.currentEntry()
	if e == nil || e.Kind != EntryChannel || !e.IsLive {
		return m, nil
	}
	ch := e.Login
	avatar := e.AvatarURL
	m.loading = true
	ctx := m.ctx
	fns := m.fns
	return m, func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, timeoutQuality)
		defer cancel()
		qualities, err := fns.Streams(c, ch)
		return qualityResultMsg{channel: ch, avatarURL: avatar, qualities: qualities, err: err}
	}
}
