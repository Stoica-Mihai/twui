package ui

// Symbols holds the display glyphs for status indicators and list markers.
// Users on terminals without Unicode glyph support can switch to the ASCII set
// via --ascii or TWUI_ASCII; TERM=linux is auto-detected in main.
type Symbols struct {
	Playing      string // active playback (default: ▶)
	AdBreak      string // ad break active (default: "AD" — 2-char label, not a glyph)
	Waiting      string // pre-roll/waiting (default: ⧖)
	Reconnecting string // transient network issue (default: ⟳)
	Favorite     string // favorite marker (default: ★)
	LoadMore     string // "load more" affordance arrow (default: ↓)
	ChatActive   string // chat pane autoscrolling (default: ▸)
	ChatPaused   string // chat pane scrolled back (default: ⏸)
}

// symbolDefs is the single source of truth: one row per glyph, unicode form
// alongside its ASCII fallback. Adding a symbol is a row here plus a field
// on Symbols — no separate per-mode constructor to keep in sync.
var symbolDefs = []struct {
	set            func(*Symbols, string)
	unicode, ascii string
}{
	{func(s *Symbols, v string) { s.Playing = v }, "▶", ">"},
	{func(s *Symbols, v string) { s.AdBreak = v }, "AD", "AD"},
	{func(s *Symbols, v string) { s.Waiting = v }, "⧖", "o"},
	{func(s *Symbols, v string) { s.Reconnecting = v }, "⟳", "~"},
	{func(s *Symbols, v string) { s.Favorite = v }, "★", "*"},
	{func(s *Symbols, v string) { s.LoadMore = v }, "↓", "v"},
	{func(s *Symbols, v string) { s.ChatActive = v }, "▸", ">"},
	{func(s *Symbols, v string) { s.ChatPaused = v }, "⏸", "||"},
}

// NewSymbols builds the glyph set for the given mode. ascii=true picks the
// 7-bit-safe fallback column; ascii=false picks the unicode column.
func NewSymbols(ascii bool) Symbols {
	var s Symbols
	for _, d := range symbolDefs {
		v := d.unicode
		if ascii {
			v = d.ascii
		}
		d.set(&s, v)
	}
	return s
}

// UnicodeSymbols returns the default glyph set.
func UnicodeSymbols() Symbols { return NewSymbols(false) }

// ASCIISymbols returns the 7-bit safe glyph set for terminals without Unicode.
func ASCIISymbols() Symbols { return NewSymbols(true) }
