package twitch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mcs/twui/pkg/stream"
	"github.com/mcs/twui/pkg/stream/hls"
)

// mockStream implements stream.Stream for testing.
type mockStream struct {
	url string
}

func (m *mockStream) Open() (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (m *mockStream) URL() string {
	return m.url
}

// farFuture returns a time far in the future for test token expiry.
func farFuture() time.Time {
	return time.Now().Add(24 * time.Hour)
}

// streamInfo builds a stream.StreamInfo for testing.
func streamInfo(name, resolution string, bandwidth int, codecs string, frameRate float64) stream.StreamInfo {
	return stream.StreamInfo{
		Name:       name,
		Resolution: resolution,
		Bandwidth:  bandwidth,
		Codecs:     codecs,
		FrameRate:  frameRate,
	}
}

// --- variantName tests ---

func TestVariantName_WithName(t *testing.T) {
	v := hls.Variant{Name: "source", Resolution: "1920x1080", Bandwidth: 6000000, FrameRate: 60}
	got := variantName(v)
	if got != "source" {
		t.Errorf("got %q, want %q", got, "source")
	}
}

func TestVariantName_FromResolution(t *testing.T) {
	v := hls.Variant{Resolution: "1920x1080", Bandwidth: 6000000, FrameRate: 30}
	got := variantName(v)
	if got != "1080p" {
		t.Errorf("got %q, want %q", got, "1080p")
	}
}

func TestVariantName_FromResolutionNon30FPS(t *testing.T) {
	v := hls.Variant{Resolution: "1920x1080", Bandwidth: 6000000, FrameRate: 60}
	got := variantName(v)
	if got != "1080p60" {
		t.Errorf("got %q, want %q", got, "1080p60")
	}
}

func TestVariantName_FromBandwidth(t *testing.T) {
	v := hls.Variant{Bandwidth: 3000000}
	got := variantName(v)
	if got != "3000k" {
		t.Errorf("got %q, want %q", got, "3000k")
	}
}

func TestVariantName_Unknown(t *testing.T) {
	v := hls.Variant{}
	got := variantName(v)
	if got != "unknown" {
		t.Errorf("got %q, want %q", got, "unknown")
	}
}

func TestVariantName_Table(t *testing.T) {
	cases := []struct {
		name    string
		variant hls.Variant
		want    string
	}{
		{"named variant", hls.Variant{Name: "720p60", Resolution: "1280x720", Bandwidth: 3000000, FrameRate: 60}, "720p60"},
		{"resolution 720p30", hls.Variant{Resolution: "1280x720", Bandwidth: 3000000, FrameRate: 30}, "720p"},
		{"resolution 480p", hls.Variant{Resolution: "852x480", Bandwidth: 1500000, FrameRate: 30}, "480p"},
		{"resolution 360p60", hls.Variant{Resolution: "640x360", Bandwidth: 750000, FrameRate: 60}, "360p60"},
		{"bandwidth only", hls.Variant{Bandwidth: 1500000}, "1500k"},
		{"audio only named", hls.Variant{Name: "audio_only", Bandwidth: 128000}, "audio_only"},
		{"0 fps treated as 30", hls.Variant{Resolution: "1920x1080", FrameRate: 0}, "1080p"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := variantName(c.variant)
			if got != c.want {
				t.Errorf("variantName = %q, want %q", got, c.want)
			}
		})
	}
}

// --- isRestricted tests ---

func TestIsRestricted_Match(t *testing.T) {
	if !isRestricted("1080p", []string{"1080p", "720p"}) {
		t.Error("expected true for matching restriction")
	}
}

func TestIsRestricted_CaseInsensitive(t *testing.T) {
	if !isRestricted("Source", []string{"source"}) {
		t.Error("expected true for case-insensitive match")
	}
}

func TestIsRestricted_NoMatch(t *testing.T) {
	if isRestricted("480p", []string{"1080p", "720p"}) {
		t.Error("expected false for non-matching restriction")
	}
}

func TestIsRestricted_EmptyList(t *testing.T) {
	if isRestricted("1080p", nil) {
		t.Error("expected false for nil restriction list")
	}
}

// --- parseRestrictedBitrates tests ---

func TestParseRestrictedBitrates_Valid(t *testing.T) {
	token := `{"chansub":{"restricted_bitrates":["1080p","720p"]}}`
	result := parseRestrictedBitrates(token)
	if len(result) != 2 {
		t.Fatalf("expected 2 restricted bitrates, got %d", len(result))
	}
	if result[0] != "1080p" || result[1] != "720p" {
		t.Errorf("got %v, want [1080p 720p]", result)
	}
}

func TestParseRestrictedBitrates_NoChanSub(t *testing.T) {
	token := `{"other":"field"}`
	result := parseRestrictedBitrates(token)
	if len(result) != 0 {
		t.Errorf("expected 0 restricted bitrates, got %d", len(result))
	}
}

func TestParseRestrictedBitrates_InvalidJSON(t *testing.T) {
	result := parseRestrictedBitrates("not json")
	if result != nil {
		t.Errorf("expected nil for invalid JSON, got %v", result)
	}
}

func TestParseRestrictedBitrates_EmptyArray(t *testing.T) {
	token := `{"chansub":{"restricted_bitrates":[]}}`
	result := parseRestrictedBitrates(token)
	if len(result) != 0 {
		t.Errorf("expected 0 restricted bitrates, got %d", len(result))
	}
}

// --- parseUsherError tests ---

func TestParseUsherError_Array(t *testing.T) {
	body := []byte(`[{"error":"channel is offline"}]`)
	msg := parseUsherError(body)
	if msg != "channel is offline" {
		t.Errorf("got %q, want %q", msg, "channel is offline")
	}
}

func TestParseUsherError_ObjectWithMessage(t *testing.T) {
	body := []byte(`{"error":"bad_request","message":"token is invalid"}`)
	msg := parseUsherError(body)
	if msg != "token is invalid" {
		t.Errorf("got %q, want %q", msg, "token is invalid")
	}
}

func TestParseUsherError_ObjectWithErrorOnly(t *testing.T) {
	body := []byte(`{"error":"forbidden"}`)
	msg := parseUsherError(body)
	if msg != "forbidden" {
		t.Errorf("got %q, want %q", msg, "forbidden")
	}
}

func TestParseUsherError_EmptyBody(t *testing.T) {
	msg := parseUsherError([]byte(""))
	if msg != "" {
		t.Errorf("expected empty string for empty body, got %q", msg)
	}
}

func TestParseUsherError_NonErrorJSON(t *testing.T) {
	body := []byte(`{"data":"something"}`)
	msg := parseUsherError(body)
	if msg != "" {
		t.Errorf("expected empty string for non-error JSON, got %q", msg)
	}
}

func TestParseUsherError_EmptyErrorArray(t *testing.T) {
	body := []byte(`[]`)
	msg := parseUsherError(body)
	if msg != "" {
		t.Errorf("expected empty string for empty array, got %q", msg)
	}
}

// --- ensureTransportWithHeaders tests ---

func TestEnsureTransportWithHeaders_NilTransport(t *testing.T) {
	tc := &TwitchClient{
		client: &http.Client{},
	}
	tc.ensureTransportWithHeaders()

	if _, ok := tc.client.Transport.(*twitchTransport); !ok {
		t.Error("expected *twitchTransport after ensureTransportWithHeaders with nil transport")
	}
}

func TestEnsureTransportWithHeaders_AlreadySet(t *testing.T) {
	tt := &twitchTransport{base: http.DefaultTransport}
	tc := &TwitchClient{
		client: &http.Client{Transport: tt},
	}
	tc.ensureTransportWithHeaders()

	// Should not double-wrap
	if tc.client.Transport != tt {
		t.Error("should not re-wrap an existing twitchTransport")
	}
}

func TestEnsureTransportWithHeaders_WrapsExisting(t *testing.T) {
	tc := &TwitchClient{
		client: &http.Client{Transport: http.DefaultTransport},
	}
	tc.ensureTransportWithHeaders()

	tt, ok := tc.client.Transport.(*twitchTransport)
	if !ok {
		t.Fatal("expected *twitchTransport wrapping existing transport")
	}
	if tt.base != http.DefaultTransport {
		t.Error("base transport should be the original http.DefaultTransport")
	}
}

// --- twitchTransport tests ---

func TestTwitchTransport_SetsTwitchHeaders(t *testing.T) {
	var gotReferer, gotOrigin string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReferer = r.Header.Get("Referer")
		gotOrigin = r.Header.Get("Origin")
	}))
	defer backend.Close()

	// Chain: client -> twitchTransport (sees ttvnw.net, sets headers) -> hostRewriter -> DefaultTransport
	rewriter := &hostRewriter{base: backend.URL, rt: http.DefaultTransport}
	tt := &twitchTransport{base: rewriter}
	// Use tt directly as transport — Do NOT wrap in another hostRewriter.
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://usher.ttvnw.net/test", nil)
	resp, err := tt.RoundTrip(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if gotReferer != "https://player.twitch.tv" {
		t.Errorf("Referer = %q, want %q", gotReferer, "https://player.twitch.tv")
	}
	if gotOrigin != "https://player.twitch.tv" {
		t.Errorf("Origin = %q, want %q", gotOrigin, "https://player.twitch.tv")
	}
}

func TestTwitchTransport_PreservesExistingHeaders(t *testing.T) {
	var gotReferer, gotOrigin string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReferer = r.Header.Get("Referer")
		gotOrigin = r.Header.Get("Origin")
	}))
	defer backend.Close()

	rewriter := &hostRewriter{base: backend.URL, rt: http.DefaultTransport}
	tt := &twitchTransport{base: rewriter}
	client := &http.Client{Transport: tt}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://usher.ttvnw.net/test", nil)
	req.Header.Set("Referer", "https://custom.example.com")
	req.Header.Set("Origin", "https://custom.example.com")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if gotReferer != "https://custom.example.com" {
		t.Errorf("Referer = %q, want %q (should preserve existing)", gotReferer, "https://custom.example.com")
	}
	if gotOrigin != "https://custom.example.com" {
		t.Errorf("Origin = %q, want %q (should preserve existing)", gotOrigin, "https://custom.example.com")
	}
}

func TestTwitchTransport_NonTwitchHost(t *testing.T) {
	var gotReferer string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReferer = r.Header.Get("Referer")
	}))
	defer backend.Close()

	tt := &twitchTransport{base: http.DefaultTransport}
	client := &http.Client{Transport: tt}

	// Request directly to the test server (non-twitch host)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, backend.URL+"/test", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if gotReferer != "" {
		t.Errorf("Referer = %q, want empty for non-twitch host", gotReferer)
	}
}

// --- Streams integration test ---

func TestStreams_OfflineChannel(t *testing.T) {
	// GQL returns a valid access token, but the master playlist returns 404.
	callCount := 0
	handler := func(w http.ResponseWriter, r *http.Request) {
		callCount++
		// First call(s): GQL for access token (may include fallback)
		if r.Header.Get("Content-Type") == "application/json" && r.Method == http.MethodPost {
			fmt.Fprint(w, `{"data":{"streamPlaybackAccessToken":{"signature":"testsig","value":"{}"}}}`)
			return
		}
		// Usher master playlist
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `[{"error":"channel is offline"}]`)
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	client := &http.Client{
		Transport: &hostRewriter{base: srv.URL, rt: http.DefaultTransport},
	}
	api := NewTwitchAPI(client, "testcid", "testua", nil, nil)
	api.integrityToken = "test-integrity"
	api.integrityExpiry = farFuture()
	usher := NewUsherService(client)
	tc := New(client, api, usher)

	_, err := tc.Streams(context.Background(), "offlinechannel")
	if err == nil {
		t.Fatal("expected error for offline channel")
	}
}

func TestStreams_Success(t *testing.T) {
	masterPlaylist := "#EXTM3U\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=6000000,RESOLUTION=1920x1080,CODECS=\"avc1.64002A\",FRAME-RATE=60.000,NAME=\"1080p60 (source)\"\n" +
		"https://video.twitch.tv/v1/playlist/1080p60.m3u8\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=3000000,RESOLUTION=1280x720,CODECS=\"avc1.4D401F\",FRAME-RATE=30.000,NAME=\"720p\"\n" +
		"https://video.twitch.tv/v1/playlist/720p.m3u8\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=128000,CODECS=\"mp4a.40.2\",NAME=\"audio_only\"\n" +
		"https://video.twitch.tv/v1/playlist/audio.m3u8\n"

	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			// Variant validation HEAD requests
			w.WriteHeader(http.StatusOK)
			return
		}
		// GQL request
		if r.Header.Get("Content-Type") == "application/json" && r.Method == http.MethodPost {
			fmt.Fprint(w, `{"data":{"streamPlaybackAccessToken":{"signature":"sig","value":"{}"}}}`)
			return
		}
		// Usher master playlist GET request
		if r.Method == http.MethodGet {
			fmt.Fprint(w, masterPlaylist)
			return
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	client := &http.Client{
		Transport: &hostRewriter{base: srv.URL, rt: http.DefaultTransport},
	}
	api := NewTwitchAPI(client, "testcid", "testua", nil, nil)
	api.integrityToken = "test-integrity"
	api.integrityExpiry = farFuture()
	usher := NewUsherService(client)
	tc := New(client, api, usher)

	streams, err := tc.Streams(context.Background(), "testchannel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(streams) == 0 {
		t.Fatal("expected at least one stream variant")
	}

	// Check that we got named variants
	for name := range streams {
		t.Logf("got stream variant: %q", name)
	}
}

// --- TwitchClient.Metadata test ---

func TestTwitchClient_Metadata(t *testing.T) {
	handler := gqlOK(`{
		"user": {
			"id": "42",
			"displayName": "TestChannel",
			"profileImageURL": "https://example.com/avatar.jpg",
			"lastBroadcast": {"title": "old"},
			"stream": {
				"title": "Live now",
				"viewersCount": 500,
				"createdAt": "2024-06-01T12:00:00Z",
				"game": {"name": "Minecraft"}
			}
		}
	}`)

	srv := httptest.NewServer(handler)
	defer srv.Close()

	client := &http.Client{
		Transport: &hostRewriter{base: srv.URL, rt: http.DefaultTransport},
	}
	api := NewTwitchAPI(client, "testcid", "testua", nil, nil)
	api.integrityToken = "test"
	api.integrityExpiry = farFuture()
	usher := NewUsherService(client)
	tc := New(client, api, usher)

	md, err := tc.Metadata(context.Background(), "testchannel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if md.Author != "TestChannel" {
		t.Errorf("Author = %q, want %q", md.Author, "TestChannel")
	}
	if md.Title != "Live now" {
		t.Errorf("Title = %q, want %q", md.Title, "Live now")
	}
}

// --- annotatedStream tests ---

func TestAnnotatedStream_StreamInfo(t *testing.T) {
	mock := &mockStream{url: "https://example.com/stream.m3u8"}
	info := streamInfo("720p", "1280x720", 3000000, "avc1.4D401F", 30)
	as := &annotatedStream{Stream: mock, info: info}

	got := as.StreamInfo()
	if got.Name != "720p" {
		t.Errorf("Name = %q, want %q", got.Name, "720p")
	}
	if got.Resolution != "1280x720" {
		t.Errorf("Resolution = %q, want %q", got.Resolution, "1280x720")
	}
}

func TestAnnotatedStream_SetOnDrop_NoInterface(t *testing.T) {
	mock := &mockStream{url: "https://example.com/stream.m3u8"}
	info := streamInfo("720p", "", 0, "", 0)
	as := &annotatedStream{Stream: mock, info: info}

	// Should not panic when the underlying stream doesn't implement Droppable
	as.SetOnDrop(func(err error) {})
}

func TestAnnotatedStream_SetOnAdBreak_NoInterface(t *testing.T) {
	mock := &mockStream{url: "https://example.com/stream.m3u8"}
	info := streamInfo("720p", "", 0, "", 0)
	as := &annotatedStream{Stream: mock, info: info}

	// Should not panic
	as.SetOnAdBreak(func(duration float64, adType string) {})
}

func TestAnnotatedStream_SetOnAdEnd_NoInterface(t *testing.T) {
	mock := &mockStream{url: "https://example.com/stream.m3u8"}
	info := streamInfo("720p", "", 0, "", 0)
	as := &annotatedStream{Stream: mock, info: info}

	// Should not panic
	as.SetOnAdEnd(func() {})
}

func TestAnnotatedStream_SetOnPreRoll_NoInterface(t *testing.T) {
	mock := &mockStream{url: "https://example.com/stream.m3u8"}
	info := streamInfo("720p", "", 0, "", 0)
	as := &annotatedStream{Stream: mock, info: info}

	// Should not panic
	as.SetOnPreRoll(func() {})
}

// --- fetchMasterPlaylist tests ---

func TestFetchMasterPlaylist_UsherError(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `[{"error":"token has expired"}]`)
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	client := &http.Client{}
	tc := &TwitchClient{client: client}

	_, err := tc.fetchMasterPlaylist(context.Background(), srv.URL+"/test.m3u8")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "token has expired") {
		t.Errorf("error %q should mention usher error message", err.Error())
	}
}

func TestFetchMasterPlaylist_GenericHTTPError(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `not json`)
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	client := &http.Client{}
	tc := &TwitchClient{client: client}

	_, err := tc.fetchMasterPlaylist(context.Background(), srv.URL+"/test.m3u8")
	if err == nil {
		t.Fatal("expected error for 500 status")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q should contain status code", err.Error())
	}
}

func TestFetchMasterPlaylist_InvalidPlaylist(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "this is not a valid m3u8")
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	client := &http.Client{}
	tc := &TwitchClient{client: client}

	_, err := tc.fetchMasterPlaylist(context.Background(), srv.URL+"/test.m3u8")
	if err == nil {
		t.Fatal("expected error for invalid playlist")
	}
}

// --- validatePlaylistURL tests ---

func TestValidatePlaylistURL_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tc := &TwitchClient{client: &http.Client{}}
	err := tc.validatePlaylistURL(context.Background(), srv.URL+"/test.m3u8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidatePlaylistURL_MethodNotAllowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tc := &TwitchClient{client: &http.Client{}}
	err := tc.validatePlaylistURL(context.Background(), srv.URL+"/test.m3u8")
	if err != nil {
		t.Fatalf("unexpected error after fallback to GET: %v", err)
	}
}

func TestValidatePlaylistURL_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	tc := &TwitchClient{client: &http.Client{}}
	err := tc.validatePlaylistURL(context.Background(), srv.URL+"/test.m3u8")
	if err == nil {
		t.Fatal("expected error for 404")
	}
}

// --- New constructor test ---

func TestNew_DefaultValues(t *testing.T) {
	client := &http.Client{}
	api := &TwitchAPI{}
	usher := &UsherService{}
	tc := New(client, api, usher)

	if tc.SupportedCodecs != "h264" {
		t.Errorf("SupportedCodecs = %q, want %q", tc.SupportedCodecs, "h264")
	}
	if tc.FFmpegPath != "ffmpeg" {
		t.Errorf("FFmpegPath = %q, want %q", tc.FFmpegPath, "ffmpeg")
	}
}
