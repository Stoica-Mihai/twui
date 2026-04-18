package twitch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mcs/twui/pkg/stream"
	"github.com/mcs/twui/pkg/stream/hls"
	"github.com/mcs/twui/pkg/stream/tsbridge"
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

	// RefreshURL, when set, returns a freshly-tokened playlist URL for the
	// same channel/quality. BypassAdBreak calls it to rebuild the inner
	// HLS pipeline on the fly.
	RefreshURL func(ctx context.Context) (string, error)

	// outer is the FilteredStream the consumer (mpv) actually reads from.
	// Stays constant across bypass swaps so pause/resume keeps addressing
	// the single reader the player holds; the embedded HLSStream.Filtered
	// drifts out of date after a swap and must not be used for control.
	outer *stream.FilteredStream

	// bridge sits between outer and the consumer and rewrites TS PCR/PTS
	// values so the timeline stays continuous across bypass swaps.
	// Populated on first Open.
	bridge *tsbridge.Bridge

	// lastEmittedSeq is the highest EXT-X-MEDIA-SEQUENCE number of any
	// content segment that's been handed to the writer across every
	// session this wrapper has opened. BypassAdBreak uses it to filter
	// out overlapping segments from a fresh session's playlist window,
	// so the player doesn't see the same video content replayed with
	// different timestamps each time the bypass pump fires.
	lastEmittedSeq int

	// bypassInFlight is set while a BypassAdBreak call is running; a
	// concurrent caller finds it set and drops its request rather than
	// queueing behind it. Protected by mu. This is the only bypass
	// throttle: it naturally paces retries to Twitch's ~2-4s playlist
	// refresh cadence, which experimentally is what keeps content
	// flowing through the ad window.
	bypassInFlight bool
}

// ErrBypassInFlight is returned by BypassAdBreak when another bypass is
// currently executing. Callers should treat it as a successful dedup and
// move on rather than retrying.
var ErrBypassInFlight = errors.New("twitch: bypass skipped, already in progress")

// ErrBypassPreContent is returned by BypassAdBreak before any content
// has been observed on the stream (preroll). Bypassing at that point
// just cycles a fresh token with the same preroll stitched in.
var ErrBypassPreContent = errors.New("twitch: bypass skipped, preroll")

// ErrBypassNotInAd is returned by BypassAdBreak when the stream is
// currently playing content. Bypassing during content opens a new
// session whose live edge overlaps the segments we've already handed
// to the player; processSegments' lastEmittedSeq filter then drops
// everything, starving the pipe until the new session's playlist
// advances past our high-water mark. The pump wakes this up once per
// tick during the post-ad debounce window — all of those calls would
// stall the player, so we return early and let the caller treat it
// as a skip.
var ErrBypassNotInAd = errors.New("twitch: bypass skipped, not in ad break")

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

	t.wireHooks(hlsStream)
	return t
}

// wireHooks attaches the TwitchHLSStream's ad-detection hooks onto an
// inner HLSStream. Called both by NewTwitchHLSStream and BypassAdBreak
// so the freshly-built inner stream routes callbacks back to this wrapper.
func (t *TwitchHLSStream) wireHooks(h *hls.HLSStream) {
	h.ProcessSegments = t.processSegments
	h.ShouldFilter = t.shouldFilter
	h.OnOpen = func() {
		slog.Info("Will skip ad segments")
	}
	h.OnPlaylistParsed = t.onPlaylistParsed
}

// Open starts the inner HLS pipeline and records the returned FilteredStream
// as the outer reader — the one the player holds and the one pause/resume
// must address even after a BypassAdBreak swap. Wraps the returned reader
// in a tsbridge.Bridge so BypassAdBreak can keep the TS timeline continuous
// across source swaps by calling MarkSwap.
func (t *TwitchHLSStream) Open() (io.ReadCloser, error) {
	rc, err := t.HLSStream.Open()
	if err != nil {
		return nil, err
	}
	br := tsbridge.New(rc)
	t.mu.Lock()
	t.outer = t.Filtered
	t.bridge = br
	t.mu.Unlock()
	return br, nil
}

// outerFiltered returns the FilteredStream the consumer is reading from —
// either the outer one recorded on Open (post-swap-safe) or, before the
// first Open, the embedded HLSStream.Filtered as a best-effort fallback.
func (t *TwitchHLSStream) outerFiltered() *stream.FilteredStream {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.outer != nil {
		return t.outer
	}
	return t.Filtered
}

// BypassAdBreak rebuilds the inner HLS pipeline against a freshly-tokened
// playlist URL and swaps it under the existing FilteredStream. The consumer
// (mpv) keeps its pipe open across the swap — it just starts receiving bytes
// from a new Twitch session that may not have the current ad stitched in.
//
// Requires RefreshURL to be set. Concurrent callers get ErrBypassInFlight;
// calls before any content has played (preroll) get ErrBypassPreContent.
// Both are "informational skips" and should not be treated as hard
// failures by callers.
func (t *TwitchHLSStream) BypassAdBreak(ctx context.Context) error {
	if t.RefreshURL == nil {
		return errors.New("twitch: BypassAdBreak requires RefreshURL")
	}

	t.mu.Lock()
	if t.bypassInFlight {
		t.mu.Unlock()
		return ErrBypassInFlight
	}
	if !t.hadContent {
		t.mu.Unlock()
		return ErrBypassPreContent
	}
	if !t.lastWasAd {
		t.mu.Unlock()
		return ErrBypassNotInAd
	}
	t.bypassInFlight = true
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		t.bypassInFlight = false
		t.mu.Unlock()
	}()

	outer := t.outerFiltered()
	if outer == nil {
		return errors.New("twitch: BypassAdBreak called before Open")
	}

	newURL, err := t.RefreshURL(ctx)
	if err != nil {
		return err
	}

	t.mu.Lock()
	oldInner := t.HLSStream
	t.mu.Unlock()

	newInner := &hls.HLSStream{
		StreamURL: newURL,
		Client:    oldInner.Client,
		// LiveEdge=3 matches RFC 8216 §6.3.3: clients SHOULD NOT start
		// live playback less than three target durations from the end
		// of the playlist, to absorb the stall risk on the first few
		// fetches. 4 (the default) made mpv fall behind live and slow-mo
		// through overlapping TS timestamps across bypasses; 1 starved
		// the stdin pipe between fetches and caused micro-pauses. 3 is
		// the conservative-but-not-excessive middle ground.
		LiveEdge:            3,
		SegmentStreamData:   oldInner.SegmentStreamData,
		MaxPlaylistAttempts: oldInner.MaxPlaylistAttempts,
		MaxSegmentAttempts:  oldInner.MaxSegmentAttempts,
		Ctx:                 oldInner.Ctx,
	}
	t.wireHooks(newInner)

	newReader, err := newInner.Open()
	if err != nil {
		return err
	}

	// Clear playlist-scoped state (date ranges, break IDs) so the new
	// inner's ad detection starts fresh. Hold lastWasAd at true across
	// the swap: the outer FilteredStream was paused by shouldFilter when
	// the old session entered the ad, and the paused flag survives the
	// swap. Forcing lastWasAd=true means the new session's first non-ad
	// segment trips the Resume + OnAdEnd branch, returning mpv and the
	// UI to playback cleanly — without this, shouldFilter would treat
	// the content as always-ad-free and never call Resume.
	t.mu.Lock()
	t.HLSStream = newInner
	t.dateRanges = nil
	t.cachedAdDateRanges = nil
	t.adBreaks = nil
	t.lastWasAd = true
	t.adNotified = false
	t.mu.Unlock()

	// Prewarm: block here until the new inner has produced its first
	// bytes so the consumer (mpv/vlc) doesn't see an empty-pipe stall
	// at the swap boundary. Player-agnostic — shrinks the dead gap that
	// any TS demuxer would otherwise have to sit through. If the new
	// inner fails to produce anything, fall back to handing over the
	// raw reader and let the consumer wait on it as before.
	head, hErr := prewarmReader(ctx, newReader)
	swapReader := newReader
	if hErr == nil && len(head) > 0 {
		swapReader = &prependedReader{head: head, rest: newReader}
	}

	// Tell the bridge the timeline is about to change so it rewrites
	// PCR/PTS/DTS on post-swap packets to continue from the last values
	// emitted. Must happen before SwapReader so the bridge is in
	// rewrite mode by the time the new source's first packet arrives.
	t.mu.Lock()
	bridge := t.bridge
	t.mu.Unlock()
	if bridge != nil {
		bridge.MarkSwap()
	}

	// SwapReader closes the old pipe reader (unblocking any in-flight
	// Read). Cancel the old inner explicitly so its worker/writer
	// goroutines exit rather than spinning on a closed pipe.
	outer.SwapReader(swapReader)
	oldInner.Cancel()

	return nil
}

// prewarmReader reads up to prewarmBufSize bytes from r so the swap into
// the FilteredStream can hand the consumer a reader that already has data
// ready. Blocks until either the buffer fills, r returns an error, ctx
// is cancelled, or the prewarm deadline elapses.
//
// On ctx cancel / deadline, returns whatever was read so far with a nil
// error — a short prewarm is still better than none.
func prewarmReader(ctx context.Context, r io.Reader) ([]byte, error) {
	const (
		prewarmBufSize  = 64 * 1024
		prewarmDeadline = 3 * time.Second
	)

	type result struct {
		n   int
		err error
	}
	buf := make([]byte, prewarmBufSize)
	ch := make(chan result, 1)
	go func() {
		n, err := r.Read(buf)
		ch <- result{n: n, err: err}
	}()

	timer := time.NewTimer(prewarmDeadline)
	defer timer.Stop()

	select {
	case res := <-ch:
		return buf[:res.n], res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		// Prewarm timed out; bail and let the consumer wait on the
		// raw reader as a fallback. The in-flight Read goroutine
		// leaks until r produces or is closed — acceptable since
		// this path is exceptional and r will be closed on session
		// teardown.
		return nil, nil
	}
}

// prependedReader serves a buffered head on the first reads, then
// transparently delegates to rest. Close closes rest.
type prependedReader struct {
	head []byte
	pos  int
	rest io.ReadCloser
}

func (p *prependedReader) Read(buf []byte) (int, error) {
	if p.pos < len(p.head) {
		n := copy(buf, p.head[p.pos:])
		p.pos += n
		return n, nil
	}
	return p.rest.Read(buf)
}

func (p *prependedReader) Close() error { return p.rest.Close() }

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
	lastEmitted := t.lastEmittedSeq
	t.mu.Unlock()

	// Drop segments this wrapper has already handed to the writer in a
	// previous session. Successive bypass sessions overlap at their
	// playlist live edges (each fresh session re-exposes the last N
	// segments via LiveEdge), and without this dedup the player replays
	// the same content once per bypass.
	if lastEmitted > 0 {
		filtered := segments[:0]
		for _, seg := range segments {
			if seg.Num > lastEmitted {
				filtered = append(filtered, seg)
			}
		}
		segments = filtered
		if len(segments) == 0 {
			return segments
		}
	}

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
			if f := t.outerFiltered(); f != nil {
				f.Pause()
			}
		}
		t.mu.Lock()
		t.lastWasAd = true
		t.mu.Unlock()
		return true
	}

	if lastWasAd {
		if f := t.outerFiltered(); f != nil {
			f.Resume()
		}
		if onAdEnd != nil {
			onAdEnd()
		}
	}
	t.mu.Lock()
	t.lastWasAd = false
	t.adNotified = false
	// Record that this content segment has been sent to the writer so
	// subsequent bypass sessions can skip it via the lastEmitted filter
	// in processSegments.
	if seg.Num > t.lastEmittedSeq {
		t.lastEmittedSeq = seg.Num
	}
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
