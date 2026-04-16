package ui

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/rivo/uniseg"
)

// viewMode identifies which tab/view is active.
type viewMode int

const (
	viewModeWatchList viewMode = iota
	viewModeBrowse
	viewModeSearch
	viewModeIgnored
)

// overlayMode identifies an active overlay.
type overlayMode int

const (
	overlayNone    overlayMode = iota
	overlayQuality             // quality picker for a channel
	overlayHelp                // help screen
	overlayTheme               // theme picker
	overlayRelated             // related/host channels
)

// Timeout and debounce constants for async operations.
const (
	timeoutRelated = 10 * time.Second
	timeoutQuality = 15 * time.Second
	timeoutSearch  = 15 * time.Second
	timeoutBrowse  = 30 * time.Second
	debounceSearch = 400 * time.Millisecond
)

// internal Bubble Tea messages
type (
	watchListResultMsg struct {
		entries   []DiscoveryEntry
		err       error
		refreshed bool // true when triggered by auto-refresh; cursor preserved
	}
	searchResultMsg struct {
		query     string
		entries   []DiscoveryEntry
		err       error
		refreshed bool
	}
	browseResultMsg struct {
		entries    []DiscoveryEntry
		nextCursor string
		err        error
		refreshed  bool
	}
	categoryStreamsResultMsg struct {
		category   string
		entries    []DiscoveryEntry
		nextCursor string
		err        error
		appendMode bool
		refresh    bool // true when triggered by auto-refresh; cursor preserved
	}
	tickMsg time.Time
	qualityResultMsg struct {
		channel   string
		avatarURL string
		qualities []string
		err       error
	}
	relatedResultMsg struct {
		channel string
		hosts   []DiscoveryEntry
		err     error
	}
	searchDebounceMsg struct {
		query string
	}
	titleScrollMsg struct{}
	statusUpdateMsg struct {
		channel string
		status  Status
		detail  string
		// notice is a transient footer message; when set, the status/detail
		// fields are ignored and only m.notice is updated.
		notice string
		gen    int
		done   bool
	}
)

// playbackSession tracks an active or recent playback.
type playbackSession struct {
	ctx    context.Context
	cancel context.CancelFunc
	status Status
	detail string
	gen    int
	ch     chan statusUpdateMsg
}

// Model is the main Bubble Tea model for twui.
type Model struct {
	fns DiscoveryFuncs

	// layout
	width, height int

	// view state
	mode    viewMode
	cursor  int
	styles  pickerStyles
	symbols Symbols

	// data per view
	watchList  []DiscoveryEntry
	browseList []DiscoveryEntry
	browseNextCursor string
	searchList  []DiscoveryEntry
	searchQuery string
	searchInput string
	searching   bool
	ignoredList []DiscoveryEntry

	categoryStack  []string
	categoryList   []DiscoveryEntry
	categoryCursor string

	loading bool
	err     error
	notice  string // transient one-line message shown in footer

	// auto-refresh
	refreshInterval  time.Duration // 0 = disabled
	refreshCountdown time.Duration
	refreshing       bool

	// overlays
	overlay        overlayMode
	overlayList    []string
	overlayCursor  int
	overlayChannel   string
	overlayAvatarURL string
	relatedHosts   []DiscoveryEntry
	relatedLoading bool

	// playback
	sessions map[string]*playbackSession
	lastGen  int

	// theme picker index
	themeIdx int

	// title scroll
	titleScrollOffset int
	titleScrollDir    int // +1 forward, -1 backward

	// global ctx
	cancel context.CancelFunc
	ctx    context.Context
}

// NewModel creates a new Model.
func NewModel(fns DiscoveryFuncs, theme Theme, refreshInterval time.Duration) *Model {
	ctx, cancel := context.WithCancel(context.Background())
	return &Model{
		fns:              fns,
		styles:           buildStyles(theme),
		symbols:          UnicodeSymbols(),
		sessions:         make(map[string]*playbackSession),
		ctx:              ctx,
		cancel:           cancel,
		refreshInterval:  refreshInterval,
		refreshCountdown: refreshInterval,
	}
}

// SetSymbols swaps the glyph set used for status indicators and list markers.
// Call before tea.Run; not safe to call while the picker is running.
func (m *Model) SetSymbols(s Symbols) {
	m.symbols = s
}

func titleScrollCmd() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg {
		return titleScrollMsg{}
	})
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Init implements tea.Model (bubbletea v2: returns tea.Cmd).
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.loadWatchList(), titleScrollCmd()}
	if m.refreshInterval > 0 {
		cmds = append(cmds, tickCmd())
	}
	return tea.Batch(cmds...)
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.MouseClickMsg:
		return m.handleMouseClick(msg)

	case tea.MouseWheelMsg:
		return m.handleMouseWheel(msg)

	case watchListResultMsg:
		if msg.refreshed {
			m.refreshing = false
		} else {
			m.loading = false
			m.err = msg.err
		}
		if msg.err == nil {
			prev := m.cursorLogin(viewModeWatchList)
			m.watchList = m.filterIgnored(msg.entries)
			if msg.refreshed && m.mode == viewModeWatchList {
				m.cursor = findEntryByLogin(m.watchList, prev)
			}
		}
		return m, nil

	case searchResultMsg:
		if msg.query != m.searchQuery {
			return m, nil
		}
		if msg.refreshed {
			m.refreshing = false
		} else {
			m.loading = false
			m.err = msg.err
		}
		if msg.err == nil {
			prev := m.cursorLogin(viewModeSearch)
			m.searchList = m.filterIgnored(msg.entries)
			if msg.refreshed && m.mode == viewModeSearch {
				m.cursor = findEntryByLogin(m.searchList, prev)
			} else {
				m.cursor = 0
			}
		}
		return m, nil

	case browseResultMsg:
		if msg.refreshed {
			m.refreshing = false
		} else {
			m.loading = false
			m.err = msg.err
		}
		if msg.err == nil {
			prevCat := m.cursorCategory()
			m.browseList = msg.entries
			m.browseNextCursor = msg.nextCursor
			if m.mode == viewModeBrowse && len(m.categoryStack) == 0 {
				if msg.refreshed {
					m.cursor = findCategoryByName(m.browseList, prevCat)
				} else {
					m.cursor = 0
				}
			}
		}
		return m, nil

	case categoryStreamsResultMsg:
		if msg.refresh {
			m.refreshing = false
		} else {
			m.loading = false
		}
		if msg.appendMode {
			if n := len(m.categoryList); n > 0 && m.categoryList[n-1].Kind == EntryLoadMore {
				m.categoryList = m.categoryList[:n-1]
			}
			if msg.err != nil {
				m.notice = fmt.Sprintf("Load more failed: %v", msg.err)
			} else {
				m.categoryList = append(m.categoryList, m.filterIgnored(msg.entries)...)
				m.categoryCursor = msg.nextCursor
			}
		} else {
			if !msg.refresh {
				m.err = msg.err
			}
			if msg.err == nil {
				prev := m.cursorLogin(viewModeBrowse)
				m.categoryList = m.filterIgnored(msg.entries)
				if msg.refresh && m.mode == viewModeBrowse && len(m.categoryStack) > 0 {
					m.cursor = findEntryByLogin(m.categoryList, prev)
				} else {
					m.cursor = 0
				}
				m.categoryCursor = msg.nextCursor
			}
		}
		return m, nil

	case qualityResultMsg:
		m.loading = false
		if msg.err != nil || len(msg.qualities) == 0 {
			return m, m.launchStream(msg.channel, "", msg.avatarURL)
		}
		m.overlay = overlayQuality
		m.overlayList = msg.qualities
		m.overlayCursor = 0
		m.overlayChannel = msg.channel
		m.overlayAvatarURL = msg.avatarURL
		return m, nil

	case relatedResultMsg:
		m.relatedLoading = false
		if msg.err == nil {
			m.relatedHosts = msg.hosts
		}
		return m, nil

	case searchDebounceMsg:
		if msg.query != m.searchInput {
			return m, nil
		}
		m.searchQuery = msg.query
		return m, m.runSearch(msg.query)

	case tickMsg:
		if m.refreshInterval == 0 {
			return m, nil
		}
		m.refreshCountdown -= time.Second
		if m.refreshCountdown <= 0 {
			m.refreshCountdown = m.refreshInterval
			m.refreshing = true
			if cmd := m.refreshCurrentView(); cmd != nil {
				return m, tea.Batch(tickCmd(), cmd)
			}
		}
		return m, tickCmd()

	case statusUpdateMsg:
		if s, ok := m.sessions[msg.channel]; ok && s.gen == msg.gen {
			if msg.done {
				s.cancel()
				delete(m.sessions, msg.channel)
				return m, nil
			}
			if msg.notice != "" {
				m.notice = msg.notice
			} else {
				s.status = msg.status
				s.detail = msg.detail
			}
			return m, waitPlayback(s.ctx, s.ch, msg.channel, msg.gen)
		}
		return m, nil

	case titleScrollMsg:
		if e := m.currentEntry(); e != nil && e.IsLive && e.Title != "" {
			title := sanitizeText(e.Title)
			titleW := m.width - 2 - 56 // colFixed from renderChannelList
			if titleW < 1 {
				titleW = 1
			}
			if uniseg.StringWidth(title) > titleW {
				gr := uniseg.NewGraphemes(title)
				var clusters []string
				for gr.Next() {
					clusters = append(clusters, gr.Str())
				}
				maxScroll := len(clusters) - 1
				for i := range clusters {
					if uniseg.StringWidth(strings.Join(clusters[i:], "")) <= titleW {
						maxScroll = i
						break
					}
				}
				if maxScroll > 0 {
					dir := m.titleScrollDir
					if dir == 0 {
						dir = 1
					}
					m.titleScrollOffset += dir
					if m.titleScrollOffset >= maxScroll {
						m.titleScrollOffset = maxScroll
						m.titleScrollDir = -1
					} else if m.titleScrollOffset <= 0 {
						m.titleScrollOffset = 0
						m.titleScrollDir = 1
					} else {
						m.titleScrollDir = dir
					}
				}
			}
		}
		return m, titleScrollCmd()
	}

	return m, nil
}

// View implements tea.Model (bubbletea v2: returns tea.View).
func (m Model) View() tea.View {
	content := m.render()
	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// Minimum terminal dimensions for the TUI to render its table frame cleanly.
// Below this, lipgloss's column flex produces corrupt borders.
const (
	minTerminalWidth  = 60
	minTerminalHeight = 10
)

// tooSmallView produces a minimal message that fits in any viewport down to 1x1.
func tooSmallView(w, h int) string {
	msg := fmt.Sprintf("Terminal too small — resize to at least %dx%d", minTerminalWidth, minTerminalHeight)
	if w < len(msg) {
		msg = "too small"
	}
	lines := make([]string, h)
	if h > 0 {
		lines[0] = msg
	}
	return strings.Join(lines, "\n")
}

func (m Model) render() string {
	if m.width == 0 {
		return ""
	}
	if m.width < minTerminalWidth || m.height < minTerminalHeight {
		return tooSmallView(m.width, m.height)
	}

	// The outer frame is a 3-row lipgloss table: tab bar, body, footer.
	// The body itself is plain text lines (channel/category/ignored tables render their own content).
	bodyHeight := m.height - 6 // top border + tab + separator + separator + footer + bottom border
	if bodyHeight < 1 {
		bodyHeight = 1
	}

	bodyStr := m.renderBody(bodyHeight)
	// Ensure body fills exactly bodyHeight lines.
	bodyLines := strings.Split(bodyStr, "\n")
	for len(bodyLines) < bodyHeight {
		bodyLines = append(bodyLines, "")
	}
	bodyStr = strings.Join(bodyLines[:bodyHeight], "\n")

	frame := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(m.styles.border).
		BorderColumn(false).
		BorderRow(true).
		BorderHeader(false).
		Width(m.width).
		Row(m.renderTabBar()).
		Row(bodyStr).
		Row(m.renderFooter())

	result := frame.String()

	if m.overlay != overlayNone {
		return overlayOn(result, m.renderOverlay(), m.width, m.height)
	}

	return result
}

func (m Model) renderTabBar() string {
	tabs := []struct {
		label string
		mode  viewMode
	}{
		{"Watch List", viewModeWatchList},
		{"Browse", viewModeBrowse},
		{"Search", viewModeSearch},
		{"Ignored", viewModeIgnored},
	}

	var parts []string
	for _, tab := range tabs {
		label := " " + tab.label + " "
		if m.mode == tab.mode {
			parts = append(parts, m.styles.tabActive.Render("["+label+"]"))
		} else {
			parts = append(parts, m.styles.text.Render(" "+label+" "))
		}
	}

	result := strings.Join(parts, m.styles.text.Render(" · "))
	if m.refreshInterval > 0 {
		if m.refreshing {
			result += m.styles.text.Render("  ↻ refreshing…")
		} else {
			result += m.styles.text.Render(fmt.Sprintf("  ↻ %s", m.refreshCountdown.Truncate(time.Second)))
		}
	}
	return result
}

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
	// Prefix: status(1) + sp(1) + fav(1) + sp(1) = 4
	// Order: Channel · Category · Title(flex) · Viewers · Uptime
	// Fixed total: 4 + 16 + 2 + 14 + 2 + 2 + 7 + 2 + 7 = 56; title gets the rest.
	const (
		colStatus   = 2
		colFav      = 2
		colChannel  = 16
		colViewers  = 7 // fits "Viewers" header (7 chars)
		colUptime   = 7 // fits "0h 52m" / "12h 30m"
		colCategory = 14
		colFixed    = 56
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

		statusCh := " "
		if sess, ok := m.sessions[e.Login]; ok {
			switch sess.status {
			case StatusPlaying:
				statusCh = m.symbols.Playing
			case StatusAdBreak:
				statusCh = m.symbols.AdBreak
			case StatusWaiting:
				statusCh = m.symbols.Waiting
			case StatusReconnecting:
				statusCh = m.symbols.Reconnecting
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
			m.width - 2,
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
			row = m.styles.selected.Render(padRight(stripANSI(row), m.width - 2))
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

func (m Model) renderFooter() string {
	if m.notice != "" {
		return m.styles.live.Render(cellTruncate("  "+m.notice, m.width - 2))
	}

	hint := func(key, desc string) string {
		return m.styles.title.Render(key) + " " + m.styles.text.Render(desc)
	}
	dot := m.styles.text.Render(" · ")

	// Left side.
	var left string
	switch {
	case m.mode == viewModeSearch && m.searching:
		left = "  " + hint("Enter", "confirm") + dot + hint("Esc", "cancel")
	case m.mode == viewModeBrowse && len(m.categoryStack) > 0:
		left = "  " + hint("Esc", "back") + m.styles.text.Render("  "+strings.Join(m.categoryStack, " › "))
	default:
		live, offline := m.countLiveOffline()
		left = fmt.Sprintf("  %d live · %d offline", live, offline)
	}

	// Right side.
	var right string
	switch {
	default:
		parts := []string{
			hint("Enter", "play"),
			hint("i", "quality"),
			hint("f", "fav"),
			hint("x", "ignore"),
			hint("r", "related"),
			hint("t", "theme"),
		}
		right = strings.Join(parts, dot) + "  "
	}

	// Pad between left and right.
	leftW := uniseg.StringWidth(stripANSI(left))
	rightW := uniseg.StringWidth(stripANSI(right))
	if gap := m.width - 2 - leftW - rightW; gap > 0 {
		return left + strings.Repeat(" ", gap) + right
	}
	return left + "  " + right
}

// countLiveOffline counts live and offline channel entries in the current view.
func (m Model) countLiveOffline() (int, int) {
	var entries []DiscoveryEntry
	switch m.mode {
	case viewModeWatchList:
		entries = m.watchList
	case viewModeBrowse:
		entries = m.categoryList
	case viewModeSearch:
		entries = m.searchList
	}
	live, offline := 0, 0
	for _, e := range entries {
		if e.Kind != EntryChannel {
			continue
		}
		if e.IsLive {
			live++
		} else {
			offline++
		}
	}
	return live, offline
}

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

func (m Model) renderThemeOverlay() string {
	w := 30
	title := " Theme Picker "
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
			row = m.styles.selected.Render(padRight(row, m.width - 2))
		} else {
			row = m.styles.offline.Render(row)
		}
		lines = append(lines, row)
	}
	return lines
}

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

func (m Model) renderHelpOverlay() string {
	w := 50
	lines := m.overlayHeader(" Keyboard Shortcuts", w)
	for _, b := range m.bindings() {
		if b.Display == "" || b.Desc == "" {
			// Continuation entry (e.g., shift+tab paired with tab) — handled
			// by the sibling binding with a non-empty Display; skip to avoid
			// rendering blank rows.
			continue
		}
		lines = append(lines, m.overlayRow(pad(fmt.Sprintf("  %-22s %s", b.Display, b.Desc), w)))
	}
	lines = append(lines, m.overlayFooter(w))
	return strings.Join(lines, "\n")
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.notice = "" // clear any transient notice on key press
	if m.overlay == overlayHelp {
		switch msg.String() {
		case "?", "esc", "q", "Q":
			m.overlay = overlayNone
		}
		return m, nil
	}

	if m.overlay == overlayQuality {
		return m.handleQualityKey(msg)
	}

	if m.overlay == overlayTheme {
		return m.handleThemeKey(msg)
	}

	if m.overlay == overlayRelated {
		return m.handleRelatedKey(msg)
	}

	if m.searching {
		return m.handleSearchInput(msg)
	}

	if newM, cmd, ok := m.dispatchBinding(msg.String()); ok {
		return newM, cmd
	}
	return m, nil
}

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

func (m Model) handleRelatedKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "r", "q", "Q":
		m.overlay = overlayNone
	}
	return m, nil
}

func (m Model) handleSearchInput(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.searching = false
		if m.searchInput == "" {
			m.searchList = nil
		}
		return m, nil
	case "enter":
		m.searching = false
		m.searchQuery = m.searchInput
		return m, m.runSearch(m.searchInput)
	case "backspace", "ctrl+h":
		if len(m.searchInput) > 0 {
			_, size := utf8.DecodeLastRuneInString(m.searchInput)
			m.searchInput = m.searchInput[:len(m.searchInput)-size]
		}
		return m, m.debounceSearch(m.searchInput)
	default:
		if msg.Text != "" {
			m.searchInput += msg.Text
			return m, m.debounceSearch(m.searchInput)
		}
	}
	return m, nil
}

func (m Model) handleMouseClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if m.overlay != overlayNone {
		return m, nil
	}
	if msg.Button == tea.MouseLeft {
		// Row 0 = tab bar, row 1 = separator, rows 2+ = body
		row := msg.Y - 2
		if row >= 0 && row < m.currentListLen() {
			if row == m.cursor {
				e := m.currentEntry()
				if e != nil && e.Kind == EntryChannel && e.IsLive {
					return m.handleEnter()
				}
			}
			m.cursor = row
		}
	}
	return m, nil
}

func (m Model) handleMouseWheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	if m.overlay != overlayNone {
		return m, nil
	}
	switch msg.Button {
	case tea.MouseWheelUp:
		m = m.moveCursor(-1)
	case tea.MouseWheelDown:
		m = m.moveCursor(1)
	}
	return m, nil
}

func (m Model) handleEnter() (tea.Model, tea.Cmd) {
	switch m.mode {
	case viewModeWatchList:
		if m.cursor < len(m.watchList) {
			e := m.watchList[m.cursor]
			if e.IsLive {
				return m, m.launchStream(e.Login, "", e.AvatarURL)
			}
		}
	case viewModeBrowse:
		if len(m.categoryStack) == 0 {
			if m.cursor < len(m.browseList) {
				e := m.browseList[m.cursor]
				if e.Kind == EntryCategory {
					m.categoryStack = append(m.categoryStack, e.CategoryName)
					m.categoryList = nil
					m.categoryCursor = ""
					m.cursor = 0
					m.loading = true
					return m, m.loadCategoryStreams(e.CategoryName, "", false)
				}
			}
		} else {
			if m.cursor < len(m.categoryList) {
				e := m.categoryList[m.cursor]
				if e.Kind == EntryLoadMore {
					return m, m.loadCategoryStreams(m.categoryStack[len(m.categoryStack)-1], e.Cursor, true)
				}
				if e.IsLive {
					return m, m.launchStream(e.Login, "", e.AvatarURL)
				}
			}
		}
	case viewModeSearch:
		if m.cursor < len(m.searchList) {
			e := m.searchList[m.cursor]
			if e.IsLive {
				return m, m.launchStream(e.Login, "", e.AvatarURL)
			}
		}
	case viewModeIgnored:
		// Enter in Ignored view un-ignores the selected channel
		return m.handleIgnoredUnignore()
	}
	return m, nil
}

func (m Model) handleEsc() (tea.Model, tea.Cmd) {
	if m.searching {
		m.searching = false
		return m, nil
	}
	if len(m.categoryStack) > 0 {
		m.categoryStack = m.categoryStack[:len(m.categoryStack)-1]
		m.cursor = 0
		if len(m.categoryStack) == 0 {
			return m, m.loadBrowse()
		}
		return m, m.loadCategoryStreams(m.categoryStack[len(m.categoryStack)-1], "", false)
	}
	return m, nil
}

func (m Model) handleFavorite() (tea.Model, tea.Cmd) {
	if m.mode == viewModeIgnored {
		// In Ignored view: un-ignore + add to favorites
		ignored := m.fns.IgnoreList()
		if m.cursor >= len(ignored) {
			return m, nil
		}
		ch := ignored[m.cursor]
		m.fns.ToggleIgnore(ch, false)
		m.fns.ToggleFavorite(ch, true)
		if m.cursor > 0 && m.cursor >= len(ignored)-1 {
			m.cursor--
		}
		return m, nil
	}
	e := m.currentEntry()
	if e == nil || e.Kind != EntryChannel {
		return m, nil
	}
	e.IsFavorite = !e.IsFavorite
	m.fns.ToggleFavorite(e.Login, e.IsFavorite)
	return m, nil
}

func (m Model) handleIgnore() (tea.Model, tea.Cmd) {
	if m.mode == viewModeIgnored {
		return m.handleIgnoredUnignore()
	}
	e := m.currentEntry()
	if e == nil || e.Kind != EntryChannel {
		return m, nil
	}
	m.fns.ToggleIgnore(e.Login, true)
	m.removeCurrentEntry()
	return m, nil
}

func (m Model) handleIgnoredUnignore() (tea.Model, tea.Cmd) {
	ignored := m.fns.IgnoreList()
	if m.cursor >= len(ignored) {
		return m, nil
	}
	ch := ignored[m.cursor]
	m.fns.ToggleIgnore(ch, false)
	if m.cursor > 0 && m.cursor >= len(ignored)-1 {
		m.cursor--
	}
	return m, nil
}

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

func (m Model) currentEntry() *DiscoveryEntry {
	switch m.mode {
	case viewModeWatchList:
		if m.cursor < len(m.watchList) {
			return &m.watchList[m.cursor]
		}
	case viewModeBrowse:
		if len(m.categoryStack) > 0 {
			if m.cursor < len(m.categoryList) {
				return &m.categoryList[m.cursor]
			}
		} else {
			if m.cursor < len(m.browseList) {
				return &m.browseList[m.cursor]
			}
		}
	case viewModeSearch:
		if m.cursor < len(m.searchList) {
			return &m.searchList[m.cursor]
		}
	case viewModeIgnored:
		// Ignored view uses IgnoreList() directly; return nil (handled separately)
		return nil
	}
	return nil
}

func (m *Model) removeCurrentEntry() {
	switch m.mode {
	case viewModeWatchList:
		if m.cursor < len(m.watchList) {
			m.watchList = append(m.watchList[:m.cursor], m.watchList[m.cursor+1:]...)
			if m.cursor >= len(m.watchList) && m.cursor > 0 {
				m.cursor--
			}
		}
	case viewModeSearch:
		if m.cursor < len(m.searchList) {
			m.searchList = append(m.searchList[:m.cursor], m.searchList[m.cursor+1:]...)
			if m.cursor >= len(m.searchList) && m.cursor > 0 {
				m.cursor--
			}
		}
	case viewModeBrowse:
		if len(m.categoryStack) > 0 && m.cursor < len(m.categoryList) {
			m.categoryList = append(m.categoryList[:m.cursor], m.categoryList[m.cursor+1:]...)
			if m.cursor >= len(m.categoryList) && m.cursor > 0 {
				m.cursor--
			}
		}
	}
}

func (m Model) moveCursor(delta int) Model {
	n := m.currentListLen()
	if n == 0 {
		m.cursor = 0
		return m
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= n {
		m.cursor = n - 1
	}
	m.titleScrollOffset = 0
	m.titleScrollDir = 1
	return m
}

func (m Model) currentListLen() int {
	switch m.mode {
	case viewModeWatchList:
		return len(m.watchList)
	case viewModeBrowse:
		if len(m.categoryStack) > 0 {
			return len(m.categoryList)
		}
		return len(m.browseList)
	case viewModeSearch:
		return len(m.searchList)
	case viewModeIgnored:
		return len(m.fns.IgnoreList())
	}
	return 0
}

func (m Model) filterIgnored(entries []DiscoveryEntry) []DiscoveryEntry {
	ignored := m.fns.IgnoreList()
	if len(ignored) == 0 {
		return entries
	}
	set := make(map[string]struct{}, len(ignored))
	for _, ch := range ignored {
		set[ch] = struct{}{}
	}
	result := make([]DiscoveryEntry, 0, len(entries))
	for _, e := range entries {
		if _, ok := set[e.Login]; !ok {
			result = append(result, e)
		}
	}
	return result
}

// --- Commands ---

func (m Model) loadWatchList() tea.Cmd {
	m.loading = true
	ctx := m.ctx
	fns := m.fns
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, timeoutBrowse)
		defer cancel()
		entries, err := fns.WatchList(c)
		return watchListResultMsg{entries: entries, err: err}
	}
}

func (m Model) loadBrowse() tea.Cmd {
	m.loading = true
	ctx := m.ctx
	fns := m.fns
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, timeoutBrowse)
		defer cancel()
		entries, next, err := fns.BrowseCategories(c, "")
		return browseResultMsg{entries: entries, nextCursor: next, err: err}
	}
}

func (m Model) loadCategoryStreams(category, cursor string, appendMode bool) tea.Cmd {
	ctx := m.ctx
	fns := m.fns
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, timeoutBrowse)
		defer cancel()
		entries, next, err := fns.CategoryStreams(c, category, cursor)
		return categoryStreamsResultMsg{
			category:   category,
			entries:    entries,
			nextCursor: next,
			err:        err,
			appendMode: appendMode,
		}
	}
}

func (m Model) loadCurrentView() tea.Cmd {
	switch m.mode {
	case viewModeWatchList:
		return m.loadWatchList()
	case viewModeBrowse:
		if len(m.categoryStack) == 0 && len(m.browseList) == 0 {
			return m.loadBrowse()
		}
	case viewModeSearch:
		if m.searchInput != "" {
			return m.runSearch(m.searchInput)
		}
	}
	return nil
}

// refreshCurrentView returns a cmd that re-fetches the active view's data,
// flagging the result as a refresh so cursor position is preserved.
func (m Model) refreshCurrentView() tea.Cmd {
	ctx := m.ctx
	fns := m.fns
	switch m.mode {
	case viewModeWatchList:
		return func() tea.Msg {
			c, cancel := context.WithTimeout(ctx, timeoutBrowse)
			defer cancel()
			entries, err := fns.WatchList(c)
			return watchListResultMsg{entries: entries, err: err, refreshed: true}
		}
	case viewModeBrowse:
		if len(m.categoryStack) > 0 {
			category := m.categoryStack[len(m.categoryStack)-1]
			return func() tea.Msg {
				c, cancel := context.WithTimeout(ctx, timeoutBrowse)
				defer cancel()
				entries, next, err := fns.CategoryStreams(c, category, "")
				return categoryStreamsResultMsg{category: category, entries: entries, nextCursor: next, err: err, refresh: true}
			}
		}
		return func() tea.Msg {
			c, cancel := context.WithTimeout(ctx, timeoutBrowse)
			defer cancel()
			entries, next, err := fns.BrowseCategories(c, "")
			return browseResultMsg{entries: entries, nextCursor: next, err: err, refreshed: true}
		}
	case viewModeSearch:
		query := m.searchQuery
		if query == "" {
			return nil
		}
		return func() tea.Msg {
			c, cancel := context.WithTimeout(ctx, timeoutSearch)
			defer cancel()
			entries, err := fns.Search(c, query)
			return searchResultMsg{query: query, entries: entries, err: err, refreshed: true}
		}
	}
	return nil
}

// cursorLogin returns the login of the entry under the cursor in the given view,
// or "" if no entry or not a channel entry. Used to remember position across refreshes.
func (m Model) cursorLogin(mode viewMode) string {
	var list []DiscoveryEntry
	switch mode {
	case viewModeWatchList:
		list = m.watchList
	case viewModeBrowse:
		list = m.categoryList
	case viewModeSearch:
		list = m.searchList
	}
	if m.cursor < 0 || m.cursor >= len(list) {
		return ""
	}
	if list[m.cursor].Kind != EntryChannel {
		return ""
	}
	return list[m.cursor].Login
}

// cursorCategory returns the CategoryName under the cursor in the browse top-level view.
func (m Model) cursorCategory() string {
	if m.cursor < 0 || m.cursor >= len(m.browseList) {
		return ""
	}
	if m.browseList[m.cursor].Kind != EntryCategory {
		return ""
	}
	return m.browseList[m.cursor].CategoryName
}

// findEntryByLogin returns the index of the channel entry with matching login, or 0 if not found.
func findEntryByLogin(entries []DiscoveryEntry, login string) int {
	if login == "" {
		return 0
	}
	for i, e := range entries {
		if e.Kind == EntryChannel && e.Login == login {
			return i
		}
	}
	return 0
}

// findCategoryByName returns the index of the category entry with matching name, or 0 if not found.
func findCategoryByName(entries []DiscoveryEntry, name string) int {
	if name == "" {
		return 0
	}
	for i, e := range entries {
		if e.Kind == EntryCategory && e.CategoryName == name {
			return i
		}
	}
	return 0
}

func (m Model) runSearch(query string) tea.Cmd {
	if query == "" {
		return nil
	}
	ctx := m.ctx
	fns := m.fns
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, timeoutSearch)
		defer cancel()
		entries, err := fns.Search(c, query)
		return searchResultMsg{query: query, entries: entries, err: err}
	}
}

func (m Model) debounceSearch(query string) tea.Cmd {
	return tea.Tick(debounceSearch, func(time.Time) tea.Msg {
		return searchDebounceMsg{query: query}
	})
}

func (m Model) launchStream(channel, quality, avatarURL string) tea.Cmd {
	m.lastGen++
	gen := m.lastGen

	if old, ok := m.sessions[channel]; ok {
		old.cancel()
		// Drain old channel
		if old.ch != nil {
			go func() {
				for range old.ch {
				}
			}()
		}
	}

	ch := make(chan statusUpdateMsg, 16)
	sctx, cancel := context.WithCancel(m.ctx)
	m.sessions[channel] = &playbackSession{
		ctx:    sctx,
		cancel: cancel,
		status: StatusWaiting,
		gen:    gen,
		ch:     ch,
	}
	ctx := sctx

	fns := m.fns
	go func() {
		// Guarantee cleanup even if fns.Launch panics. Without this, a panicking
		// Launch would leave ch open forever — and the drain goroutine spawned
		// by the next launchStream for the same channel would block indefinitely.
		defer func() {
			if r := recover(); r != nil {
				slog.Error("Launch panicked", "channel", channel, "panic", r)
			}
			// Best-effort done signal; non-blocking since a cancelled session
			// may no longer be reading ch.
			select {
			case ch <- statusUpdateMsg{channel: channel, done: true, gen: gen}:
			default:
			}
			close(ch)
		}()

		send := func(status Status, detail string) {
			select {
			case ch <- statusUpdateMsg{channel: channel, status: status, detail: detail, gen: gen}:
			default:
			}
		}
		notice := func(text string) {
			select {
			case ch <- statusUpdateMsg{channel: channel, notice: text, gen: gen}:
			default:
			}
		}
		fns.Launch(ctx, channel, quality, avatarURL, send, notice)
	}()

	return waitPlayback(ctx, ch, channel, gen)
}

// waitPlayback returns a Cmd that reads one event from the playback channel.
func waitPlayback(ctx context.Context, ch <-chan statusUpdateMsg, channel string, gen int) tea.Cmd {
	return func() tea.Msg {
		select {
		case msg, ok := <-ch:
			if !ok || msg.done {
				return statusUpdateMsg{channel: channel, done: true, gen: gen}
			}
			return msg
		case <-ctx.Done():
			return statusUpdateMsg{channel: channel, done: true, gen: gen}
		}
	}
}

// --- Overlay helpers ---

func (m Model) overlayHeader(title string, w int) []string {
	return []string{
		m.styles.border.Render("┌" + strings.Repeat("─", w) + "┐"),
		m.styles.border.Render("│") + m.styles.title.Render(pad(title, w)) + m.styles.border.Render("│"),
		m.styles.border.Render("├" + strings.Repeat("─", w) + "┤"),
	}
}

func (m Model) overlayFooter(w int) string {
	return m.styles.border.Render("└" + strings.Repeat("─", w) + "┘")
}

func (m Model) overlayRow(content string) string {
	return m.styles.border.Render("│") + content + m.styles.border.Render("│")
}

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

// --- Formatting helpers ---

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
	if uniseg.StringWidth(s) <= width {
		return s
	}
	w := 0
	var sb strings.Builder
	gr := uniseg.NewGraphemes(s)
	for gr.Next() {
		gw := gr.Width()
		if w+gw > width-1 { // leave 1 cell for the ellipsis
			break
		}
		sb.WriteString(gr.Str())
		w += gw
	}
	sb.WriteRune('…')
	return sb.String()
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

// overlayOn centers overlay content on top of the base string.
func overlayOn(base, overlay string, width, height int) string {
	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")

	oHeight := len(overlayLines)
	oWidth := 0
	for _, l := range overlayLines {
		// Strip ANSI for width calculation
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

