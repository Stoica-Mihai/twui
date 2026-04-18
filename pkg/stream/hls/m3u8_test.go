package hls

import "testing"

func TestParseAttrList_Simple(t *testing.T) {
	a := parseAttrList(`BANDWIDTH=1234,RESOLUTION=1920x1080`)
	if a.Int("BANDWIDTH") != 1234 {
		t.Errorf("BANDWIDTH = %d, want 1234", a.Int("BANDWIDTH"))
	}
	if a.Get("RESOLUTION") != "1920x1080" {
		t.Errorf("RESOLUTION = %q, want 1920x1080", a.Get("RESOLUTION"))
	}
}

func TestParseAttrList_QuotedValues(t *testing.T) {
	a := parseAttrList(`CODECS="mp4a.40.2,avc1.64001F",NAME="1080p60"`)
	if got := a.Get("CODECS"); got != "mp4a.40.2,avc1.64001F" {
		t.Errorf("CODECS = %q, want mp4a.40.2,avc1.64001F", got)
	}
	if got := a.Get("NAME"); got != "1080p60" {
		t.Errorf("NAME = %q, want 1080p60", got)
	}
}

func TestParseAttrList_MixedQuotedUnquoted(t *testing.T) {
	a := parseAttrList(`TYPE="AUDIO",DEFAULT=YES,AUTOSELECT=YES,GROUP-ID="audio_only",NAME="Audio"`)
	if a.Get("TYPE") != "AUDIO" {
		t.Errorf("TYPE = %q", a.Get("TYPE"))
	}
	if a.Get("DEFAULT") != "YES" {
		t.Errorf("DEFAULT = %q", a.Get("DEFAULT"))
	}
	if a.Get("GROUP-ID") != "audio_only" {
		t.Errorf("GROUP-ID = %q", a.Get("GROUP-ID"))
	}
}

func TestParseAttrList_FloatAndMissing(t *testing.T) {
	a := parseAttrList(`FRAME-RATE=59.940`)
	if got := a.Float("FRAME-RATE"); got != 59.940 {
		t.Errorf("FRAME-RATE = %v, want 59.94", got)
	}
	if got := a.Float("MISSING"); got != 0 {
		t.Errorf("missing key should return 0, got %v", got)
	}
	if got := a.Int("MISSING"); got != 0 {
		t.Errorf("missing key int should return 0, got %d", got)
	}
	if got := a.Get("MISSING"); got != "" {
		t.Errorf("missing key get should return empty, got %q", got)
	}
}

func TestParseAttrList_XAttributes(t *testing.T) {
	a := parseAttrList(`ID="ad1",CLASS="com.apple.hls.interstitial",X-AD-TYPE="midroll",X-AD-BREAK="spot1"`)
	x := a.XAttrs()
	if len(x) != 2 {
		t.Fatalf("XAttrs count = %d, want 2: %v", len(x), x)
	}
	if x["X-AD-TYPE"] != "midroll" {
		t.Errorf("X-AD-TYPE = %q", x["X-AD-TYPE"])
	}
	if x["X-AD-BREAK"] != "spot1" {
		t.Errorf("X-AD-BREAK = %q", x["X-AD-BREAK"])
	}
}

func TestParseAttrList_QuotedCommaNotSeparator(t *testing.T) {
	// Codec lists contain commas inside quotes and must not be split.
	a := parseAttrList(`A="x,y,z",B=1`)
	if a.Get("A") != "x,y,z" {
		t.Errorf("A = %q, want x,y,z", a.Get("A"))
	}
	if a.Int("B") != 1 {
		t.Errorf("B = %d, want 1", a.Int("B"))
	}
}

func TestParseAttrList_EmptyString(t *testing.T) {
	a := parseAttrList("")
	if len(a) != 0 {
		t.Errorf("empty input should yield empty map, got %v", a)
	}
}

func TestParseAttrList_UnterminatedQuote(t *testing.T) {
	// Should not panic; consumes remainder as the value.
	a := parseAttrList(`A="unterminated`)
	if a.Get("A") != "unterminated" {
		t.Errorf("A = %q, want unterminated", a.Get("A"))
	}
}
