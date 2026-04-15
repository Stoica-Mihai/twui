package ui

import (
	"context"
	"testing"

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

// newTestModel creates a Model with mock callbacks.
func newTestModel(state *mockState) Model {
	m := NewModel(mockFns(state), DefaultTheme())
	m.width = 120
	m.height = 30
	return *m
}

// pressKey constructs a tea.KeyPressMsg for the given key string.
// Supports printable characters, "tab", "shift+tab", "enter", "esc".
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
