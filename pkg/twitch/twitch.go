package twitch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/mcs/twui/pkg/session"
	"github.com/mcs/twui/pkg/stream"
	"github.com/mcs/twui/pkg/stream/hls"
)

// TwitchClient provides access to Twitch streams and metadata.
type TwitchClient struct {
	api    *TwitchAPI
	usher  *UsherService
	client *http.Client

	LowLatency      bool
	SupportedCodecs string
	FFmpegPath      string
	Options         *session.Options
}

// New creates a new TwitchClient.
func New(client *http.Client, api *TwitchAPI, usher *UsherService) *TwitchClient {
	return &TwitchClient{
		api:             api,
		usher:           usher,
		client:          client,
		SupportedCodecs: "h264",
		FFmpegPath:      "ffmpeg",
	}
}

// Streams returns available HLS stream variants for a live channel.
func (t *TwitchClient) Streams(ctx context.Context, channel string) (map[string]stream.Stream, error) {
	master, restricted, err := t.fetchMaster(ctx, channel)
	if err != nil {
		return nil, err
	}

	streams, err := t.buildStreams(ctx, master, restricted)
	if err != nil {
		return nil, err
	}

	// Wire an ad-bypass refresher that can re-enter the master-playlist
	// fetch and return the fresh variant URL for a given quality. The
	// bypass hook calls this to rebuild the inner HLS pipeline in place.
	for name, s := range streams {
		if tw := extractTwitchHLS(s); tw != nil {
			quality := name
			tw.RefreshURL = func(ctx context.Context) (string, error) {
				// Fresh identity per refresh; see TwitchAPI.RotateIdentity.
				t.api.RotateIdentity()
				return t.refreshVariantURL(ctx, channel, quality)
			}
		}
	}

	return streams, nil
}

// fetchMaster is the token + master-playlist half of Streams, factored out
// so refreshVariantURL can re-enter it without rebuilding stream objects.
func (t *TwitchClient) fetchMaster(ctx context.Context, channel string) (*hls.MasterPlaylist, []string, error) {
	t.ensureTransportWithHeaders()

	tokenResp, err := t.api.AccessToken(ctx, channel)
	if err != nil {
		return nil, nil, err
	}

	restricted := parseRestrictedBitrates(tokenResp.Token)

	opts := UsherOpts{
		SupportedCodecs:          t.SupportedCodecs,
		AllowSource:              true,
		AllowAudioOnly:           true,
		FastBread:                t.LowLatency,
		Platform:                 "web",
		PlaylistIncludeFramerate: true,
	}
	masterURL := t.usher.ChannelHLS(channel, tokenResp.Sig, tokenResp.Token, opts)

	master, err := t.fetchMasterPlaylist(ctx, masterURL)
	if err != nil {
		return nil, nil, err
	}

	return master, restricted, nil
}

// refreshVariantURL fetches a fresh access token and master playlist for
// channel and returns the variant URL matching quality. Used by
// TwitchHLSStream.BypassAdBreak to relocate mpv onto a new session timeline.
func (t *TwitchClient) refreshVariantURL(ctx context.Context, channel, quality string) (string, error) {
	master, _, err := t.fetchMaster(ctx, channel)
	if err != nil {
		return "", err
	}
	for _, v := range master.Variants {
		if variantName(v) == quality && v.URL != "" {
			return v.URL, nil
		}
	}
	return "", fmt.Errorf("twitch: quality %q not found on refresh", quality)
}

// extractTwitchHLS returns the *TwitchHLSStream inside s, if s is a non-muxed
// annotatedStream. Returns nil for muxed streams (bypass not yet supported
// there) or other wrappers.
func extractTwitchHLS(s stream.Stream) *TwitchHLSStream {
	as, ok := s.(*annotatedStream)
	if !ok {
		return nil
	}
	tw, _ := as.Stream.(*TwitchHLSStream)
	return tw
}

// Metadata fetches stream metadata for a channel.
func (t *TwitchClient) Metadata(ctx context.Context, channel string) (*Metadata, error) {
	md, err := t.api.StreamMetadata(ctx, channel)
	if err != nil {
		return nil, err
	}
	slog.Info("Stream metadata", "author", md.Author, "title", md.Title, "category", md.Category)
	return md, nil
}

func (t *TwitchClient) fetchMasterPlaylist(ctx context.Context, masterURL string) (*hls.MasterPlaylist, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, masterURL, nil)
	if err != nil {
		return nil, fmt.Errorf("twitch: create master playlist request: %w", err)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("twitch: fetch master playlist: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("twitch: read master playlist: %w", err)
	}

	if isHTTPError(resp.StatusCode) {
		errMsg := parseUsherError(body)
		if errMsg != "" {
			return nil, fmt.Errorf("twitch: %s", errMsg)
		}
		return nil, fmt.Errorf("twitch: master playlist HTTP %d", resp.StatusCode)
	}

	master, err := hls.ParseMaster(string(body), masterURL)
	if err != nil {
		return nil, fmt.Errorf("twitch: parse master playlist: %w", err)
	}

	return master, nil
}

func (t *TwitchClient) buildStreams(
	ctx context.Context,
	master *hls.MasterPlaylist,
	restricted []string,
) (map[string]stream.Stream, error) {
	audioMedia := make(map[string]hls.Media)
	for _, m := range master.Media {
		if m.Type == "AUDIO" && m.URI != "" {
			audioMedia[m.GroupID] = m
		}
	}

	type candidate struct {
		name string
		v    hls.Variant
	}
	var candidates []candidate
	for _, v := range master.Variants {
		name := variantName(v)
		if isRestricted(name, restricted) {
			slog.Warn("Skipping restricted bitrate", "quality", name)
			continue
		}
		candidates = append(candidates, candidate{name: name, v: v})
	}

	var accessible []candidate
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	for _, c := range candidates {
		c := c
		g.Go(func() error {
			if err := t.validatePlaylistURL(gctx, c.v.URL); err != nil {
				slog.Debug("Variant not accessible, skipping", "name", c.name, "err", err)
				return nil
			}
			mu.Lock()
			accessible = append(accessible, c)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	streams := make(map[string]stream.Stream)
	for _, c := range accessible {
		videoStream := t.newHLSStream(ctx, c.v.URL)

		info := stream.StreamInfo{
			Name:       c.name,
			URL:        c.v.URL,
			Resolution: c.v.Resolution,
			Bandwidth:  c.v.Bandwidth,
			Codecs:     c.v.Codecs,
			FrameRate:  c.v.FrameRate,
		}

		if c.v.Audio != "" {
			if am, ok := audioMedia[c.v.Audio]; ok && am.URI != "" {
				audioStream := t.newHLSStream(ctx, am.URI)
				muxed := &stream.MuxedStream{
					Video:  NewTwitchHLSStream(videoStream, t.LowLatency).HLSStream,
					Audio:  NewTwitchHLSStream(audioStream, t.LowLatency).HLSStream,
					FFmpeg: t.FFmpegPath,
				}
				streams[c.name] = &annotatedStream{Stream: muxed, info: info}
				continue
			}
		}

		twitchStream := NewTwitchHLSStream(videoStream, t.LowLatency)
		streams[c.name] = &annotatedStream{Stream: twitchStream, info: info}
	}

	if len(streams) == 0 {
		return nil, ErrChannelOffline
	}

	return streams, nil
}

func (t *TwitchClient) newHLSStream(ctx context.Context, playlistURL string) *hls.HLSStream {
	h := &hls.HLSStream{
		StreamURL: playlistURL,
		Client:    t.client,
		Ctx:       ctx,
	}

	if t.LowLatency {
		h.LiveEdge = 2
		h.SegmentStreamData = true
	}

	if t.Options != nil {
		if attempts := t.Options.GetInt("hls-segment-attempts"); attempts > 0 {
			h.MaxSegmentAttempts = attempts
		}
		if attempts := t.Options.GetInt("hls-playlist-reload-attempts"); attempts > 0 {
			h.MaxPlaylistAttempts = attempts
		}
	}

	return h
}

func (t *TwitchClient) validatePlaylistURL(ctx context.Context, playlistURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, playlistURL, nil)
	if err != nil {
		return err
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()

	if resp.StatusCode == http.StatusMethodNotAllowed {
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, playlistURL, nil)
		if err != nil {
			return err
		}
		resp, err = t.client.Do(req)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	if isHTTPError(resp.StatusCode) {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// isHTTPError reports whether status is a 4xx/5xx response — the threshold
// the Twitch endpoints treat as "request failed, body is an error payload."
func isHTTPError(status int) bool { return status >= 400 }

func (t *TwitchClient) ensureTransportWithHeaders() {
	if t.client.Transport == nil {
		t.client.Transport = &twitchTransport{base: http.DefaultTransport}
	} else if _, ok := t.client.Transport.(*twitchTransport); !ok {
		t.client.Transport = &twitchTransport{base: t.client.Transport}
	}
}

type twitchTransport struct {
	base http.RoundTripper
}

// twitchPlayerOrigin is the Referer/Origin value sent with anonymous Twitch
// requests — Twitch rejects some endpoints without it.
const twitchPlayerOrigin = "https://player.twitch.tv"

func (t *twitchTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Hostname()
	if strings.HasSuffix(host, "twitch.tv") || strings.HasSuffix(host, "ttvnw.net") {
		req = req.Clone(req.Context())
		if req.Header.Get("Referer") == "" {
			req.Header.Set("Referer", twitchPlayerOrigin)
		}
		if req.Header.Get("Origin") == "" {
			req.Header.Set("Origin", twitchPlayerOrigin)
		}
	}
	return t.base.RoundTrip(req)
}

func variantName(v hls.Variant) string {
	if v.Name != "" {
		return v.Name
	}
	if v.Resolution != "" {
		parts := strings.SplitN(v.Resolution, "x", 2)
		if len(parts) == 2 {
			name := parts[1] + "p"
			if v.FrameRate > 0 && v.FrameRate != 30 {
				name = fmt.Sprintf("%sp%.0f", parts[1], v.FrameRate)
			}
			return name
		}
	}
	if v.Bandwidth > 0 {
		return fmt.Sprintf("%dk", v.Bandwidth/1000)
	}
	return "unknown"
}

func isRestricted(name string, restricted []string) bool {
	for _, r := range restricted {
		if strings.EqualFold(name, r) {
			return true
		}
	}
	return false
}

func parseRestrictedBitrates(tokenJSON string) []string {
	var token struct {
		ChanSub struct {
			RestrictedBitrates []string `json:"restricted_bitrates"`
		} `json:"chansub"`
	}
	if err := json.Unmarshal([]byte(tokenJSON), &token); err != nil {
		return nil
	}
	return token.ChanSub.RestrictedBitrates
}

func parseUsherError(body []byte) string {
	var errArray []struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &errArray) == nil && len(errArray) > 0 && errArray[0].Error != "" {
		return errArray[0].Error
	}

	var errObj struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &errObj) == nil {
		if errObj.Message != "" {
			return errObj.Message
		}
		if errObj.Error != "" {
			return errObj.Error
		}
	}

	return ""
}

// annotatedStream wraps a stream.Stream with StreamInfo metadata.
type annotatedStream struct {
	stream.Stream
	info stream.StreamInfo
}

func (a *annotatedStream) StreamInfo() stream.StreamInfo {
	return a.info
}

func (a *annotatedStream) SetOnDrop(fn func(error)) {
	if d, ok := a.Stream.(stream.Droppable); ok {
		d.SetOnDrop(fn)
	}
}

func (a *annotatedStream) SetOnAdBreak(fn func(duration float64, adType string)) {
	if n, ok := a.Stream.(stream.AdBreakNotifier); ok {
		n.SetOnAdBreak(fn)
	}
}

func (a *annotatedStream) SetOnAdEnd(fn func()) {
	if n, ok := a.Stream.(stream.AdEndNotifier); ok {
		n.SetOnAdEnd(fn)
	}
}

func (a *annotatedStream) SetOnPreRoll(fn func()) {
	if n, ok := a.Stream.(stream.PreRollNotifier); ok {
		n.SetOnPreRoll(fn)
	}
}

// BypassAdBreak forwards to the inner stream when it implements AdBypasser.
// Muxed streams currently return an error — bypass would need to re-sync two
// HLS pipelines, and we haven't built that yet.
func (a *annotatedStream) BypassAdBreak(ctx context.Context) error {
	if b, ok := a.Stream.(stream.AdBypasser); ok {
		return b.BypassAdBreak(ctx)
	}
	return errors.New("twitch: ad-break bypass not supported on this stream")
}

// DegradeAdFilter forwards to the inner stream when it implements it.
func (a *annotatedStream) DegradeAdFilter() {
	if d, ok := a.Stream.(stream.AdFilterDegrader); ok {
		d.DegradeAdFilter()
	}
}

var _ stream.StreamInfoProvider = (*annotatedStream)(nil)
var _ stream.AdBypasser = (*annotatedStream)(nil)
var _ stream.AdFilterDegrader = (*annotatedStream)(nil)
