package ui

// Symbols holds the display glyphs for status indicators and list markers.
// Users on terminals without Unicode glyph support can switch to the ASCII set
// via --ascii or TWUI_ASCII; TERM=linux is auto-detected in main.
type Symbols struct {
	Playing      string // active playback (default: ▶)
	AdBreak      string // ad break active (default: ◐)
	Waiting      string // pre-roll/waiting (default: ○)
	Reconnecting string // transient network issue (default: ⟳)
	Favorite     string // favorite marker (default: ★)
	LoadMore     string // "load more" affordance arrow (default: ↓)
	ChatActive   string // chat pane autoscrolling (default: ▸)
	ChatPaused   string // chat pane scrolled back (default: ⏸)
}

// UnicodeSymbols returns the default glyph set.
func UnicodeSymbols() Symbols {
	return Symbols{
		Playing:      "▶",
		AdBreak:      "◐",
		Waiting:      "○",
		Reconnecting: "⟳",
		Favorite:     "★",
		LoadMore:     "↓",
		ChatActive:   "▸",
		ChatPaused:   "⏸",
	}
}

// ASCIISymbols returns the 7-bit safe glyph set for terminals without Unicode.
func ASCIISymbols() Symbols {
	return Symbols{
		Playing:      ">",
		AdBreak:      "*",
		Waiting:      "o",
		Reconnecting: "~",
		Favorite:     "*",
		LoadMore:     "v",
		ChatActive:   ">",
		ChatPaused:   "||",
	}
}
