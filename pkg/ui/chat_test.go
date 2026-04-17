package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mcs/twui/pkg/chat"
)

func mkMsg(text string) chat.Chat {
	return chat.Chat{Login: "u", DisplayName: "u", Text: text}
}

// push inserts n messages numbered "msg0".."msg(n-1)" for test setup.
func pushN(s *ChatSession, n int) {
	for i := 0; i < n; i++ {
		s.Push(mkMsg(fmt.Sprintf("msg%d", i)))
	}
}

func TestChatSession_Empty(t *testing.T) {
	s := NewChatSession("c", 10)
	if got := s.View(5); got != nil {
		t.Errorf("View on empty = %v, want nil", got)
	}
	if s.IsPaused() {
		t.Error("empty session should not be paused")
	}
	// ScrollBack/Forward on empty should be no-ops.
	s.ScrollBack(3)
	s.ScrollForward(3)
	if s.IsPaused() {
		t.Error("scroll on empty should not pause")
	}
}

func TestChatSession_Push_BelowCap(t *testing.T) {
	s := NewChatSession("c", 10)
	pushN(s, 5)
	if s.Len() != 5 {
		t.Errorf("Len = %d, want 5", s.Len())
	}
	v := s.View(10)
	if len(v) != 5 {
		t.Fatalf("View len = %d, want 5", len(v))
	}
	if v[0].Text != "msg0" || v[4].Text != "msg4" {
		t.Errorf("View = %v", v)
	}
}

func TestChatSession_Push_EvictsOldestAtCap(t *testing.T) {
	s := NewChatSession("c", 3)
	pushN(s, 5)
	if s.Len() != 3 {
		t.Errorf("Len = %d, want 3 (cap)", s.Len())
	}
	v := s.View(10)
	if len(v) != 3 || v[0].Text != "msg2" || v[2].Text != "msg4" {
		t.Errorf("View after eviction = %v, want [msg2, msg3, msg4]", v)
	}
}

func TestChatSession_View_HeightSmallerThanBuffer(t *testing.T) {
	s := NewChatSession("c", 100)
	pushN(s, 20)
	v := s.View(5)
	if len(v) != 5 {
		t.Fatalf("View len = %d", len(v))
	}
	// Most recent 5, oldest-to-newest within slice.
	if v[0].Text != "msg15" || v[4].Text != "msg19" {
		t.Errorf("View = %v", v)
	}
}

func TestChatSession_ScrollBack_Pauses(t *testing.T) {
	s := NewChatSession("c", 100)
	pushN(s, 20)
	if s.IsPaused() {
		t.Fatal("should start live")
	}
	s.ScrollBack(3)
	if !s.IsPaused() {
		t.Error("ScrollBack should enter paused state")
	}
	// View bottom should now be 16 (len 20, back 3 from tail index 19).
	v := s.View(5)
	if len(v) != 5 || v[4].Text != "msg16" {
		t.Errorf("View after ScrollBack(3) = %v (expect bottom = msg16)", v)
	}
}

func TestChatSession_ScrollForward_ResumesAtBottom(t *testing.T) {
	s := NewChatSession("c", 100)
	pushN(s, 10)
	s.ScrollBack(5) // viewBottom=4
	if !s.IsPaused() {
		t.Fatal("should be paused after ScrollBack")
	}
	s.ScrollForward(3) // viewBottom=7, still < 9
	if !s.IsPaused() {
		t.Error("should still be paused at viewBottom=7/9")
	}
	s.ScrollForward(10) // overshoots to tail → resumes
	if s.IsPaused() {
		t.Errorf("should auto-resume on reaching bottom; viewBottom=%d Len=%d", s.viewBottom, s.Len())
	}
}

func TestChatSession_ScrollBack_ClampsAtStart(t *testing.T) {
	s := NewChatSession("c", 100)
	pushN(s, 5)
	s.ScrollBack(99)
	v := s.View(10)
	// Paused, anchor at 0; View should return just msg0 (1 message).
	if len(v) != 1 || v[0].Text != "msg0" {
		t.Errorf("View = %v, want [msg0]", v)
	}
}

func TestChatSession_ScrollForward_LiveNoop(t *testing.T) {
	s := NewChatSession("c", 100)
	pushN(s, 5)
	// Already at tail; ScrollForward should be a no-op and NOT pause.
	s.ScrollForward(3)
	if s.IsPaused() {
		t.Error("ScrollForward on live should not pause")
	}
}

func TestChatSession_Push_WhilePausedDoesNotChangeAnchor(t *testing.T) {
	s := NewChatSession("c", 100)
	pushN(s, 10)
	s.ScrollBack(3) // viewBottom=6, looking at msg4..msg6

	vBefore := s.View(3)
	// Push several more; anchor should hold.
	for i := 10; i < 15; i++ {
		s.Push(mkMsg(fmt.Sprintf("msg%d", i)))
	}
	vAfter := s.View(3)
	if len(vBefore) != 3 || len(vAfter) != 3 {
		t.Fatalf("wrong lengths: %d / %d", len(vBefore), len(vAfter))
	}
	for i := range vBefore {
		if vBefore[i].Text != vAfter[i].Text {
			t.Errorf("anchor drifted: before=%v after=%v", vBefore, vAfter)
		}
	}
	if s.NewSincePause() != 5 {
		t.Errorf("NewSincePause = %d, want 5", s.NewSincePause())
	}
}

func TestChatSession_Push_WhilePausedShiftsAnchorOnEviction(t *testing.T) {
	// Cap=10, pause at viewBottom=5, push 20 more. Every eviction shifts
	// the anchor down by 1; anchor bottoms out at 0 and stays there.
	s := NewChatSession("c", 10)
	pushN(s, 10)
	s.ScrollBack(4) // viewBottom=5

	for i := 10; i < 30; i++ {
		s.Push(mkMsg(fmt.Sprintf("msg%d", i)))
	}
	if s.Len() != 10 {
		t.Errorf("Len = %d, want 10 (cap)", s.Len())
	}
	if !s.IsPaused() {
		t.Error("still paused after many pushes")
	}
	// Anchor should be clamped at 0 since we pushed 20 and only had 4 slots
	// of headroom.
	v := s.View(5)
	if len(v) != 1 {
		t.Errorf("View at anchor 0 with height 5 should return 1 message, got %d", len(v))
	}
}

func TestChatSession_Resume_ClearsCounterAndAnchor(t *testing.T) {
	s := NewChatSession("c", 100)
	pushN(s, 10)
	s.ScrollBack(3)
	s.Push(mkMsg("new1"))
	s.Push(mkMsg("new2"))

	if s.NewSincePause() != 2 {
		t.Errorf("NewSincePause = %d, want 2", s.NewSincePause())
	}
	s.Resume()
	if s.IsPaused() {
		t.Error("after Resume, IsPaused should be false")
	}
	if s.NewSincePause() != 0 {
		t.Errorf("after Resume, NewSincePause = %d, want 0", s.NewSincePause())
	}
	// After resume, View shows the newest.
	v := s.View(1)
	if len(v) != 1 || v[0].Text != "new2" {
		t.Errorf("View after Resume = %v, want [new2]", v)
	}
}

func TestChatSession_NewSincePauseNotIncrementedWhileLive(t *testing.T) {
	s := NewChatSession("c", 100)
	pushN(s, 5)
	if s.NewSincePause() != 0 {
		t.Errorf("NewSincePause while live = %d, want 0", s.NewSincePause())
	}
}

func TestChatSession_DefaultCapacity(t *testing.T) {
	s := NewChatSession("c", 0) // asks for default
	if s.cap != defaultChatBacklog {
		t.Errorf("default cap = %d, want %d", s.cap, defaultChatBacklog)
	}
}

func TestChatSession_ZeroOrNegativeScrollNoops(t *testing.T) {
	s := NewChatSession("c", 100)
	pushN(s, 5)
	s.ScrollBack(0)
	s.ScrollBack(-2)
	s.ScrollForward(0)
	s.ScrollForward(-5)
	if s.IsPaused() {
		t.Error("zero/negative scrolls should not pause")
	}
}

// --- Renderer tests ---

// chatModel returns a width-160 Model with one chat session focused.
func chatModel(t *testing.T, sessions ...string) Model {
	t.Helper()
	m := newTestModel(&mockState{})
	m.width = 160
	m.height = 40
	m.chatVisible = true
	for _, ch := range sessions {
		m.chatSessions[ch] = NewChatSession(ch, 500)
		m.chatOrder = append(m.chatOrder, ch)
	}
	if len(sessions) > 0 {
		m.chatFocus = sessions[0]
	}
	return m
}

func TestRenderChatPane_ReturnsExactHeight(t *testing.T) {
	m := chatModel(t, "shroud")
	out := m.renderChatPane(chatPaneHeight)
	if len(out) != chatPaneHeight {
		t.Errorf("pane lines = %d, want %d", len(out), chatPaneHeight)
	}
}

func TestRenderChatPane_ZeroHeightReturnsNil(t *testing.T) {
	m := chatModel(t, "shroud")
	if out := m.renderChatPane(0); out != nil {
		t.Errorf("height 0 should yield nil, got %v", out)
	}
}

func TestRenderChatHeader_LiveShowsActiveGlyph(t *testing.T) {
	m := chatModel(t, "shroud")
	h := m.renderChatHeader()
	if !strings.Contains(stripANSI(h), m.symbols.ChatActive) {
		t.Errorf("header missing active glyph: %q", stripANSI(h))
	}
	if !strings.Contains(stripANSI(h), "shroud") {
		t.Errorf("header missing channel name: %q", stripANSI(h))
	}
	if strings.Contains(stripANSI(h), "PAUSED") {
		t.Errorf("live header should not mention PAUSED: %q", stripANSI(h))
	}
}

func TestRenderChatHeader_PausedShowsPausedGlyphAndCounter(t *testing.T) {
	m := chatModel(t, "shroud")
	sess := m.chatSessions["shroud"]
	pushN(sess, 20)
	sess.ScrollBack(3)
	for i := 0; i < 7; i++ {
		sess.Push(mkMsg("new"))
	}

	h := m.renderChatHeader()
	plain := stripANSI(h)
	if !strings.Contains(plain, m.symbols.ChatPaused) {
		t.Errorf("paused header missing paused glyph: %q", plain)
	}
	if !strings.Contains(plain, "PAUSED") {
		t.Errorf("paused header missing PAUSED label: %q", plain)
	}
	if !strings.Contains(plain, "7 new") {
		t.Errorf("paused header missing new counter: %q", plain)
	}
	if !strings.Contains(plain, "Space resume") {
		t.Errorf("paused header missing resume hint: %q", plain)
	}
}

func TestRenderChatHeader_MultipleSessionsShowsIndexAndCycle(t *testing.T) {
	m := chatModel(t, "shroud", "xqc", "zackrawrr")
	h := m.renderChatHeader()
	plain := stripANSI(h)
	if !strings.Contains(plain, "1 of 3") {
		t.Errorf("header missing index: %q", plain)
	}
	if !strings.Contains(plain, "C cycle") {
		t.Errorf("header missing cycle hint: %q", plain)
	}
}

func TestRenderChatHeader_SingleSessionOmitsCycleHint(t *testing.T) {
	m := chatModel(t, "shroud")
	plain := stripANSI(m.renderChatHeader())
	if strings.Contains(plain, "C cycle") {
		t.Errorf("single-session header should not advertise cycle: %q", plain)
	}
}

func TestRenderChatLine_FormatsBadgesAndEmotes(t *testing.T) {
	m := chatModel(t, "c")
	msg := chat.Chat{
		Login:       "ron",
		DisplayName: "Ron",
		Text:        "Kappa hello",
		Badges: []chat.Badge{
			{Name: "broadcaster", Version: "1"},
			{Name: "subscriber", Version: "12"},
		},
		Emotes: []chat.Emote{
			{ID: "25", Ranges: []chat.Range{{Start: 0, End: 4}}, Name: "Kappa"},
		},
	}
	plain := stripANSI(m.renderChatLine(msg))
	if !strings.Contains(plain, "[BC]") {
		t.Errorf("missing [BC]: %q", plain)
	}
	if !strings.Contains(plain, "[12mo]") {
		t.Errorf("missing [12mo] sub badge: %q", plain)
	}
	if !strings.Contains(plain, "Ron:") {
		t.Errorf("missing display name and colon: %q", plain)
	}
	if !strings.Contains(plain, "Kappa hello") {
		t.Errorf("missing text content (emote styling is in-place): %q", plain)
	}
}

func TestRenderChatLine_FallsBackToLoginWhenNoDisplayName(t *testing.T) {
	m := chatModel(t, "c")
	msg := chat.Chat{Login: "ron", Text: "hi"}
	plain := stripANSI(m.renderChatLine(msg))
	if !strings.Contains(plain, "ron:") {
		t.Errorf("expected login fallback: %q", plain)
	}
}

func TestRenderChatLine_UnknownBadgeIsSkipped(t *testing.T) {
	m := chatModel(t, "c")
	msg := chat.Chat{
		Login: "r",
		Text:  "hi",
		Badges: []chat.Badge{
			{Name: "glitchcon2020", Version: "1"}, // unknown → skipped
			{Name: "moderator", Version: "1"},
		},
	}
	plain := stripANSI(m.renderChatLine(msg))
	if strings.Contains(plain, "glitchcon") {
		t.Errorf("unknown badge leaked into output: %q", plain)
	}
	if !strings.Contains(plain, "[MOD]") {
		t.Errorf("known badge missing: %q", plain)
	}
}

func TestChatUserStyle_Stable(t *testing.T) {
	m := chatModel(t, "c")
	a1 := m.chatUserStyle("alice").Render("x")
	a2 := m.chatUserStyle("alice").Render("x")
	if a1 != a2 {
		t.Errorf("same login should yield identical output: %q vs %q", a1, a2)
	}
}

func TestRenderChatText_NoEmotesPassesThrough(t *testing.T) {
	m := chatModel(t, "c")
	msg := chat.Chat{Text: "hello world"}
	if !strings.Contains(stripANSI(m.renderChatText(msg)), "hello world") {
		t.Errorf("text missing: %q", m.renderChatText(msg))
	}
}

func TestRenderChatText_OverlappingRangesDropSecond(t *testing.T) {
	m := chatModel(t, "c")
	msg := chat.Chat{
		Text: "Kappa",
		Emotes: []chat.Emote{
			{ID: "25", Ranges: []chat.Range{{Start: 0, End: 4}}, Name: "Kappa"},
			{ID: "26", Ranges: []chat.Range{{Start: 2, End: 4}}, Name: "ppa"}, // overlapping — skipped
		},
	}
	plain := stripANSI(m.renderChatText(msg))
	if plain != "Kappa" {
		t.Errorf("overlapping-emote render = %q, want Kappa", plain)
	}
}

func TestRenderChatText_OutOfRangeEmoteIgnored(t *testing.T) {
	m := chatModel(t, "c")
	msg := chat.Chat{
		Text:   "hi",
		Emotes: []chat.Emote{{ID: "x", Ranges: []chat.Range{{Start: 5, End: 9}}, Name: "oops"}},
	}
	plain := stripANSI(m.renderChatText(msg))
	if plain != "hi" {
		t.Errorf("out-of-range emote should be ignored: %q", plain)
	}
}

func TestJoinLeftRight_FillsGap(t *testing.T) {
	got := joinLeftRight("L", "R", 10)
	if got != "L        R" {
		t.Errorf("joinLeftRight = %q", got)
	}
}

func TestJoinLeftRight_MinimumOneSpace(t *testing.T) {
	got := joinLeftRight("LLLLL", "RRRRR", 5) // overflow
	if !strings.Contains(got, " ") {
		t.Errorf("joinLeftRight should always insert at least one space: %q", got)
	}
}
