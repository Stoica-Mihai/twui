package ui

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/mattn/go-runewidth"
)

// viewMode identifies which tab/view is active.
type viewMode int

const (
	viewModeWatchList viewMode = iota
	viewModeBrowse
	viewModeSearch
)

// overlayMode identifies an active overlay.
type overlayMode int

const (
	overlayNone    overlayMode = iota
	overlayQuality             // quality picker for a channel
	overlayHelp                // help screen
	overlayTheme               // theme picker
)

// internal Bubble Tea messages
type (
	watchListResultMsg struct {
		entries []DiscoveryEntry
		err     error
	}
	searchResultMsg struct {
		query   string
		entries []DiscoveryEntry
		err     error
	}
	browseResultMsg struct {
		entries    []DiscoveryEntry
		nextCursor string
		err        error
	}
	categoryStreamsResultMsg struct {
		category   string
		entries    []DiscoveryEntry
		nextCursor string
		err        error
		appendMode bool
	}
	qualityResultMsg struct {
		channel   string
		qualities []string
		err       error
	}
	searchDebounceMsg struct {
		query string
	}
	statusUpdateMsg struct {
		channel string
		status  Status
		detail  string
		gen     int
	}
)

// playbackSession tracks an active or recent playback.
type playbackSession struct {
	cancel context.CancelFunc
	status Status
	detail string
	gen    int
}

// Model is the main Bubble Tea model for twui.
type Model struct {
	fns DiscoveryFuncs

	// layout
	width, height int

	// view state
	mode   viewMode
	cursor int
	styles pickerStyles

	// data per view
	watchList  []DiscoveryEntry
	browseList []DiscoveryEntry
	browseNextCursor string
	searchList  []DiscoveryEntry
	searchQuery string
	searchInput string
	searching   bool

	categoryStack  []string
	categoryList   []DiscoveryEntry
	categoryCursor string

	loading bool
	err     error

	// overlays
	overlay        overlayMode
	overlayList    []string
	overlayCursor  int
	overlayChannel string

	// playback
	sessions map[string]*playbackSession
	lastGen  int

	// theme picker index
	themeIdx int

	// title scroll
	titleScrollOffset int

	// global ctx
	cancel context.CancelFunc
	ctx    context.Context
}

// NewModel creates a new Model.
func NewModel(fns DiscoveryFuncs, theme Theme) *Model {
	ctx, cancel := context.WithCancel(context.Background())
	return &Model{
		fns:      fns,
		styles:   buildStyles(theme),
		sessions: make(map[string]*playbackSession),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Init implements tea.Model (bubbletea v2: returns tea.Cmd).
func (m Model) Init() tea.Cmd {
	return m.loadWatchList()
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
		m.loading = false
		m.err = msg.err
		if msg.err == nil {
			m.watchList = m.filterIgnored(msg.entries)
		}
		return m, nil

	case searchResultMsg:
		if msg.query != m.searchQuery {
			return m, nil
		}
		m.loading = false
		m.err = msg.err
		if msg.err == nil {
			m.searchList = m.filterIgnored(msg.entries)
			m.cursor = 0
		}
		return m, nil

	case browseResultMsg:
		m.loading = false
		m.err = msg.err
		if msg.err == nil {
			m.browseList = msg.entries
			m.browseNextCursor = msg.nextCursor
			if m.mode == viewModeBrowse && len(m.categoryStack) == 0 {
				m.cursor = 0
			}
		}
		return m, nil

	case categoryStreamsResultMsg:
		m.loading = false
		m.err = msg.err
		if msg.err == nil {
			filtered := m.filterIgnored(msg.entries)
			if msg.appendMode {
				m.categoryList = append(m.categoryList, filtered...)
			} else {
				m.categoryList = filtered
				m.cursor = 0
			}
			m.categoryCursor = msg.nextCursor
		}
		return m, nil

	case qualityResultMsg:
		m.loading = false
		if msg.err != nil || len(msg.qualities) == 0 {
			return m, m.launchStream(msg.channel, "")
		}
		m.overlay = overlayQuality
		m.overlayList = msg.qualities
		m.overlayCursor = 0
		m.overlayChannel = msg.channel
		return m, nil

	case searchDebounceMsg:
		if msg.query != m.searchInput {
			return m, nil
		}
		m.searchQuery = msg.query
		return m, m.runSearch(msg.query)

	case statusUpdateMsg:
		if s, ok := m.sessions[msg.channel]; ok && s.gen == msg.gen {
			s.status = msg.status
			s.detail = msg.detail
		}
		return m, nil
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

func (m Model) render() string {
	if m.width == 0 {
		return ""
	}

	var sb strings.Builder

	sb.WriteString(m.renderTabBar())
	sb.WriteString("\n")
	sb.WriteString(m.styles.border.Render(strings.Repeat("─", m.width)))
	sb.WriteString("\n")

	bodyHeight := m.height - 4
	if bodyHeight < 1 {
		bodyHeight = 1
	}

	body := m.renderBody(bodyHeight)
	sb.WriteString(body)

	sb.WriteString(m.styles.border.Render(strings.Repeat("─", m.width)))
	sb.WriteString("\n")
	sb.WriteString(m.renderFooter())

	result := sb.String()

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

	return strings.Join(parts, m.styles.border.Render("│"))
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

	colStatus := 2
	colFav := 2
	colViewers := 8
	colUptime := 8
	colCategory := 14
	colTitle := m.width - colStatus - colFav - colViewers - colUptime - colCategory - 22
	if colTitle < 10 {
		colTitle = 10
	}
	colChannel := m.width - colStatus - colFav - colViewers - colUptime - colCategory - colTitle - 4
	if colChannel < 8 {
		colChannel = 8
	}

	header := m.styles.title.Render(
		strings.Repeat(" ", colStatus+colFav+2) +
			pad("Channel", colChannel) +
			pad("Viewers", colViewers) +
			pad("Uptime", colUptime) +
			pad("Category", colCategory) +
			pad("Title", colTitle),
	)

	lines := []string{header}
	visibleStart := 0
	if m.cursor >= height-2 {
		visibleStart = m.cursor - (height - 3)
	}
	if visibleStart < 0 {
		visibleStart = 0
	}

	for i, e := range entries {
		if i < visibleStart {
			continue
		}
		if len(lines) >= height {
			break
		}

		selected := i == m.cursor

		statusStr := " "
		if sess, ok := m.sessions[e.Login]; ok {
			switch sess.status {
			case StatusPlaying:
				statusStr = m.styles.playing.Render("▶")
			case StatusAdBreak:
				statusStr = m.styles.adBreak.Render("◐")
			case StatusWaiting:
				statusStr = m.styles.waiting.Render("○")
			case StatusReconnecting:
				statusStr = m.styles.reconnecting.Render("⟳")
			}
		}

		favStr := " "
		if e.IsFavorite {
			favStr = m.styles.favorite.Render("★")
		}

		chanStr := cellTruncate(e.DisplayName, colChannel)

		viewStr := ""
		if e.IsLive && e.ViewerCount > 0 {
			viewStr = formatViewers(e.ViewerCount)
		}

		uptimeStr := ""
		if e.IsLive && !e.StartedAt.IsZero() {
			uptimeStr = formatUptime(time.Since(e.StartedAt))
		}

		catStr := cellTruncate(e.Category, colCategory)
		titleFull := e.Title
		titleStr := cellTruncate(titleFull, colTitle)
		if selected && runewidth.StringWidth(titleFull) > colTitle {
			titleStr = scrollTitle(titleFull, colTitle, m.titleScrollOffset)
		}

		row := statusStr + " " + favStr + " " +
			pad(chanStr, colChannel) +
			pad(viewStr, colViewers) +
			pad(uptimeStr, colUptime)

		if e.IsLive {
			row += m.styles.category.Render(pad(catStr, colCategory))
			row += m.styles.live.Render(pad(titleStr, colTitle))
		} else {
			row += m.styles.offline.Render(pad(catStr, colCategory) + pad(titleStr, colTitle))
		}

		if selected {
			row = m.styles.selected.Render(padRight(row, m.width))
		}

		lines = append(lines, row)
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
	colName := m.width - colViewers - 2
	if colName < 10 {
		colName = 10
	}

	header := m.styles.title.Render(pad("Category", colName) + pad("Viewers", colViewers))
	lines := []string{header}

	visibleStart := 0
	if m.cursor >= height-2 {
		visibleStart = m.cursor - (height - 3)
	}

	for i, e := range entries {
		if i < visibleStart {
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
			row = m.styles.selected.Render(padRight(row, m.width))
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
	var hints string
	switch {
	case m.mode == viewModeSearch && m.searching:
		hints = "  Type to search  Enter: confirm  Esc: cancel"
	case m.mode == viewModeBrowse && len(m.categoryStack) > 0:
		hints = "  Esc: back  " + strings.Join(m.categoryStack, " › ")
	default:
		hints = "  Tab: switch  j/k: nav  Enter: open  f: fav  x: ignore  i: quality  t: theme  ?: help  q: quit"
	}
	return m.styles.text.Render(cellTruncate(hints, m.width))
}

func (m Model) renderOverlay() string {
	switch m.overlay {
	case overlayQuality:
		return m.renderQualityOverlay()
	case overlayHelp:
		return m.renderHelpOverlay()
	case overlayTheme:
		return m.renderThemeOverlay()
	}
	return ""
}

func (m Model) renderQualityOverlay() string {
	title := fmt.Sprintf(" Quality — %s ", m.overlayChannel)
	w := len(title)
	lines := []string{
		m.styles.border.Render("┌" + strings.Repeat("─", w) + "┐"),
		m.styles.border.Render("│") + m.styles.title.Render(title) + m.styles.border.Render("│"),
		m.styles.border.Render("├" + strings.Repeat("─", w) + "┤"),
	}
	for i, q := range m.overlayList {
		row := fmt.Sprintf("  %-*s  ", w-4, q)
		if i == m.overlayCursor {
			row = m.styles.selected.Render(row)
		}
		lines = append(lines, m.styles.border.Render("│")+row+m.styles.border.Render("│"))
	}
	lines = append(lines, m.styles.border.Render("└"+strings.Repeat("─", w)+"┘"))
	return strings.Join(lines, "\n")
}

func (m Model) renderThemeOverlay() string {
	w := 30
	title := " Theme Picker "
	lines := []string{
		m.styles.border.Render("┌" + strings.Repeat("─", w) + "┐"),
		m.styles.border.Render("│") + m.styles.title.Render(pad(title, w)) + m.styles.border.Render("│"),
		m.styles.border.Render("├" + strings.Repeat("─", w) + "┤"),
	}
	for i, p := range Presets {
		row := fmt.Sprintf("  %-*s  ", w-4, p.Name)
		if i == m.themeIdx {
			row = m.styles.selected.Render(row)
		}
		lines = append(lines, m.styles.border.Render("│")+row+m.styles.border.Render("│"))
	}
	lines = append(lines, m.styles.border.Render("└"+strings.Repeat("─", w)+"┘"))
	return strings.Join(lines, "\n")
}

func (m Model) renderHelpOverlay() string {
	keys := [][2]string{
		{"Tab / Shift+Tab", "Switch view"},
		{"j / k / ↑ / ↓", "Navigate"},
		{"PgUp / PgDn", "Page scroll"},
		{"g / G", "Top / bottom"},
		{"Enter", "Open / launch stream"},
		{"/", "Activate search"},
		{"Esc", "Back / cancel"},
		{"f", "Toggle favorite"},
		{"x", "Toggle ignore"},
		{"i", "Quality picker"},
		{"t", "Theme picker"},
		{"r", "Refresh watch list"},
		{"?", "Toggle help"},
		{"q / Ctrl+C", "Quit"},
	}
	w := 50
	lines := []string{
		m.styles.border.Render("┌" + strings.Repeat("─", w) + "┐"),
		m.styles.border.Render("│") + m.styles.title.Render(pad(" Keyboard Shortcuts", w)) + m.styles.border.Render("│"),
		m.styles.border.Render("├" + strings.Repeat("─", w) + "┤"),
	}
	for _, kv := range keys {
		row := fmt.Sprintf("  %-22s %s", kv[0], kv[1])
		lines = append(lines,
			m.styles.border.Render("│")+pad(row, w)+m.styles.border.Render("│"))
	}
	lines = append(lines, m.styles.border.Render("└"+strings.Repeat("─", w)+"┘"))
	return strings.Join(lines, "\n")
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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

	if m.searching {
		return m.handleSearchInput(msg)
	}

	switch msg.String() {
	case "q", "Q", "ctrl+c":
		m.cancel()
		return m, tea.Quit

	case "?":
		m.overlay = overlayHelp
		return m, nil

	case "tab":
		m.mode = (m.mode + 1) % 3
		m.cursor = 0
		return m, m.loadCurrentView()

	case "shift+tab":
		m.mode = (m.mode + 2) % 3
		m.cursor = 0
		return m, m.loadCurrentView()

	case "j", "down":
		m = m.moveCursor(1)
		return m, nil

	case "k", "up":
		m = m.moveCursor(-1)
		return m, nil

	case "pgdown":
		m = m.moveCursor(10)
		return m, nil

	case "pgup":
		m = m.moveCursor(-10)
		return m, nil

	case "g":
		m.cursor = 0
		return m, nil

	case "G":
		m.cursor = m.currentListLen() - 1
		return m, nil

	case "home":
		m.cursor = 0
		return m, nil

	case "end":
		n := m.currentListLen()
		if n > 0 {
			m.cursor = n - 1
		}
		return m, nil

	case "enter":
		return m.handleEnter()

	case "esc":
		return m.handleEsc()

	case "f":
		return m.handleFavorite()

	case "x":
		return m.handleIgnore()

	case "i":
		return m.handleQualityPicker()

	case "t":
		m.overlay = overlayTheme
		m.themeIdx = 0
		return m, nil

	case "r":
		return m, m.loadWatchList()

	case "/":
		if m.mode != viewModeSearch {
			m.mode = viewModeSearch
		}
		m.searching = true
		return m, nil
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
		m.overlay = overlayNone
		return m, m.launchStream(ch, quality)
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
		// Theme already applied via buildStyles above; caller writes config if needed
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
				return m, m.launchStream(e.Login, "")
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
					return m, m.launchStream(e.Login, "")
				}
			}
		}
	case viewModeSearch:
		if m.cursor < len(m.searchList) {
			e := m.searchList[m.cursor]
			if e.IsLive {
				return m, m.launchStream(e.Login, "")
			}
		}
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
	e := m.currentEntry()
	if e == nil || e.Kind != EntryChannel {
		return m, nil
	}
	e.IsFavorite = !e.IsFavorite
	m.fns.ToggleFavorite(e.Login, e.IsFavorite)
	return m, nil
}

func (m Model) handleIgnore() (tea.Model, tea.Cmd) {
	e := m.currentEntry()
	if e == nil || e.Kind != EntryChannel {
		return m, nil
	}
	m.fns.ToggleIgnore(e.Login, true)
	m.removeCurrentEntry()
	return m, nil
}

func (m Model) handleQualityPicker() (tea.Model, tea.Cmd) {
	e := m.currentEntry()
	if e == nil || e.Kind != EntryChannel || !e.IsLive {
		return m, nil
	}
	ch := e.Login
	m.loading = true
	ctx := m.ctx
	fns := m.fns
	return m, func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		qualities, err := fns.Streams(c, ch)
		return qualityResultMsg{channel: ch, qualities: qualities, err: err}
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
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
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
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		entries, next, err := fns.BrowseCategories(c, "")
		return browseResultMsg{entries: entries, nextCursor: next, err: err}
	}
}

func (m Model) loadCategoryStreams(category, cursor string, appendMode bool) tea.Cmd {
	ctx := m.ctx
	fns := m.fns
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
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

func (m Model) runSearch(query string) tea.Cmd {
	if query == "" {
		return nil
	}
	ctx := m.ctx
	fns := m.fns
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		entries, err := fns.Search(c, query)
		return searchResultMsg{query: query, entries: entries, err: err}
	}
}

func (m Model) debounceSearch(query string) tea.Cmd {
	return tea.Tick(400*time.Millisecond, func(time.Time) tea.Msg {
		return searchDebounceMsg{query: query}
	})
}

func (m Model) launchStream(channel, quality string) tea.Cmd {
	m.lastGen++
	gen := m.lastGen

	if old, ok := m.sessions[channel]; ok {
		old.cancel()
	}

	ctx, cancel := context.WithCancel(m.ctx)
	m.sessions[channel] = &playbackSession{
		cancel: cancel,
		status: StatusWaiting,
		gen:    gen,
	}

	fns := m.fns
	return func() tea.Msg {
		go fns.Launch(ctx, channel, quality, func(status Status, detail string) {
			// Status updates are sent back through the program. The caller (main.go)
			// wires up a send function that dispatches to the tea.Program.
		})
		return statusUpdateMsg{channel: channel, status: StatusWaiting, detail: "", gen: gen}
	}
}

// --- Formatting helpers ---

func pad(s string, width int) string {
	w := runewidth.StringWidth(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

func padRight(s string, width int) string {
	w := runewidth.StringWidth(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

func cellTruncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= width {
		return s
	}
	w := 0
	var sb strings.Builder
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if w+rw > width-1 {
			break
		}
		sb.WriteRune(r)
		w += rw
	}
	sb.WriteRune('…')
	return sb.String()
}

func scrollTitle(s string, width, offset int) string {
	runes := []rune(s)
	n := len(runes)
	if n == 0 || width <= 0 {
		return strings.Repeat(" ", width)
	}
	stride := n + 4
	start := offset % stride
	if start >= n {
		start = 0
	}

	var sb strings.Builder
	w := 0
	for i := start; i < n && w < width; i++ {
		rw := runewidth.RuneWidth(runes[i])
		if w+rw > width {
			break
		}
		sb.WriteRune(runes[i])
		w += rw
	}
	result := sb.String()
	if runewidth.StringWidth(result) < width {
		result += strings.Repeat(" ", width-runewidth.StringWidth(result))
	}
	return result
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
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m2)
	}
	return fmt.Sprintf("%dm", m2)
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
		if w := runewidth.StringWidth(plain); w > oWidth {
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

	prefix := strings.Repeat(" ", leftPad)
	for i, ol := range overlayLines {
		row := topPad + i
		if row < 0 || row >= len(result) {
			continue
		}
		bl := []rune(result[row])
		for len(bl) < leftPad {
			bl = append(bl, ' ')
		}
		result[row] = string(bl[:leftPad]) + prefix + ol
	}

	return strings.Join(result, "\n")
}

// stripANSI removes ANSI escape sequences for width measurement.
func stripANSI(s string) string {
	var out strings.Builder
	inEsc := false
	for _, r := range s {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\x1b' {
			inEsc = true
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}
