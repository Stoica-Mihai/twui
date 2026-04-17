package ui

import (
	"fmt"
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
