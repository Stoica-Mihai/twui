package twitch

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestChannelHLS_ContainsChannel(t *testing.T) {
	u := NewUsherService(http.DefaultClient)
	result := u.ChannelHLS("testchannel", "sig123", "token456", UsherOpts{})
	if !strings.Contains(result, "testchannel") {
		t.Errorf("URL %q does not contain channel name", result)
	}
}

func TestChannelHLS_ContainsSigAndToken(t *testing.T) {
	u := NewUsherService(http.DefaultClient)
	result := u.ChannelHLS("chan", "mysig", "mytoken", UsherOpts{})

	parsed, err := url.Parse(result)
	if err != nil {
		t.Fatalf("invalid URL: %v", err)
	}
	q := parsed.Query()

	if got := q.Get("sig"); got != "mysig" {
		t.Errorf("sig = %q, want %q", got, "mysig")
	}
	if got := q.Get("token"); got != "mytoken" {
		t.Errorf("token = %q, want %q", got, "mytoken")
	}
}

func TestChannelHLS_AllowSourcePresent(t *testing.T) {
	u := NewUsherService(http.DefaultClient)
	result := u.ChannelHLS("chan", "", "", UsherOpts{AllowSource: true})

	parsed, _ := url.Parse(result)
	if got := parsed.Query().Get("allow_source"); got != "true" {
		t.Errorf("allow_source = %q, want %q", got, "true")
	}
}

func TestChannelHLS_SupportedCodecs(t *testing.T) {
	u := NewUsherService(http.DefaultClient)
	result := u.ChannelHLS("chan", "", "", UsherOpts{SupportedCodecs: "h264,h265"})

	parsed, _ := url.Parse(result)
	if got := parsed.Query().Get("supported_codecs"); got != "h264,h265" {
		t.Errorf("supported_codecs = %q, want %q", got, "h264,h265")
	}
}

func TestChannelHLS_Platform(t *testing.T) {
	u := NewUsherService(http.DefaultClient)
	result := u.ChannelHLS("chan", "", "", UsherOpts{Platform: "web"})

	parsed, _ := url.Parse(result)
	if got := parsed.Query().Get("platform"); got != "web" {
		t.Errorf("platform = %q, want %q", got, "web")
	}
}

func TestChannelHLS_FastBreadLowLatency(t *testing.T) {
	u := NewUsherService(http.DefaultClient)
	result := u.ChannelHLS("chan", "", "", UsherOpts{FastBread: true})

	parsed, _ := url.Parse(result)
	if got := parsed.Query().Get("fast_bread"); got != "true" {
		t.Errorf("fast_bread = %q, want %q", got, "true")
	}
}

func TestChannelHLS_URLScheme(t *testing.T) {
	u := NewUsherService(http.DefaultClient)
	result := u.ChannelHLS("chan", "", "", UsherOpts{})
	if !strings.HasPrefix(result, "https://usher.ttvnw.net/") {
		t.Errorf("URL %q does not have expected prefix", result)
	}
}
