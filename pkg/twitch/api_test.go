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
func newTestAPI(t *testing.T, handler http.HandlerFunc) *TwitchAPI {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	client := &http.Client{
		Transport: &hostRewriter{base: srv.URL, rt: http.DefaultTransport},
	}
	return NewTwitchAPI(client, "testcid", "testua", nil, nil)
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

// isPersistedQueryRequest returns true when the request body contains
// a persisted query extension (not a full query text).
func isPersistedQueryRequest(r *http.Request) bool {
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return false
	}
	_, hasQuery := body["query"]
	return !hasQuery
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
