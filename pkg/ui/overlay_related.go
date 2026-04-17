package ui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// renderRelatedOverlay shows other live streams in the same category as the
// channel under the cursor — a pragmatic replacement for Twitch's removed
// Host feature. Title and empty-state reflect that.
func (m Model) renderRelatedOverlay() string {
	var title string
	if m.overlayCategory != "" {
		title = fmt.Sprintf(" Related — %s (%s) ", m.overlayChannel, m.overlayCategory)
	} else {
		title = fmt.Sprintf(" Related — %s ", m.overlayChannel)
	}
	w := 50
	if len(title) > w {
		w = len(title)
	}
	lines := m.overlayHeader(title, w)
	switch {
	case m.relatedLoading:
		lines = append(lines, m.overlayRow(m.styles.text.Render(pad("  Loading...", w))))
	case len(m.relatedStreams) == 0:
		msg := "  No other streams in this category."
		if m.overlayCategory != "" {
			msg = fmt.Sprintf("  No other live streams in %s.", m.overlayCategory)
		}
		lines = append(lines, m.overlayRow(m.styles.text.Render(pad(msg, w))))
	default:
		for _, s := range m.relatedStreams {
			name := s.DisplayName
			if name == "" {
				name = s.Login
			}
			row := fmt.Sprintf("  %-26s %7s", cellTruncate(name, 26), formatViewers(s.ViewerCount))
			lines = append(lines, m.overlayRow(m.styles.live.Render(pad(row, w))))
		}
	}
	lines = append(lines, m.overlayFooter(w))
	return strings.Join(lines, "\n")
}

// handleRelated opens the related-streams overlay for the current cursor
// entry and kicks off the lookup asynchronously. Requires the entry to be
// live and carry a category — offline or category-less rows are no-ops.
func (m Model) handleRelated() (tea.Model, tea.Cmd) {
	e := m.currentEntry()
	if e == nil || e.Kind != EntryChannel || !e.IsLive || e.Category == "" {
		return m, nil
	}
	ch := e.Login
	cat := e.Category
	m.overlay = overlayRelated
	m.overlayChannel = ch
	m.overlayCategory = cat
	m.relatedStreams = nil
	m.relatedLoading = true
	ctx := m.ctx
	fns := m.fns
	return m, func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, timeoutRelated)
		defer cancel()
		related, err := fns.RelatedChannels(c, ch, cat)
		return relatedResultMsg{channel: ch, streams: related, err: err}
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
