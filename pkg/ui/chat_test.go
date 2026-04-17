package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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

// --- Layout integration ---

func TestChatPaneActive_RequiresVisibleAndSessions(t *testing.T) {
	m := newTestModel(&mockState{})
	m.width = 120
	m.height = 40

	if m.chatPaneActive() {
		t.Error("no sessions → chatPaneActive should be false")
	}

	m.chatSessions["shroud"] = NewChatSession("shroud", 500)
	m.chatOrder = []string{"shroud"}
	m.chatFocus = "shroud"
	m.chatVisible = false

	if m.chatPaneActive() {
		t.Error("chatVisible=false → chatPaneActive should be false")
	}

	m.chatVisible = true
	if !m.chatPaneActive() {
		t.Error("visible + sessions + focus → chatPaneActive should be true")
	}
}

// --- Keymap integration ---

func TestChatKey_ToggleVisibility(t *testing.T) {
	m := newTestModel(&mockState{})
	m.width = 120
	m.height = 40

	newM, _, ok := m.dispatchBinding("c")
	if !ok {
		t.Fatal("c should dispatch")
	}
	if !newM.chatVisible {
		t.Error("c should flip chatVisible to true")
	}
	newM2, _, _ := newM.dispatchBinding("c")
	if newM2.chatVisible {
		t.Error("second c should flip back to false")
	}
}

func TestChatKey_CycleFocus(t *testing.T) {
	m := newTestModel(&mockState{})
	for _, ch := range []string{"a", "b", "c"} {
		m.chatSessions[ch] = NewChatSession(ch, 100)
		m.chatOrder = append(m.chatOrder, ch)
	}
	m.chatFocus = "a"

	for _, want := range []string{"b", "c", "a"} {
		newM, _, ok := m.dispatchBinding("C")
		if !ok {
			t.Fatal("C should dispatch")
		}
		if newM.chatFocus != want {
			t.Errorf("cycle → %q, want %q", newM.chatFocus, want)
		}
		m = newM
	}
}

func TestChatKey_CycleFocus_NoopOnSingleSession(t *testing.T) {
	m := newTestModel(&mockState{})
	m.chatSessions["only"] = NewChatSession("only", 100)
	m.chatOrder = []string{"only"}
	m.chatFocus = "only"

	newM, _, _ := m.dispatchBinding("C")
	if newM.chatFocus != "only" {
		t.Errorf("cycle with one session should be a no-op, got %q", newM.chatFocus)
	}
}

func TestChatKey_SpaceResumes(t *testing.T) {
	m := newTestModel(&mockState{})
	s := NewChatSession("c", 100)
	pushN(s, 10)
	s.ScrollBack(3)
	m.chatSessions["c"] = s
	m.chatOrder = []string{"c"}
	m.chatFocus = "c"

	if !s.IsPaused() {
		t.Fatal("setup: session should be paused")
	}
	_, _, ok := m.dispatchBinding("space")
	if !ok {
		t.Fatal("space should dispatch")
	}
	if s.IsPaused() {
		t.Error("space should resume the session")
	}
}

func TestChatKey_BracketScrolls(t *testing.T) {
	m := newTestModel(&mockState{})
	s := NewChatSession("c", 100)
	pushN(s, 10)
	m.chatSessions["c"] = s
	m.chatOrder = []string{"c"}
	m.chatFocus = "c"

	// [ scrolls back → pauses
	m.dispatchBinding("[")
	if !s.IsPaused() {
		t.Error("[ should cause pause via ScrollBack")
	}

	// ] scrolls forward
	startBottom := 9 - 1 // after [ pushes ScrollBack(1), viewBottom was len-1-1 = 8
	if s.viewBottom != startBottom {
		t.Errorf("viewBottom after [ = %d, want %d", s.viewBottom, startBottom)
	}

	m.dispatchBinding("]")
	// Back to bottom → auto-resume
	if s.IsPaused() {
		t.Error("] should auto-resume when it hits the tail")
	}
}

func TestChatKey_BraceScrollsPage(t *testing.T) {
	m := newTestModel(&mockState{})
	s := NewChatSession("c", 200)
	pushN(s, 50)
	m.chatSessions["c"] = s
	m.chatOrder = []string{"c"}
	m.chatFocus = "c"

	m.dispatchBinding("{") // page back
	if !s.IsPaused() {
		t.Error("{ should cause pause")
	}
	// Expect viewBottom = tail - (chatPaneHeight - 1)
	want := s.Len() - 1 - (chatPaneHeight - 1)
	if s.viewBottom != want {
		t.Errorf("viewBottom after { = %d, want %d", s.viewBottom, want)
	}
}

func TestChatKey_EscHidesPane(t *testing.T) {
	m := newTestModel(&mockState{})
	m.chatVisible = true
	m.chatSessions["c"] = NewChatSession("c", 100)
	m.chatOrder = []string{"c"}
	m.chatFocus = "c"

	newM, _ := m.handleKey(pressKey("esc"))
	m2 := newM.(Model)
	if m2.chatVisible {
		t.Error("Esc should hide pane when chat is visible")
	}
}

func TestChatKey_EscResumesBeforeHiding(t *testing.T) {
	m := newTestModel(&mockState{})
	m.chatVisible = true
	s := NewChatSession("c", 100)
	pushN(s, 10)
	s.ScrollBack(3)
	m.chatSessions["c"] = s
	m.chatOrder = []string{"c"}
	m.chatFocus = "c"

	if !s.IsPaused() {
		t.Fatal("setup: should be paused")
	}
	m.handleKey(pressKey("esc"))
	if s.IsPaused() {
		t.Error("Esc should also clear paused state")
	}
}

func TestChatKey_EscFallsThroughWhenPaneHidden(t *testing.T) {
	// Esc with chatVisible=false should NOT run the chat-hide logic; it
	// falls through to the existing Esc handler. Since we don't have a
	// search/category stack set up, this just no-ops — we assert state
	// didn't change instead of asserting a specific side effect.
	m := newTestModel(&mockState{})
	m.chatVisible = false
	before := m.chatVisible
	m.handleKey(pressKey("esc"))
	if m.chatVisible != before {
		t.Errorf("chatVisible flipped unexpectedly")
	}
}

// --- Lifecycle ---

func TestChatLifecycle_ReceivedMsgPushes(t *testing.T) {
	m := newTestModel(&mockState{})
	m.chatSessions["c"] = NewChatSession("c", 100)
	m.chatOrder = []string{"c"}
	m.chatFocus = "c"

	msg := chatReceivedMsg{
		channel: "c",
		msg:     chat.Chat{Login: "alice", Text: "hi"},
	}
	newM, _ := m.Update(msg)
	m2 := newM.(Model)
	s := m2.chatSessions["c"]
	if s.Len() != 1 || s.buffer[0].Text != "hi" {
		t.Errorf("message not pushed: len=%d", s.Len())
	}
}

func TestChatLifecycle_ReceivedMsgUnknownChannelIgnored(t *testing.T) {
	m := newTestModel(&mockState{})
	// No sessions, no conns — incoming msg for "ghost" should be dropped
	// without panic and without changing state.
	msg := chatReceivedMsg{channel: "ghost", msg: chat.Chat{Text: "hi"}}
	newM, cmd := m.Update(msg)
	if cmd != nil {
		t.Error("unknown-channel receive should not re-arm a Cmd")
	}
	if len(newM.(Model).chatSessions) != 0 {
		t.Error("state changed unexpectedly")
	}
}

func TestChatLifecycle_ClosedMsgDropsAndRefocuses(t *testing.T) {
	m := newTestModel(&mockState{})
	m.chatSessions["a"] = NewChatSession("a", 100)
	m.chatSessions["b"] = NewChatSession("b", 100)
	m.chatOrder = []string{"a", "b"}
	m.chatFocus = "a"
	m.chatVisible = true

	newM, _ := m.Update(chatClosedMsg{channel: "a"})
	m2 := newM.(Model)

	if _, ok := m2.chatSessions["a"]; ok {
		t.Error("closed session should be removed")
	}
	if len(m2.chatOrder) != 1 || m2.chatOrder[0] != "b" {
		t.Errorf("chatOrder after close = %v, want [b]", m2.chatOrder)
	}
	if m2.chatFocus != "b" {
		t.Errorf("chatFocus after close = %q, want b", m2.chatFocus)
	}
	if !m2.chatVisible {
		t.Error("chatVisible should stay true while other sessions remain")
	}
}

func TestChatLifecycle_ClosedLastSessionHidesPane(t *testing.T) {
	m := newTestModel(&mockState{})
	m.chatSessions["a"] = NewChatSession("a", 100)
	m.chatOrder = []string{"a"}
	m.chatFocus = "a"
	m.chatVisible = true

	newM, _ := m.Update(chatClosedMsg{channel: "a"})
	m2 := newM.(Model)

	if m2.chatFocus != "" {
		t.Errorf("chatFocus = %q, want empty after last session closes", m2.chatFocus)
	}
	if m2.chatVisible {
		t.Error("chatVisible should flip off when all sessions are gone")
	}
}

func TestChatLifecycle_StartChatAutoshows(t *testing.T) {
	m := newTestModel(&mockState{})
	m.chatVisible = false

	newM, _, ok := m.startChat("shroud")
	if !ok {
		t.Fatal("startChat should return ok=true for a fresh channel")
	}
	// Cleanup the goroutine we just spawned.
	t.Cleanup(func() {
		if conn, ok := newM.chatConns["shroud"]; ok {
			conn.cancel()
		}
	})

	if !newM.chatVisible {
		t.Error("startChat should flip chatVisible on (first session)")
	}
	if newM.chatFocus != "shroud" {
		t.Errorf("chatFocus = %q, want shroud", newM.chatFocus)
	}
	if _, ok := newM.chatSessions["shroud"]; !ok {
		t.Error("chatSessions missing shroud entry")
	}
}

// --- Mouse wheel ---

// mouseWheel builds a tea.MouseWheelMsg at the given row.
func mouseWheel(y int, down bool) tea.MouseWheelMsg {
	btn := tea.MouseWheelUp
	if down {
		btn = tea.MouseWheelDown
	}
	return tea.MouseWheelMsg{X: 0, Y: y, Button: btn}
}

func TestMouseWheel_OverChatPaneScrollsChat(t *testing.T) {
	m := newTestModel(&mockState{})
	m.width = 160
	m.height = 40
	s := NewChatSession("c", 100)
	pushN(s, 30)
	m.chatSessions["c"] = s
	m.chatOrder = []string{"c"}
	m.chatFocus = "c"
	m.chatVisible = true

	// Chat pane occupies Y = m.height - chatPaneHeight - 3 .. m.height - 4.
	// For m.height=40, chatPaneHeight=10: Y in [27..36].
	middleY := 30

	// Wheel up inside pane → ScrollBack(1) → pauses at viewBottom=28.
	newM, _ := m.handleMouseWheel(mouseWheel(middleY, false))
	m2 := newM.(Model)
	if !m2.chatSessions["c"].IsPaused() {
		t.Error("wheel up over pane should pause chat")
	}

	// Wheel up twice more → viewBottom steps back: 27, 26.
	newM, _ = newM.(Model).handleMouseWheel(mouseWheel(middleY, false))
	newM, _ = newM.(Model).handleMouseWheel(mouseWheel(middleY, false))
	if got := newM.(Model).chatSessions["c"].viewBottom; got != 26 {
		t.Errorf("viewBottom after 3x wheel-up = %d, want 26", got)
	}

	// One wheel-down advances toward tail: 27.
	newM, _ = newM.(Model).handleMouseWheel(mouseWheel(middleY, true))
	if got := newM.(Model).chatSessions["c"].viewBottom; got != 27 {
		t.Errorf("viewBottom after wheel-down = %d, want 27", got)
	}
}

func TestMouseWheel_OutsideChatPaneMovesCursor(t *testing.T) {
	m := newTestModel(&mockState{})
	m.width = 160
	m.height = 40
	m.chatSessions["c"] = NewChatSession("c", 100)
	m.chatOrder = []string{"c"}
	m.chatFocus = "c"
	m.chatVisible = true
	// Give the picker at least a few list entries so cursor can move.
	m.watchList = []DiscoveryEntry{
		{Kind: EntryChannel, Login: "a", IsLive: true},
		{Kind: EntryChannel, Login: "b", IsLive: true},
	}
	m.cursor = 0

	// Y=5 is in the body region, not chat pane.
	newM, _ := m.handleMouseWheel(mouseWheel(5, true))
	if newM.(Model).cursor != 1 {
		t.Errorf("cursor after wheel-down over body = %d, want 1", newM.(Model).cursor)
	}
}

func TestMouseWheel_ChatNotActiveFallsThrough(t *testing.T) {
	m := newTestModel(&mockState{})
	m.width = 160
	m.height = 40
	m.watchList = []DiscoveryEntry{{Kind: EntryChannel, Login: "a"}, {Kind: EntryChannel, Login: "b"}}
	m.cursor = 0

	// Chat not active — wheel at any Y moves cursor.
	newM, _ := m.handleMouseWheel(mouseWheel(30, true))
	if newM.(Model).cursor != 1 {
		t.Errorf("cursor = %d, want 1 when chat off", newM.(Model).cursor)
	}
}

func TestChatLifecycle_StartChatIsIdempotent(t *testing.T) {
	m := newTestModel(&mockState{})
	m, _, ok := m.startChat("shroud")
	if !ok {
		t.Fatal("first startChat should ok")
	}
	t.Cleanup(func() {
		if conn, ok := m.chatConns["shroud"]; ok {
			conn.cancel()
		}
	})

	_, cmd, ok2 := m.startChat("shroud")
	if ok2 {
		t.Error("second startChat for same channel should return ok=false")
	}
	if cmd != nil {
		t.Error("second startChat should return nil Cmd")
	}
}

func TestRender_BodyShrinksWhenChatPaneShows(t *testing.T) {
	// With chat off, the body gets one line count; with chat on, it shrinks
	// by chatPaneHeight + the border separator.
	m := newTestModel(&mockState{})
	m.width = 120
	m.height = 40

	// Chat off: render should succeed and not contain the chat header glyph.
	out1 := m.render()
	if strings.Contains(stripANSI(out1), m.symbols.ChatActive) {
		t.Error("chat glyph leaked when chat is off")
	}

	// Turn chat on with a session; a `▸ Chat —` line must appear.
	m.chatSessions["shroud"] = NewChatSession("shroud", 500)
	m.chatOrder = []string{"shroud"}
	m.chatFocus = "shroud"
	m.chatVisible = true

	out2 := m.render()
	if !strings.Contains(stripANSI(out2), m.symbols.ChatActive) {
		t.Error("chat pane missing after enabling")
	}
	if !strings.Contains(stripANSI(out2), "Chat — shroud") {
		t.Errorf("pane header missing channel name")
	}

}
