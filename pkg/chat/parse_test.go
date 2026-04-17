package chat

import (
	"testing"
	"time"
)

// --- Parse: raw IRC line parsing ---

func TestParse_Ping(t *testing.T) {
	m, err := Parse("PING :tmi.twitch.tv")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Command != "PING" {
		t.Errorf("Command = %q, want PING", m.Command)
	}
	if len(m.Params) != 1 || m.Params[0] != "tmi.twitch.tv" {
		t.Errorf("Params = %v, want [tmi.twitch.tv]", m.Params)
	}
	if m.Source != "" {
		t.Errorf("Source = %q, want empty", m.Source)
	}
	if len(m.Tags) != 0 {
		t.Errorf("Tags = %v, want empty", m.Tags)
	}
}

func TestParse_CommandOnly(t *testing.T) {
	m, err := Parse("RECONNECT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Command != "RECONNECT" {
		t.Errorf("Command = %q, want RECONNECT", m.Command)
	}
	if len(m.Params) != 0 {
		t.Errorf("Params = %v, want []", m.Params)
	}
}

func TestParse_Notice_WithSource(t *testing.T) {
	m, err := Parse(":tmi.twitch.tv NOTICE * :Improperly formatted auth")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Source != "tmi.twitch.tv" {
		t.Errorf("Source = %q, want tmi.twitch.tv", m.Source)
	}
	if m.Command != "NOTICE" {
		t.Errorf("Command = %q, want NOTICE", m.Command)
	}
	if len(m.Params) != 2 || m.Params[0] != "*" || m.Params[1] != "Improperly formatted auth" {
		t.Errorf("Params = %v, want [* Improperly formatted auth]", m.Params)
	}
}

func TestParse_Privmsg_NoTags(t *testing.T) {
	m, err := Parse(":dallas!dallas@dallas.tmi.twitch.tv PRIVMSG #channel :hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Source != "dallas!dallas@dallas.tmi.twitch.tv" {
		t.Errorf("Source = %q", m.Source)
	}
	if m.Command != "PRIVMSG" {
		t.Errorf("Command = %q, want PRIVMSG", m.Command)
	}
	if len(m.Params) != 2 || m.Params[0] != "#channel" || m.Params[1] != "hello world" {
		t.Errorf("Params = %v", m.Params)
	}
}

func TestParse_Privmsg_WithTags(t *testing.T) {
	line := `@badges=broadcaster/1;color=#FF0000;display-name=Ronni;emotes=25:0-4;id=abc-123;tmi-sent-ts=1592000000000 :ronni!ronni@ronni.tmi.twitch.tv PRIVMSG #dallas :Kappa hey there`
	m, err := Parse(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Command != "PRIVMSG" {
		t.Errorf("Command = %q", m.Command)
	}
	if m.Tags["color"] != "#FF0000" {
		t.Errorf("color = %q", m.Tags["color"])
	}
	if m.Tags["display-name"] != "Ronni" {
		t.Errorf("display-name = %q", m.Tags["display-name"])
	}
	if m.Tags["tmi-sent-ts"] != "1592000000000" {
		t.Errorf("tmi-sent-ts = %q", m.Tags["tmi-sent-ts"])
	}
}

func TestParse_Tags_EmptyValue(t *testing.T) {
	// color= should parse to an explicit empty string entry.
	m, err := Parse(`@color=;display-name=Foo :foo!foo@foo.tmi.twitch.tv PRIVMSG #c :hi`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v, ok := m.Tags["color"]; !ok || v != "" {
		t.Errorf("color tag: ok=%v v=%q, want ok=true v=\"\"", ok, v)
	}
	if m.Tags["display-name"] != "Foo" {
		t.Errorf("display-name = %q", m.Tags["display-name"])
	}
}

func TestParse_Tags_KeyWithoutValue(t *testing.T) {
	// A bare key (no '=') is equivalent to an empty value.
	m, err := Parse(`@mod;color=#00FF00 :x!x@x.tmi PRIVMSG #c :hi`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v, ok := m.Tags["mod"]; !ok || v != "" {
		t.Errorf("mod tag: ok=%v v=%q", ok, v)
	}
	if m.Tags["color"] != "#00FF00" {
		t.Errorf("color = %q", m.Tags["color"])
	}
}

func TestParse_Tags_EscapeSequences(t *testing.T) {
	// \s=space, \:=semicolon, \\=backslash, \r=CR, \n=LF. Unknown escapes pass the
	// following character through unchanged (spec: drop the backslash).
	cases := []struct {
		in, want string
	}{
		{`plain`, `plain`},
		{`with\sspace`, `with space`},
		{`semi\:colon`, `semi;colon`},
		{`back\\slash`, `back\slash`},
		{`cr\rand\nlf`, "cr\rand\nlf"},
		{`unknown\qescape`, `unknownqescape`},
		{`trailing\`, `trailing`}, // lone trailing backslash is dropped
	}
	for _, c := range cases {
		line := `@display-name=` + c.in + ` PRIVMSG #c :x`
		m, err := Parse(line)
		if err != nil {
			t.Fatalf("parse %q: %v", line, err)
		}
		if got := m.Tags["display-name"]; got != c.want {
			t.Errorf("escape %q → %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParse_MiddleParamsAndTrailing(t *testing.T) {
	// Trailing parameter contains spaces; middle params don't.
	m, err := Parse(":src USERNOTICE #chan :thanks for the 5 months!")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Params) != 2 {
		t.Fatalf("Params len = %d, want 2: %v", len(m.Params), m.Params)
	}
	if m.Params[0] != "#chan" {
		t.Errorf("Params[0] = %q", m.Params[0])
	}
	if m.Params[1] != "thanks for the 5 months!" {
		t.Errorf("Params[1] = %q", m.Params[1])
	}
}

func TestParse_NoTrailing(t *testing.T) {
	// Middle-params-only command (like a numeric).
	m, err := Parse(":tmi.twitch.tv 001 dallas")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Command != "001" {
		t.Errorf("Command = %q", m.Command)
	}
	if len(m.Params) != 1 || m.Params[0] != "dallas" {
		t.Errorf("Params = %v", m.Params)
	}
}

func TestParse_StripsCRLF(t *testing.T) {
	// Real wire lines end with CRLF; parser must tolerate that.
	m, err := Parse("PING :x\r\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Command != "PING" || len(m.Params) != 1 || m.Params[0] != "x" {
		t.Errorf("m = %+v", m)
	}
}

func TestParse_Errors(t *testing.T) {
	// Empty and whitespace-only lines are errors.
	for _, in := range []string{"", " ", "\r\n", "@onlytags;"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) expected error, got nil", in)
		}
	}
}

// --- AsChat: PRIVMSG-specific interpretation ---

func TestAsChat_RequiresPrivmsg(t *testing.T) {
	m, err := Parse("PING :x")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.AsChat(); ok {
		t.Error("AsChat on PING should return ok=false")
	}
}

func TestAsChat_FullTags(t *testing.T) {
	line := `@badges=broadcaster/1,subscriber/12;color=#FF4500;display-name=TheStreamer;emotes=25:0-4,6-10/1902:12-16;id=abcd-1234;tmi-sent-ts=1700000000000;mod=0 :thestreamer!thestreamer@thestreamer.tmi.twitch.tv PRIVMSG #thestreamer :Kappa Kappa Keepo hi chat`
	m, err := Parse(line)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := m.AsChat()
	if !ok {
		t.Fatal("AsChat returned ok=false for a valid PRIVMSG")
	}
	if c.Channel != "thestreamer" {
		t.Errorf("Channel = %q", c.Channel)
	}
	if c.Login != "thestreamer" {
		t.Errorf("Login = %q", c.Login)
	}
	if c.DisplayName != "TheStreamer" {
		t.Errorf("DisplayName = %q", c.DisplayName)
	}
	if c.Color != "#FF4500" {
		t.Errorf("Color = %q", c.Color)
	}
	if c.ID != "abcd-1234" {
		t.Errorf("ID = %q", c.ID)
	}
	if c.Text != "Kappa Kappa Keepo hi chat" {
		t.Errorf("Text = %q", c.Text)
	}

	// Badges
	if len(c.Badges) != 2 {
		t.Fatalf("Badges len = %d, want 2: %+v", len(c.Badges), c.Badges)
	}
	if c.Badges[0].Name != "broadcaster" || c.Badges[0].Version != "1" {
		t.Errorf("Badges[0] = %+v", c.Badges[0])
	}
	if c.Badges[1].Name != "subscriber" || c.Badges[1].Version != "12" {
		t.Errorf("Badges[1] = %+v", c.Badges[1])
	}

	// Emotes: "25" appears at [0,4] and [6,10]; "1902" at [12,16].
	if len(c.Emotes) != 2 {
		t.Fatalf("Emotes len = %d: %+v", len(c.Emotes), c.Emotes)
	}
	// Order is spec-unspecified; find each by ID.
	byID := map[string]Emote{c.Emotes[0].ID: c.Emotes[0], c.Emotes[1].ID: c.Emotes[1]}
	if e, ok := byID["25"]; !ok || len(e.Ranges) != 2 || e.Ranges[0] != (Range{0, 4}) || e.Ranges[1] != (Range{6, 10}) {
		t.Errorf("emote 25 ranges wrong: %+v", e)
	}
	if e, ok := byID["25"]; !ok || e.Name != "Kappa" {
		t.Errorf("emote 25 name = %q, want Kappa", e.Name)
	}
	if e, ok := byID["1902"]; !ok || len(e.Ranges) != 1 || e.Ranges[0] != (Range{12, 16}) {
		t.Errorf("emote 1902 ranges wrong: %+v", e)
	}
	if e, ok := byID["1902"]; !ok || e.Name != "Keepo" {
		t.Errorf("emote 1902 name = %q, want Keepo", e.Name)
	}

	// Sent timestamp: 1700000000000 ms → 2023-11-14T22:13:20Z
	if c.Sent.IsZero() {
		t.Error("Sent should be set")
	}
	if got := c.Sent.UTC(); got.Year() != 2023 || got.Month() != time.November {
		t.Errorf("Sent = %v, want 2023-11-14-ish", got)
	}
}

func TestAsChat_DisplayNameFallsBackToLogin(t *testing.T) {
	line := `@display-name= :ronni!ronni@ronni.tmi.twitch.tv PRIVMSG #c :hi`
	m, _ := Parse(line)
	c, ok := m.AsChat()
	if !ok {
		t.Fatal("AsChat ok=false")
	}
	if c.DisplayName != "ronni" {
		t.Errorf("DisplayName = %q, want login fallback 'ronni'", c.DisplayName)
	}
}

func TestAsChat_EmptyColorIsEmpty(t *testing.T) {
	line := `@color= :ronni!ronni@ronni.tmi.twitch.tv PRIVMSG #c :hi`
	m, _ := Parse(line)
	c, _ := m.AsChat()
	if c.Color != "" {
		t.Errorf("Color = %q, want empty", c.Color)
	}
}

func TestAsChat_NoTagsFallsBackGracefully(t *testing.T) {
	// Completely untagged PRIVMSG still yields a valid Chat — fields that come
	// from tags end up empty/zero.
	line := `:ronni!ronni@ronni.tmi.twitch.tv PRIVMSG #c :hello`
	m, _ := Parse(line)
	c, ok := m.AsChat()
	if !ok {
		t.Fatal("AsChat ok=false")
	}
	if c.Login != "ronni" || c.DisplayName != "ronni" {
		t.Errorf("Login/DisplayName = %q/%q", c.Login, c.DisplayName)
	}
	if !c.Sent.IsZero() {
		t.Errorf("Sent should be zero, got %v", c.Sent)
	}
	if len(c.Badges) != 0 || len(c.Emotes) != 0 {
		t.Errorf("badges/emotes should be empty: %+v / %+v", c.Badges, c.Emotes)
	}
}

func TestAsChat_InvalidTimestampIsZero(t *testing.T) {
	line := `@tmi-sent-ts=not-a-number :r!r@r PRIVMSG #c :hi`
	m, _ := Parse(line)
	c, _ := m.AsChat()
	if !c.Sent.IsZero() {
		t.Errorf("Sent should be zero for unparseable ts, got %v", c.Sent)
	}
}

func TestAsChat_MalformedBadgesIgnored(t *testing.T) {
	// badges entries without a slash should be dropped, others kept.
	line := `@badges=broadcaster/1,malformed,subscriber/6 :r!r@r PRIVMSG #c :hi`
	m, _ := Parse(line)
	c, _ := m.AsChat()
	if len(c.Badges) != 2 {
		t.Fatalf("Badges = %+v, want 2 entries", c.Badges)
	}
}

func TestAsChat_MalformedEmotesIgnored(t *testing.T) {
	// Malformed emote specs should not crash the parser.
	line := `@emotes=25:not-a-range/86:0-1 :r!r@r PRIVMSG #c :hi there`
	m, _ := Parse(line)
	c, ok := m.AsChat()
	if !ok {
		t.Fatal("AsChat returned false")
	}
	if len(c.Emotes) != 1 || c.Emotes[0].ID != "86" {
		t.Errorf("Emotes = %+v, want only the well-formed 86 entry", c.Emotes)
	}
}

func TestAsChat_ChannelStripsHash(t *testing.T) {
	line := `:r!r@r PRIVMSG #MeNotSanta :hi`
	m, _ := Parse(line)
	c, _ := m.AsChat()
	if c.Channel != "MeNotSanta" {
		t.Errorf("Channel = %q", c.Channel)
	}
}

func TestAsChat_MissingText(t *testing.T) {
	// No trailing param → empty text is fine (malformed PRIVMSG from the wild).
	// We should still return a Chat so logging has something.
	line := `:r!r@r PRIVMSG #c`
	m, err := Parse(line)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := m.AsChat()
	if !ok {
		t.Fatal("AsChat should accept a PRIVMSG with no text")
	}
	if c.Text != "" {
		t.Errorf("Text = %q, want empty", c.Text)
	}
}
