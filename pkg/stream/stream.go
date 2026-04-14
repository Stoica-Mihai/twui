package stream

import "io"

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
