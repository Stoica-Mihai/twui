package chat

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// ErrMalformed is returned by Parse for input that can't be interpreted as a
// valid IRC line (empty / blank / no command token).
var ErrMalformed = errors.New("chat: malformed IRC line")

// Parse reads a single IRCv3 line and returns its components. Trailing CRLF
// is tolerated. Tag values are escape-decoded. An unparseable line returns
// ErrMalformed.
func Parse(line string) (*Message, error) {
	raw := line
	// Strip any trailing CR/LF so callers can feed raw wire bytes.
	line = strings.TrimRight(line, "\r\n")
	line = strings.TrimLeft(line, " ")
	if line == "" {
		return nil, ErrMalformed
	}

	m := &Message{Raw: raw}

	// Tags: optional '@'-prefixed block ending at the first space.
	if strings.HasPrefix(line, "@") {
		end := strings.IndexByte(line, ' ')
		if end < 0 {
			// "@foo;bar" with nothing else — no command can follow.
			return nil, ErrMalformed
		}
		m.Tags = parseTags(line[1:end])
		line = strings.TrimLeft(line[end+1:], " ")
	}

	// Source: optional ':'-prefixed block ending at the next space.
	if strings.HasPrefix(line, ":") {
		end := strings.IndexByte(line, ' ')
		if end < 0 {
			return nil, ErrMalformed
		}
		m.Source = line[1:end]
		line = strings.TrimLeft(line[end+1:], " ")
	}

	// Command: required.
	if line == "" {
		return nil, ErrMalformed
	}
	if sp := strings.IndexByte(line, ' '); sp >= 0 {
		m.Command = line[:sp]
		line = strings.TrimLeft(line[sp+1:], " ")
	} else {
		m.Command = line
		return m, nil
	}

	// Params: middle parameters separated by spaces; one final trailing
	// parameter that begins with ':' and runs to end of line (may contain
	// spaces).
	for line != "" {
		if strings.HasPrefix(line, ":") {
			m.Params = append(m.Params, line[1:])
			break
		}
		sp := strings.IndexByte(line, ' ')
		if sp < 0 {
			m.Params = append(m.Params, line)
			break
		}
		m.Params = append(m.Params, line[:sp])
		line = strings.TrimLeft(line[sp+1:], " ")
	}

	return m, nil
}

// parseTags splits the tags block (without the leading '@') on ';' and
// decodes each value per the IRCv3 spec.
func parseTags(s string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(s, ";") {
		if pair == "" {
			continue
		}
		eq := strings.IndexByte(pair, '=')
		if eq < 0 {
			// Bare key → empty value.
			out[pair] = ""
			continue
		}
		out[pair[:eq]] = unescapeTagValue(pair[eq+1:])
	}
	return out
}

// unescapeTagValue reverses the IRCv3 tag value escaping scheme:
//   - \s → space
//   - \: → semicolon
//   - \\ → backslash
//   - \r → CR
//   - \n → LF
//   - \X (any other char) → X, i.e. the backslash is dropped
//   - lone trailing '\' → dropped
func unescapeTagValue(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		// Backslash: look at next byte.
		if i == len(s)-1 {
			// Lone trailing backslash — drop it.
			break
		}
		nxt := s[i+1]
		switch nxt {
		case 's':
			b.WriteByte(' ')
		case ':':
			b.WriteByte(';')
		case '\\':
			b.WriteByte('\\')
		case 'r':
			b.WriteByte('\r')
		case 'n':
			b.WriteByte('\n')
		default:
			// Unknown escape: spec says drop the backslash, keep the char.
			b.WriteByte(nxt)
		}
		i++ // consume the escaped char
	}
	return b.String()
}

// AsChat converts a PRIVMSG Message into a Chat with Twitch tag fields
// decoded. Returns (nil, false) for non-PRIVMSG messages or ones with no
// channel parameter.
func (m *Message) AsChat() (*Chat, bool) {
	if m == nil || m.Command != "PRIVMSG" || len(m.Params) < 1 {
		return nil, false
	}

	c := &Chat{
		Channel: strings.TrimPrefix(m.Params[0], "#"),
		Login:   loginFromSource(m.Source),
		Color:   m.Tags["color"],
		ID:      m.Tags["id"],
	}

	if len(m.Params) >= 2 {
		c.Text = m.Params[1]
	}

	if dn := m.Tags["display-name"]; dn != "" {
		c.DisplayName = dn
	} else {
		c.DisplayName = c.Login
	}

	if ts := m.Tags["tmi-sent-ts"]; ts != "" {
		if ms, err := strconv.ParseInt(ts, 10, 64); err == nil {
			c.Sent = time.UnixMilli(ms)
		}
	}

	c.Badges = parseBadges(m.Tags["badges"])
	c.Emotes = parseEmotes(m.Tags["emotes"], c.Text)

	return c, true
}

// loginFromSource extracts the nick portion of a `nick!user@host` prefix.
// Falls back to the full source if the prefix isn't in that form.
func loginFromSource(src string) string {
	if src == "" {
		return ""
	}
	if bang := strings.IndexByte(src, '!'); bang >= 0 {
		return src[:bang]
	}
	return src
}

// parseBadges splits "name/version,name/version" into structured Badges.
// Entries without a slash are dropped; duplicates are kept (Twitch doesn't
// emit them, but if it did the renderer can cope).
func parseBadges(s string) []Badge {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]Badge, 0, len(parts))
	for _, p := range parts {
		slash := strings.IndexByte(p, '/')
		if slash <= 0 {
			continue
		}
		out = append(out, Badge{Name: p[:slash], Version: p[slash+1:]})
	}
	return out
}

// parseEmotes decodes the `emotes` tag into []Emote with Name populated from
// msgText at the first range of each emote. Malformed entries are skipped.
//
// Format: `emoteID:start-end[,start-end]/emoteID:start-end...`
// Ranges are inclusive character (not byte) indices into the message text.
func parseEmotes(s, msgText string) []Emote {
	if s == "" {
		return nil
	}
	textRunes := []rune(msgText)
	parts := strings.Split(s, "/")
	out := make([]Emote, 0, len(parts))
	for _, p := range parts {
		colon := strings.IndexByte(p, ':')
		if colon <= 0 {
			continue
		}
		id := p[:colon]
		ranges := parseEmoteRanges(p[colon+1:])
		if len(ranges) == 0 {
			continue
		}
		e := Emote{ID: id, Ranges: ranges}
		// Derive the emote name from the first range of msgText, if in bounds.
		first := ranges[0]
		if first.Start >= 0 && first.End < len(textRunes) && first.End >= first.Start {
			e.Name = string(textRunes[first.Start : first.End+1])
		}
		out = append(out, e)
	}
	return out
}

// parseEmoteRanges parses "start-end[,start-end]" → []Range. Each range must
// have two integers separated by '-'; otherwise that range is dropped.
func parseEmoteRanges(s string) []Range {
	parts := strings.Split(s, ",")
	out := make([]Range, 0, len(parts))
	for _, p := range parts {
		dash := strings.IndexByte(p, '-')
		if dash <= 0 {
			continue
		}
		start, err1 := strconv.Atoi(p[:dash])
		end, err2 := strconv.Atoi(p[dash+1:])
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, Range{Start: start, End: end})
	}
	return out
}
