package twitch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// hostRewriter redirects all outgoing requests to the test server,
// regardless of the original URL host.
type hostRewriter struct {
	base string
	rt   http.RoundTripper
}

func (h *hostRewriter) RoundTrip(req *http.Request) (*http.Response, error) {
	base, _ := url.Parse(h.base)
	r2 := req.Clone(req.Context())
	r2.URL.Scheme = base.Scheme
	r2.URL.Host = base.Host
	return h.rt.RoundTrip(r2)
}

// newTestAPI creates a TwitchAPI whose HTTP client routes all requests to the
// given handler, regardless of what URL the code tries to reach.
// The integrity token is pre-seeded so tests do not receive an extra integrity request.
func newTestAPI(t *testing.T, handler http.HandlerFunc) *TwitchAPI {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	client := &http.Client{
		Transport: &hostRewriter{base: srv.URL, rt: http.DefaultTransport},
	}
	api := NewTwitchAPI(client, "testcid", "testua", nil, nil)
	api.integrityToken = "test-integrity-token"
	api.integrityExpiry = time.Now().Add(24 * time.Hour)
	return api
}

// gqlOK returns a handler that always responds with {"data": <data>}.
func gqlOK(data string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"data":%s}`, data)
	}
}

// gqlError returns a handler that always responds with a GQL errors array.
func gqlErrors(msg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"errors":[{"message":%q}]}`, msg)
	}
}

// --- doGQL tests ---

func TestDoGQL_EmptyHash_SkipsPersistedQuery(t *testing.T) {
	// Register a temporary test operation.
	const testOp = "twuiTestOpEmptyHash"
	fallbackQueries[testOp] = `query twuiTestOpEmptyHash { __typename }`
	defer delete(fallbackQueries, testOp)

	requestCount := 0
	api := newTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		fmt.Fprint(w, `{"data":{"__typename":"Query"}}`)
	})

	_, err := api.doGQL(context.Background(), testOp, "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if requestCount != 1 {
		t.Errorf("expected 1 request (no persisted attempt), got %d", requestCount)
	}
}

func TestDoGQL_HashHit_SucceedsFirstTry(t *testing.T) {
	const testOp = "twuiTestOpHashHit"
	const testHash = "deadbeef"
	fallbackQueries[testOp] = `query twuiTestOpHashHit { __typename }`
	defer delete(fallbackQueries, testOp)

	requestCount := 0
	api := newTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		fmt.Fprint(w, `{"data":{"__typename":"Query"}}`)
	})

	_, err := api.doGQL(context.Background(), testOp, testHash, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if requestCount != 1 {
		t.Errorf("expected 1 request (persisted hit), got %d", requestCount)
	}
}

func TestDoGQL_HashMiss_FallsBack(t *testing.T) {
	const testOp = "twuiTestOpHashMiss"
	const testHash = "badhash"
	fallbackQueries[testOp] = `query twuiTestOpHashMiss { __typename }`
	defer delete(fallbackQueries, testOp)

	requestCount := 0
	api := newTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount == 1 {
			// First request is the persisted query — return hash miss.
			fmt.Fprint(w, `{"errors":[{"message":"PersistedQueryNotFound"}]}`)
		} else {
			fmt.Fprint(w, `{"data":{"__typename":"Query"}}`)
		}
	})

	_, err := api.doGQL(context.Background(), testOp, testHash, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if requestCount != 2 {
		t.Errorf("expected 2 requests (persisted miss + fallback), got %d", requestCount)
	}
}

func TestDoGQL_RateLimit(t *testing.T) {
	const testOp = "twuiTestOpRateLimit"
	fallbackQueries[testOp] = `query twuiTestOpRateLimit { __typename }`
	defer delete(fallbackQueries, testOp)

	api := newTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(http.StatusTooManyRequests)
	})

	_, err := api.doGQL(context.Background(), testOp, "", nil, nil)
	var rle *RateLimitedError
	if !errors.As(err, &rle) {
		t.Fatalf("expected *RateLimitedError, got %T: %v", err, err)
	}
	if rle.RetryAfter != 10*time.Second {
		t.Errorf("RetryAfter = %v, want 10s", rle.RetryAfter)
	}
}

func TestDoGQL_Unauthorized(t *testing.T) {
	const testOp = "twuiTestOpUnauth"
	fallbackQueries[testOp] = `query twuiTestOpUnauth { __typename }`
	defer delete(fallbackQueries, testOp)

	api := newTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	_, err := api.doGQL(context.Background(), testOp, "", nil, nil)
	if !errors.Is(err, ErrAccessDenied) {
		t.Errorf("expected ErrAccessDenied, got %v", err)
	}
}

func TestDoGQL_Forbidden(t *testing.T) {
	const testOp = "twuiTestOpForbidden"
	fallbackQueries[testOp] = `query twuiTestOpForbidden { __typename }`
	defer delete(fallbackQueries, testOp)

	api := newTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})

	_, err := api.doGQL(context.Background(), testOp, "", nil, nil)
	if !errors.Is(err, ErrAccessDenied) {
		t.Errorf("expected ErrAccessDenied, got %v", err)
	}
}

func TestDoGQL_GQLErrors(t *testing.T) {
	const testOp = "twuiTestOpGQLError"
	fallbackQueries[testOp] = `query twuiTestOpGQLError { __typename }`
	defer delete(fallbackQueries, testOp)

	api := newTestAPI(t, gqlErrors(`Unknown type "DirectoryRowOptions"`))

	_, err := api.doGQL(context.Background(), testOp, "", nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrGQLServerError) {
		t.Errorf("expected ErrGQLServerError wrapped, got %v", err)
	}
	if !strings.Contains(err.Error(), "DirectoryRowOptions") {
		t.Errorf("error %q does not mention the type name", err.Error())
	}
}

func TestDoGQL_UnexpectedStatus(t *testing.T) {
	const testOp = "twuiTestOpStatus"
	fallbackQueries[testOp] = `query twuiTestOpStatus { __typename }`
	defer delete(fallbackQueries, testOp)

	api := newTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "internal error")
	})

	_, err := api.doGQL(context.Background(), testOp, "", nil, nil)
	if err == nil {
		t.Fatal("expected error for 500 status, got nil")
	}
}

// --- AccessToken tests ---

func TestAccessToken_Success(t *testing.T) {
	api := newTestAPI(t, gqlOK(`{
		"streamPlaybackAccessToken": {
			"signature": "testsig",
			"value": "testtoken"
		}
	}`))

	token, err := api.AccessToken(context.Background(), "testchannel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token.Sig != "testsig" {
		t.Errorf("Sig = %q, want %q", token.Sig, "testsig")
	}
	if token.Token != "testtoken" {
		t.Errorf("Token = %q, want %q", token.Token, "testtoken")
	}
}

func TestAccessToken_Null(t *testing.T) {
	api := newTestAPI(t, gqlOK(`{"streamPlaybackAccessToken": null}`))

	_, err := api.AccessToken(context.Background(), "testchannel")
	if !errors.Is(err, ErrAccessDenied) {
		t.Errorf("expected ErrAccessDenied, got %v", err)
	}
}

// --- StreamMetadata tests ---

func TestStreamMetadata_Live(t *testing.T) {
	api := newTestAPI(t, gqlOK(`{
		"user": {
			"id": "123",
			"displayName": "TestStreamer",
			"profileImageURL": "https://example.com/avatar.jpg",
			"lastBroadcast": {"title": "old title"},
			"stream": {
				"title": "Live title",
				"viewersCount": 1234,
				"createdAt": "2024-01-15T10:00:00Z",
				"game": {"name": "Just Chatting"}
			}
		}
	}`))

	md, err := api.StreamMetadata(context.Background(), "teststreamer")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if md.Author != "TestStreamer" {
		t.Errorf("Author = %q, want %q", md.Author, "TestStreamer")
	}
	if md.Title != "Live title" {
		t.Errorf("Title = %q, want %q (stream title should override lastBroadcast)", md.Title, "Live title")
	}
	if md.ViewerCount != 1234 {
		t.Errorf("ViewerCount = %d, want 1234", md.ViewerCount)
	}
	if md.Category != "Just Chatting" {
		t.Errorf("Category = %q, want %q", md.Category, "Just Chatting")
	}
	if md.StartedAt.IsZero() {
		t.Error("StartedAt should not be zero for a live stream")
	}
}

func TestStreamMetadata_Offline(t *testing.T) {
	api := newTestAPI(t, gqlOK(`{
		"user": {
			"id": "456",
			"displayName": "OfflineStreamer",
			"profileImageURL": "",
			"lastBroadcast": {"title": "last title"},
			"stream": null
		}
	}`))

	md, err := api.StreamMetadata(context.Background(), "offlinestreamer")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if md.Author != "OfflineStreamer" {
		t.Errorf("Author = %q, want %q", md.Author, "OfflineStreamer")
	}
	if !md.StartedAt.IsZero() {
		t.Error("StartedAt should be zero for offline channel")
	}
	if md.Title != "last title" {
		t.Errorf("Title = %q, want %q", md.Title, "last title")
	}
}

func TestStreamMetadata_NotFound(t *testing.T) {
	api := newTestAPI(t, gqlOK(`{"user": null}`))

	_, err := api.StreamMetadata(context.Background(), "nobody")
	if !errors.Is(err, ErrChannelNotFound) {
		t.Errorf("expected ErrChannelNotFound, got %v", err)
	}
}

// --- parseGQLRetryAfter tests ---

// --- parseRFC3339 tests ---

func TestParseRFC3339_EmptyString(t *testing.T) {
	got := parseRFC3339("")
	if !got.IsZero() {
		t.Errorf("parseRFC3339(%q) = %v, want zero time", "", got)
	}
}

func TestParseRFC3339_ValidTimestamp(t *testing.T) {
	input := "2024-06-15T12:30:00Z"
	got := parseRFC3339(input)
	if got.IsZero() {
		t.Fatalf("parseRFC3339(%q) returned zero time", input)
	}
	if got.Year() != 2024 || got.Month() != time.June || got.Day() != 15 {
		t.Errorf("parseRFC3339(%q) = %v, wrong date", input, got)
	}
	if got.Hour() != 12 || got.Minute() != 30 {
		t.Errorf("parseRFC3339(%q) = %v, wrong time", input, got)
	}
}

func TestParseRFC3339_InvalidString(t *testing.T) {
	got := parseRFC3339("not-a-date")
	if !got.IsZero() {
		t.Errorf("parseRFC3339(%q) = %v, want zero time for invalid input", "not-a-date", got)
	}
}

func TestParseRFC3339_PartialTimestamp(t *testing.T) {
	// A date without time component is not valid RFC3339.
	got := parseRFC3339("2024-06-15")
	if !got.IsZero() {
		t.Errorf("parseRFC3339(%q) = %v, want zero time for partial timestamp", "2024-06-15", got)
	}
}

// --- newDeviceID tests ---

func TestNewDeviceID_UUIDFormat(t *testing.T) {
	id := newDeviceID()
	// UUID v4 format: 8-4-4-4-12 hex chars
	parts := strings.Split(id, "-")
	if len(parts) != 5 {
		t.Fatalf("newDeviceID() = %q, want 5 dash-separated parts, got %d", id, len(parts))
	}
	expectedLens := []int{8, 4, 4, 4, 12}
	for i, p := range parts {
		if len(p) != expectedLens[i] {
			t.Errorf("part %d of %q has len %d, want %d", i, id, len(p), expectedLens[i])
		}
	}
}

func TestNewDeviceID_UniquePerCall(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := newDeviceID()
		if seen[id] {
			t.Fatalf("duplicate device ID: %q", id)
		}
		seen[id] = true
	}
}

func TestNewDeviceID_Version4Bits(t *testing.T) {
	// UUID v4 has specific bits: version nibble = 4, variant bits = 10xx.
	id := newDeviceID()
	parts := strings.Split(id, "-")
	// Third group first char should be '4'
	if parts[2][0] != '4' {
		t.Errorf("UUID version nibble = %c, want '4' in %q", parts[2][0], id)
	}
	// Fourth group first char should be 8, 9, a, or b (variant bits 10xx)
	c := parts[3][0]
	if c != '8' && c != '9' && c != 'a' && c != 'b' {
		t.Errorf("UUID variant nibble = %c, want 8/9/a/b in %q", c, id)
	}
}

// --- newClientSessionID tests ---

func TestNewClientSessionID_Length(t *testing.T) {
	id := newClientSessionID()
	if len(id) != 16 {
		t.Errorf("newClientSessionID() = %q, len %d, want 16", id, len(id))
	}
}

func TestNewClientSessionID_HexCharsOnly(t *testing.T) {
	id := newClientSessionID()
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			t.Errorf("newClientSessionID() = %q, contains non-hex char %c", id, c)
		}
	}
}

func TestNewClientSessionID_UniquePerCall(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := newClientSessionID()
		if seen[id] {
			t.Fatalf("duplicate client session ID: %q", id)
		}
		seen[id] = true
	}
}

// --- NewTwitchAPI tests ---

func TestNewTwitchAPI_DefaultClientID(t *testing.T) {
	api := NewTwitchAPI(http.DefaultClient, "", "", nil, nil)
	if api.ClientID != DefaultClientID {
		t.Errorf("ClientID = %q, want default %q", api.ClientID, DefaultClientID)
	}
}

func TestNewTwitchAPI_DefaultUserAgent(t *testing.T) {
	api := NewTwitchAPI(http.DefaultClient, "", "", nil, nil)
	if api.UserAgent != DefaultUserAgent {
		t.Errorf("UserAgent = %q, want default %q", api.UserAgent, DefaultUserAgent)
	}
}

func TestNewTwitchAPI_CustomValues(t *testing.T) {
	api := NewTwitchAPI(http.DefaultClient, "customcid", "customua", nil, nil)
	if api.ClientID != "customcid" {
		t.Errorf("ClientID = %q, want %q", api.ClientID, "customcid")
	}
	if api.UserAgent != "customua" {
		t.Errorf("UserAgent = %q, want %q", api.UserAgent, "customua")
	}
}

func TestNewTwitchAPI_SetsDeviceAndSessionIDs(t *testing.T) {
	api := NewTwitchAPI(http.DefaultClient, "", "", nil, nil)
	if api.DeviceID == "" {
		t.Error("DeviceID should be set")
	}
	if api.clientSessionID == "" {
		t.Error("clientSessionID should be set")
	}
}

// --- fetchIntegrityToken tests ---

func TestFetchIntegrityToken_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/integrity" || strings.Contains(r.URL.Path, "integrity") || r.Header.Get("Content-Type") == "application/json" {
			expiry := time.Now().Add(10 * time.Minute).Format(time.RFC3339)
			fmt.Fprintf(w, `{"token":"test-integrity-123","expiration":%q}`, expiry)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	client := &http.Client{
		Transport: &hostRewriter{base: srv.URL, rt: http.DefaultTransport},
	}
	api := NewTwitchAPI(client, "testcid", "testua", nil, nil)

	token, expiry := api.fetchIntegrityToken(context.Background())
	if token != "test-integrity-123" {
		t.Errorf("token = %q, want %q", token, "test-integrity-123")
	}
	if expiry.IsZero() {
		t.Error("expiry should not be zero")
	}
}

func TestFetchIntegrityToken_BadStatus(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	client := &http.Client{
		Transport: &hostRewriter{base: srv.URL, rt: http.DefaultTransport},
	}
	api := NewTwitchAPI(client, "testcid", "testua", nil, nil)

	token, _ := api.fetchIntegrityToken(context.Background())
	if token != "" {
		t.Errorf("expected empty token on bad status, got %q", token)
	}
}

func TestFetchIntegrityToken_InvalidJSON(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not json")
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	client := &http.Client{
		Transport: &hostRewriter{base: srv.URL, rt: http.DefaultTransport},
	}
	api := NewTwitchAPI(client, "testcid", "testua", nil, nil)

	token, _ := api.fetchIntegrityToken(context.Background())
	if token != "" {
		t.Errorf("expected empty token on invalid JSON, got %q", token)
	}
}

func TestFetchIntegrityToken_EmptyToken(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"token":"","expiration":"2099-01-01T00:00:00Z"}`)
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	client := &http.Client{
		Transport: &hostRewriter{base: srv.URL, rt: http.DefaultTransport},
	}
	api := NewTwitchAPI(client, "testcid", "testua", nil, nil)

	token, _ := api.fetchIntegrityToken(context.Background())
	if token != "" {
		t.Errorf("expected empty token when token field is empty, got %q", token)
	}
}

func TestFetchIntegrityToken_InvalidExpiration(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"token":"validtoken","expiration":"not-a-date"}`)
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	client := &http.Client{
		Transport: &hostRewriter{base: srv.URL, rt: http.DefaultTransport},
	}
	api := NewTwitchAPI(client, "testcid", "testua", nil, nil)

	token, expiry := api.fetchIntegrityToken(context.Background())
	if token != "validtoken" {
		t.Errorf("token = %q, want %q", token, "validtoken")
	}
	// Should fall back to ~5 minutes from now
	if expiry.Before(time.Now()) {
		t.Error("expiry should be in the future even with invalid expiration string")
	}
}

// --- getIntegrityToken tests ---

func TestGetIntegrityToken_CacheHit(t *testing.T) {
	api := NewTwitchAPI(http.DefaultClient, "testcid", "testua", nil, nil)
	api.integrityToken = "cached-token"
	api.integrityExpiry = time.Now().Add(10 * time.Minute)

	got := api.getIntegrityToken(context.Background())
	if got != "cached-token" {
		t.Errorf("got %q, want cached %q", got, "cached-token")
	}
}

func TestGetIntegrityToken_ExpiredRefreshes(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		expiry := time.Now().Add(10 * time.Minute).Format(time.RFC3339)
		fmt.Fprintf(w, `{"token":"refreshed-token","expiration":%q}`, expiry)
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	client := &http.Client{
		Transport: &hostRewriter{base: srv.URL, rt: http.DefaultTransport},
	}
	api := NewTwitchAPI(client, "testcid", "testua", nil, nil)
	// Set expired token
	api.integrityToken = "expired-token"
	api.integrityExpiry = time.Now().Add(-5 * time.Minute)

	got := api.getIntegrityToken(context.Background())
	if got != "refreshed-token" {
		t.Errorf("got %q, want %q after refresh", got, "refreshed-token")
	}
}

// --- isPersistedQueryNotFound tests ---

func TestIsPersistedQueryNotFound_True(t *testing.T) {
	err := &gqlServerError{Message: "PersistedQueryNotFound"}
	if !isPersistedQueryNotFound(err) {
		t.Error("expected true for PersistedQueryNotFound message")
	}
}

func TestIsPersistedQueryNotFound_False(t *testing.T) {
	err := &gqlServerError{Message: "some other error"}
	if isPersistedQueryNotFound(err) {
		t.Error("expected false for non-PersistedQueryNotFound message")
	}
}

func TestIsPersistedQueryNotFound_NonGQLError(t *testing.T) {
	err := fmt.Errorf("random error")
	if isPersistedQueryNotFound(err) {
		t.Error("expected false for non-gqlServerError type")
	}
}

// --- StreamMetadata edge case tests ---

func TestStreamMetadata_NoLastBroadcast(t *testing.T) {
	api := newTestAPI(t, gqlOK(`{
		"user": {
			"id": "789",
			"displayName": "NoLastBroadcast",
			"profileImageURL": "",
			"lastBroadcast": null,
			"stream": null
		}
	}`))

	md, err := api.StreamMetadata(context.Background(), "nolb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if md.Title != "" {
		t.Errorf("Title = %q, want empty when lastBroadcast is null", md.Title)
	}
}

func TestStreamMetadata_LiveNoGame(t *testing.T) {
	api := newTestAPI(t, gqlOK(`{
		"user": {
			"id": "111",
			"displayName": "NoGame",
			"profileImageURL": "",
			"lastBroadcast": null,
			"stream": {
				"title": "Streaming",
				"viewersCount": 42,
				"createdAt": "2024-03-01T00:00:00Z",
				"game": null
			}
		}
	}`))

	md, err := api.StreamMetadata(context.Background(), "nogame")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if md.Category != "" {
		t.Errorf("Category = %q, want empty when game is null", md.Category)
	}
	if md.ViewerCount != 42 {
		t.Errorf("ViewerCount = %d, want 42", md.ViewerCount)
	}
}

// --- AccessToken edge case tests ---

func TestAccessToken_MissingField(t *testing.T) {
	// streamPlaybackAccessToken key missing entirely from data
	api := newTestAPI(t, gqlOK(`{"otherField": "value"}`))

	_, err := api.AccessToken(context.Background(), "testchannel")
	if !errors.Is(err, ErrAccessDenied) {
		t.Errorf("expected ErrAccessDenied for missing field, got %v", err)
	}
}

func TestAccessToken_CustomParams(t *testing.T) {
	var receivedBody map[string]json.RawMessage
	api := newTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		fmt.Fprint(w, `{"data":{"streamPlaybackAccessToken":{"signature":"s","value":"t"}}}`)
	})

	// Use custom access token params
	api.accessTokenParams = map[string]string{
		"playerType": "site",
	}

	token, err := api.AccessToken(context.Background(), "chan")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token.Sig != "s" || token.Token != "t" {
		t.Errorf("unexpected token values: sig=%q token=%q", token.Sig, token.Token)
	}
}

// --- doGQL edge case tests ---

func TestDoGQL_NoFallbackQuery(t *testing.T) {
	api := newTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not reach server when no fallback query exists")
	})

	_, err := api.doGQL(context.Background(), "NonExistentOperation", "", nil, nil)
	if err == nil {
		t.Fatal("expected error for missing fallback query")
	}
	if !strings.Contains(err.Error(), "no query text") {
		t.Errorf("error %q should mention missing query text", err.Error())
	}
}

func TestDoGQL_ExtraHeaders(t *testing.T) {
	const testOp = "twuiTestOpHeaders"
	fallbackQueries[testOp] = `query twuiTestOpHeaders { __typename }`
	defer delete(fallbackQueries, testOp)

	var gotHeader string
	api := newTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Custom-Header")
		fmt.Fprint(w, `{"data":{"__typename":"Query"}}`)
	})

	extraHeaders := map[string]string{
		"X-Custom-Header": "custom-value",
	}
	_, err := api.doGQL(context.Background(), testOp, "", nil, extraHeaders)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotHeader != "custom-value" {
		t.Errorf("X-Custom-Header = %q, want %q", gotHeader, "custom-value")
	}
}

func TestDoGQL_CustomHeaders(t *testing.T) {
	const testOp = "twuiTestOpCustom"
	fallbackQueries[testOp] = `query twuiTestOpCustom { __typename }`
	defer delete(fallbackQueries, testOp)

	var gotHeader string
	handler := func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Api-Header")
		fmt.Fprint(w, `{"data":{"__typename":"Query"}}`)
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	client := &http.Client{
		Transport: &hostRewriter{base: srv.URL, rt: http.DefaultTransport},
	}
	api := NewTwitchAPI(client, "testcid", "testua", map[string]string{
		"X-Api-Header": "api-value",
	}, nil)
	api.integrityToken = "test-integrity-token"
	api.integrityExpiry = time.Now().Add(24 * time.Hour)

	_, err := api.doGQL(context.Background(), testOp, "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotHeader != "api-value" {
		t.Errorf("X-Api-Header = %q, want %q", gotHeader, "api-value")
	}
}

// --- parseGQLRetryAfter tests ---

func TestParseGQLRetryAfter(t *testing.T) {
	cases := []struct {
		header string
		want   time.Duration
	}{
		{"", 0},
		{"0", 0},
		{"invalid", 0},
		{"5", 5 * time.Second},
		{"30", 30 * time.Second},
		// Over the 30-second cap
		{"999", 30 * time.Second},
	}
	for _, c := range cases {
		got := parseGQLRetryAfter(c.header)
		if got != c.want {
			t.Errorf("parseGQLRetryAfter(%q) = %v, want %v", c.header, got, c.want)
		}
	}
}
