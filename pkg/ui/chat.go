package ui

import "github.com/mcs/twui/pkg/chat"

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
