package ui

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

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
	// watchListStartedMsg carries the producer's cancel so a superseded
	// stream can be aborted before the model stores it.
	watchListStartedMsg struct {
		ch        <-chan DiscoveryEntry
		cancel    context.CancelFunc
		epoch     int
		refreshed bool
	}
	watchListEntryMsg struct {
		entry     DiscoveryEntry
		epoch     int
		refreshed bool
	}
	// watchListDoneMsg's epoch guards against stale streams superseded by a
	// newer load.
	watchListDoneMsg struct {
		epoch     int
		refreshed bool
		err       error
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
	tickMsg          time.Time
	qualityResultMsg struct {
		channel   string
		avatarURL string
		qualities []string
		err       error
	}
	relatedResultMsg struct {
		channel string
		streams []DiscoveryEntry
		err     error
	}
	searchDebounceMsg struct {
		query string
	}
	titleScrollMsg  struct{}
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
	watchList []DiscoveryEntry
	// watchListStream is the live channel of a streaming load — readers re-arm
	// against it on every watchListEntryMsg. Cleared on done/stale.
	watchListStream <-chan DiscoveryEntry
	// watchListCancel cancels the context backing the current stream. Called
	// when the stream is superseded (refresh mid-load) or when it completes
	// naturally, so the in-flight fan-out goroutines shut down promptly.
	watchListCancel context.CancelFunc
	// watchListEpoch is bumped on every new load so stale msgs are dropped.
	watchListEpoch   int
	browseList       []DiscoveryEntry
	browseNextCursor string
	searchList       []DiscoveryEntry
	searchQuery      string
	searchInput      string
	searching        bool

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
	overlay          overlayMode
	overlayList      []string
	overlayCursor    int
	overlayChannel   string
	overlayCategory  string
	overlayAvatarURL string
	relatedStreams   []DiscoveryEntry
	relatedLoading   bool

	// playback
	sessions map[string]*playbackSession
	lastGen  int

	// chat pane
	chatSessions map[string]*ChatSession         // keyed by channel login
	chatConns    map[string]*chatConn            // live IRC clients keyed by channel login
	chatOrder    []string                        // launch order for C-cycle
	chatFocus    string                          // channel of the session currently in the pane
	chatVisible  bool                            // toggled by `c`; set true on first session
	chatConfig   ChatConfig                      // runtime chat behaviour; see SetChatConfig
	chatFactory  func(channel string) ChatSource // nil → chat.NewClient; see SetChatFactory

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
		chatSessions:     make(map[string]*ChatSession),
		chatConns:        make(map[string]*chatConn),
		chatConfig:       DefaultChatConfig(),
		ctx:              ctx,
		cancel:           cancel,
		refreshInterval:  refreshInterval,
		refreshCountdown: refreshInterval,
		loading:          true,
		watchListEpoch:   1,
	}
}

// SetChatConfig overrides the chat behaviour. Intended to be called once
// after NewModel before tea.Run — not safe to call concurrently with the
// event loop.
func (m *Model) SetChatConfig(cfg ChatConfig) {
	if cfg.MaxBacklog <= 0 {
		cfg.MaxBacklog = defaultChatBacklog
	}
	m.chatConfig = cfg
}

// SetChatFactory installs a custom ChatSource builder for startChat to use
// instead of the default chat.NewClient. Intended for demo mode and tests.
// Must be called before tea.Run.
func (m *Model) SetChatFactory(fn func(channel string) ChatSource) {
	m.chatFactory = fn
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

// finishAsync clears the loading or refreshing flag for a completed async op.
// Refresh errors are intentionally swallowed so background failures don't blank
// the UI mid-view; only initial-load errors are surfaced via m.err.
func (m *Model) finishAsync(refreshed bool, err error) {
	if refreshed {
		m.refreshing = false
		return
	}
	m.loading = false
	m.err = err
}

// Init implements tea.Model (bubbletea v2: returns tea.Cmd).
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.loadWatchList(false), titleScrollCmd()}
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

	case watchListStartedMsg:
		if msg.epoch != m.watchListEpoch {
			// Stale stream (superseded by a newer load); cancel it so the
			// producer goroutine exits instead of blocking on `out <-`.
			msg.cancel()
			return m, nil
		}
		m.watchListStream = msg.ch
		m.watchListCancel = msg.cancel
		return m, readWatchListEntry(msg.ch, msg.epoch, msg.refreshed)

	case watchListEntryMsg:
		if msg.epoch != m.watchListEpoch {
			return m, nil
		}
		prev := m.cursorLogin(viewModeWatchList)
		m.watchList = sortLiveFirst(m.filterIgnored(mergeWatchListEntry(m.watchList, msg.entry)))
		if m.mode == viewModeWatchList {
			m.cursor = findEntryByLogin(m.watchList, prev)
		}
		return m, readWatchListEntry(m.watchListStream, msg.epoch, msg.refreshed)

	case watchListDoneMsg:
		if msg.epoch != m.watchListEpoch {
			return m, nil
		}
		m.finishAsync(msg.refreshed, msg.err)
		m = m.cancelWatchListLocked()
		return m, nil

	case searchResultMsg:
		if msg.query != m.searchQuery {
			return m, nil
		}
		m.finishAsync(msg.refreshed, msg.err)
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
		m.finishAsync(msg.refreshed, msg.err)
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
		if msg.appendMode {
			// Append failures surface as a notice (not a blocking error),
			// so we only clear the async flag — m.err is left untouched.
			if msg.refresh {
				m.refreshing = false
			} else {
				m.loading = false
			}
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
			m.finishAsync(msg.refresh, msg.err)
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
			newM, cmd := m.launchStream(msg.channel, "", msg.avatarURL)
			return newM, cmd
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
			m.relatedStreams = msg.streams
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
			if m.mode == viewModeWatchList {
				m = m.cancelWatchListLocked()
				m.watchListEpoch++
			}
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
				// Also tear down the chat connection; the chatClosedMsg
				// that follows will finish the bookkeeping cleanup.
				m = m.stopChat(msg.channel)
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

	case chatReceivedMsg:
		if s, ok := m.chatSessions[msg.channel]; ok {
			s.Push(msg.msg)
		}
		if conn, ok := m.chatConns[msg.channel]; ok {
			return m, waitChatMsg(conn.client.Messages(), msg.channel, conn.ctx)
		}
		return m, nil

	case chatClosedMsg:
		m = m.handleChatClosed(msg.channel)
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

	// Outer frame rows: tab bar, body, [chat pane], footer. Each row is
	// separated by a border line drawn by lipgloss's NormalBorder.
	chatOn := m.chatPaneActive()

	// Fixed non-body content. 6 accounts for top border + tab + separator +
	// separator + footer + bottom border; chat adds one more separator and
	// its fixed height.
	nonBodyRows := 6
	if chatOn {
		nonBodyRows += chatPaneHeight + 1
	}
	bodyHeight := m.height - nonBodyRows
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
		Row(bodyStr)

	if chatOn {
		frame = frame.Row(strings.Join(m.renderChatPane(chatPaneHeight), "\n"))
	}
	frame = frame.Row(m.renderFooter())

	result := frame.String()

	if m.overlay != overlayNone {
		return overlayOn(result, m.renderOverlay(), m.width, m.height)
	}

	return result
}

// chatPaneActive returns true when the chat pane should be drawn as part of
// the frame: enabled by the user and there is at least one live session.
func (m Model) chatPaneActive() bool {
	return m.chatVisible && len(m.chatSessions) > 0 && m.chatFocus != ""
}

func (m Model) renderFooter() string {
	if m.notice != "" {
		return m.styles.live.Render(cellTruncate("  "+m.notice, m.width-2))
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

	// All bindings are always shown. When the full labels don't fit, the
	// footer collapses to keys-only and the `? help` hint reminds the user
	// that the overlay is one keystroke away.
	type kv struct{ key, desc string }
	hints := []kv{
		{"Enter", "play"},
		{"i", "quality"},
		{"f", "fav"},
		{"x", "ignore"},
	}
	if len(m.chatSessions) > 0 && !m.chatVisible {
		hints = append(hints, kv{"C", "chat"})
	}
	hints = append(hints, kv{"r", "related"}, kv{"t", "theme"}, kv{"?", "help"})

	const (
		minGap  = 2
		tailPad = 2
	)
	budget := (m.width - 2) - uniseg.StringWidth(stripANSI(left)) - minGap - tailPad

	buildRight := func(withDesc bool) string {
		parts := make([]string, 0, len(hints))
		for _, h := range hints {
			if withDesc {
				parts = append(parts, hint(h.key, h.desc))
			} else {
				parts = append(parts, m.styles.title.Render(h.key))
			}
		}
		return strings.Join(parts, dot)
	}

	right := buildRight(true)
	if uniseg.StringWidth(stripANSI(right)) > budget {
		right = buildRight(false)
	}
	if right != "" {
		right += "  "
	}

	return joinLeftRight(left, right, m.width-2)
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

	// Esc hides the chat pane whenever it's visible, before the normal Esc
	// handler (which walks out of category stacks etc.) gets a chance.
	if msg.String() == "esc" && m.chatVisible {
		m.chatVisible = false
		if s := m.currentChatSession(); s != nil {
			s.Resume()
		}
		return m, nil
	}

	if newM, cmd, ok := m.dispatchBinding(msg.String()); ok {
		return newM, cmd
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

	// Wheel events inside the chat pane scroll the chat (and pause it on
	// the first back-scroll). Events elsewhere move the picker cursor.
	if m.chatPaneActive() && m.mouseYInChatPane(msg.Y) {
		if s := m.currentChatSession(); s != nil {
			switch msg.Button {
			case tea.MouseWheelUp:
				s.ScrollBack(1)
			case tea.MouseWheelDown:
				s.ScrollForward(1)
			}
		}
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

// mouseYInChatPane returns true when y falls inside the chat pane rows of
// the rendered frame. Chat rows span (m.height - chatPaneHeight - 3) through
// (m.height - 4) inclusive when the pane is visible.
func (m Model) mouseYInChatPane(y int) bool {
	top := m.height - chatPaneHeight - 3
	bottom := m.height - 4
	return y >= top && y <= bottom
}

func (m Model) handleEnter() (tea.Model, tea.Cmd) {
	switch m.mode {
	case viewModeWatchList:
		if m.cursor < len(m.watchList) {
			e := m.watchList[m.cursor]
			if e.IsLive {
				newM, cmd := m.launchStream(e.Login, "", e.AvatarURL)
				return newM, cmd
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
					newM, cmd := m.launchStream(e.Login, "", e.AvatarURL)
					return newM, cmd
				}
			}
		}
	case viewModeSearch:
		if m.cursor < len(m.searchList) {
			e := m.searchList[m.cursor]
			if e.IsLive {
				newM, cmd := m.launchStream(e.Login, "", e.AvatarURL)
				return newM, cmd
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
	return m.resetTitleScroll()
}

// resetTitleScroll rewinds the row-title marquee to its starting position.
// Called by every cursor-moving handler so a new row's title starts scrolling
// from the beginning rather than mid-sweep.
func (m Model) resetTitleScroll() Model {
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

// sortLiveFirst reorders channel entries so live channels come first, sorted
// by viewer count descending; offline channels follow in their original order.
// Non-channel entries (e.g. LoadMore rows) keep their relative position at
// the tail so they don't get mixed into the live block.
func sortLiveFirst(entries []DiscoveryEntry) []DiscoveryEntry {
	out := make([]DiscoveryEntry, len(entries))
	copy(out, entries)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Kind == EntryChannel && b.Kind == EntryChannel {
			if a.IsLive != b.IsLive {
				return a.IsLive
			}
			if a.IsLive {
				return a.ViewerCount > b.ViewerCount
			}
		}
		return false
	})
	return out
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

// loadWatchList returns a Cmd that starts a streaming watch-list load.
// Rather than blocking until every favorite resolves, it opens the stream
// channel from fns.WatchList and hands it back to the event loop via
// watchListStartedMsg; subsequent entries arrive one at a time via
// readWatchListEntry. The cancel func rides along in the started msg so the
// event loop can shut the stream down on refresh or teardown.
func (m Model) loadWatchList(refreshed bool) tea.Cmd {
	ctx := m.ctx
	fns := m.fns
	epoch := m.watchListEpoch
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, timeoutBrowse)
		ch, err := fns.WatchList(c)
		if err != nil {
			cancel()
			return watchListDoneMsg{epoch: epoch, refreshed: refreshed, err: err}
		}
		return watchListStartedMsg{ch: ch, cancel: cancel, epoch: epoch, refreshed: refreshed}
	}
}

// readWatchListEntry returns a one-shot Cmd that reads the next entry from
// the streaming watch-list channel. On close it emits watchListDoneMsg; the
// Update handler is responsible for re-arming it after each entry.
func readWatchListEntry(ch <-chan DiscoveryEntry, epoch int, refreshed bool) tea.Cmd {
	return func() tea.Msg {
		entry, ok := <-ch
		if !ok {
			return watchListDoneMsg{epoch: epoch, refreshed: refreshed}
		}
		return watchListEntryMsg{entry: entry, epoch: epoch, refreshed: refreshed}
	}
}

// mergeWatchListEntry replaces the channel entry with matching Login in place
// if present, else appends. Used on each streaming entry so a refresh updates
// rows without clearing the list first (prevents blink + preserves cursor).
func mergeWatchListEntry(list []DiscoveryEntry, entry DiscoveryEntry) []DiscoveryEntry {
	for i := range list {
		if list[i].Kind == EntryChannel && list[i].Login == entry.Login {
			list[i] = entry
			return list
		}
	}
	return append(list, entry)
}

// cancelWatchListLocked cancels the in-flight watch-list stream and clears
// its handles. Safe to call when no stream is active. Named "...Locked"
// because the caller holds the Model (Bubble Tea's single-writer invariant);
// no mutex is involved.
func (m Model) cancelWatchListLocked() Model {
	if m.watchListCancel != nil {
		m.watchListCancel()
		m.watchListCancel = nil
	}
	m.watchListStream = nil
	return m
}

// loadBrowse returns a Cmd that fetches the top-level category list. See
// loadWatchList for the m.loading ownership contract.
func (m Model) loadBrowse() tea.Cmd {
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
		return m.loadWatchList(false)
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
		return m.loadWatchList(true)
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

func (m Model) launchStream(channel, quality, avatarURL string) (Model, tea.Cmd) {
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

	cmds := []tea.Cmd{waitPlayback(ctx, ch, channel, gen)}

	// Start (or reuse) the chat session + IRC client for this channel.
	// With AutoOpen=false we only connect lazily — either when this stream
	// launches while the pane is already visible, or when the user toggles
	// the pane open later (handled in the C keybinding).
	if m.chatConfig.AutoOpen || m.chatVisible {
		newM, chatCmd, started := m.startChat(channel)
		m = newM
		if started {
			cmds = append(cmds, chatCmd)
		}
	}

	return m, tea.Batch(cmds...)
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
