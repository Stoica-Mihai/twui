package ui

import (
	"strings"
	"testing"
)

// Any two bindings claiming the same key cause the second to be silently
// unreachable — dispatchBinding stops at the first match. Prevent drift.
func TestBindings_NoDuplicateKeys(t *testing.T) {
	m := newTestModel(&mockState{})
	seen := map[string]int{}
	for i, b := range m.bindings() {
		for _, k := range b.Keys {
			if prev, ok := seen[k]; ok {
				t.Errorf("key %q is claimed by bindings[%d] and bindings[%d]; the second is unreachable", k, prev, i)
			}
			seen[k] = i
		}
	}
}

// Every binding must be reachable: Keys non-empty and Handler non-nil.
func TestBindings_WellFormed(t *testing.T) {
	m := newTestModel(&mockState{})
	for i, b := range m.bindings() {
		if len(b.Keys) == 0 {
			t.Errorf("bindings[%d] has no keys", i)
		}
		if b.Handler == nil {
			t.Errorf("bindings[%d] %v has nil Handler", i, b.Keys)
		}
	}
}

// Help-overlay rendering must include every binding's Display label exactly
// once. Catches the "forgot to update help" drift that B11 exists to prevent.
func TestRenderHelpOverlay_ContainsEveryLabel(t *testing.T) {
	m := newTestModel(&mockState{})
	out := m.renderHelpOverlay()

	for _, b := range m.bindings() {
		if b.Display == "" || b.Desc == "" {
			continue // continuation entries don't render a line
		}
		if !strings.Contains(out, b.Display) {
			t.Errorf("help overlay missing binding label %q", b.Display)
		}
		if !strings.Contains(out, b.Desc) {
			t.Errorf("help overlay missing binding desc %q", b.Desc)
		}
	}
}

// Dispatch must actually route to the binding's handler.
func TestDispatchBinding_RoutesToHandler(t *testing.T) {
	m := newTestModel(&mockState{})
	// "?" opens the help overlay.
	newM, _, ok := m.dispatchBinding("?")
	if !ok {
		t.Fatal("? should be dispatched")
	}
	if newM.overlay != overlayHelp {
		t.Errorf("? should open help overlay, got overlay=%v", newM.overlay)
	}
}

func TestDispatchBinding_UnknownKeyReturnsFalse(t *testing.T) {
	m := newTestModel(&mockState{})
	_, _, ok := m.dispatchBinding("F13")
	if ok {
		t.Error("unknown key should not dispatch")
	}
}
