package ui

import "testing"

func TestStatusConstants(t *testing.T) {
	if StatusWaiting != 0 {
		t.Errorf("StatusWaiting = %d, want 0", StatusWaiting)
	}
	if StatusPlaying != 1 {
		t.Errorf("StatusPlaying = %d, want 1", StatusPlaying)
	}
	if StatusAdBreak != 2 {
		t.Errorf("StatusAdBreak = %d, want 2", StatusAdBreak)
	}
	if StatusReconnecting != 3 {
		t.Errorf("StatusReconnecting = %d, want 3", StatusReconnecting)
	}
}

func TestStatusIota(t *testing.T) {
	// Verify iota ordering is stable.
	statuses := []Status{StatusWaiting, StatusPlaying, StatusAdBreak, StatusReconnecting}
	for i, s := range statuses {
		if int(s) != i {
			t.Errorf("statuses[%d] = %d, want %d", i, s, i)
		}
	}
}
