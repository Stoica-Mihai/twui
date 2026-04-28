package twitch

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mcs/twui/pkg/session"
)

const (
	DefaultClientID   = "kimne78kx3ncx6brgo4mv6wki5h1ko"
	GQLEndpoint       = "https://gql.twitch.tv/gql"
	IntegrityEndpoint = "https://passport.twitch.tv/integrity"
	DefaultUserAgent  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:130.0) Gecko/20100101 Firefox/130.0"

	integrityExpiryBuffer = time.Minute // refresh before this much time remains
)

// Persisted query hashes for Twitch GQL operations.
// Empty hash → skip straight to fallback query text (see doGQL).
const (
	hashAccessToken = "ed230aa1e33e07eebb8928504583da78a5173989fadfb1ac94be06a04f3cdbe9"
	hashViewerCount = "a5f2e34d626a9f4f5c0204f910bab2194948a9502089be558bb6e779a9e1b3d2"
	// StreamMetadata hash cleared: the persisted query mapped to this hash omits
	// viewersCount, displayName, and stream.title, causing blank metadata.
	// Always use fallback query which selects all needed fields.
)

// fallbackQueries holds full GQL query text for each operation, used when
// persisted query hashes are stale (PersistedQueryNotFound).
var fallbackQueries = map[string]string{
	"PlaybackAccessToken": `query PlaybackAccessToken($login: String!, $isLive: Boolean!, $vodID: ID!, $isVod: Boolean!, $playerType: String!, $platform: String!) {
  streamPlaybackAccessToken(channelName: $login, params: {platform: $platform, playerBackend: "mediaplayer", playerType: $playerType}) @include(if: $isLive) {
    value
    signature
    __typename
  }
  videoPlaybackAccessToken(id: $vodID, params: {platform: $platform, playerBackend: "mediaplayer", playerType: $playerType}) @include(if: $isVod) {
    value
    signature
    __typename
  }
}`,
	"StreamMetadata": `query StreamMetadata($channelLogin: String!) {
  user(login: $channelLogin) {
    id
    displayName
    profileImageURL(width: 70)
    lastBroadcast {
      title
      __typename
    }
    stream {
      title
      viewersCount
      createdAt
      game {
        name
        __typename
      }
      __typename
    }
    __typename
  }
}`,
	"VideoPlayerStreamInfoOverlayChannel": `query VideoPlayerStreamInfoOverlayChannel($channel: String!) {
  user(login: $channel) {
    stream {
      viewersCount
      __typename
    }
    __typename
  }
}`,
}

// TwitchAPI handles Twitch GQL persisted queries for access tokens and metadata.
type TwitchAPI struct {
	ClientID  string
	UserAgent string

	// ProxyURL, when non-empty, replaces gql.twitch.tv and passport.twitch.tv
	// with {ProxyURL}/gql and {ProxyURL}/integrity. The proxy must relay both
	// endpoints from a single egress IP so the integrity token Twitch issues
	// stays bound to the same origin that later sends the GQL call. Used to
	// shift the apparent source of the PlaybackAccessToken request away from
	// a residential ISP, which suppresses most server-side mid-roll stitching.
	ProxyURL string

	// identityMu guards deviceID and clientSessionID against concurrent
	// reads (in setAuthHeaders) and writes (in RotateIdentity).
	identityMu      sync.RWMutex
	deviceID        string
	clientSessionID string

	client            *http.Client
	headers           map[string]string
	accessTokenParams map[string]string

	integrityMu     sync.Mutex
	integrityToken  string
	integrityExpiry time.Time
}

// DeviceID returns the current X-Device-ID. Safe to call concurrently
// with RotateIdentity.
func (a *TwitchAPI) DeviceID() string {
	a.identityMu.RLock()
	defer a.identityMu.RUnlock()
	return a.deviceID
}

// RotateIdentity assigns fresh X-Device-ID and Client-Session-Id values
// and invalidates the cached integrity token (which Twitch binds to the
// previous identity). The next outgoing request will look like a
// brand-new viewer — used by ad-bypass to defeat sticky stitching that
// keys on (deviceID, sessionID, ip).
func (a *TwitchAPI) RotateIdentity() {
	a.identityMu.Lock()
	a.deviceID = newDeviceID()
	a.clientSessionID = newClientSessionID()
	dev := a.deviceID
	a.identityMu.Unlock()

	a.integrityMu.Lock()
	a.integrityToken = ""
	a.integrityExpiry = time.Time{}
	a.integrityMu.Unlock()

	slog.Debug("Twitch identity rotated", "device", dev)
}

func (a *TwitchAPI) gqlURL() string {
	if a.ProxyURL != "" {
		return strings.TrimRight(a.ProxyURL, "/") + "/gql"
	}
	return GQLEndpoint
}

func (a *TwitchAPI) integrityURL() string {
	if a.ProxyURL != "" {
		return strings.TrimRight(a.ProxyURL, "/") + "/integrity"
	}
	return IntegrityEndpoint
}

// newDeviceID generates a random UUID v4 string for use as X-Device-ID.
func newDeviceID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// newClientSessionID generates a 16-character hex string for use as Client-Session-Id.
func newClientSessionID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// AccessTokenResponse holds the signature and token returned by the
// PlaybackAccessToken GQL query.
type AccessTokenResponse struct {
	Sig   string `json:"signature"`
	Token string `json:"value"`
}

// gqlRequest is the JSON body sent to the GQL endpoint for persisted queries.
type gqlRequest struct {
	OperationName string         `json:"operationName"`
	Extensions    gqlExtensions  `json:"extensions"`
	Variables     map[string]any `json:"variables"`
}

type gqlExtensions struct {
	PersistedQuery gqlPersistedQuery `json:"persistedQuery"`
}

type gqlPersistedQuery struct {
	Version    int    `json:"version"`
	SHA256Hash string `json:"sha256Hash"`
}

// gqlFallbackRequest is the JSON body sent when falling back to a full query.
// Extensions is included so the server can register the query hash (APQ registration).
type gqlFallbackRequest struct {
	OperationName string         `json:"operationName"`
	Query         string         `json:"query"`
	Extensions    gqlExtensions  `json:"extensions"`
	Variables     map[string]any `json:"variables"`
}

// gqlResponse is the generic wrapper for GQL responses.
type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []gqlError      `json:"errors,omitempty"`
}

type gqlError struct {
	Message string `json:"message"`
}

// NewTwitchAPI creates a new TwitchAPI.
func NewTwitchAPI(client *http.Client, clientID, userAgent string, customHeaders map[string]string, accessTokenParams map[string]string) *TwitchAPI {
	if clientID == "" {
		clientID = DefaultClientID
	}
	if userAgent == "" {
		userAgent = DefaultUserAgent
	}
	return &TwitchAPI{
		ClientID:          clientID,
		UserAgent:         userAgent,
		deviceID:          newDeviceID(),
		clientSessionID:   newClientSessionID(),
		client:            client,
		headers:           customHeaders,
		accessTokenParams: accessTokenParams,
	}
}

// getIntegrityToken returns a cached Client-Integrity token, fetching a fresh one when expired.
// If the fetch fails it returns "" — callers continue without the header.
func (a *TwitchAPI) getIntegrityToken(ctx context.Context) string {
	a.integrityMu.Lock()
	defer a.integrityMu.Unlock()
	if a.integrityToken != "" && time.Now().Before(a.integrityExpiry.Add(-integrityExpiryBuffer)) {
		return a.integrityToken
	}
	token, expiry := a.fetchIntegrityToken(ctx)
	if token != "" {
		a.integrityToken = token
		a.integrityExpiry = expiry
	}
	return token
}

// setAuthHeaders writes the Twitch client identity headers onto req. Called
// by every GQL/integrity request; keeps the header set in one place so
// Client-ID, Client-Session-Id, X-Device-ID and User-Agent stay in sync.
func (a *TwitchAPI) setAuthHeaders(req *http.Request) {
	a.identityMu.RLock()
	dev := a.deviceID
	sess := a.clientSessionID
	a.identityMu.RUnlock()

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Client-ID", a.ClientID)
	req.Header.Set("Client-Session-Id", sess)
	req.Header.Set("X-Device-ID", dev)
	req.Header.Set("User-Agent", a.UserAgent)
}

// fetchIntegrityToken makes a single request to the Twitch passport endpoint.
func (a *TwitchAPI) fetchIntegrityToken(ctx context.Context) (string, time.Time) {
	url := a.integrityURL()
	slog.Debug("integrity request", "url", url, "viaProxy", a.ProxyURL != "")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader("{}"))
	if err != nil {
		return "", time.Time{}
	}
	a.setAuthHeaders(req)

	resp, err := a.client.Do(req)
	if err != nil {
		slog.Debug("integrity token fetch failed", "err", err)
		return "", time.Time{}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		slog.Debug("integrity token: bad status", "status", resp.StatusCode, "body", string(body))
		return "", time.Time{}
	}

	var ir struct {
		Token      string `json:"token"`
		Expiration string `json:"expiration"`
	}
	if err := json.Unmarshal(body, &ir); err != nil || ir.Token == "" {
		return "", time.Time{}
	}

	expiry, err := time.Parse(time.RFC3339, ir.Expiration)
	if err != nil {
		expiry = time.Now().Add(5 * time.Minute)
	}
	slog.Debug("integrity token fetched", "expiry", expiry)
	return ir.Token, expiry
}

// AccessToken fetches a live stream access token via the PlaybackAccessToken
// persisted query. The id is the channel login name.
func (a *TwitchAPI) AccessToken(ctx context.Context, id string) (*AccessTokenResponse, error) {
	variables := map[string]any{
		"isLive":     true,
		"isVod":      false,
		"login":      id,
		"vodID":      "",
		"playerType": "embed",
		"platform":   "site",
	}
	for k, v := range a.accessTokenParams {
		variables[k] = v
	}

	body, err := a.doGQL(ctx, "PlaybackAccessToken", hashAccessToken, variables, nil)
	if err != nil {
		return nil, fmt.Errorf("twitch: fetch access token: %w", err)
	}

	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, fmt.Errorf("twitch: parse access token response: %w", err)
	}

	raw, ok := wrapper["streamPlaybackAccessToken"]
	if !ok || string(raw) == "null" {
		return nil, fmt.Errorf("twitch: access denied — Client-ID or User-Agent may be rejected: %w", ErrAccessDenied)
	}

	var token AccessTokenResponse
	if err := json.Unmarshal(raw, &token); err != nil {
		return nil, fmt.Errorf("twitch: parse access token: %w", err)
	}

	return &token, nil
}

// StreamMetadata fetches metadata for a live channel.
func (a *TwitchAPI) StreamMetadata(ctx context.Context, channel string) (*Metadata, error) {
	variables := map[string]any{
		"channelLogin": channel,
	}

	body, err := a.doGQL(ctx, "StreamMetadata", "", variables, nil)
	if err != nil {
		return nil, fmt.Errorf("twitch: fetch stream metadata: %w", err)
	}

	var data struct {
		User *struct {
			ID              string `json:"id"`
			DisplayName     string `json:"displayName"`
			ProfileImageURL string `json:"profileImageURL"`
			LastBroadcast   *struct {
				Title string `json:"title"`
			} `json:"lastBroadcast"`
			Stream *struct {
				Title        string `json:"title"`
				ViewersCount int    `json:"viewersCount"`
				CreatedAt    string `json:"createdAt"`
				Game         *struct {
					Name string `json:"name"`
				} `json:"game"`
			} `json:"stream"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("twitch: parse stream metadata: %w", err)
	}

	if data.User == nil {
		return nil, fmt.Errorf("%w: %q", ErrChannelNotFound, channel)
	}

	md := &Metadata{
		ID:        data.User.ID,
		Author:    data.User.DisplayName,
		AvatarURL: data.User.ProfileImageURL,
	}

	if data.User.LastBroadcast != nil {
		md.Title = data.User.LastBroadcast.Title
	}

	if data.User.Stream != nil {
		if data.User.Stream.Title != "" {
			md.Title = data.User.Stream.Title
		}
		md.ViewerCount = data.User.Stream.ViewersCount
		md.StartedAt = parseRFC3339(data.User.Stream.CreatedAt)
		if data.User.Stream.Game != nil {
			md.Category = data.User.Stream.Game.Name
		}
	}

	return md, nil
}

// marshalGQLBody serializes a GQL request envelope. Centralized so persisted
// and fallback paths produce identical errors on marshal failure (unlikely
// in practice — these types are plain JSON).
func marshalGQLBody(req any) ([]byte, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("twitch: marshal gql request: %w", err)
	}
	return b, nil
}

// doGQL sends a persisted query and falls back to full query text on hash miss.
func (a *TwitchAPI) doGQL(ctx context.Context, operationName, hash string, variables map[string]any, extraHeaders map[string]string) (json.RawMessage, error) {
	// When hash is empty, skip straight to full query fallback.
	if hash != "" {
		bodyBytes, err := marshalGQLBody(gqlRequest{
			OperationName: operationName,
			Extensions: gqlExtensions{
				PersistedQuery: gqlPersistedQuery{Version: 1, SHA256Hash: hash},
			},
			Variables: variables,
		})
		if err != nil {
			return nil, err
		}

		data, err := a.doGQLRoundTrip(ctx, bodyBytes, extraHeaders, operationName)
		if err == nil {
			return data, nil
		}

		if !isPersistedQueryNotFound(err) {
			return nil, err
		}
	}

	queryText, ok := fallbackQueries[operationName]
	if !ok {
		return nil, fmt.Errorf("twitch: no query text for operation %q", operationName)
	}

	slog.Debug("GQL fallback query", "operation", operationName)

	sum := sha256.Sum256([]byte(queryText))
	queryHash := hex.EncodeToString(sum[:])
	bodyBytes, err := marshalGQLBody(gqlFallbackRequest{
		OperationName: operationName,
		Query:         queryText,
		Extensions: gqlExtensions{
			PersistedQuery: gqlPersistedQuery{Version: 1, SHA256Hash: queryHash},
		},
		Variables: variables,
	})
	if err != nil {
		return nil, err
	}

	return a.doGQLRoundTrip(ctx, bodyBytes, extraHeaders, operationName)
}

// doGQLRoundTrip sends a pre-marshaled GQL request body and processes the response.
func (a *TwitchAPI) doGQLRoundTrip(ctx context.Context, bodyBytes []byte, extraHeaders map[string]string, operationName string) (json.RawMessage, error) {
	url := a.gqlURL()
	slog.Debug("GQL request", "operation", operationName, "url", url, "viaProxy", a.ProxyURL != "")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("twitch: create gql request: %w", err)
	}

	a.setAuthHeaders(req)
	if token := a.getIntegrityToken(ctx); token != "" {
		req.Header.Set("Client-Integrity", token)
	}

	for k, v := range a.headers {
		req.Header.Set(k, v)
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("twitch: gql request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("twitch: read gql response: %w", err)
	}

	slog.Debug("GQL response", "operation", operationName, "status", resp.StatusCode)

	if resp.StatusCode == http.StatusTooManyRequests {
		retryAfter := session.ParseRetryAfter(resp.Header.Get("Retry-After"))
		return nil, &RateLimitedError{RetryAfter: retryAfter}
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("twitch: access denied: %w", ErrAccessDenied)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gql: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	var gqlResp gqlResponse
	if err := json.Unmarshal(respBody, &gqlResp); err != nil {
		return nil, fmt.Errorf("twitch: parse gql response: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		return nil, &gqlServerError{Message: gqlResp.Errors[0].Message}
	}

	return gqlResp.Data, nil
}

// gqlServerError wraps a GQL error message.
type gqlServerError struct {
	Message string
}

func (e *gqlServerError) Error() string {
	return fmt.Sprintf("twitch: gql error: %s", e.Message)
}

func (e *gqlServerError) Unwrap() error {
	return ErrGQLServerError
}

func isPersistedQueryNotFound(err error) bool {
	var gqlErr *gqlServerError
	if !errors.As(err, &gqlErr) {
		return false
	}
	return strings.Contains(gqlErr.Message, "PersistedQueryNotFound")
}

// parseRFC3339 parses an RFC3339 timestamp, returning zero time on empty or invalid input.
func parseRFC3339(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, s)
	return t
}
