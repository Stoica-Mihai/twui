package twitch

import (
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mcs/twui/pkg/stream/hls"
)

// TwitchSegment extends hls.Segment with Twitch-specific flags.
type TwitchSegment struct {
	hls.Segment
	Ad       bool
	Prefetch bool
}

// adBreakInfo tracks a detected advertisement break.
type adBreakInfo struct {
	ID       string
	Duration float64
	Type     string
}

// TwitchHLSStream wraps hls.HLSStream and sets up Twitch-specific hooks
// for ad filtering, prefetch segment handling, and discontinuity correction.
type TwitchHLSStream struct {
	*hls.HLSStream
	lowLatency bool

	mu         sync.Mutex
	adBreaks   []adBreakInfo
	hadContent bool
	lastWasAd  bool
	adNotified bool

	dateRanges         []hls.DateRange
	cachedAdDateRanges []hls.DateRange

	AdTitlePatterns []string
	AdClassPatterns []string
	AdIDPrefixes    []string

	OnAdBreak func(duration float64, adType string)
	OnAdEnd   func()
	OnPreRoll func()
}

// SetOnAdBreak implements stream.AdBreakNotifier.
func (t *TwitchHLSStream) SetOnAdBreak(fn func(duration float64, adType string)) {
	t.mu.Lock()
	t.OnAdBreak = fn
	t.mu.Unlock()
}

// SetOnAdEnd implements stream.AdEndNotifier.
func (t *TwitchHLSStream) SetOnAdEnd(fn func()) {
	t.mu.Lock()
	t.OnAdEnd = fn
	t.mu.Unlock()
}

// SetOnPreRoll implements stream.PreRollNotifier.
func (t *TwitchHLSStream) SetOnPreRoll(fn func()) {
	t.mu.Lock()
	t.OnPreRoll = fn
	t.mu.Unlock()
}

// NewTwitchHLSStream creates a TwitchHLSStream wrapping the given HLSStream.
func NewTwitchHLSStream(hlsStream *hls.HLSStream, lowLatency bool) *TwitchHLSStream {
	t := &TwitchHLSStream{
		HLSStream:       hlsStream,
		lowLatency:      lowLatency,
		AdTitlePatterns: []string{"Amazon"},
		AdClassPatterns: []string{"twitch-stitched-ad"},
		AdIDPrefixes:    []string{"stitched-ad-"},
	}

	hlsStream.ProcessSegments = t.processSegments
	hlsStream.ShouldFilter = t.shouldFilter
	hlsStream.OnOpen = func() {
		slog.Info("Will skip ad segments")
	}
	hlsStream.OnPlaylistParsed = t.onPlaylistParsed

	return t
}

func (t *TwitchHLSStream) onPlaylistParsed(playlist *hls.MediaPlaylist) {
	t.mu.Lock()
	t.dateRanges = playlist.DateRanges
	t.cachedAdDateRanges = t.adDateRangesLocked()
	t.mu.Unlock()
}

func (t *TwitchHLSStream) processSegments(segments []hls.Segment, isFirst bool) []hls.Segment {
	if len(segments) == 0 {
		return segments
	}

	t.mu.Lock()
	adDateRanges := t.adDateRangesLocked()
	titlePatterns := t.AdTitlePatterns
	hadContent := t.hadContent
	onPreRoll := t.OnPreRoll
	t.mu.Unlock()

	t.logNewAdBreaks(adDateRanges)

	type classified struct {
		seg      hls.Segment
		isAd     bool
		prefetch bool
	}
	items := make([]classified, 0, len(segments))
	allAds := true

	for _, seg := range segments {
		isAd := isSegmentAd(seg, adDateRanges, titlePatterns)
		prefetch := seg.Prefetch

		if isAd {
			seg.Duration = 0
		} else {
			allAds = false
		}

		items = append(items, classified{seg: seg, isAd: isAd, prefetch: prefetch})
	}

	if isFirst && allAds && !hadContent {
		slog.Info("Waiting for pre-roll ads to finish, be patient")
		if onPreRoll != nil {
			onPreRoll()
		}
	}
	if !allAds {
		t.mu.Lock()
		t.hadContent = true
		t.mu.Unlock()
	}

	if !t.lowLatency {
		filtered := items[:0]
		for _, item := range items {
			if !item.prefetch {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}

	for i := 1; i < len(items); i++ {
		if !items[i].isAd && !items[i-1].isAd && items[i].seg.Discontinuity {
			items[i].seg.Discontinuity = false
		}
	}

	result := make([]hls.Segment, 0, len(items))
	for _, item := range items {
		result = append(result, item.seg)
	}

	return result
}

func (t *TwitchHLSStream) shouldFilter(seg hls.Segment) bool {
	t.mu.Lock()
	cachedAdDateRanges := t.cachedAdDateRanges
	titlePatterns := t.AdTitlePatterns
	lastWasAd := t.lastWasAd
	onAdEnd := t.OnAdEnd
	t.mu.Unlock()

	isAd := isSegmentAd(seg, cachedAdDateRanges, titlePatterns)

	if isAd {
		if !lastWasAd {
			slog.Debug("Filtering ad segment", "num", seg.Num, "title", seg.Title)
			if t.HLSStream.Filtered != nil {
				t.HLSStream.Filtered.Pause()
			}
		}
		t.mu.Lock()
		t.lastWasAd = true
		t.mu.Unlock()
		return true
	}

	if lastWasAd {
		if t.HLSStream.Filtered != nil {
			t.HLSStream.Filtered.Resume()
		}
		if onAdEnd != nil {
			onAdEnd()
		}
	}
	t.mu.Lock()
	t.lastWasAd = false
	t.adNotified = false
	t.mu.Unlock()
	return false
}

func (t *TwitchHLSStream) adDateRangesLocked() []hls.DateRange {
	var result []hls.DateRange
	for _, dr := range t.dateRanges {
		if isAdDateRange(dr, t.AdClassPatterns, t.AdIDPrefixes) {
			if dr.Duration <= 0 {
				if filledStr, ok := dr.X["X-TV-TWITCH-AD-POD-FILLED-DURATION"]; ok {
					if filled := parseFloatSafe(filledStr); filled > 0 {
						dr.Duration = filled
					}
				}
			}
			result = append(result, dr)
		}
	}
	return result
}

func isAdDateRange(dr hls.DateRange, classPatterns []string, idPrefixes []string) bool {
	for _, p := range classPatterns {
		if dr.Class == p {
			return true
		}
	}
	for _, prefix := range idPrefixes {
		if strings.HasPrefix(dr.ID, prefix) {
			return true
		}
	}
	return false
}

func isSegmentAd(seg hls.Segment, adDateRanges []hls.DateRange, titlePatterns []string) bool {
	for _, pattern := range titlePatterns {
		if strings.Contains(seg.Title, pattern) {
			return true
		}
	}

	if !seg.Date.IsZero() {
		for _, dr := range adDateRanges {
			if dr.Start.IsZero() {
				continue
			}
			duration := dr.Duration
			if duration <= 0 {
				duration = dr.PlannedDuration
			}
			if duration <= 0 {
				continue
			}
			end := dr.Start.Add(time.Duration(duration * float64(time.Second)))
			if !seg.Date.Before(dr.Start) && seg.Date.Before(end) {
				return true
			}
		}
	}

	return false
}

func (t *TwitchHLSStream) logNewAdBreaks(adDateRanges []hls.DateRange) {
	for _, dr := range adDateRanges {
		t.mu.Lock()
		alreadySeen := t.hasAdBreakLocked(dr.ID)
		t.mu.Unlock()
		if alreadySeen {
			continue
		}

		duration := dr.Duration
		if filledDur, ok := dr.X["X-TV-TWITCH-AD-POD-FILLED-DURATION"]; ok {
			if parsed := parseFloatSafe(filledDur); parsed > 0 {
				duration = parsed
			}
		}

		adType := "unknown"
		if rollType, ok := dr.X["X-TV-TWITCH-AD-ROLL-TYPE"]; ok && rollType != "" {
			adType = rollType
		}

		slog.Info("Detected advertisement break", "id", dr.ID, "duration", duration, "type", adType)

		t.mu.Lock()
		onAdBreak := t.OnAdBreak
		alreadyNotified := t.adNotified
		if onAdBreak != nil && !alreadyNotified {
			t.adNotified = true
		}
		t.mu.Unlock()
		if onAdBreak != nil && !alreadyNotified {
			onAdBreak(duration, adType)
		}

		t.mu.Lock()
		if len(t.adBreaks) >= 10 {
			t.adBreaks = t.adBreaks[1:]
		}
		t.adBreaks = append(t.adBreaks, adBreakInfo{ID: dr.ID, Duration: duration, Type: adType})
		t.mu.Unlock()
	}
}

func (t *TwitchHLSStream) hasAdBreakLocked(id string) bool {
	for _, ab := range t.adBreaks {
		if ab.ID == id {
			return true
		}
	}
	return false
}

func parseFloatSafe(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v
}
