package hls

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/mcs/twui/pkg/session"
	"github.com/mcs/twui/pkg/stream"
)

const (
	defaultLiveEdge        = 4
	defaultMaxAttempts     = 3
	liveSegmentChanSize    = 16
	playlistRetryBaseDelay = 1 * time.Second
	segmentRetryBaseDelay  = 1 * time.Second
	maxKeyCacheSize        = 32
	maxSegmentSize         = 100 * 1024 * 1024 // 100 MB
)

// retryDelay computes the delay before the next retry attempt. If retryAfterHeader
// parses to a positive duration, uses that (capped by session.MaxRetryAfter).
// Otherwise uses full jitter: rand(0, baseDelay * 2^attempt).
func retryDelay(attempt int, baseDelay time.Duration, retryAfterHeader string) time.Duration {
	if ra := session.ParseRetryAfter(retryAfterHeader); ra > 0 {
		return ra
	}
	return time.Duration(rand.Int64N(int64(baseDelay) * (1 << uint(attempt))))
}

// keyCache is a bounded cache for AES-128 encryption keys, evicting the oldest
// entry when the size limit is reached.
type keyCache struct {
	mu    sync.Mutex
	data  map[string][]byte
	order []string
}

// get returns the cached key for the given URI, or nil if not cached.
func (kc *keyCache) get(uri string) ([]byte, bool) {
	kc.mu.Lock()
	defer kc.mu.Unlock()
	key, ok := kc.data[uri]
	return key, ok
}

// set adds a key to the cache. If the URI is already cached, this is a no-op.
// When the cache is full, the oldest entry is evicted first.
func (kc *keyCache) set(uri string, key []byte) {
	kc.mu.Lock()
	defer kc.mu.Unlock()
	if _, exists := kc.data[uri]; exists {
		return // Already cached, no need to evict
	}
	if len(kc.data) >= maxKeyCacheSize {
		oldest := kc.order[0]
		delete(kc.data, oldest)
		kc.order = kc.order[1:]
	}
	kc.order = append(kc.order, uri)
	kc.data[uri] = key
}

// segmentChanSize returns the segment channel buffer size for live streams.
func segmentChanSize() int {
	return liveSegmentChanSize
}

// segmentItem is an internal type sent through the segment channel.
// It can represent either a regular segment or a map/init segment.
type segmentItem struct {
	segment  *Segment
	mapEntry *MapEntry // non-nil if this is a map/init segment to fetch
}

// HLSStream implements stream.Stream for HLS media playlists.
type HLSStream struct {
	StreamURL         string
	Client            *http.Client
	LiveEdge          int
	SegmentStreamData bool

	// MaxPlaylistAttempts is the number of fetch attempts for the HLS playlist.
	// When 0, defaults to 3.
	MaxPlaylistAttempts int

	// MaxSegmentAttempts is the number of fetch attempts per segment or map.
	// When 0, defaults to 3.
	MaxSegmentAttempts int

	// Ctx is an optional parent context. When set, goroutines will exit
	// when this context is cancelled (e.g. on SIGINT). If nil, defaults
	// to context.Background().
	Ctx context.Context

	// Hooks for subclass override (e.g. Twitch)
	ProcessSegments  func(segments []Segment, isFirst bool) []Segment
	ShouldFilter     func(seg Segment) bool
	OnOpen           func()
	OnPlaylistParsed func(playlist *MediaPlaylist)

	// dropMu protects onStreamDrop, which is written by SetOnDrop (main
	// goroutine) and read by the worker goroutine.
	dropMu sync.Mutex
	// onStreamDrop is called when the stream ends due to exhausted retries
	// (not on normal EXT-X-ENDLIST completion or context cancellation).
	onStreamDrop func(err error)

	// Filtered is set during Open() and exposed for subclass use (e.g. ad filtering pause/resume).
	Filtered *stream.FilteredStream

	cancel context.CancelFunc
}

// URL returns the stream playlist URL.
func (h *HLSStream) URL() string {
	return h.StreamURL
}

// SetOnDrop sets the onStreamDrop callback. This implements stream.Droppable.
func (h *HLSStream) SetOnDrop(fn func(error)) {
	h.dropMu.Lock()
	h.onStreamDrop = fn
	h.dropMu.Unlock()
}

// Open starts the HLS stream and returns an io.ReadCloser that produces
// the concatenated segment data.
func (h *HLSStream) Open() (io.ReadCloser, error) {
	if h.LiveEdge <= 0 {
		h.LiveEdge = defaultLiveEdge
	}
	if h.Client == nil {
		h.Client = http.DefaultClient
	}

	parent := h.Ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	h.cancel = cancel

	pr, pw := io.Pipe()
	segCh := make(chan segmentItem, segmentChanSize())

	go h.worker(ctx, segCh, pw)
	go h.writer(ctx, segCh, pw)

	fs := stream.NewFilteredStream(pr)
	h.Filtered = fs

	if h.OnOpen != nil {
		h.OnOpen()
	}

	// Wrap in a closer that cancels the context on close.
	return &hlsReadCloser{
		FilteredStream: fs,
		cancel:         cancel,
	}, nil
}

// hlsReadCloser wraps FilteredStream and cancels the context on Close.
type hlsReadCloser struct {
	*stream.FilteredStream
	cancel context.CancelFunc
}

func (r *hlsReadCloser) Close() error {
	r.cancel()
	return r.FilteredStream.Close()
}

// worker fetches and parses the playlist, sending new segments to the channel.
func (h *HLSStream) worker(ctx context.Context, segCh chan<- segmentItem, pw *io.PipeWriter) {
	defer close(segCh)

	lastNum := -1
	isFirst := true
	var lastMap *MapEntry
	var sentMap bool

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		playlist, err := h.fetchPlaylist(ctx)
		if err != nil {
			slog.Error("Failed to fetch playlist after retries", "err", err)
			// Context cancelled means intentional stop — don't fire onStreamDrop.
			if ctx.Err() == nil {
				h.dropMu.Lock()
				fn := h.onStreamDrop
				h.dropMu.Unlock()
				if fn != nil {
					go fn(err)
				}
			}
			return
		}

		if h.OnPlaylistParsed != nil {
			h.OnPlaylistParsed(playlist)
		}

		segments := playlist.Segments
		if len(segments) == 0 {
			if playlist.Ended {
				return
			}
			h.sleepReload(ctx, playlist)
			continue
		}

		wasFirst := isFirst
		if isFirst {
			segments = h.applyFirstLoad(segments, playlist)
			isFirst = false
		} else {
			// Only process segments newer than what we've already seen
			var newSegs []Segment
			for _, seg := range segments {
				if seg.Num > lastNum {
					newSegs = append(newSegs, seg)
				}
			}
			segments = newSegs
		}

		if h.ProcessSegments != nil {
			segments = h.ProcessSegments(segments, wasFirst)
		}

		// Send map entry if present and changed
		if playlist.Map != nil && (!sentMap || lastMap == nil || lastMap.URI != playlist.Map.URI) {
			lastMap = playlist.Map
			sentMap = true
			select {
			case segCh <- segmentItem{mapEntry: playlist.Map}:
			case <-ctx.Done():
				return
			}
		}

		for _, seg := range segments {
			lastNum = seg.Num
			select {
			case segCh <- segmentItem{segment: &seg}:
			case <-ctx.Done():
				return
			}
		}

		if playlist.Ended {
			return
		}

		h.sleepReload(ctx, playlist)
	}
}

// applyFirstLoad reduces the segment list for the initial load.
// For ended playlists, all segments are returned. For live playlists,
// only the last N segments (live edge) are kept.
func (h *HLSStream) applyFirstLoad(segments []Segment, playlist *MediaPlaylist) []Segment {
	if playlist.Ended {
		return segments
	}

	// Live: apply live edge
	if len(segments) > h.LiveEdge {
		segments = segments[len(segments)-h.LiveEdge:]
	}
	return segments
}

// fetchPlaylist fetches and parses the playlist with retries.
func (h *HLSStream) fetchPlaylist(ctx context.Context) (*MediaPlaylist, error) {
	maxAttempts := h.MaxPlaylistAttempts
	if maxAttempts == 0 {
		maxAttempts = defaultMaxAttempts
	}
	var lastErr error
	var retryAfterHeader string
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			delay := retryDelay(attempt-1, playlistRetryBaseDelay, retryAfterHeader)
			retryAfterHeader = "" // reset for next iteration
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.StreamURL, nil)
		if err != nil {
			lastErr = err
			continue
		}

		resp, err := h.Client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			continue
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
		resp.Body.Close()
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			retryAfterHeader = resp.Header.Get("Retry-After")
			lastErr = fmt.Errorf("hls: playlist HTTP 429")
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("hls: playlist HTTP %d", resp.StatusCode)
			continue
		}

		playlist, err := ParseMedia(string(body), h.StreamURL)
		if err != nil {
			lastErr = err
			continue
		}

		return playlist, nil
	}
	return nil, fmt.Errorf("hls: fetch playlist: %w", lastErr)
}

// sleepReload waits for the appropriate reload interval before refetching.
func (h *HLSStream) sleepReload(ctx context.Context, playlist *MediaPlaylist) {
	duration := time.Duration(playlist.TargetDuration * float64(time.Second))
	if duration <= 0 {
		// Fallback: use last segment duration
		if len(playlist.Segments) > 0 {
			last := playlist.Segments[len(playlist.Segments)-1]
			duration = time.Duration(last.Duration * float64(time.Second))
		}
	}
	if duration <= 0 {
		duration = 5 * time.Second
	}

	select {
	case <-time.After(duration):
	case <-ctx.Done():
	}
}

// writer reads segments from the channel, fetches their data, and writes
// to the pipe.
func (h *HLSStream) writer(ctx context.Context, segCh <-chan segmentItem, pw *io.PipeWriter) {
	defer pw.Close()

	kc := &keyCache{data: make(map[string][]byte)}

	for item := range segCh {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if item.mapEntry != nil {
			if err := h.fetchAndWriteMap(ctx, item.mapEntry, pw); err != nil {
				slog.Error("Failed to fetch map segment", "err", err)
				return
			}
			continue
		}

		seg := item.segment
		if h.ShouldFilter != nil && h.ShouldFilter(*seg) {
			// Fetch but discard data
			h.fetchAndDiscard(ctx, seg)
			continue
		}

		if err := h.fetchAndWriteSegment(ctx, seg, pw, kc); err != nil {
			slog.Error("Failed to fetch segment", "num", seg.Num, "err", err)
			return
		}
	}
}

// fetchAndWriteMap fetches a map/init segment and writes it to the pipe.
func (h *HLSStream) fetchAndWriteMap(ctx context.Context, m *MapEntry, pw *io.PipeWriter) error {
	maxAttempts := h.MaxSegmentAttempts
	if maxAttempts == 0 {
		maxAttempts = defaultMaxAttempts
	}
	var lastErr error
	var retryAfterHeader string
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			delay := retryDelay(attempt-1, segmentRetryBaseDelay, retryAfterHeader)
			retryAfterHeader = ""
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URI, nil)
		if err != nil {
			lastErr = err
			continue
		}
		if m.ByteRange != nil {
			end := m.ByteRange.Offset + m.ByteRange.Length - 1
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", m.ByteRange.Offset, end))
		}

		resp, err := h.Client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			retryAfterHeader = resp.Header.Get("Retry-After")
			resp.Body.Close()
			lastErr = fmt.Errorf("hls: map HTTP 429")
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			lastErr = fmt.Errorf("hls: map HTTP %d", resp.StatusCode)
			continue
		}

		data, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		_, err = pw.Write(data)
		return err
	}
	return fmt.Errorf("hls: fetch map: %w", lastErr)
}

// fetchAndDiscard fetches a segment and discards the response body.
func (h *HLSStream) fetchAndDiscard(ctx context.Context, seg *Segment) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, seg.URL, nil)
	if err != nil {
		slog.Debug("Failed to fetch filtered segment", "url", seg.URL, "err", err)
		return
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		slog.Debug("Failed to fetch filtered segment", "url", seg.URL, "err", err)
		return
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		slog.Debug("Failed to drain filtered segment response", "url", seg.URL, "err", err)
	}
	resp.Body.Close()
}

// fetchAndWriteSegment fetches a segment, optionally decrypts it, and writes
// the data to the pipe.
func (h *HLSStream) fetchAndWriteSegment(
	ctx context.Context,
	seg *Segment,
	pw *io.PipeWriter,
	kc *keyCache,
) error {
	maxAttempts := h.MaxSegmentAttempts
	if maxAttempts == 0 {
		maxAttempts = defaultMaxAttempts
	}
	var lastErr error
	var retryAfterHeader string
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			delay := retryDelay(attempt-1, segmentRetryBaseDelay, retryAfterHeader)
			retryAfterHeader = ""
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, seg.URL, nil)
		if err != nil {
			lastErr = err
			continue
		}
		if seg.ByteRange != nil {
			end := seg.ByteRange.Offset + seg.ByteRange.Length - 1
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", seg.ByteRange.Offset, end))
		}

		resp, err := h.Client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			retryAfterHeader = resp.Header.Get("Retry-After")
			resp.Body.Close()
			lastErr = fmt.Errorf("hls: segment HTTP 429")
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			lastErr = fmt.Errorf("hls: segment HTTP %d", resp.StatusCode)
			continue
		}

		if seg.Key == nil {
			// No encryption: stream directly from response to pipe.
			_, err = io.Copy(pw, resp.Body)
			resp.Body.Close()
			if err != nil {
				lastErr = err
				continue
			}
			return nil
		}

		data, err := io.ReadAll(io.LimitReader(resp.Body, maxSegmentSize))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		// Decrypt if needed
		if seg.Key.Method == "AES-128" {
			key, err := h.getKey(ctx, seg.Key.URI, kc)
			if err != nil {
				lastErr = fmt.Errorf("hls: fetch key: %w", err)
				continue
			}
			data, err = decryptAES128CBC(data, key, seg.Key.IV)
			if err != nil {
				lastErr = fmt.Errorf("hls: decrypt: %w", err)
				continue
			}
		}

		_, err = pw.Write(data)
		return err
	}
	return fmt.Errorf("hls: fetch segment %d: %w", seg.Num, lastErr)
}

// getKey fetches an AES-128 key, caching by URI. The cache is bounded;
// the oldest entry is evicted when full.
func (h *HLSStream) getKey(ctx context.Context, uri string, kc *keyCache) ([]byte, error) {
	if key, ok := kc.get(uri); ok {
		return key, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	key, err := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
	if err != nil {
		return nil, err
	}

	kc.set(uri, key)
	return key, nil
}

// decryptAES128CBC decrypts data using AES-128-CBC with PKCS#7 padding.
func decryptAES128CBC(data, key, iv []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	if len(iv) != aes.BlockSize {
		return nil, fmt.Errorf("hls: invalid IV length %d", len(iv))
	}

	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("hls: ciphertext length %d not a multiple of block size", len(data))
	}

	mode := cipher.NewCBCDecrypter(block, iv)
	plaintext := make([]byte, len(data))
	mode.CryptBlocks(plaintext, data)

	// Remove PKCS#7 padding
	if len(plaintext) == 0 {
		return plaintext, nil
	}
	padLen := int(plaintext[len(plaintext)-1])
	if padLen > aes.BlockSize || padLen == 0 {
		return nil, fmt.Errorf("hls: invalid PKCS#7 padding")
	}
	for i := len(plaintext) - padLen; i < len(plaintext); i++ {
		if plaintext[i] != byte(padLen) {
			return nil, fmt.Errorf("hls: invalid PKCS#7 padding")
		}
	}
	return plaintext[:len(plaintext)-padLen], nil
}

// Ensure HLSStream satisfies the stream.Stream interface.
var _ stream.Stream = (*HLSStream)(nil)

// Verify hlsReadCloser implements io.ReadCloser.
var _ io.ReadCloser = (*hlsReadCloser)(nil)

// Ensure FilteredStream methods are accessible through hlsReadCloser.
// FilteredStream.Pause and .Resume are available via the embedded field.
// Usage:
//   rc, _ := hlsStream.Open()
//   if fs, ok := rc.(*hlsReadCloser); ok {
//       fs.Pause()
//       fs.Resume()
//   }
