package ui

import tea "charm.land/bubbletea/v2"

// Binding pairs one or more key strings with an action and its help text.
// A single slice of Bindings drives both key dispatch (handleKey) and the
// help overlay (renderHelpOverlay), so the two cannot drift out of sync.
type Binding struct {
	// Keys are the tea.KeyPressMsg.String() values that trigger Handler.
	Keys []string
	// Display is the label shown in the help overlay. Empty falls back to
	// a slash-joined Keys list (e.g. "j / k").
	Display string
	// Desc is the human-readable help text for the overlay.
	Desc string
	// Handler runs when one of Keys matches. Returns the updated Model and
	// an optional tea.Cmd. Signature matches handleKey's top-level contract.
	Handler func(m Model) (Model, tea.Cmd)
}

// bindings returns the full top-level keymap. "Top-level" means keys that
// apply when no overlay is open and the user isn't in search-input mode —
// overlays define their own local keymaps in their respective handlers.
//
// Order matters only for the help overlay (which renders in this order);
// dispatch is a flat lookup, so no ordering-sensitive collisions.
func (m Model) bindings() []Binding {
	return []Binding{
		{
			Keys: []string{"tab"}, Display: "Tab / Shift+Tab", Desc: "Switch view",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.mode = (m.mode + 1) % 4
				m.cursor = 0
				m.titleScrollOffset = 0
				m.titleScrollDir = 1
				return m, m.loadCurrentView()
			},
		},
		{
			Keys: []string{"shift+tab"}, Display: "", Desc: "",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.mode = (m.mode + 3) % 4
				m.cursor = 0
				m.titleScrollOffset = 0
				m.titleScrollDir = 1
				return m, m.loadCurrentView()
			},
		},
		{
			Keys: []string{"j", "down"}, Display: "j / k / ↑ / ↓", Desc: "Navigate",
			Handler: func(m Model) (Model, tea.Cmd) { return m.moveCursor(1), nil },
		},
		{
			Keys: []string{"k", "up"}, Display: "", Desc: "",
			Handler: func(m Model) (Model, tea.Cmd) { return m.moveCursor(-1), nil },
		},
		{
			Keys: []string{"pgdown"}, Display: "PgUp / PgDn", Desc: "Page scroll",
			// pgup is its own entry below — same display label, opposite delta.
			Handler: func(m Model) (Model, tea.Cmd) { return m.moveCursor(10), nil },
		},
		{
			Keys: []string{"pgup"}, Display: "", Desc: "",
			Handler: func(m Model) (Model, tea.Cmd) { return m.moveCursor(-10), nil },
		},
		{
			Keys: []string{"g"}, Display: "g / G", Desc: "Top / bottom",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.cursor = 0
				m.titleScrollOffset = 0
				m.titleScrollDir = 1
				return m, nil
			},
		},
		{
			Keys: []string{"G"}, Display: "", Desc: "",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.cursor = m.currentListLen() - 1
				m.titleScrollOffset = 0
				m.titleScrollDir = 1
				return m, nil
			},
		},
		{
			Keys: []string{"home"}, Display: "", Desc: "",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.cursor = 0
				m.titleScrollOffset = 0
				m.titleScrollDir = 1
				return m, nil
			},
		},
		{
			Keys: []string{"end"}, Display: "", Desc: "",
			Handler: func(m Model) (Model, tea.Cmd) {
				n := m.currentListLen()
				if n > 0 {
					m.cursor = n - 1
				}
				m.titleScrollOffset = 0
				m.titleScrollDir = 1
				return m, nil
			},
		},
		{
			Keys: []string{"enter"}, Display: "Enter", Desc: "Open / launch stream",
			Handler: func(m Model) (Model, tea.Cmd) {
				newM, cmd := m.handleEnter()
				return newM.(Model), cmd
			},
		},
		{
			Keys: []string{"/"}, Display: "/", Desc: "Activate search",
			Handler: func(m Model) (Model, tea.Cmd) {
				if m.mode != viewModeSearch {
					m.mode = viewModeSearch
				}
				m.searching = true
				return m, nil
			},
		},
		{
			Keys: []string{"esc"}, Display: "Esc", Desc: "Back / cancel",
			Handler: func(m Model) (Model, tea.Cmd) {
				newM, cmd := m.handleEsc()
				return newM.(Model), cmd
			},
		},
		{
			Keys: []string{"f"}, Display: "f", Desc: "Toggle favorite",
			Handler: func(m Model) (Model, tea.Cmd) {
				newM, cmd := m.handleFavorite()
				return newM.(Model), cmd
			},
		},
		{
			Keys: []string{"x"}, Display: "x", Desc: "Toggle ignore",
			Handler: func(m Model) (Model, tea.Cmd) {
				newM, cmd := m.handleIgnore()
				return newM.(Model), cmd
			},
		},
		{
			Keys: []string{"i"}, Display: "i", Desc: "Quality picker",
			Handler: func(m Model) (Model, tea.Cmd) {
				newM, cmd := m.handleQualityPicker()
				return newM.(Model), cmd
			},
		},
		{
			Keys: []string{"t"}, Display: "t", Desc: "Theme picker",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.overlay = overlayTheme
				m.themeIdx = 0
				return m, nil
			},
		},
		{
			Keys: []string{"r"}, Display: "r", Desc: "Related/host channels",
			Handler: func(m Model) (Model, tea.Cmd) {
				newM, cmd := m.handleRelated()
				return newM.(Model), cmd
			},
		},
		{
			Keys: []string{"R"}, Display: "R", Desc: "Manual refresh",
			Handler: func(m Model) (Model, tea.Cmd) {
				if m.refreshInterval > 0 && !m.refreshing {
					m.refreshCountdown = m.refreshInterval
					m.refreshing = true
					if cmd := m.refreshCurrentView(); cmd != nil {
						return m, cmd
					}
				}
				return m, nil
			},
		},
		{
			Keys: []string{"?"}, Display: "?", Desc: "Toggle help",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.overlay = overlayHelp
				return m, nil
			},
		},
		{
			Keys: []string{"q", "Q", "ctrl+c"}, Display: "q / Ctrl+C", Desc: "Quit",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.cancel()
				return m, tea.Quit
			},
		},

		// --- Chat pane bindings ---
		// `c` is the frequent action (cycling between live chats); `C`
		// (with Shift) is the rare one (showing/hiding the pane entirely).

		{
			Keys: []string{"c"}, Display: "c", Desc: "Cycle chat session",
			Handler: func(m Model) (Model, tea.Cmd) {
				m = m.cycleChatFocus()
				return m, nil
			},
		},
		{
			Keys: []string{"C"}, Display: "C", Desc: "Toggle chat pane",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.chatVisible = !m.chatVisible
				if !m.chatVisible {
					return m, nil
				}
				// Lazy-connect: when AutoOpen is off, launchStream skips
				// startChat so nothing connects until the user opens the
				// pane. Reveal any active playback now.
				var cmds []tea.Cmd
				for channel := range m.sessions {
					newM, cmd, started := m.startChat(channel)
					m = newM
					if started {
						cmds = append(cmds, cmd)
					}
				}
				return m, tea.Batch(cmds...)
			},
		},
		{
			Keys: []string{"space", " "}, Display: "Space", Desc: "Resume chat scroll",
			Handler: func(m Model) (Model, tea.Cmd) {
				if s := m.currentChatSession(); s != nil {
					s.Resume()
				}
				return m, nil
			},
		},
		{
			Keys: []string{"["}, Display: "[ / ]", Desc: "Chat scroll back / forward (line)",
			Handler: func(m Model) (Model, tea.Cmd) {
				if s := m.currentChatSession(); s != nil {
					s.ScrollBack(1)
				}
				return m, nil
			},
		},
		{
			Keys: []string{"]"}, Display: "", Desc: "",
			Handler: func(m Model) (Model, tea.Cmd) {
				if s := m.currentChatSession(); s != nil {
					s.ScrollForward(1)
				}
				return m, nil
			},
		},
		{
			Keys: []string{"{"}, Display: "{ / }", Desc: "Chat scroll back / forward (page)",
			Handler: func(m Model) (Model, tea.Cmd) {
				if s := m.currentChatSession(); s != nil {
					s.ScrollBack(chatPaneHeight - 1)
				}
				return m, nil
			},
		},
		{
			Keys: []string{"}"}, Display: "", Desc: "",
			Handler: func(m Model) (Model, tea.Cmd) {
				if s := m.currentChatSession(); s != nil {
					s.ScrollForward(chatPaneHeight - 1)
				}
				return m, nil
			},
		},
	}
}

// dispatchBinding looks up a binding for key and runs its handler, returning
// (newModel, cmd, true) on a hit or (m, nil, false) on no match.
func (m Model) dispatchBinding(key string) (Model, tea.Cmd, bool) {
	for _, b := range m.bindings() {
		for _, k := range b.Keys {
			if k == key {
				newM, cmd := b.Handler(m)
				return newM, cmd, true
			}
		}
	}
	return m, nil, false
}
