package hls

import "time"

// ByteRange represents an HTTP byte range for partial segment requests.
type ByteRange struct {
	Length int
	Offset int
}

// Key holds segment encryption information.
type Key struct {
	Method string // "AES-128", "NONE"
	URI    string
	IV     []byte
}

// MapEntry represents an EXT-X-MAP initialization segment.
type MapEntry struct {
	URI       string
	ByteRange *ByteRange
}

// DateRange represents an EXT-X-DATERANGE tag.
type DateRange struct {
	ID              string
	Class           string
	Start           time.Time
	End             time.Time
	Duration        float64
	PlannedDuration float64
	EndOnNext       bool
	X               map[string]string // X-prefixed attributes
}

// Segment represents a single HLS media segment.
type Segment struct {
	Num           int
	URL           string
	Duration      float64
	Title         string
	ByteRange     *ByteRange
	Key           *Key
	Date          time.Time
	Discontinuity bool
	Map           *MapEntry
	Prefetch      bool
}
