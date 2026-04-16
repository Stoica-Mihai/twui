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

func TestCommonParams_DefaultFalseValues(t *testing.T) {
	u := NewUsherService(http.DefaultClient)
	params := u.commonParams(UsherOpts{})

	if got := params.Get("allow_source"); got != "false" {
		t.Errorf("allow_source = %q, want %q", got, "false")
	}
	if got := params.Get("allow_audio_only"); got != "false" {
		t.Errorf("allow_audio_only = %q, want %q", got, "false")
	}
	if got := params.Get("fast_bread"); got != "false" {
		t.Errorf("fast_bread = %q, want %q", got, "false")
	}
	// p (cache buster) should always be present
	if params.Get("p") == "" {
		t.Error("p (cache buster) should always be set")
	}
	// No supported_codecs when empty
	if params.Get("supported_codecs") != "" {
		t.Error("supported_codecs should be absent when empty")
	}
	// No platform when empty
	if params.Get("platform") != "" {
		t.Error("platform should be absent when empty")
	}
	// No playlist_include_framerate when false
	if params.Get("playlist_include_framerate") != "" {
		t.Error("playlist_include_framerate should be absent when false")
	}
}

func TestCommonParams_AllOptsSet(t *testing.T) {
	u := NewUsherService(http.DefaultClient)
	params := u.commonParams(UsherOpts{
		SupportedCodecs:          "h264,av1",
		AllowSource:              true,
		AllowAudioOnly:           true,
		FastBread:                true,
		Platform:                 "web",
		PlaylistIncludeFramerate: true,
	})

	cases := map[string]string{
		"allow_source":               "true",
		"allow_audio_only":           "true",
		"fast_bread":                 "true",
		"supported_codecs":           "h264,av1",
		"platform":                   "web",
		"playlist_include_framerate": "true",
	}
	for k, want := range cases {
		if got := params.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestCommonParams_CacheBusterIsNumeric(t *testing.T) {
	u := NewUsherService(http.DefaultClient)
	for i := 0; i < 20; i++ {
		params := u.commonParams(UsherOpts{})
		p := params.Get("p")
		for _, c := range p {
			if c < '0' || c > '9' {
				t.Errorf("p = %q contains non-numeric char %c", p, c)
			}
		}
	}
}

func TestChannelHLS_SpecialCharacters(t *testing.T) {
	u := NewUsherService(http.DefaultClient)
	result := u.ChannelHLS("user/name", "sig", "tok", UsherOpts{})

	parsed, err := url.Parse(result)
	if err != nil {
		t.Fatalf("invalid URL: %v", err)
	}
	// Path should properly escape the forward slash
	if !strings.Contains(parsed.Path, "user%2Fname") && !strings.Contains(result, "user%2Fname") {
		// url.PathEscape encodes / as %2F
		t.Errorf("URL path should escape special chars, got %q", parsed.Path)
	}
}

func TestChannelHLS_PlaylistIncludeFramerate(t *testing.T) {
	u := NewUsherService(http.DefaultClient)
	result := u.ChannelHLS("chan", "", "", UsherOpts{PlaylistIncludeFramerate: true})

	parsed, _ := url.Parse(result)
	if got := parsed.Query().Get("playlist_include_framerate"); got != "true" {
		t.Errorf("playlist_include_framerate = %q, want %q", got, "true")
	}
}
