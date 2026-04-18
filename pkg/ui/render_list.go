package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

// --- Body dispatch ---

func (m Model) renderBody(height int) string {
	var lines []string
	switch m.mode {
	case viewModeWatchList:
		lines = m.renderChannelList(m.watchList, height)
	case viewModeBrowse:
		if len(m.categoryStack) > 0 {
			lines = m.renderChannelList(m.categoryList, height)
		} else {
			lines = m.renderCategoryList(m.browseList, height)
		}
	case viewModeSearch:
		lines = m.renderSearchView(height)
	case viewModeIgnored:
		lines = m.renderIgnoredList(height)
	}

	for len(lines) < height {
		lines = append(lines, "")
	}

	return strings.Join(lines[:height], "\n") + "\n"
}

// --- Per-view list renderers ---

func (m Model) renderChannelList(entries []DiscoveryEntry, height int) []string {
	if m.loading {
		return []string{"  Loading..."}
	}
	if m.err != nil {
		return []string{fmt.Sprintf("  Error: %v", m.err)}
	}
	if len(entries) == 0 {
		return []string{"  No channels."}
	}

	// Column widths; 4 separators of 2 spaces each between the 5 data columns.
	// Prefix: status(2) + sp(1) + fav(1) + sp(1) = 5 — status is 2 cells
	// because StatusAdBreak renders as the 2-char label "AD"; other statuses
	// pad a trailing space onto their 1-char glyph to match.
	// Order: Channel · Category · Title(flex) · Viewers · Uptime
	// Fixed total: 5 + 16 + 2 + 14 + 2 + 2 + 7 + 2 + 7 = 57; title gets the rest.
	const (
		colStatus   = 3 // 2-cell glyph/label + trailing separator cell
		colFav      = 2
		colChannel  = 16
		colViewers  = 7 // fits "Viewers" header (7 chars)
		colUptime   = 7 // fits "0h 52m" / "12h 30m"
		colCategory = 14
		colFixed    = 57
	)
	colTitle := m.width - 2 - colFixed
	if colTitle < 10 {
		colTitle = 10
	}

	header := m.styles.title.Render(
		strings.Repeat(" ", colStatus+colFav) +
			pad("Channel", colChannel) + "  " +
			pad("Category", colCategory) + "  " +
			pad("Title", colTitle) + "  " +
			pad("Viewers", colViewers) + "  " +
			pad("Uptime", colUptime),
	)
	sep := m.styles.border.Render(strings.Repeat("─", m.width-2))

	lines := []string{header, sep}
	start := calcVisibleStart(m.cursor, height-1) // -1 for separator

	for i, e := range entries {
		if i < start {
			continue
		}
		if len(lines) >= height {
			break
		}

		selected := i == m.cursor

		if e.Kind == EntryLoadMore {
			label := padRight("  "+m.symbols.LoadMore+"  Load more  (Enter)", m.width-2)
			if selected {
				lines = append(lines, m.styles.selected.Render(label))
			} else {
				lines = append(lines, m.styles.title.Render(label))
			}
			continue
		}

		statusCh := "  "
		if sess, ok := m.sessions[e.Login]; ok {
			switch sess.status {
			case StatusPlaying:
				statusCh = m.symbols.Playing + " "
			case StatusAdBreak:
				statusCh = m.symbols.AdBreak
			case StatusWaiting:
				statusCh = m.symbols.Waiting + " "
			case StatusReconnecting:
				statusCh = m.symbols.Reconnecting + " "
			}
		}

		favCh := " "
		if e.IsFavorite {
			favCh = m.symbols.Favorite
		}

		displayName := e.DisplayName
		if displayName == "" {
			displayName = e.Login
		}
		chanStr := cellTruncate(displayName, colChannel)

		viewStr := ""
		if e.IsLive {
			viewStr = formatViewers(e.ViewerCount)
		}

		uptimeStr := ""
		if e.IsLive && !e.StartedAt.IsZero() {
			uptimeStr = formatUptime(time.Since(e.StartedAt))
		}

		cat := e.Category
		if !e.IsLive {
			cat = "—"
		}
		catStr := pad(cellTruncate(cat, colCategory), colCategory)

		var titleStr string
		if !e.IsLive {
			titleStr = padRight("offline", colTitle)
		} else if e.IsLive && e.Title != "" {
			title := sanitizeText(e.Title)
			if uniseg.StringWidth(title) <= colTitle {
				titleStr = padRight(title, colTitle)
			} else if selected {
				gr := uniseg.NewGraphemes(title)
				var clusters []string
				for gr.Next() {
					clusters = append(clusters, gr.Str())
				}
				offset := m.titleScrollOffset
				if offset >= len(clusters) {
					offset = 0
				}
				var sb strings.Builder
				w := 0
				for _, c := range clusters[offset:] {
					gw := uniseg.StringWidth(c)
					if w+gw > colTitle {
						break
					}
					sb.WriteString(c)
					w += gw
				}
				titleStr = padRight(sb.String(), colTitle)
			} else {
				titleStr = padRight(cellTruncate(title, colTitle), colTitle)
			}
		} else {
			titleStr = strings.Repeat(" ", colTitle)
		}

		row := padRight(
			statusCh+" "+favCh+" "+
				pad(chanStr, colChannel)+"  "+
				catStr+"  "+
				titleStr+"  "+
				pad(viewStr, colViewers)+"  "+
				pad(uptimeStr, colUptime),
			m.width-2,
		)

		switch {
		case selected:
			lines = append(lines, m.styles.selected.Render(row))
		case e.IsLive:
			lines = append(lines, m.styles.live.Render(row))
		default:
			lines = append(lines, m.styles.offline.Render(row))
		}
	}

	return lines
}

func (m Model) renderCategoryList(entries []DiscoveryEntry, height int) []string {
	if m.loading {
		return []string{"  Loading..."}
	}
	if m.err != nil {
		return []string{fmt.Sprintf("  Error: %v", m.err)}
	}
	if len(entries) == 0 {
		return []string{"  No categories found. Loading..."}
	}

	colViewers := 10
	colName := m.width - 2 - colViewers - 2
	if colName < 10 {
		colName = 10
	}

	header := m.styles.title.Render(pad("Category", colName) + pad("Viewers", colViewers))
	sep := m.styles.border.Render(strings.Repeat("─", m.width-2))
	lines := []string{header, sep}
	start := calcVisibleStart(m.cursor, height-1)

	for i, e := range entries {
		if i < start {
			continue
		}
		if len(lines) >= height {
			break
		}

		selected := i == m.cursor
		nameStr := cellTruncate(e.CategoryName, colName)
		viewStr := formatViewers(e.CategoryViewers)

		row := m.styles.category.Render(pad(nameStr, colName)) +
			m.styles.text.Render(pad(viewStr, colViewers))

		if selected {
			row = m.styles.selected.Render(padRight(stripANSI(row), m.width-2))
		}

		lines = append(lines, row)
	}

	return lines
}

func (m Model) renderSearchView(height int) []string {
	cursor := ""
	if m.searching {
		cursor = "█"
	}
	prompt := "  / " + m.searchInput + cursor
	if !m.searching && m.searchInput == "" {
		prompt = m.styles.text.Render("  / type to search channels...  Tab=browse  Esc=clear")
	}

	lines := []string{prompt, ""}
	if len(lines) >= height {
		return lines
	}

	channelLines := m.renderChannelList(m.searchList, height-2)
	lines = append(lines, channelLines...)
	return lines
}

func (m Model) renderIgnoredList(height int) []string {
	ignored := m.fns.IgnoreList()
	if len(ignored) == 0 {
		return []string{"  No ignored channels.  Press x on a channel in any view to ignore it."}
	}

	header := m.styles.title.Render(pad("  Ignored Channel", m.width-2-2))
	sep := m.styles.border.Render(strings.Repeat("─", m.width-2))
	lines := []string{header, sep}
	start := calcVisibleStart(m.cursor, height-1)

	for i, ch := range ignored {
		if i < start {
			continue
		}
		if len(lines) >= height {
			break
		}
		selected := i == m.cursor
		row := "  " + ch
		if selected {
			row = m.styles.selected.Render(padRight(row, m.width-2))
		} else {
			row = m.styles.offline.Render(row)
		}
		lines = append(lines, row)
	}
	return lines
}

// --- Formatting and layout helpers ---

// calcVisibleStart returns the first row index to show so the cursor stays visible.
// Keeps 1 header row + 1 padding row before the cursor becomes the bottom item.
func calcVisibleStart(cursor, height int) int {
	return max(0, cursor-(height-3))
}

// sanitizeText replaces newlines and other control characters with spaces so
// stream titles fetched from the API never cause unexpected line breaks.
func sanitizeText(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, s)
}

func pad(s string, width int) string {
	w := uniseg.StringWidth(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

func padRight(s string, width int) string {
	w := uniseg.StringWidth(stripANSI(s))
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

// stripANSI removes ANSI/VT100 escape sequences so runewidth can measure
// the visual width of a string that may contain terminal styling.
func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inEsc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			// Sequences end at the first letter (CSI sequences like \x1b[...m).
			if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
				inEsc = false
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func cellTruncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, "…")
}

func formatViewers(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func formatUptime(d time.Duration) string {
	h := int(d.Hours())
	m2 := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh %02dm", h, m2)
}
