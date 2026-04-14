package twitch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultClientID  = "kimne78kx3ncx6brgo4mv6wki5h1ko"
	GQLEndpoint      = "https://gql.twitch.tv/gql"
	DefaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:130.0) Gecko/20100101 Firefox/130.0"

	maxGQLRetryAfter = 30 * time.Second
)

// Persisted query hashes for Twitch GQL operations.
const (
	hashAccessToken     = "ed230aa1e33e07eebb8928504583da78a5173989fadfb1ac94be06a04f3cdbe9"
	hashStreamMetadata1 = "b57f9b910f8cd1a4659d894fe7550ccc81ec9052c01e438b290fd66a040b9b93"
	hashViewerCount     = "a5f2e34d626a9f4f5c0204f910bab2194948a9502089be558bb6e779a9e1b3d2"
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
	"StreamMetadata": `query StreamMetadata($channelLogin: String!, $includeIsDJ: Boolean!) {
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
	ClientID          string
	UserAgent         string
	client            *http.Client
	headers           map[string]string
	accessTokenParams map[string]string
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
type gqlFallbackRequest struct {
	OperationName string         `json:"operationName"`
	Query         string         `json:"query"`
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
		client:            client,
		headers:           customHeaders,
		accessTokenParams: accessTokenParams,
	}
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

	extraHeaders := map[string]string{
		"User-Agent": a.UserAgent,
	}

	body, err := a.doGQL(ctx, "PlaybackAccessToken", hashAccessToken, variables, extraHeaders)
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
		"includeIsDJ":  true,
	}

	body, err := a.doGQL(ctx, "StreamMetadata", hashStreamMetadata1, variables, nil)
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
		if data.User.Stream.CreatedAt != "" {
			if t, err := time.Parse(time.RFC3339, data.User.Stream.CreatedAt); err == nil {
				md.StartedAt = t
			}
		}
		if data.User.Stream.Game != nil {
			md.Category = data.User.Stream.Game.Name
		}
	}

	return md, nil
}

// doGQL sends a persisted query and falls back to full query text on hash miss.
func (a *TwitchAPI) doGQL(ctx context.Context, operationName, hash string, variables map[string]any, extraHeaders map[string]string) (json.RawMessage, error) {
	// When hash is empty, skip straight to full query fallback.
	if hash != "" {
		reqBody := gqlRequest{
			OperationName: operationName,
			Extensions: gqlExtensions{
				PersistedQuery: gqlPersistedQuery{
					Version:    1,
					SHA256Hash: hash,
				},
			},
			Variables: variables,
		}

		bodyBytes, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("twitch: marshal gql request: %w", err)
		}

		slog.Debug("GQL request", "operation", operationName)

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

	fallbackBody := gqlFallbackRequest{
		OperationName: operationName,
		Query:         queryText,
		Variables:     variables,
	}
	bodyBytes, err := json.Marshal(fallbackBody)
	if err != nil {
		return nil, fmt.Errorf("twitch: marshal gql fallback request: %w", err)
	}

	return a.doGQLRoundTrip(ctx, bodyBytes, extraHeaders, operationName)
}

// doGQLRoundTrip sends a pre-marshaled GQL request body and processes the response.
func (a *TwitchAPI) doGQLRoundTrip(ctx context.Context, bodyBytes []byte, extraHeaders map[string]string, operationName string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, GQLEndpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("twitch: create gql request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Client-ID", a.ClientID)

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
		retryAfter := parseGQLRetryAfter(resp.Header.Get("Retry-After"))
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

func parseGQLRetryAfter(header string) time.Duration {
	s, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || s <= 0 {
		return 0
	}
	d := time.Duration(s) * time.Second
	if d > maxGQLRetryAfter {
		d = maxGQLRetryAfter
	}
	return d
}
