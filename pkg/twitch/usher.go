package twitch

import (
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
)

// UsherOpts controls the query parameters sent to the Usher playlist endpoints.
type UsherOpts struct {
	SupportedCodecs          string
	AllowSource              bool
	AllowAudioOnly           bool
	FastBread                bool   // low-latency
	Platform                 string // "web"
	PlaylistIncludeFramerate bool
}

// UsherService builds Usher HLS playlist URLs for live channels and VODs.
type UsherService struct {
	client *http.Client
}

// NewUsherService creates a new UsherService using the given HTTP client.
func NewUsherService(client *http.Client) *UsherService {
	return &UsherService{client: client}
}

// ChannelHLS returns the Usher master playlist URL for a live channel.
//
// Live streams use sig/token as the auth parameter names.
func (u *UsherService) ChannelHLS(channel, sig, token string, opts UsherOpts) string {
	params := u.commonParams(opts)
	params.Set("sig", sig)
	params.Set("token", token)

	return fmt.Sprintf(
		"https://usher.ttvnw.net/api/v2/channel/hls/%s.m3u8?%s",
		url.PathEscape(channel),
		params.Encode(),
	)
}

// commonParams builds the shared query parameters for both live and VOD Usher URLs.
func (u *UsherService) commonParams(opts UsherOpts) url.Values {
	params := url.Values{}

	// Random cache-buster
	params.Set("p", strconv.Itoa(rand.Intn(1000000)))

	params.Set("allow_source", strconv.FormatBool(opts.AllowSource))
	params.Set("allow_audio_only", strconv.FormatBool(opts.AllowAudioOnly))
	params.Set("fast_bread", strconv.FormatBool(opts.FastBread))

	if opts.SupportedCodecs != "" {
		params.Set("supported_codecs", opts.SupportedCodecs)
	}

	if opts.Platform != "" {
		params.Set("platform", opts.Platform)
	}

	if opts.PlaylistIncludeFramerate {
		params.Set("playlist_include_framerate", "true")
	}

	return params
}
