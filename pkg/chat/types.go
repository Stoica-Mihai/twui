// Package chat parses Twitch IRC messages and maintains read-only chat
// connections for the twui TUI. Connections are anonymous (justinfan<rand>)
// and per-channel; lifecycle is owned by the caller (normally bound to a
// playback session).
package chat

import "time"

// Message is a raw parsed IRC line in IRCv3 form.
//
//	[@tags SP] [:source SP] command [params] [:trailing]
//
// Tag values are already escape-decoded.
type Message struct {
	Raw     string            // original wire line (debug / logging)
	Tags    map[string]string // IRCv3 tags with values decoded
	Source  string            // prefix, the chunk between ':' and the first SPACE
	Command string            // PRIVMSG, PING, NOTICE, numeric, ...
	Params  []string          // middle params plus the trailing param at the tail
}

// Chat is a PRIVMSG specialised with Twitch tag fields extracted. It is
// derived from a Message via AsChat — the raw Message is kept so callers can
// also inspect unknown tags if needed.
type Chat struct {
	Channel     string    // channel name without the leading '#'
	Login       string    // Twitch login (from the source nick)
	DisplayName string    // display-name tag, or Login if that tag is empty
	Color       string    // color tag, hex "#RRGGBB"; empty when user never set one
	Badges      []Badge   // parsed badges tag
	Emotes      []Emote   // parsed emotes tag
	Text        string    // message text (the trailing parameter)
	Sent        time.Time // tmi-sent-ts parsed as milliseconds since epoch; zero if absent
	ID          string    // id tag (message UUID), empty if absent
}

// Badge is a Twitch chat badge such as broadcaster/1 or subscriber/12.
// Version for most badges is "1"; subscriber uses the subscription tenure
// in months, and a few others (bits, gift-subs) encode their numeric value.
type Badge struct {
	Name    string // broadcaster, moderator, vip, subscriber, turbo, partner, ...
	Version string // typically "1"; subscriber uses months
}

// Emote is a Twitch chat emote occurrence. Ranges are character (not byte)
// indices into Chat.Text and are inclusive per the Twitch tag spec.
type Emote struct {
	ID     string  // Twitch emote id
	Ranges []Range // every occurrence of this emote in the message
	Name   string  // derived from Text at the first Range (user-typed emote name)
}

// Range is an inclusive character range [Start,End] inside the message text.
type Range struct {
	Start int
	End   int
}
