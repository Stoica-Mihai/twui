package ui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
)

// maxRelatedVisible caps how many rows the overlay shows at once. The
// underlying pool (m.relatedStreams) can hold more so that ignoring rows
// slides new candidates into view without a refetch. When the pool is
// larger than this cap, a small "· N more" hint sits in the empty row.
const maxRelatedVisible = 15

// renderRelatedOverlay shows other live streams in the same category as the
// channel under the cursor — a pragmatic replacement for Twitch's removed
// Host feature. One row is highlighted (m.overlayCursor); Enter launches it.
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
		visible := m.relatedStreams
		remaining := 0
		if len(visible) > maxRelatedVisible {
			remaining = len(visible) - maxRelatedVisible
			visible = visible[:maxRelatedVisible]
		}

		rows := make([][]string, 0, len(visible))
		for _, s := range visible {
			name := s.DisplayName
			if name == "" {
				name = s.Login
			}
			if s.IsFavorite {
				name = m.symbols.Favorite + " " + name
			} else {
				name = "  " + name
			}
			rows = append(rows, []string{name, formatViewers(s.ViewerCount)})
		}

		selIdx := m.overlayCursor
		pad := lipgloss.NewStyle().Padding(0, 1)
		t := table.New().
			Border(lipgloss.NormalBorder()).
			BorderStyle(m.styles.border).
			BorderTop(false).
			BorderBottom(false).
			BorderLeft(false).
			BorderRight(false).
			BorderRow(false).
			BorderColumn(false).
			BorderHeader(true).
			Width(w).
			Headers("Channel", "Viewers").
			StyleFunc(func(row, col int) lipgloss.Style {
				switch {
				case row == table.HeaderRow:
					return pad.Inherit(m.styles.title)
				case row == selIdx:
					return pad.Inherit(m.styles.selected)
				case col == 1:
					return pad.Inherit(m.styles.offline).Align(lipgloss.Right)
				default:
					return pad.Inherit(m.styles.live)
				}
			}).
			Rows(rows...)

		// Wrap each rendered table line with the overlay's side borders.
		for _, tl := range strings.Split(t.String(), "\n") {
			lines = append(lines, m.overlayRow(tl))
		}

		if remaining > 0 {
			hint := fmt.Sprintf("  · %d more in pool — ignore visible rows to reveal", remaining)
			lines = append(lines, m.overlayRow(m.styles.text.Render(padRight(hint, w))))
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
	m.overlayCursor = 0
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
// Navigation mirrors the quality picker: j/k (or arrows) move the cursor,
// Enter launches the selected related stream, f/x favorite or ignore it
// (discovery-friendly — users often find new faves or irritants here),
// Esc/r/q closes.
func (m Model) handleRelatedKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "r", "q", "Q":
		m.overlay = overlayNone
	case "j", "down":
		// Cursor stays within the visible window even when the pool is
		// deeper — rows beyond maxRelatedVisible aren't rendered.
		limit := len(m.relatedStreams) - 1
		if limit > maxRelatedVisible-1 {
			limit = maxRelatedVisible - 1
		}
		if m.overlayCursor < limit {
			m.overlayCursor++
		}
	case "k", "up":
		if m.overlayCursor > 0 {
			m.overlayCursor--
		}
	case "enter":
		if m.overlayCursor < 0 || m.overlayCursor >= len(m.relatedStreams) {
			return m, nil
		}
		sel := m.relatedStreams[m.overlayCursor]
		m.overlay = overlayNone
		newM, cmd := m.launchStream(sel.Login, "", sel.AvatarURL)
		return newM, cmd
	case "f":
		if m.overlayCursor < 0 || m.overlayCursor >= len(m.relatedStreams) {
			return m, nil
		}
		e := &m.relatedStreams[m.overlayCursor]
		e.IsFavorite = !e.IsFavorite
		m.fns.ToggleFavorite(e.Login, e.IsFavorite)
	case "x":
		if m.overlayCursor < 0 || m.overlayCursor >= len(m.relatedStreams) {
			return m, nil
		}
		e := m.relatedStreams[m.overlayCursor]
		m.fns.ToggleIgnore(e.Login, true)
		// Drop the now-ignored row from the overlay so it matches the rest
		// of twui (ignored channels never appear in live views).
		m.relatedStreams = append(m.relatedStreams[:m.overlayCursor], m.relatedStreams[m.overlayCursor+1:]...)
		if m.overlayCursor >= len(m.relatedStreams) && m.overlayCursor > 0 {
			m.overlayCursor--
		}
	}
	return m, nil
}
