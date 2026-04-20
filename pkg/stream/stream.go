package stream

import (
	"context"
	"io"
)

// Stream is the core interface for media streams.
type Stream interface {
	Open() (io.ReadCloser, error)
	URL() string
}

// StreamInfo carries optional variant metadata for extended JSON output.
type StreamInfo struct {
	Name       string  `json:"name"`
	URL        string  `json:"url"`
	Resolution string  `json:"resolution,omitempty"`
	Bandwidth  int     `json:"bandwidth,omitempty"`
	Codecs     string  `json:"codecs,omitempty"`
	FrameRate  float64 `json:"framerate,omitempty"`
}

// StreamInfoProvider is an optional interface implemented by streams that
// carry per-variant metadata (e.g. HLS variants from a master playlist).
type StreamInfoProvider interface {
	StreamInfo() StreamInfo
}

// Droppable is an optional interface implemented by streams that can signal
// an unintended drop (retry exhaustion), distinct from normal end or
// context cancellation.
type Droppable interface {
	SetOnDrop(fn func(error))
}

// AdBreakNotifier is an optional interface implemented by streams that can
// signal when a new ad break is detected. duration is in seconds; adType is
// the roll type (e.g. "PREROLL", "MIDROLL").
type AdBreakNotifier interface {
	SetOnAdBreak(fn func(duration float64, adType string))
}

// AdEndNotifier is an optional interface implemented by streams that can
// signal when an ad break ends (first non-ad segment after a run of ads).
type AdEndNotifier interface {
	SetOnAdEnd(fn func())
}

// PreRollNotifier is an optional interface implemented by streams that can
// signal when a pre-roll ad is detected before any content has played.
type PreRollNotifier interface {
	SetOnPreRoll(fn func())
}

// AdBypasser is an optional interface implemented by streams that can
// refresh their underlying source in place (new session / token / playlist)
// without signalling EOF to the consumer. Used to try to skip mid-roll ads
// by moving the player onto a freshly-stitched timeline.
type AdBypasser interface {
	BypassAdBreak(ctx context.Context) error
}

// AdFilterDegrader is an optional interface implemented by streams that
// filter ad segments. DegradeAdFilter tells the stream to stop dropping
// ad segments and let them through — used when the bypass pump has
// tried repeatedly without success, so the player gets to play the ads
// rather than freezing on an empty pipe.
type AdFilterDegrader interface {
	DegradeAdFilter()
}
