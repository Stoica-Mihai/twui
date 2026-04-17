package ui

import (
	"context"
	"fmt"
	"hash/fnv"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"

	"github.com/mcs/twui/pkg/chat"
)

// defaultChatBacklog is the per-session message cap when no explicit size
// is given. Tuned so a 30-messages-per-second hype moment fits several
// minutes of scrollback.
const defaultChatBacklog = 500

// ChatConfig is the runtime-configurable chat behaviour — set by main from
// the [chat] TOML section or the --chat CLI flag via (*Model).SetChatConfig.
type ChatConfig struct {
	// Enabled controls whether IRC connections are opened at all. When
	// false, launched streams skip chat setup entirely.
	Enabled bool
	// MaxBacklog is the per-session message buffer cap. <=0 uses the
	// package default (500).
	MaxBacklog int
}

// DefaultChatConfig returns a ChatConfig with chat enabled and the default
// backlog size. Used by NewModel so zero-configured Models behave sensibly.
func DefaultChatConfig() ChatConfig {
	return ChatConfig{Enabled: true, MaxBacklog: defaultChatBacklog}
}

// ChatSession owns the chat state for one active stream: a bounded message
// buffer plus scroll/pause state. All methods are single-goroutine — the
// Bubble Tea event loop is the only caller.
type ChatSession struct {
	Channel string

	buffer        []chat.Chat
	cap           int
	viewBottom    int // -1 = follow tail (live); >=0 = pinned to that index
	newSincePause int
}

// NewChatSession returns an empty ChatSession with the given backlog
// capacity. capacity <= 0 falls back to defaultChatBacklog.
func NewChatSession(channel string, capacity int) *ChatSession {
	if capacity <= 0 {
		capacity = defaultChatBacklog
	}
	return &ChatSession{
		Channel:    channel,
		cap:        capacity,
		viewBottom: -1,
	}
}

// Push appends a message. If the buffer is at capacity, the oldest entry
// is evicted. While paused, the pinned index is adjusted so the anchor
// tracks the same message even as the buffer trims.
func (s *ChatSession) Push(m chat.Chat) {
	s.buffer = append(s.buffer, m)
	if len(s.buffer) > s.cap {
		drop := len(s.buffer) - s.cap
		s.buffer = s.buffer[drop:]
		if s.viewBottom >= 0 {
			s.viewBottom -= drop
			if s.viewBottom < 0 {
				s.viewBottom = 0
			}
		}
	}
	if s.viewBottom >= 0 {
		s.newSincePause++
	}
}

// View returns up to height most-recent-first-absent messages ending at the
// current viewport bottom (either the live tail, or the paused anchor).
// Returned slice is safe to retain — callers must not mutate entries.
func (s *ChatSession) View(height int) []chat.Chat {
	if height <= 0 || len(s.buffer) == 0 {
		return nil
	}
	end := len(s.buffer)
	if s.viewBottom >= 0 {
		end = s.viewBottom + 1
	}
	start := end - height
	if start < 0 {
		start = 0
	}
	return s.buffer[start:end]
}

// ScrollBack moves the viewport n messages toward older history. On the
// first backwards scroll the session enters paused state.
func (s *ChatSession) ScrollBack(n int) {
	if n <= 0 || len(s.buffer) == 0 {
		return
	}
	if s.viewBottom < 0 {
		s.viewBottom = len(s.buffer) - 1
	}
	s.viewBottom -= n
	if s.viewBottom < 0 {
		s.viewBottom = 0
	}
}

// ScrollForward moves the viewport n messages toward the present. When the
// viewport reaches (or passes) the tail, the session auto-resumes.
func (s *ChatSession) ScrollForward(n int) {
	if n <= 0 || s.viewBottom < 0 {
		return
	}
	s.viewBottom += n
	if s.viewBottom >= len(s.buffer)-1 {
		s.Resume()
	}
}

// Resume exits paused state and pins the viewport to the newest message.
// Clears the new-since-pause counter.
func (s *ChatSession) Resume() {
	s.viewBottom = -1
	s.newSincePause = 0
}

// IsPaused reports whether the viewport is frozen away from the newest msg.
func (s *ChatSession) IsPaused() bool { return s.viewBottom >= 0 }

// NewSincePause returns the count of messages pushed while paused.
func (s *ChatSession) NewSincePause() int { return s.newSincePause }

// Len returns the current size of the message buffer (for tests / metrics).
func (s *ChatSession) Len() int { return len(s.buffer) }

// --- Bubble Tea messages for chat events ---

// chatReceivedMsg arrives each time the IRC client forwards one parsed chat
// line. Update pushes it into the matching ChatSession and re-arms the
// waitChatMsg Cmd to read the next one.
type chatReceivedMsg struct {
	channel string
	msg     chat.Chat
}

// chatClosedMsg arrives when an IRC client's Messages() channel closes —
// either because the playback session ended (ctx cancelled) or the client
// hit a fatal error. Update cleans up the session + connection bookkeeping
// and, if the dropped channel was focused, picks a new focus.
type chatClosedMsg struct {
	channel string
}

// chatConn tracks the IRC client plus its cancel/ctx for one channel.
type chatConn struct {
	client *chat.Client
	ctx    context.Context
	cancel context.CancelFunc
}

// waitChatMsg returns a Cmd that reads a single message from an IRC client's
// Messages() channel and surfaces it as a chatReceivedMsg (or chatClosedMsg
// if the channel closed / the ctx was cancelled).
func waitChatMsg(msgs <-chan *chat.Chat, channel string, ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		select {
		case m, ok := <-msgs:
			if !ok {
				return chatClosedMsg{channel: channel}
			}
			return chatReceivedMsg{channel: channel, msg: *m}
		case <-ctx.Done():
			return chatClosedMsg{channel: channel}
		}
	}
}

// startChat ensures a ChatSession + IRC client are running for the given
// channel. If one is already running or chat is disabled in config, returns
// (m, nil, false). Otherwise creates the session, starts the client
// goroutine, and returns a Cmd that reads the first message. The Model
// returned reflects any state mutations (chatOrder append, focus
// assignment, chatVisible flip).
func (m Model) startChat(channel string) (Model, tea.Cmd, bool) {
	if !m.chatConfig.Enabled {
		return m, nil, false
	}
	if _, running := m.chatConns[channel]; running {
		return m, nil, false
	}

	client := chat.NewClient(channel)
	ctx, cancel := context.WithCancel(m.ctx)
	go func() {
		if err := client.Run(ctx); err != nil && ctx.Err() == nil {
			slog.Debug("chat client exited", "channel", channel, "err", err)
		}
	}()

	if m.chatConns == nil {
		m.chatConns = make(map[string]*chatConn)
	}
	m.chatConns[channel] = &chatConn{client: client, ctx: ctx, cancel: cancel}

	if _, exists := m.chatSessions[channel]; !exists {
		m.chatSessions[channel] = NewChatSession(channel, m.chatConfig.MaxBacklog)
		m.chatOrder = append(m.chatOrder, channel)
	}
	if m.chatFocus == "" {
		m.chatFocus = channel
	}
	if !m.chatVisible {
		m.chatVisible = true
	}

	return m, waitChatMsg(client.Messages(), channel, ctx), true
}

// stopChat cancels the IRC client for a channel and removes its session
// from the model. Called when a playback session ends; the matching
// chatClosedMsg finishes the cleanup when Messages() drains.
func (m Model) stopChat(channel string) Model {
	if conn, ok := m.chatConns[channel]; ok {
		conn.cancel()
	}
	return m
}

// handleChatClosed drops a channel from every chat bookkeeping slot and
// picks a new focus if needed. Returns the updated Model.
func (m Model) handleChatClosed(channel string) Model {
	if conn, ok := m.chatConns[channel]; ok {
		conn.cancel()
		delete(m.chatConns, channel)
	}
	delete(m.chatSessions, channel)
	for i, ch := range m.chatOrder {
		if ch == channel {
			m.chatOrder = append(m.chatOrder[:i], m.chatOrder[i+1:]...)
			break
		}
	}
	if m.chatFocus == channel {
		if len(m.chatOrder) > 0 {
			m.chatFocus = m.chatOrder[0]
		} else {
			m.chatFocus = ""
			m.chatVisible = false
		}
	}
	return m
}

// --- Model helpers for chat keymap dispatch ---

// currentChatSession returns the ChatSession the pane is currently showing,
// or nil if no sessions are live or no focus is set.
func (m Model) currentChatSession() *ChatSession {
	if m.chatFocus == "" {
		return nil
	}
	return m.chatSessions[m.chatFocus]
}

// cycleChatFocus advances m.chatFocus to the next channel in m.chatOrder,
// wrapping. No-op when there are fewer than two live sessions.
func (m Model) cycleChatFocus() Model {
	if len(m.chatOrder) < 2 {
		return m
	}
	idx := -1
	for i, ch := range m.chatOrder {
		if ch == m.chatFocus {
			idx = i
			break
		}
	}
	if idx < 0 {
		m.chatFocus = m.chatOrder[0]
		return m
	}
	m.chatFocus = m.chatOrder[(idx+1)%len(m.chatOrder)]
	return m
}

// chatPaneHeight is the fixed number of lines the bottom chat pane occupies
// when visible, including its 1-line header.
const chatPaneHeight = 10

// renderChatPane returns exactly `height` lines (first line is the header,
// remaining are message rows). Called from render() when the pane is visible.
func (m Model) renderChatPane(height int) []string {
	if height < 1 {
		return nil
	}
	lines := make([]string, 0, height)
	lines = append(lines, m.renderChatHeader())

	sess := m.chatSessions[m.chatFocus]
	if sess == nil {
		for len(lines) < height {
			lines = append(lines, "")
		}
		return lines
	}

	// Each message may wrap to multiple lines, so fetch `height-1` messages
	// (enough if every message is one line), collect their wrapped lines,
	// and keep the newest `height-1` lines so the tail stays visible.
	contentHeight := height - 1
	msgs := sess.View(contentHeight)
	var msgLines []string
	for _, msg := range msgs {
		msgLines = append(msgLines, strings.Split(m.renderChatLine(msg), "\n")...)
	}
	if len(msgLines) > contentHeight {
		msgLines = msgLines[len(msgLines)-contentHeight:]
	}
	lines = append(lines, msgLines...)
	for len(lines) < height {
		lines = append(lines, "")
	}
	return lines[:height]
}

// renderChatHeader composes the one-line status row shown above chat rows.
// It adapts to live vs. paused state, shows the active channel and the
// [N of M] session index, and puts the contextual key hint on the right.
func (m Model) renderChatHeader() string {
	sess := m.chatSessions[m.chatFocus]
	if sess == nil {
		return ""
	}

	idx, total := m.chatFocusIndex()

	var left string
	if sess.IsPaused() {
		left = m.styles.favorite.Render(m.symbols.ChatPaused) +
			" Chat — " +
			m.styles.live.Render(sess.Channel) +
			m.styles.favorite.Render(fmt.Sprintf(" — PAUSED · %d new", sess.NewSincePause()))
	} else {
		left = m.styles.live.Render(m.symbols.ChatActive) +
			" Chat — " +
			m.styles.live.Render(sess.Channel)
	}

	// Hint is always "C hide" + context. c-cycle is only meaningful with
	// 2+ sessions. [ ] scroll hints at the scrollback keys. Space resume
	// takes precedence when paused because resuming is the one thing the
	// user actually needs to do right then.
	var right string
	switch {
	case sess.IsPaused():
		right = m.styles.favorite.Render("Space resume") +
			m.styles.text.Render("  ·  C hide")
	case total > 1:
		right = m.styles.text.Render(fmt.Sprintf("[%d of %d]  c cycle  [ ] scroll  C hide", idx, total))
	default:
		right = m.styles.text.Render("[ ] scroll  C hide")
	}

	return joinLeftRight(left, right, m.width-2)
}

// chatFocusIndex returns the 1-based position of m.chatFocus inside
// m.chatOrder and the total session count. Returns 0,0 when focus is unset.
func (m Model) chatFocusIndex() (idx, total int) {
	for i, ch := range m.chatOrder {
		if ch == m.chatFocus {
			return i + 1, len(m.chatOrder)
		}
	}
	return 0, len(m.chatOrder)
}

// renderChatLine formats a chat message, wrapping long lines across multiple
// rows. Returns a string with '\n' separators between wrapped rows so callers
// can split or render as a block.
func (m Model) renderChatLine(msg chat.Chat) string {
	var b strings.Builder

	// Timestamp column (fixed 8 chars + space) keeps alignment.
	ts := "        "
	if !msg.Sent.IsZero() {
		ts = msg.Sent.Format("15:04:05")
	}
	b.WriteString(m.styles.offline.Render(ts + "  "))

	// Badges (leading space separator between them).
	for _, bdg := range msg.Badges {
		if rendered := m.renderChatBadge(bdg); rendered != "" {
			b.WriteString(rendered)
			b.WriteByte(' ')
		}
	}

	// Username + colon.
	name := msg.DisplayName
	if name == "" {
		name = msg.Login
	}
	b.WriteString(m.chatUserStyle(msg.Login).Render(name))
	b.WriteString(m.styles.text.Render(": "))

	// Message text with emote styling in place.
	b.WriteString(m.renderChatText(msg))

	w := m.width - 2
	if w < 1 {
		w = 1
	}
	return ansi.WrapWc(b.String(), w, " ")
}

// subBadgeLabel turns a Twitch subscriber-badge version string into a compact
// label. Twitch encodes tier and months together: versions under 1000 are
// tier-1 month counts; 2000-series are tier 2 (months = v-2000); 3000-series
// are tier 3 (months = v-3000). Without this decoding, a 24-month tier-2 sub
// renders as "2024mo" and looks like a two-millennium badge.
func subBadgeLabel(version string) string {
	v, err := strconv.Atoi(version)
	if err != nil || v < 0 {
		return version + "mo"
	}
	switch {
	case v >= 3000 && v < 4000:
		return fmt.Sprintf("T3 %dmo", v-3000)
	case v >= 2000 && v < 3000:
		return fmt.Sprintf("T2 %dmo", v-2000)
	default:
		return fmt.Sprintf("%dmo", v)
	}
}

// renderChatBadge returns a short styled label for one Twitch badge. Unknown
// badge kinds render as empty so the row stays clean.
func (m Model) renderChatBadge(b chat.Badge) string {
	var label string
	var style lipgloss.Style
	switch b.Name {
	case "broadcaster":
		label, style = "[BC]", m.styles.reconnecting
	case "moderator":
		label, style = "[MOD]", m.styles.live
	case "vip":
		label, style = "[VIP]", m.styles.tabActive
	case "subscriber":
		label, style = "["+subBadgeLabel(b.Version)+"]", m.styles.favorite
	default:
		return ""
	}
	return style.Render(label)
}

// renderChatText applies emote styling to the parts of the message text
// that the Twitch `emotes` tag flagged. Text outside emote ranges passes
// through unchanged.
func (m Model) renderChatText(c chat.Chat) string {
	if len(c.Emotes) == 0 {
		return m.styles.text.Render(c.Text)
	}

	type span struct{ start, end int }
	var spans []span
	for _, e := range c.Emotes {
		for _, r := range e.Ranges {
			spans = append(spans, span{r.Start, r.End})
		}
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })

	runes := []rune(c.Text)
	var b strings.Builder
	cursor := 0
	emoteStyle := m.styles.favorite
	textStyle := m.styles.text
	for _, s := range spans {
		if s.start < cursor || s.end >= len(runes) || s.end < s.start {
			continue
		}
		if s.start > cursor {
			b.WriteString(textStyle.Render(string(runes[cursor:s.start])))
		}
		b.WriteString(emoteStyle.Render(string(runes[s.start : s.end+1])))
		cursor = s.end + 1
	}
	if cursor < len(runes) {
		b.WriteString(textStyle.Render(string(runes[cursor:])))
	}
	return b.String()
}

// chatUserStyle returns a consistent theme-derived style for a given login.
// Same login always hashes to the same slot so a user's colour is stable
// across the whole session, while being theme-aware (monochrome yields
// attribute-only styles since the underlying entries have no colours).
func (m Model) chatUserStyle(login string) lipgloss.Style {
	palette := []lipgloss.Style{
		m.styles.live,
		m.styles.favorite,
		m.styles.tabActive,
		m.styles.category,
		m.styles.waiting,
		m.styles.reconnecting,
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(login))
	return palette[h.Sum32()%uint32(len(palette))]
}

// joinLeftRight puts `left` at column 0 and right-aligns `right` within `width`,
// separating with spaces. Widths are measured after stripping ANSI so styled
// segments don't inflate the padding count.
func joinLeftRight(left, right string, width int) string {
	leftW := uniseg.StringWidth(stripANSI(left))
	rightW := uniseg.StringWidth(stripANSI(right))
	gap := width - leftW - rightW
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}
