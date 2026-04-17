package ui

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/rivo/uniseg"

	"github.com/mcs/twui/pkg/chat"
)

// defaultChatBacklog is the per-session message cap when no explicit size
// is given. Tuned so a 30-messages-per-second hype moment fits several
// minutes of scrollback.
const defaultChatBacklog = 500

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

	msgs := sess.View(height - 1)
	for _, msg := range msgs {
		lines = append(lines, m.renderChatLine(msg))
	}
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

	var right string
	switch {
	case sess.IsPaused():
		right = m.styles.favorite.Render("Space resume")
	case total > 1:
		right = m.styles.text.Render(fmt.Sprintf("[%d of %d]  C cycle  [ ] scroll", idx, total))
	default:
		right = m.styles.text.Render("[ ] scroll")
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

// renderChatLine produces one formatted row for a chat message, truncated
// to fit within the pane width.
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

	// Trim to the pane's content width.
	return cellTruncate(b.String(), m.width-2)
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
		label, style = "["+b.Version+"mo]", m.styles.favorite
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
