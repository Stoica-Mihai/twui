package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// mockState tracks callback invocations from DiscoveryFuncs.
type mockState struct {
	ignored           []string
	lastIgnoreChannel string
	lastIgnoreAdd     bool
	lastFavChannel    string
	lastFavAdd        bool
	lastTheme         string
}

// mockFns returns a complete DiscoveryFuncs wired to the given state.
func mockFns(state *mockState) DiscoveryFuncs {
	return DiscoveryFuncs{
		WatchList: func(ctx context.Context) ([]DiscoveryEntry, error) {
			return []DiscoveryEntry{
				{Kind: EntryChannel, Login: "streamer1", DisplayName: "Streamer1", IsLive: true},
				{Kind: EntryChannel, Login: "streamer2", DisplayName: "Streamer2", IsLive: false},
			}, nil
		},
		Search: func(ctx context.Context, query string) ([]DiscoveryEntry, error) {
			return nil, nil
		},
		BrowseCategories: func(ctx context.Context, cursor string) ([]DiscoveryEntry, string, error) {
			return nil, "", nil
		},
		CategoryStreams: func(ctx context.Context, cat, cursor string) ([]DiscoveryEntry, string, error) {
			return nil, "", nil
		},
		Streams: func(ctx context.Context, channel string) ([]string, error) {
			return []string{"source", "720p60"}, nil
		},
		Launch: func(ctx context.Context, channel, quality string, send func(Status, string)) {},
		ToggleFavorite: func(channel string, add bool) {
			state.lastFavChannel = channel
			state.lastFavAdd = add
		},
		ToggleIgnore: func(channel string, add bool) {
			if add {
				state.ignored = append(state.ignored, channel)
			} else {
				filtered := state.ignored[:0]
				for _, ch := range state.ignored {
					if ch != channel {
						filtered = append(filtered, ch)
					}
				}
				state.ignored = filtered
			}
			state.lastIgnoreChannel = channel
			state.lastIgnoreAdd = add
		},
		IgnoreList: func() []string { return state.ignored },
		HostingChannels: func(ctx context.Context, channel string) ([]DiscoveryEntry, error) {
			return nil, nil
		},
		WriteTheme: func(name string) { state.lastTheme = name },
	}
}

// newTestModel creates a Model with mock callbacks. Auto-refresh disabled.
func newTestModel(state *mockState) Model {
	m := NewModel(mockFns(state), DefaultTheme(), 0)
	m.width = 120
	m.height = 30
	return *m
}

// pressKey constructs a tea.KeyPressMsg for the given key string.
// Supports printable characters, "tab", "shift+tab", "enter", "esc",
// "backspace", "pgup", "pgdown", "home", "end".
func pressKey(s string) tea.KeyPressMsg {
	switch s {
	case "tab":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyTab})
	case "shift+tab":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift})
	case "enter":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	case "esc":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc})
	case "backspace":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace})
	case "pgup":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp})
	case "pgdown":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown})
	case "home":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyHome})
	case "end":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd})
	default:
		r := []rune(s)[0]
		return tea.KeyPressMsg(tea.Key{Code: r, Text: s})
	}
}

// updateKey sends a single key press to the model and returns the new model.
func updateKey(m Model, key string) Model {
	newM, _ := m.Update(pressKey(key))
	return newM.(Model)
}

// --- Message-based tests ---

func TestModel_WatchListResult_PopulatesList(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)

	entries := []DiscoveryEntry{
		{Kind: EntryChannel, Login: "chan1", IsLive: true},
		{Kind: EntryChannel, Login: "chan2", IsLive: false},
	}
	newM, _ := m.Update(watchListResultMsg{entries: entries, err: nil})
	m2 := newM.(Model)

	if len(m2.watchList) != 2 {
		t.Errorf("watchList len = %d, want 2", len(m2.watchList))
	}
	if m2.watchList[0].Login != "chan1" {
		t.Errorf("watchList[0].Login = %q, want %q", m2.watchList[0].Login, "chan1")
	}
}

func TestModel_WatchListResult_FiltersIgnored(t *testing.T) {
	state := &mockState{ignored: []string{"ignored1"}}
	m := newTestModel(state)

	entries := []DiscoveryEntry{
		{Kind: EntryChannel, Login: "ignored1", IsLive: true},
		{Kind: EntryChannel, Login: "visible", IsLive: true},
	}
	newM, _ := m.Update(watchListResultMsg{entries: entries, err: nil})
	m2 := newM.(Model)

	if len(m2.watchList) != 1 {
		t.Errorf("watchList len = %d, want 1 (ignored should be filtered)", len(m2.watchList))
	}
	if m2.watchList[0].Login != "visible" {
		t.Errorf("expected visible channel, got %q", m2.watchList[0].Login)
	}
}

func TestModel_SearchResult_FreshMsgApplied(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.searchQuery = "hello"

	entries := []DiscoveryEntry{
		{Kind: EntryChannel, Login: "result1"},
	}
	newM, _ := m.Update(searchResultMsg{query: "hello", entries: entries})
	m2 := newM.(Model)

	if len(m2.searchList) != 1 {
		t.Errorf("searchList len = %d, want 1", len(m2.searchList))
	}
	if m2.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (reset on new search)", m2.cursor)
	}
}

func TestModel_SearchResult_StaleMsgDropped(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.searchQuery = "current"
	m.searchList = []DiscoveryEntry{{Kind: EntryChannel, Login: "existing"}}

	// Message for a different query → should be dropped.
	newM, _ := m.Update(searchResultMsg{query: "stale", entries: []DiscoveryEntry{}})
	m2 := newM.(Model)

	if len(m2.searchList) != 1 {
		t.Errorf("stale message should not replace searchList; got len %d", len(m2.searchList))
	}
}

func TestModel_BrowseResult_SetsCategories(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeBrowse

	entries := []DiscoveryEntry{
		{Kind: EntryCategory, CategoryName: "Just Chatting", CategoryViewers: 100000},
	}
	newM, _ := m.Update(browseResultMsg{entries: entries, nextCursor: "page2"})
	m2 := newM.(Model)

	if len(m2.browseList) != 1 {
		t.Errorf("browseList len = %d, want 1", len(m2.browseList))
	}
	if m2.browseNextCursor != "page2" {
		t.Errorf("browseNextCursor = %q, want %q", m2.browseNextCursor, "page2")
	}
}

func TestModel_CategoryStreams_Replace(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.categoryList = []DiscoveryEntry{{Kind: EntryChannel, Login: "old"}}
	m.cursor = 5

	entries := []DiscoveryEntry{
		{Kind: EntryChannel, Login: "new1"},
		{Kind: EntryChannel, Login: "new2"},
	}
	newM, _ := m.Update(categoryStreamsResultMsg{
		category:   "Fortnite",
		entries:    entries,
		nextCursor: "",
		appendMode: false,
	})
	m2 := newM.(Model)

	if len(m2.categoryList) != 2 {
		t.Errorf("categoryList len = %d, want 2", len(m2.categoryList))
	}
	if m2.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (reset on replace)", m2.cursor)
	}
}

func TestModel_CategoryStreams_Append(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.categoryList = []DiscoveryEntry{{Kind: EntryChannel, Login: "existing"}}
	m.cursor = 0

	entries := []DiscoveryEntry{
		{Kind: EntryChannel, Login: "appended"},
	}
	newM, _ := m.Update(categoryStreamsResultMsg{
		category:   "Fortnite",
		entries:    entries,
		nextCursor: "",
		appendMode: true,
	})
	m2 := newM.(Model)

	if len(m2.categoryList) != 2 {
		t.Errorf("categoryList len = %d, want 2 (append should add)", len(m2.categoryList))
	}
}

func TestModel_StatusUpdate_TracksSession(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	gen := 1
	ch := make(chan statusUpdateMsg, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.sessions["testchan"] = &playbackSession{
		ctx:    ctx,
		cancel: cancel,
		gen:    gen,
		ch:     ch,
	}

	newM, _ := m.Update(statusUpdateMsg{
		channel: "testchan",
		status:  StatusPlaying,
		detail:  "720p60",
		gen:     gen,
	})
	m2 := newM.(Model)

	sess, ok := m2.sessions["testchan"]
	if !ok {
		t.Fatal("session should still exist after non-done update")
	}
	if sess.status != StatusPlaying {
		t.Errorf("sess.status = %v, want StatusPlaying", sess.status)
	}
}

func TestModel_StatusUpdate_DoneClears(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	gen := 1
	ch := make(chan statusUpdateMsg, 1)
	close(ch)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	m.sessions["testchan"] = &playbackSession{
		ctx:    ctx,
		cancel: cancel,
		gen:    gen,
		ch:     ch,
	}

	newM, _ := m.Update(statusUpdateMsg{
		channel: "testchan",
		done:    true,
		gen:     gen,
	})
	m2 := newM.(Model)

	if _, ok := m2.sessions["testchan"]; ok {
		t.Error("session should be removed after done=true")
	}
}

// --- Key-press tests ---

func TestModel_QuitKey(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)

	_, cmd := m.Update(pressKey("q"))
	if cmd == nil {
		t.Fatal("q key should return a non-nil Quit cmd")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("cmd() returned %T, want tea.QuitMsg", msg)
	}
}

func TestModel_TabCycle_FourViews(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	// Start at WatchList.
	if m.mode != viewModeWatchList {
		t.Fatalf("initial mode = %v, want viewModeWatchList", m.mode)
	}

	m = updateKey(m, "tab")
	if m.mode != viewModeBrowse {
		t.Errorf("after 1 tab: mode = %v, want viewModeBrowse", m.mode)
	}

	m = updateKey(m, "tab")
	if m.mode != viewModeSearch {
		t.Errorf("after 2 tabs: mode = %v, want viewModeSearch", m.mode)
	}

	m = updateKey(m, "tab")
	if m.mode != viewModeIgnored {
		t.Errorf("after 3 tabs: mode = %v, want viewModeIgnored", m.mode)
	}

	m = updateKey(m, "tab")
	if m.mode != viewModeWatchList {
		t.Errorf("after 4 tabs: mode = %v, want viewModeWatchList (wrap)", m.mode)
	}
}

func TestModel_ShiftTab_Reverse(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	// From WatchList, shift+tab should go to Ignored.
	m = updateKey(m, "shift+tab")
	if m.mode != viewModeIgnored {
		t.Errorf("shift+tab from WatchList: mode = %v, want viewModeIgnored", m.mode)
	}
}

func TestModel_CursorJK(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.watchList = []DiscoveryEntry{
		{Kind: EntryChannel, Login: "a"},
		{Kind: EntryChannel, Login: "b"},
		{Kind: EntryChannel, Login: "c"},
	}
	m.cursor = 0

	m = updateKey(m, "j")
	if m.cursor != 1 {
		t.Errorf("j: cursor = %d, want 1", m.cursor)
	}

	m = updateKey(m, "j")
	if m.cursor != 2 {
		t.Errorf("j: cursor = %d, want 2", m.cursor)
	}

	// At bottom — j should clamp.
	m = updateKey(m, "j")
	if m.cursor != 2 {
		t.Errorf("j at bottom: cursor = %d, want 2 (clamped)", m.cursor)
	}

	m = updateKey(m, "k")
	if m.cursor != 1 {
		t.Errorf("k: cursor = %d, want 1", m.cursor)
	}
}

func TestModel_CursorClampAtZero(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.watchList = []DiscoveryEntry{{Kind: EntryChannel, Login: "only"}}
	m.cursor = 0

	m = updateKey(m, "k") // at top
	if m.cursor != 0 {
		t.Errorf("k at top: cursor = %d, want 0 (clamped)", m.cursor)
	}
}

func TestModel_HelpOverlay_OpenClose(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)

	m = updateKey(m, "?")
	if m.overlay != overlayHelp {
		t.Errorf("? key: overlay = %v, want overlayHelp", m.overlay)
	}

	m = updateKey(m, "esc")
	if m.overlay != overlayNone {
		t.Errorf("esc key: overlay = %v, want overlayNone", m.overlay)
	}
}

func TestModel_ThemeOverlay_Open(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)

	m = updateKey(m, "t")
	if m.overlay != overlayTheme {
		t.Errorf("t key: overlay = %v, want overlayTheme", m.overlay)
	}
}

func TestModel_ThemeOverlay_ApplyCallsWriteTheme(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)

	// Open theme overlay.
	m = updateKey(m, "t")
	// Navigate to second preset (index 1 = dracula).
	m = updateKey(m, "j")
	// Apply with Enter.
	m = updateKey(m, "enter")

	if m.overlay != overlayNone {
		t.Error("Enter should close the theme overlay")
	}
	if state.lastTheme == "" {
		t.Error("Enter on theme picker should call WriteTheme")
	}
}

func TestModel_ThemeOverlay_EscReverts(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)

	m = updateKey(m, "t")
	m = updateKey(m, "esc")

	if m.overlay != overlayNone {
		t.Error("Esc should close theme overlay")
	}
	if state.lastTheme != "" {
		t.Error("Esc should not call WriteTheme")
	}
}

func TestModel_IgnoreKey_RemovesFromList(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeWatchList
	m.watchList = []DiscoveryEntry{
		{Kind: EntryChannel, Login: "streamer1", IsLive: true},
		{Kind: EntryChannel, Login: "streamer2", IsLive: false},
	}
	m.cursor = 0

	m = updateKey(m, "x")

	if state.lastIgnoreChannel != "streamer1" {
		t.Errorf("ToggleIgnore called with %q, want %q", state.lastIgnoreChannel, "streamer1")
	}
	if !state.lastIgnoreAdd {
		t.Error("ToggleIgnore should be called with add=true")
	}
	if len(m.watchList) != 1 {
		t.Errorf("watchList len = %d, want 1 (removed entry)", len(m.watchList))
	}
}

func TestModel_FavoriteKey_CallsCallback(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeWatchList
	m.watchList = []DiscoveryEntry{
		{Kind: EntryChannel, Login: "streamer1", IsLive: true, IsFavorite: false},
	}
	m.cursor = 0

	m = updateKey(m, "f")

	if state.lastFavChannel != "streamer1" {
		t.Errorf("ToggleFavorite called with %q, want %q", state.lastFavChannel, "streamer1")
	}
	if !state.lastFavAdd {
		t.Error("ToggleFavorite should be called with add=true (toggling from not-fav)")
	}
}

func TestModel_IgnoredView_XUnignores(t *testing.T) {
	state := &mockState{ignored: []string{"bannedchan"}}
	m := newTestModel(state)
	m.mode = viewModeIgnored
	m.cursor = 0

	m = updateKey(m, "x")

	if state.lastIgnoreChannel != "bannedchan" {
		t.Errorf("x in Ignored: ToggleIgnore called with %q, want %q", state.lastIgnoreChannel, "bannedchan")
	}
	if state.lastIgnoreAdd {
		t.Error("x in Ignored: ToggleIgnore should be called with add=false")
	}
	if len(state.ignored) != 0 {
		t.Errorf("state.ignored len = %d, want 0 after un-ignore", len(state.ignored))
	}
}

func TestModel_IgnoredView_FUnignoresAndFavorites(t *testing.T) {
	state := &mockState{ignored: []string{"chan1"}}
	m := newTestModel(state)
	m.mode = viewModeIgnored
	m.cursor = 0

	m = updateKey(m, "f")

	if state.lastIgnoreChannel != "chan1" {
		t.Errorf("f in Ignored: ToggleIgnore called with %q, want %q", state.lastIgnoreChannel, "chan1")
	}
	if state.lastIgnoreAdd {
		t.Error("f in Ignored: ToggleIgnore should be called with add=false")
	}
	if state.lastFavChannel != "chan1" {
		t.Errorf("f in Ignored: ToggleFavorite called with %q, want %q", state.lastFavChannel, "chan1")
	}
	if !state.lastFavAdd {
		t.Error("f in Ignored: ToggleFavorite should be called with add=true")
	}
}

func TestModel_RelatedOverlay_Opens(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeWatchList
	m.watchList = []DiscoveryEntry{
		{Kind: EntryChannel, Login: "streamer1", IsLive: true},
	}
	m.cursor = 0

	m = updateKey(m, "r")

	if m.overlay != overlayRelated {
		t.Errorf("r key: overlay = %v, want overlayRelated", m.overlay)
	}
	if m.overlayChannel != "streamer1" {
		t.Errorf("overlayChannel = %q, want %q", m.overlayChannel, "streamer1")
	}
}

func TestModel_RelatedOverlay_EscCloses(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.overlay = overlayRelated
	m.overlayChannel = "some"

	m = updateKey(m, "esc")

	if m.overlay != overlayNone {
		t.Errorf("esc: overlay = %v, want overlayNone", m.overlay)
	}
}

func TestModel_SearchMode_SlashActivates(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeWatchList

	m = updateKey(m, "/")

	if m.mode != viewModeSearch {
		t.Errorf("mode = %v, want viewModeSearch", m.mode)
	}
	if !m.searching {
		t.Error("searching should be true after /")
	}
}

func TestModel_FilterIgnored_RemovesFromAllLists(t *testing.T) {
	state := &mockState{ignored: []string{"blocked"}}
	m := newTestModel(state)

	entries := []DiscoveryEntry{
		{Kind: EntryChannel, Login: "blocked"},
		{Kind: EntryChannel, Login: "allowed"},
	}
	filtered := m.filterIgnored(entries)

	if len(filtered) != 1 {
		t.Errorf("filterIgnored len = %d, want 1", len(filtered))
	}
	if filtered[0].Login != "allowed" {
		t.Errorf("filtered[0].Login = %q, want %q", filtered[0].Login, "allowed")
	}
}

func TestModel_CurrentListLen_PerMode(t *testing.T) {
	state := &mockState{ignored: []string{"a", "b"}}
	m := newTestModel(state)
	m.watchList = []DiscoveryEntry{{}, {}, {}}
	m.browseList = []DiscoveryEntry{{}}
	m.searchList = []DiscoveryEntry{{}, {}}

	m.mode = viewModeWatchList
	if got := m.currentListLen(); got != 3 {
		t.Errorf("WatchList len = %d, want 3", got)
	}

	m.mode = viewModeBrowse
	if got := m.currentListLen(); got != 1 {
		t.Errorf("Browse len = %d, want 1", got)
	}

	m.mode = viewModeSearch
	if got := m.currentListLen(); got != 2 {
		t.Errorf("Search len = %d, want 2", got)
	}

	m.mode = viewModeIgnored
	if got := m.currentListLen(); got != 2 {
		t.Errorf("Ignored len = %d, want 2", got)
	}
}

// --- Auto-refresh ---

// NewModel must accept an auto-refresh interval (0 = disabled).
func TestNewModel_StoresRefreshInterval(t *testing.T) {
	m := NewModel(mockFns(&mockState{}), DefaultTheme(), 90*time.Second)
	if m.refreshInterval != 90*time.Second {
		t.Errorf("refreshInterval = %v, want 90s", m.refreshInterval)
	}
	if m.refreshCountdown != 90*time.Second {
		t.Errorf("refreshCountdown = %v, want 90s (initial)", m.refreshCountdown)
	}
}

func TestNewModel_ZeroIntervalDisablesRefresh(t *testing.T) {
	m := NewModel(mockFns(&mockState{}), DefaultTheme(), 0)
	if m.refreshInterval != 0 {
		t.Errorf("refreshInterval = %v, want 0 (disabled)", m.refreshInterval)
	}
}

// tickMsg should decrement the countdown and re-arm the tick.
func TestModel_Tick_DecrementsCountdown(t *testing.T) {
	m := NewModel(mockFns(&mockState{}), DefaultTheme(), 60*time.Second)
	m.width, m.height = 120, 30
	m.refreshCountdown = 5 * time.Second

	newM, cmd := m.Update(tickMsg(time.Now()))
	m2 := newM.(Model)

	if m2.refreshCountdown != 4*time.Second {
		t.Errorf("countdown = %v, want 4s", m2.refreshCountdown)
	}
	if cmd == nil {
		t.Error("tickMsg should re-arm the tick command, got nil")
	}
}

// When countdown reaches zero, tickMsg should reset countdown and trigger a refresh.
func TestModel_Tick_FiresRefreshAtZero(t *testing.T) {
	m := NewModel(mockFns(&mockState{}), DefaultTheme(), 60*time.Second)
	m.width, m.height = 120, 30
	m.refreshCountdown = 1 * time.Second

	newM, cmd := m.Update(tickMsg(time.Now()))
	m2 := newM.(Model)

	if m2.refreshCountdown != 60*time.Second {
		t.Errorf("countdown = %v, want 60s (reset)", m2.refreshCountdown)
	}
	if cmd == nil {
		t.Error("expected refresh command + re-armed tick, got nil")
	}
}

// With interval == 0, ticks must be ignored (no command returned).
func TestModel_Tick_IgnoredWhenDisabled(t *testing.T) {
	m := NewModel(mockFns(&mockState{}), DefaultTheme(), 0)
	m.width, m.height = 120, 30

	_, cmd := m.Update(tickMsg(time.Now()))
	if cmd != nil {
		t.Error("tick on disabled refresh should return nil cmd")
	}
}

// Refresh result for the WatchList view must preserve cursor by login.
func TestModel_RefreshResult_PreservesCursorByLogin(t *testing.T) {
	m := newTestModel(&mockState{})
	m.mode = viewModeWatchList
	m.watchList = []DiscoveryEntry{
		{Kind: EntryChannel, Login: "a"},
		{Kind: EntryChannel, Login: "b"},
		{Kind: EntryChannel, Login: "c"},
	}
	m.cursor = 1 // pointing at "b"

	// Refresh returns a reordered list — "b" is now at index 2.
	newEntries := []DiscoveryEntry{
		{Kind: EntryChannel, Login: "c"},
		{Kind: EntryChannel, Login: "a"},
		{Kind: EntryChannel, Login: "b"},
	}
	newM, _ := m.Update(watchListResultMsg{entries: newEntries, refreshed: true})
	m2 := newM.(Model)

	if m2.cursor != 2 {
		t.Errorf("cursor = %d, want 2 (should follow login 'b')", m2.cursor)
	}
}

// Refresh of category streams must also preserve cursor by login.
func TestModel_CategoryStreamsRefresh_PreservesCursor(t *testing.T) {
	m := newTestModel(&mockState{})
	m.mode = viewModeBrowse
	m.categoryStack = []string{"Just Chatting"}
	m.categoryList = []DiscoveryEntry{
		{Kind: EntryChannel, Login: "x"},
		{Kind: EntryChannel, Login: "y"},
	}
	m.cursor = 1 // pointing at "y"

	newEntries := []DiscoveryEntry{
		{Kind: EntryChannel, Login: "z"},
		{Kind: EntryChannel, Login: "y"},
		{Kind: EntryChannel, Login: "x"},
	}
	newM, _ := m.Update(categoryStreamsResultMsg{
		category: "Just Chatting",
		entries:  newEntries,
		refresh:  true,
	})
	m2 := newM.(Model)

	if m2.cursor != 1 {
		t.Errorf("cursor = %d, want 1 (should follow login 'y')", m2.cursor)
	}
}

// Browse-categories refresh must preserve cursor by category name.
func TestModel_BrowseRefresh_PreservesCursorByCategory(t *testing.T) {
	m := newTestModel(&mockState{})
	m.mode = viewModeBrowse
	m.browseList = []DiscoveryEntry{
		{Kind: EntryCategory, CategoryName: "Just Chatting"},
		{Kind: EntryCategory, CategoryName: "Fortnite"},
	}
	m.cursor = 1

	newEntries := []DiscoveryEntry{
		{Kind: EntryCategory, CategoryName: "Valorant"},
		{Kind: EntryCategory, CategoryName: "Fortnite"},
		{Kind: EntryCategory, CategoryName: "Just Chatting"},
	}
	newM, _ := m.Update(browseResultMsg{entries: newEntries, refreshed: true})
	m2 := newM.(Model)

	if m2.cursor != 1 {
		t.Errorf("cursor = %d, want 1 (Fortnite at new index 1)", m2.cursor)
	}
}

// --- Utility function tests (additional) ---

func TestCountLiveOffline(t *testing.T) {
	m := newTestModel(&mockState{})
	m.mode = viewModeWatchList
	m.watchList = []DiscoveryEntry{
		{Kind: EntryChannel, IsLive: true},
		{Kind: EntryChannel, IsLive: true},
		{Kind: EntryChannel, IsLive: false},
		{Kind: EntryCategory}, // not a channel, should be skipped
	}
	live, offline := m.countLiveOffline()
	if live != 2 || offline != 1 {
		t.Errorf("countLiveOffline = (%d, %d), want (2, 1)", live, offline)
	}
}

// --- Search input tests ---

func TestSearchMode_SlashActivates(t *testing.T) {
	m := newTestModel(&mockState{})
	m.mode = viewModeWatchList
	m = updateKey(m, "/")
	if m.mode != viewModeSearch {
		t.Errorf("mode = %d, want viewModeSearch", m.mode)
	}
	if !m.searching {
		t.Error("searching should be true after /")
	}
}

func TestSearchMode_TypingAppendsToInput(t *testing.T) {
	m := newTestModel(&mockState{})
	m.mode = viewModeSearch
	m.searching = true
	m = updateKey(m, "a")
	m = updateKey(m, "b")
	if m.searchInput != "ab" {
		t.Errorf("searchInput = %q, want %q", m.searchInput, "ab")
	}
}

func TestSearchMode_BackspaceDeletesChar(t *testing.T) {
	m := newTestModel(&mockState{})
	m.mode = viewModeSearch
	m.searching = true
	m.searchInput = "abc"
	m = updateKey(m, "backspace")
	if m.searchInput != "ab" {
		t.Errorf("searchInput = %q after backspace, want %q", m.searchInput, "ab")
	}
}

func TestSearchMode_EscCancels(t *testing.T) {
	m := newTestModel(&mockState{})
	m.mode = viewModeSearch
	m.searching = true
	m.searchInput = "test"
	m = updateKey(m, "esc")
	if m.searching {
		t.Error("searching should be false after esc")
	}
}

// --- Enter key tests ---

func TestEnterKey_BrowseCategory_EntersCategory(t *testing.T) {
	m := newTestModel(&mockState{})
	m.mode = viewModeBrowse
	m.browseList = []DiscoveryEntry{
		{Kind: EntryCategory, CategoryName: "Just Chatting"},
	}
	m.cursor = 0
	m = updateKey(m, "enter")
	if len(m.categoryStack) != 1 || m.categoryStack[0] != "Just Chatting" {
		t.Errorf("categoryStack = %v, want [Just Chatting]", m.categoryStack)
	}
	if !m.loading {
		t.Error("should be loading after entering category")
	}
}

func TestEnterKey_IgnoredView_Unignores(t *testing.T) {
	state := &mockState{ignored: []string{"badchan"}}
	m := newTestModel(state)
	m.mode = viewModeIgnored
	m.cursor = 0
	m = updateKey(m, "enter")
	if state.lastIgnoreChannel != "badchan" || state.lastIgnoreAdd {
		t.Errorf("expected un-ignore of badchan, got channel=%q add=%v",
			state.lastIgnoreChannel, state.lastIgnoreAdd)
	}
}

// --- Esc key tests ---

func TestEscKey_BrowseCategoryStack_Pops(t *testing.T) {
	m := newTestModel(&mockState{})
	m.mode = viewModeBrowse
	m.categoryStack = []string{"Just Chatting"}
	m = updateKey(m, "esc")
	if len(m.categoryStack) != 0 {
		t.Errorf("categoryStack = %v, want empty after esc", m.categoryStack)
	}
}

func TestEscKey_Search_StopsSearching(t *testing.T) {
	m := newTestModel(&mockState{})
	m.mode = viewModeSearch
	m.searching = true
	m = updateKey(m, "esc")
	if m.searching {
		t.Error("searching should be false after esc")
	}
}

// --- Navigation tests ---

func TestNavigationKeys_GoTop(t *testing.T) {
	m := newTestModel(&mockState{})
	m.watchList = make([]DiscoveryEntry, 20)
	m.cursor = 15
	m = updateKey(m, "g")
	if m.cursor != 0 {
		t.Errorf("cursor = %d after g, want 0", m.cursor)
	}
}

func TestNavigationKeys_GoBottom(t *testing.T) {
	m := newTestModel(&mockState{})
	m.watchList = make([]DiscoveryEntry, 20)
	m.cursor = 0
	m = updateKey(m, "G")
	if m.cursor != 19 {
		t.Errorf("cursor = %d after G, want 19", m.cursor)
	}
}

func TestNavigationKeys_PageDown(t *testing.T) {
	m := newTestModel(&mockState{})
	m.watchList = make([]DiscoveryEntry, 50)
	m.cursor = 0
	m = updateKey(m, "pgdown")
	if m.cursor != 10 {
		t.Errorf("cursor = %d after pgdown, want 10", m.cursor)
	}
}

func TestNavigationKeys_PageUp(t *testing.T) {
	m := newTestModel(&mockState{})
	m.watchList = make([]DiscoveryEntry, 50)
	m.cursor = 15
	m = updateKey(m, "pgup")
	if m.cursor != 5 {
		t.Errorf("cursor = %d after pgup, want 5", m.cursor)
	}
}

// --- currentEntry tests ---

func TestCurrentEntry_WatchList(t *testing.T) {
	m := newTestModel(&mockState{})
	m.mode = viewModeWatchList
	m.watchList = []DiscoveryEntry{{Kind: EntryChannel, Login: "test"}}
	m.cursor = 0
	e := m.currentEntry()
	if e == nil || e.Login != "test" {
		t.Errorf("currentEntry = %v, want entry with login 'test'", e)
	}
}

func TestCurrentEntry_OutOfBounds(t *testing.T) {
	m := newTestModel(&mockState{})
	m.mode = viewModeWatchList
	m.watchList = nil
	m.cursor = 5
	if e := m.currentEntry(); e != nil {
		t.Errorf("currentEntry out of bounds = %v, want nil", e)
	}
}

func TestCurrentEntry_BrowseTopLevel(t *testing.T) {
	m := newTestModel(&mockState{})
	m.mode = viewModeBrowse
	m.browseList = []DiscoveryEntry{{Kind: EntryCategory, CategoryName: "Fortnite"}}
	m.cursor = 0
	e := m.currentEntry()
	if e == nil || e.CategoryName != "Fortnite" {
		t.Errorf("currentEntry browse = %v, want category Fortnite", e)
	}
}

func TestCurrentEntry_BrowseCategory(t *testing.T) {
	m := newTestModel(&mockState{})
	m.mode = viewModeBrowse
	m.categoryStack = []string{"Just Chatting"}
	m.categoryList = []DiscoveryEntry{{Kind: EntryChannel, Login: "streamer"}}
	m.cursor = 0
	e := m.currentEntry()
	if e == nil || e.Login != "streamer" {
		t.Errorf("currentEntry category = %v, want login 'streamer'", e)
	}
}

func TestCurrentEntry_Search(t *testing.T) {
	m := newTestModel(&mockState{})
	m.mode = viewModeSearch
	m.searchList = []DiscoveryEntry{{Kind: EntryChannel, Login: "found"}}
	m.cursor = 0
	e := m.currentEntry()
	if e == nil || e.Login != "found" {
		t.Errorf("currentEntry search = %v, want login 'found'", e)
	}
}

func TestCurrentEntry_Ignored_ReturnsNil(t *testing.T) {
	m := newTestModel(&mockState{ignored: []string{"ch1"}})
	m.mode = viewModeIgnored
	m.cursor = 0
	if e := m.currentEntry(); e != nil {
		t.Errorf("currentEntry ignored = %v, want nil", e)
	}
}

// --- removeCurrentEntry tests ---

func TestRemoveCurrentEntry_WatchList(t *testing.T) {
	m := newTestModel(&mockState{})
	m.mode = viewModeWatchList
	m.watchList = []DiscoveryEntry{
		{Kind: EntryChannel, Login: "a"},
		{Kind: EntryChannel, Login: "b"},
		{Kind: EntryChannel, Login: "c"},
	}
	m.cursor = 1
	m.removeCurrentEntry()
	if len(m.watchList) != 2 {
		t.Fatalf("watchList len = %d, want 2", len(m.watchList))
	}
	if m.watchList[1].Login != "c" {
		t.Errorf("watchList[1] = %q, want 'c'", m.watchList[1].Login)
	}
}

func TestRemoveCurrentEntry_LastItem_CursorClamps(t *testing.T) {
	m := newTestModel(&mockState{})
	m.mode = viewModeWatchList
	m.watchList = []DiscoveryEntry{
		{Kind: EntryChannel, Login: "a"},
		{Kind: EntryChannel, Login: "b"},
	}
	m.cursor = 1 // pointing at last item
	m.removeCurrentEntry()
	if m.cursor != 0 {
		t.Errorf("cursor = %d after removing last item, want 0", m.cursor)
	}
}

// --- Quality picker tests ---

func TestQualityPicker_OfflineChannel_NoOp(t *testing.T) {
	m := newTestModel(&mockState{})
	m.mode = viewModeWatchList
	m.watchList = []DiscoveryEntry{{Kind: EntryChannel, Login: "offline", IsLive: false}}
	m.cursor = 0
	m = updateKey(m, "i")
	if m.loading {
		t.Error("pressing i on offline channel should not trigger loading")
	}
}

// --- Render tests ---

func TestRenderTabBar_ContainsTabs(t *testing.T) {
	m := newTestModel(&mockState{})
	bar := m.renderTabBar()
	for _, tab := range []string{"Watch List", "Browse", "Search", "Ignored"} {
		if !strings.Contains(bar, tab) {
			t.Errorf("tab bar missing %q: %s", tab, bar)
		}
	}
}

func TestRenderTabBar_RefreshIndicator(t *testing.T) {
	m := NewModel(mockFns(&mockState{}), DefaultTheme(), 60*time.Second)
	m.width, m.height = 120, 30
	bar := m.renderTabBar()
	if !strings.Contains(bar, "↻") {
		t.Errorf("tab bar missing refresh indicator: %s", bar)
	}
}

func TestRenderTabBar_NoRefreshWhenDisabled(t *testing.T) {
	m := newTestModel(&mockState{})
	bar := m.renderTabBar()
	if strings.Contains(bar, "↻") {
		t.Errorf("tab bar should not have refresh indicator when disabled: %s", bar)
	}
}

func TestRenderFooter_DefaultShowsLiveCount(t *testing.T) {
	m := newTestModel(&mockState{})
	m.watchList = []DiscoveryEntry{
		{Kind: EntryChannel, IsLive: true},
		{Kind: EntryChannel, IsLive: false},
	}
	footer := m.renderFooter()
	stripped := stripANSI(footer)
	if !strings.Contains(stripped, "1 live") {
		t.Errorf("footer missing live count: %s", stripped)
	}
	if !strings.Contains(stripped, "1 offline") {
		t.Errorf("footer missing offline count: %s", stripped)
	}
}

func TestRenderFooter_Notice(t *testing.T) {
	m := newTestModel(&mockState{})
	m.notice = "something happened"
	footer := m.renderFooter()
	if !strings.Contains(stripANSI(footer), "something happened") {
		t.Errorf("footer missing notice: %s", stripANSI(footer))
	}
}

// --- Key handlers: g, G, pgup, pgdown ---

func TestModel_GoToTop(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.watchList = make([]DiscoveryEntry, 20)
	for i := range m.watchList {
		m.watchList[i] = DiscoveryEntry{Kind: EntryChannel, Login: fmt.Sprintf("ch%d", i)}
	}
	m.cursor = 15

	m = updateKey(m, "g")
	if m.cursor != 0 {
		t.Errorf("g: cursor = %d, want 0", m.cursor)
	}
}

func TestModel_GoToBottom(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.watchList = make([]DiscoveryEntry, 20)
	for i := range m.watchList {
		m.watchList[i] = DiscoveryEntry{Kind: EntryChannel, Login: fmt.Sprintf("ch%d", i)}
	}
	m.cursor = 0

	m = updateKey(m, "G")
	if m.cursor != 19 {
		t.Errorf("G: cursor = %d, want 19", m.cursor)
	}
}

func TestModel_PageDown(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.watchList = make([]DiscoveryEntry, 25)
	for i := range m.watchList {
		m.watchList[i] = DiscoveryEntry{Kind: EntryChannel, Login: fmt.Sprintf("ch%d", i)}
	}
	m.cursor = 0

	m = updateKey(m, "pgdown")
	if m.cursor != 10 {
		t.Errorf("pgdown from 0: cursor = %d, want 10", m.cursor)
	}

	// pgdown again
	m = updateKey(m, "pgdown")
	if m.cursor != 20 {
		t.Errorf("pgdown from 10: cursor = %d, want 20", m.cursor)
	}

	// pgdown should clamp at end
	m = updateKey(m, "pgdown")
	if m.cursor != 24 {
		t.Errorf("pgdown at end: cursor = %d, want 24 (clamped)", m.cursor)
	}
}

func TestModel_PageUp(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.watchList = make([]DiscoveryEntry, 25)
	for i := range m.watchList {
		m.watchList[i] = DiscoveryEntry{Kind: EntryChannel, Login: fmt.Sprintf("ch%d", i)}
	}
	m.cursor = 20

	m = updateKey(m, "pgup")
	if m.cursor != 10 {
		t.Errorf("pgup from 20: cursor = %d, want 10", m.cursor)
	}

	// pgup should clamp at 0
	m.cursor = 5
	m = updateKey(m, "pgup")
	if m.cursor != 0 {
		t.Errorf("pgup from 5: cursor = %d, want 0 (clamped)", m.cursor)
	}
}

// --- currentEntry() ---

func TestModel_CurrentEntry_WatchList(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeWatchList
	m.watchList = []DiscoveryEntry{
		{Kind: EntryChannel, Login: "alpha"},
		{Kind: EntryChannel, Login: "beta"},
	}
	m.cursor = 1

	e := m.currentEntry()
	if e == nil {
		t.Fatal("currentEntry() returned nil")
	}
	if e.Login != "beta" {
		t.Errorf("currentEntry().Login = %q, want %q", e.Login, "beta")
	}
}

func TestModel_CurrentEntry_BrowseCategory(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeBrowse
	m.browseList = []DiscoveryEntry{
		{Kind: EntryCategory, CategoryName: "Just Chatting"},
	}
	m.cursor = 0

	e := m.currentEntry()
	if e == nil {
		t.Fatal("currentEntry() in browse mode returned nil")
	}
	if e.CategoryName != "Just Chatting" {
		t.Errorf("currentEntry().CategoryName = %q, want %q", e.CategoryName, "Just Chatting")
	}
}

func TestModel_CurrentEntry_BrowseCategoryStreams(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeBrowse
	m.categoryStack = []string{"Just Chatting"}
	m.categoryList = []DiscoveryEntry{
		{Kind: EntryChannel, Login: "streamer_in_cat"},
	}
	m.cursor = 0

	e := m.currentEntry()
	if e == nil {
		t.Fatal("currentEntry() in category streams returned nil")
	}
	if e.Login != "streamer_in_cat" {
		t.Errorf("currentEntry().Login = %q, want %q", e.Login, "streamer_in_cat")
	}
}

func TestModel_CurrentEntry_Search(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeSearch
	m.searchList = []DiscoveryEntry{
		{Kind: EntryChannel, Login: "found1"},
	}
	m.cursor = 0

	e := m.currentEntry()
	if e == nil {
		t.Fatal("currentEntry() in search mode returned nil")
	}
	if e.Login != "found1" {
		t.Errorf("currentEntry().Login = %q, want %q", e.Login, "found1")
	}
}

func TestModel_CurrentEntry_Ignored_ReturnsNil(t *testing.T) {
	state := &mockState{ignored: []string{"someone"}}
	m := newTestModel(state)
	m.mode = viewModeIgnored
	m.cursor = 0

	e := m.currentEntry()
	if e != nil {
		t.Error("currentEntry() in ignored mode should return nil")
	}
}

func TestModel_CurrentEntry_EmptyList_ReturnsNil(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeWatchList
	m.watchList = nil
	m.cursor = 0

	e := m.currentEntry()
	if e != nil {
		t.Error("currentEntry() on empty list should return nil")
	}
}

// --- removeCurrentEntry() ---

func TestModel_RemoveCurrentEntry_WatchList(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeWatchList
	m.watchList = []DiscoveryEntry{
		{Kind: EntryChannel, Login: "a"},
		{Kind: EntryChannel, Login: "b"},
		{Kind: EntryChannel, Login: "c"},
	}
	m.cursor = 1

	m.removeCurrentEntry()

	if len(m.watchList) != 2 {
		t.Fatalf("watchList len = %d, want 2", len(m.watchList))
	}
	if m.watchList[0].Login != "a" || m.watchList[1].Login != "c" {
		t.Errorf("watchList = [%s, %s], want [a, c]", m.watchList[0].Login, m.watchList[1].Login)
	}
	if m.cursor != 1 {
		t.Errorf("cursor = %d, want 1 (stays at same position)", m.cursor)
	}
}

func TestModel_RemoveCurrentEntry_LastItem_CursorBacksUp(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeWatchList
	m.watchList = []DiscoveryEntry{
		{Kind: EntryChannel, Login: "a"},
		{Kind: EntryChannel, Login: "b"},
	}
	m.cursor = 1

	m.removeCurrentEntry()

	if len(m.watchList) != 1 {
		t.Fatalf("watchList len = %d, want 1", len(m.watchList))
	}
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (backed up from removed last item)", m.cursor)
	}
}

func TestModel_RemoveCurrentEntry_SearchList(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeSearch
	m.searchList = []DiscoveryEntry{
		{Kind: EntryChannel, Login: "s1"},
		{Kind: EntryChannel, Login: "s2"},
	}
	m.cursor = 0

	m.removeCurrentEntry()

	if len(m.searchList) != 1 {
		t.Fatalf("searchList len = %d, want 1", len(m.searchList))
	}
	if m.searchList[0].Login != "s2" {
		t.Errorf("searchList[0].Login = %q, want %q", m.searchList[0].Login, "s2")
	}
}

func TestModel_RemoveCurrentEntry_CategoryList(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeBrowse
	m.categoryStack = []string{"Gaming"}
	m.categoryList = []DiscoveryEntry{
		{Kind: EntryChannel, Login: "c1"},
		{Kind: EntryChannel, Login: "c2"},
		{Kind: EntryChannel, Login: "c3"},
	}
	m.cursor = 2

	m.removeCurrentEntry()

	if len(m.categoryList) != 2 {
		t.Fatalf("categoryList len = %d, want 2", len(m.categoryList))
	}
	if m.cursor != 1 {
		t.Errorf("cursor = %d, want 1 (backed up after removing last)", m.cursor)
	}
}

// --- handleIgnore() ---

func TestModel_HandleIgnore_AddsToIgnoreListAndRemoves(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeSearch
	m.searchList = []DiscoveryEntry{
		{Kind: EntryChannel, Login: "target"},
		{Kind: EntryChannel, Login: "keep"},
	}
	m.cursor = 0

	m = updateKey(m, "x")

	if state.lastIgnoreChannel != "target" {
		t.Errorf("ToggleIgnore called with %q, want %q", state.lastIgnoreChannel, "target")
	}
	if !state.lastIgnoreAdd {
		t.Error("ToggleIgnore should be called with add=true")
	}
	if len(m.searchList) != 1 {
		t.Errorf("searchList len = %d, want 1 (removed ignored entry)", len(m.searchList))
	}
}

func TestModel_HandleIgnore_NoOpOnCategory(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeBrowse
	m.browseList = []DiscoveryEntry{
		{Kind: EntryCategory, CategoryName: "Gaming"},
	}
	m.cursor = 0

	m = updateKey(m, "x")

	// Should be a no-op because current entry is a category, not a channel.
	if state.lastIgnoreChannel != "" {
		t.Errorf("ToggleIgnore should not be called on a category entry, got channel=%q", state.lastIgnoreChannel)
	}
}

// --- handleIgnoredUnignore() ---

func TestModel_HandleIgnoredUnignore_RemovesFromIgnoreList(t *testing.T) {
	state := &mockState{ignored: []string{"ch1", "ch2", "ch3"}}
	m := newTestModel(state)
	m.mode = viewModeIgnored
	m.cursor = 1

	m = updateKey(m, "x") // x in ignored view calls handleIgnoredUnignore

	if state.lastIgnoreChannel != "ch2" {
		t.Errorf("ToggleIgnore called with %q, want %q", state.lastIgnoreChannel, "ch2")
	}
	if state.lastIgnoreAdd {
		t.Error("ToggleIgnore should be called with add=false")
	}
	if len(state.ignored) != 2 {
		t.Errorf("ignored len = %d, want 2", len(state.ignored))
	}
}

func TestModel_HandleIgnoredUnignore_CursorClamps(t *testing.T) {
	state := &mockState{ignored: []string{"only"}}
	m := newTestModel(state)
	m.mode = viewModeIgnored
	m.cursor = 0

	m = updateKey(m, "x")

	// After removing the only item, cursor should be 0.
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0 after removing last ignored entry", m.cursor)
	}
}

func TestModel_HandleIgnoredUnignore_ViaEnter(t *testing.T) {
	state := &mockState{ignored: []string{"ch1"}}
	m := newTestModel(state)
	m.mode = viewModeIgnored
	m.cursor = 0

	m = updateKey(m, "enter")

	if state.lastIgnoreChannel != "ch1" {
		t.Errorf("Enter in Ignored: ToggleIgnore called with %q, want %q", state.lastIgnoreChannel, "ch1")
	}
	if state.lastIgnoreAdd {
		t.Error("Enter in Ignored: ToggleIgnore should be called with add=false")
	}
}

// --- handleRelated() ---

func TestModel_HandleRelated_OpensOverlay(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeWatchList
	m.watchList = []DiscoveryEntry{
		{Kind: EntryChannel, Login: "host_target", IsLive: true},
	}
	m.cursor = 0

	newM, cmd := m.Update(pressKey("r"))
	m2 := newM.(Model)

	if m2.overlay != overlayRelated {
		t.Errorf("overlay = %v, want overlayRelated", m2.overlay)
	}
	if m2.overlayChannel != "host_target" {
		t.Errorf("overlayChannel = %q, want %q", m2.overlayChannel, "host_target")
	}
	if !m2.relatedLoading {
		t.Error("relatedLoading should be true")
	}
	if cmd == nil {
		t.Error("handleRelated should return a non-nil cmd")
	}
}

func TestModel_HandleRelated_NoOpOnCategory(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeBrowse
	m.browseList = []DiscoveryEntry{
		{Kind: EntryCategory, CategoryName: "Gaming"},
	}
	m.cursor = 0

	m = updateKey(m, "r")

	if m.overlay != overlayNone {
		t.Errorf("overlay = %v, want overlayNone (no-op on category)", m.overlay)
	}
}

func TestModel_HandleRelated_NoOpOnEmptyList(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeWatchList
	m.watchList = nil
	m.cursor = 0

	m = updateKey(m, "r")

	if m.overlay != overlayNone {
		t.Errorf("overlay = %v, want overlayNone (no entry)", m.overlay)
	}
}

// --- Search mode ---

func TestModel_SearchMode_TypeAndBackspace(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)

	// Enter search mode
	m = updateKey(m, "/")
	if !m.searching {
		t.Fatal("searching should be true after /")
	}

	// Type "abc"
	m = updateKey(m, "a")
	m = updateKey(m, "b")
	m = updateKey(m, "c")
	if m.searchInput != "abc" {
		t.Errorf("searchInput = %q, want %q", m.searchInput, "abc")
	}

	// Backspace removes one character
	m = updateKey(m, "backspace")
	if m.searchInput != "ab" {
		t.Errorf("searchInput after backspace = %q, want %q", m.searchInput, "ab")
	}
}

func TestModel_SearchMode_EscCancel(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)

	m = updateKey(m, "/")
	m = updateKey(m, "h")
	m = updateKey(m, "i")

	m = updateKey(m, "esc")
	if m.searching {
		t.Error("esc should set searching to false")
	}
	// Input is preserved after esc (only cleared if empty)
	if m.searchInput != "hi" {
		t.Errorf("searchInput = %q, want %q (preserved after esc)", m.searchInput, "hi")
	}
}

func TestModel_SearchMode_EscOnEmptyInput_ClearsList(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeSearch
	m.searching = true
	m.searchInput = ""
	m.searchList = []DiscoveryEntry{{Kind: EntryChannel, Login: "old"}}

	m = updateKey(m, "esc")
	if m.searching {
		t.Error("esc should set searching to false")
	}
	if m.searchList != nil {
		t.Errorf("searchList should be nil after esc with empty input, got len %d", len(m.searchList))
	}
}

func TestModel_SearchMode_EnterConfirms(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)

	m = updateKey(m, "/")
	m = updateKey(m, "t")
	m = updateKey(m, "e")
	m = updateKey(m, "s")
	m = updateKey(m, "t")
	m = updateKey(m, "enter")

	if m.searching {
		t.Error("enter should set searching to false")
	}
	if m.searchQuery != "test" {
		t.Errorf("searchQuery = %q, want %q", m.searchQuery, "test")
	}
}

// --- Browse mode ---

func TestModel_BrowseMode_EnterOnCategory(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeBrowse
	m.browseList = []DiscoveryEntry{
		{Kind: EntryCategory, CategoryName: "Just Chatting"},
		{Kind: EntryCategory, CategoryName: "Fortnite"},
	}
	m.cursor = 0

	newM, cmd := m.Update(pressKey("enter"))
	m2 := newM.(Model)

	if len(m2.categoryStack) != 1 {
		t.Fatalf("categoryStack len = %d, want 1", len(m2.categoryStack))
	}
	if m2.categoryStack[0] != "Just Chatting" {
		t.Errorf("categoryStack[0] = %q, want %q", m2.categoryStack[0], "Just Chatting")
	}
	if m2.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (reset after entering category)", m2.cursor)
	}
	if !m2.loading {
		t.Error("loading should be true after entering category")
	}
	if cmd == nil {
		t.Error("entering a category should return a non-nil cmd")
	}
}

func TestModel_BrowseMode_EscBackFromCategory(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeBrowse
	m.categoryStack = []string{"Just Chatting"}
	m.categoryList = []DiscoveryEntry{
		{Kind: EntryChannel, Login: "someone"},
	}
	m.cursor = 0

	newM, cmd := m.Update(pressKey("esc"))
	m2 := newM.(Model)

	if len(m2.categoryStack) != 0 {
		t.Errorf("categoryStack len = %d, want 0 after esc", len(m2.categoryStack))
	}
	if m2.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (reset)", m2.cursor)
	}
	if cmd == nil {
		t.Error("esc from category should trigger loadBrowse cmd")
	}
}

// --- renderFooter() ---

func TestModel_RenderFooter_DefaultShowsLiveOffline(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeWatchList
	m.watchList = []DiscoveryEntry{
		{Kind: EntryChannel, Login: "a", IsLive: true},
		{Kind: EntryChannel, Login: "b", IsLive: true},
		{Kind: EntryChannel, Login: "c", IsLive: false},
	}

	footer := m.renderFooter()
	plain := stripANSI(footer)

	if !strings.Contains(plain, "2 live") {
		t.Errorf("footer should contain '2 live', got %q", plain)
	}
	if !strings.Contains(plain, "1 offline") {
		t.Errorf("footer should contain '1 offline', got %q", plain)
	}
}

func TestModel_RenderFooter_SearchingShowsHints(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeSearch
	m.searching = true

	footer := m.renderFooter()
	plain := stripANSI(footer)

	if !strings.Contains(plain, "Enter") {
		t.Errorf("footer in search mode should contain 'Enter', got %q", plain)
	}
	if !strings.Contains(plain, "Esc") {
		t.Errorf("footer in search mode should contain 'Esc', got %q", plain)
	}
}

func TestModel_RenderFooter_BrowseCategoryShowsBreadcrumb(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeBrowse
	m.categoryStack = []string{"Just Chatting"}

	footer := m.renderFooter()
	plain := stripANSI(footer)

	if !strings.Contains(plain, "Esc") {
		t.Errorf("footer in browse category should contain 'Esc', got %q", plain)
	}
	if !strings.Contains(plain, "Just Chatting") {
		t.Errorf("footer should show category breadcrumb, got %q", plain)
	}
}

func TestModel_RenderFooter_NoticeOverrides(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.notice = "Something happened"

	footer := m.renderFooter()
	plain := stripANSI(footer)

	if !strings.Contains(plain, "Something happened") {
		t.Errorf("footer should show notice, got %q", plain)
	}
}

// --- renderTabBar() ---

func TestModel_RenderTabBar_ShowsAllTabs(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)

	bar := m.renderTabBar()
	plain := stripANSI(bar)

	for _, tab := range []string{"Watch List", "Browse", "Search", "Ignored"} {
		if !strings.Contains(plain, tab) {
			t.Errorf("tab bar should contain %q, got %q", tab, plain)
		}
	}
}

func TestModel_RenderTabBar_HighlightsActiveTab(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeSearch

	bar := m.renderTabBar()
	plain := stripANSI(bar)

	// Active tab is bracketed: "[ Search ]"
	if !strings.Contains(plain, "[ Search ]") {
		t.Errorf("active tab should be bracketed, got %q", plain)
	}
}

func TestModel_RenderTabBar_ShowsRefreshCountdown(t *testing.T) {
	m := NewModel(mockFns(&mockState{}), DefaultTheme(), 60*time.Second)
	m.width, m.height = 120, 30
	m.refreshCountdown = 45 * time.Second

	bar := m.renderTabBar()
	plain := stripANSI(bar)

	if !strings.Contains(plain, "45s") {
		t.Errorf("tab bar should show countdown '45s', got %q", plain)
	}
}

func TestModel_RenderTabBar_ShowsRefreshing(t *testing.T) {
	m := NewModel(mockFns(&mockState{}), DefaultTheme(), 60*time.Second)
	m.width, m.height = 120, 30
	m.refreshing = true

	bar := m.renderTabBar()
	plain := stripANSI(bar)

	if !strings.Contains(plain, "refreshing") {
		t.Errorf("tab bar should show 'refreshing' indicator, got %q", plain)
	}
}

// --- formatViewers() ---

func TestFormatViewers(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0k"},
		{1500, "1.5k"},
		{99999, "100.0k"},
	}
	for _, tt := range tests {
		got := formatViewers(tt.n)
		if got != tt.want {
			t.Errorf("formatViewers(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// --- formatUptime() ---

func TestFormatUptime(t *testing.T) {
	tests := []struct {
		dur  time.Duration
		want string
	}{
		{0, "0h 00m"},
		{30 * time.Minute, "0h 30m"},
		{90 * time.Minute, "1h 30m"},
		{12*time.Hour + 5*time.Minute, "12h 05m"},
	}
	for _, tt := range tests {
		got := formatUptime(tt.dur)
		if got != tt.want {
			t.Errorf("formatUptime(%v) = %q, want %q", tt.dur, got, tt.want)
		}
	}
}

// --- sanitizeText() ---

func TestSanitizeText(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"hello", "hello"},
		{"line1\nline2", "line1 line2"},
		{"tab\there", "tab here"},
		{"null\x00char", "null char"},
		{"\x7Fdel", " del"},
		{"normal text", "normal text"},
	}
	for _, tt := range tests {
		got := sanitizeText(tt.in)
		if got != tt.want {
			t.Errorf("sanitizeText(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// --- cellTruncate() ---

func TestCellTruncate(t *testing.T) {
	tests := []struct {
		s     string
		width int
		want  string
	}{
		// Fits within width
		{"hello", 10, "hello"},
		// Exact width
		{"hello", 5, "hello"},
		// Truncated with ellipsis
		{"hello world", 6, "hello\u2026"},
		// Zero width
		{"hello", 0, ""},
		// Single width (only ellipsis fits)
		{"hello", 1, "\u2026"},
	}
	for _, tt := range tests {
		got := cellTruncate(tt.s, tt.width)
		if got != tt.want {
			t.Errorf("cellTruncate(%q, %d) = %q, want %q", tt.s, tt.width, got, tt.want)
		}
	}
}

// --- calcVisibleStart() ---

func TestCalcVisibleStart(t *testing.T) {
	tests := []struct {
		cursor, height int
		want           int
	}{
		{0, 20, 0},         // cursor at top, no scroll needed
		{5, 20, 0},         // cursor near top, still no scroll
		{17, 20, 0},        // cursor at height-3, still visible
		{18, 20, 1},        // cursor at height-2, starts scrolling
		{25, 20, 8},        // cursor well past visible area
		{0, 5, 0},          // small height, cursor at top
		{10, 5, 8},         // small height, cursor past visible area
	}
	for _, tt := range tests {
		got := calcVisibleStart(tt.cursor, tt.height)
		if got != tt.want {
			t.Errorf("calcVisibleStart(%d, %d) = %d, want %d", tt.cursor, tt.height, got, tt.want)
		}
	}
}

// --- countLiveOffline() ---

func TestModel_CountLiveOffline_WatchList(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeWatchList
	m.watchList = []DiscoveryEntry{
		{Kind: EntryChannel, Login: "a", IsLive: true},
		{Kind: EntryChannel, Login: "b", IsLive: true},
		{Kind: EntryChannel, Login: "c", IsLive: false},
		{Kind: EntryChannel, Login: "d", IsLive: false},
		{Kind: EntryChannel, Login: "e", IsLive: false},
	}

	live, offline := m.countLiveOffline()
	if live != 2 {
		t.Errorf("live = %d, want 2", live)
	}
	if offline != 3 {
		t.Errorf("offline = %d, want 3", offline)
	}
}

func TestModel_CountLiveOffline_Search(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeSearch
	m.searchList = []DiscoveryEntry{
		{Kind: EntryChannel, Login: "a", IsLive: true},
	}

	live, offline := m.countLiveOffline()
	if live != 1 {
		t.Errorf("live = %d, want 1", live)
	}
	if offline != 0 {
		t.Errorf("offline = %d, want 0", offline)
	}
}

func TestModel_CountLiveOffline_Browse_CategoryStreams(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeBrowse
	m.categoryStack = []string{"Gaming"}
	m.categoryList = []DiscoveryEntry{
		{Kind: EntryChannel, Login: "a", IsLive: true},
		{Kind: EntryChannel, Login: "b", IsLive: true},
		{Kind: EntryLoadMore},
	}

	live, offline := m.countLiveOffline()
	if live != 2 {
		t.Errorf("live = %d, want 2", live)
	}
	if offline != 0 {
		t.Errorf("offline = %d, want 0", offline)
	}
}

func TestModel_CountLiveOffline_Ignored_ReturnsZero(t *testing.T) {
	state := &mockState{ignored: []string{"a", "b"}}
	m := newTestModel(state)
	m.mode = viewModeIgnored

	live, offline := m.countLiveOffline()
	if live != 0 {
		t.Errorf("live = %d, want 0 (ignored view doesn't count)", live)
	}
	if offline != 0 {
		t.Errorf("offline = %d, want 0 (ignored view doesn't count)", offline)
	}
}

func TestModel_CountLiveOffline_SkipsNonChannelEntries(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeWatchList
	m.watchList = []DiscoveryEntry{
		{Kind: EntryChannel, Login: "a", IsLive: true},
		{Kind: EntryLoadMore},
		{Kind: EntryCategory, CategoryName: "Gaming"},
	}

	live, offline := m.countLiveOffline()
	if live != 1 {
		t.Errorf("live = %d, want 1", live)
	}
	if offline != 0 {
		t.Errorf("offline = %d, want 0", offline)
	}
}

// --- Window resize ---

func TestModel_WindowResize(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)

	newM, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	m2 := newM.(Model)

	if m2.width != 200 || m2.height != 50 {
		t.Errorf("size = %dx%d, want 200x50", m2.width, m2.height)
	}
}

// --- Home/End keys ---

func TestModel_HomeKey(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.watchList = make([]DiscoveryEntry, 10)
	for i := range m.watchList {
		m.watchList[i] = DiscoveryEntry{Kind: EntryChannel, Login: fmt.Sprintf("ch%d", i)}
	}
	m.cursor = 7

	m = updateKey(m, "home")
	if m.cursor != 0 {
		t.Errorf("home: cursor = %d, want 0", m.cursor)
	}
}

func TestModel_EndKey(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.watchList = make([]DiscoveryEntry, 10)
	for i := range m.watchList {
		m.watchList[i] = DiscoveryEntry{Kind: EntryChannel, Login: fmt.Sprintf("ch%d", i)}
	}
	m.cursor = 0

	m = updateKey(m, "end")
	if m.cursor != 9 {
		t.Errorf("end: cursor = %d, want 9", m.cursor)
	}
}

// --- relatedResultMsg ---

func TestModel_RelatedResult_StoresHosts(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.overlay = overlayRelated
	m.overlayChannel = "target"
	m.relatedLoading = true

	hosts := []DiscoveryEntry{
		{Kind: EntryChannel, Login: "host1"},
		{Kind: EntryChannel, Login: "host2"},
	}
	newM, _ := m.Update(relatedResultMsg{channel: "target", hosts: hosts})
	m2 := newM.(Model)

	if m2.relatedLoading {
		t.Error("relatedLoading should be false after result")
	}
	if len(m2.relatedHosts) != 2 {
		t.Errorf("relatedHosts len = %d, want 2", len(m2.relatedHosts))
	}
}

// --- MoveCursor with empty list ---

func TestModel_MoveCursor_EmptyList(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.watchList = nil
	m.cursor = 0

	m = updateKey(m, "j")
	if m.cursor != 0 {
		t.Errorf("cursor on empty list = %d, want 0", m.cursor)
	}
}

// --- Search from non-search mode ---

func TestModel_SlashFromBrowse_SwitchesToSearch(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeBrowse

	m = updateKey(m, "/")

	if m.mode != viewModeSearch {
		t.Errorf("mode = %v, want viewModeSearch", m.mode)
	}
	if !m.searching {
		t.Error("searching should be true")
	}
}

// --- Search debounce message ---

func TestModel_SearchDebounce_MatchingQuery(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeSearch
	m.searchInput = "test"

	newM, cmd := m.Update(searchDebounceMsg{query: "test"})
	m2 := newM.(Model)

	if m2.searchQuery != "test" {
		t.Errorf("searchQuery = %q, want %q", m2.searchQuery, "test")
	}
	if cmd == nil {
		t.Error("matching debounce should trigger search cmd")
	}
}

func TestModel_SearchDebounce_StaleQuery(t *testing.T) {
	state := &mockState{}
	m := newTestModel(state)
	m.mode = viewModeSearch
	m.searchInput = "current"

	newM, cmd := m.Update(searchDebounceMsg{query: "old"})
	m2 := newM.(Model)

	if m2.searchQuery == "old" {
		t.Error("stale debounce should not update searchQuery")
	}
	if cmd != nil {
		t.Error("stale debounce should return nil cmd")
	}
}
