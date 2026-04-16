package ui

import (
	"context"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// handleSearchInput consumes a key press while the user is editing the
// search input. Esc cancels, Enter submits immediately, Backspace/Ctrl+H
// delete a rune, and any other printable key appends and restarts the
// debounce timer so the query fires automatically after the user pauses.
func (m Model) handleSearchInput(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.searching = false
		if m.searchInput == "" {
			m.searchList = nil
		}
		return m, nil
	case "enter":
		m.searching = false
		m.searchQuery = m.searchInput
		return m, m.runSearch(m.searchInput)
	case "backspace", "ctrl+h":
		if len(m.searchInput) > 0 {
			_, size := utf8.DecodeLastRuneInString(m.searchInput)
			m.searchInput = m.searchInput[:len(m.searchInput)-size]
		}
		return m, m.debounceSearch(m.searchInput)
	default:
		if msg.Text != "" {
			m.searchInput += msg.Text
			return m, m.debounceSearch(m.searchInput)
		}
	}
	return m, nil
}

// runSearch fires the Search API call immediately and wraps the result in a
// searchResultMsg. Empty queries produce no command.
func (m Model) runSearch(query string) tea.Cmd {
	if query == "" {
		return nil
	}
	ctx := m.ctx
	fns := m.fns
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, timeoutSearch)
		defer cancel()
		entries, err := fns.Search(c, query)
		return searchResultMsg{query: query, entries: entries, err: err}
	}
}

// debounceSearch schedules a searchDebounceMsg for `debounceSearch` from now;
// the Update handler compares the message's query against the current input
// to avoid firing stale searches.
func (m Model) debounceSearch(query string) tea.Cmd {
	return tea.Tick(debounceSearch, func(time.Time) tea.Msg {
		return searchDebounceMsg{query: query}
	})
}
