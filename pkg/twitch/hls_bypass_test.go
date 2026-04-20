package twitch

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mcs/twui/pkg/stream"
	"github.com/mcs/twui/pkg/stream/hls"
)

type noopReadCloser struct {
	r  io.Reader
	mu sync.Mutex
}

func (n *noopReadCloser) Read(p []byte) (int, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.r.Read(p)
}
func (n *noopReadCloser) Close() error { return nil }

func TestTwitchHLSStream_BypassAdBreak_RequiresRefreshURL(t *testing.T) {
	s := NewTwitchHLSStream(&hls.HLSStream{}, false)

	err := s.BypassAdBreak(context.Background())
	if err == nil || !strings.Contains(err.Error(), "RefreshURL") {
		t.Errorf("BypassAdBreak without RefreshURL err = %v, want mention of RefreshURL", err)
	}
}

func TestTwitchHLSStream_BypassAdBreak_BeforeOpen(t *testing.T) {
	s := NewTwitchHLSStream(&hls.HLSStream{}, false)
	s.RefreshURL = func(ctx context.Context) (string, error) {
		t.Fatal("RefreshURL should not run when called before Open")
		return "", nil
	}
	// Get past the preroll + not-in-ad guards so we reach the outer==nil check.
	s.hadContent = true
	s.lastWasAd = true

	err := s.BypassAdBreak(context.Background())
	if err == nil || !strings.Contains(err.Error(), "before Open") {
		t.Errorf("BypassAdBreak before Open err = %v, want 'before Open' error", err)
	}
}

func TestTwitchHLSStream_BypassAdBreak_RefreshURLError(t *testing.T) {
	s := NewTwitchHLSStream(&hls.HLSStream{}, false)
	sentinel := errors.New("token service down")
	s.RefreshURL = func(ctx context.Context) (string, error) {
		return "", sentinel
	}
	s.outer = stream.NewFilteredStream(&noopReadCloser{r: bytes.NewReader(nil)})
	s.hadContent = true
	s.lastWasAd = true

	err := s.BypassAdBreak(context.Background())
	if !errors.Is(err, sentinel) {
		t.Errorf("BypassAdBreak err = %v, want %v", err, sentinel)
	}
}

func TestTwitchHLSStream_BypassAdBreak_SkipsPreroll(t *testing.T) {
	s := NewTwitchHLSStream(&hls.HLSStream{}, false)
	s.RefreshURL = func(ctx context.Context) (string, error) {
		t.Fatal("RefreshURL must not run during preroll skip")
		return "", nil
	}

	err := s.BypassAdBreak(context.Background())
	if !errors.Is(err, ErrBypassPreContent) {
		t.Errorf("BypassAdBreak pre-content err = %v, want ErrBypassPreContent", err)
	}
}

// TestTwitchHLSStream_BypassAdBreak_SkipsContent locks in the fix for the
// post-preroll playback stall: the pump keeps ticking for its full debounce
// window after OnAdBreak, so plenty of those ticks land while the stream is
// actually playing content. A bypass at that moment would spin up a new
// session whose live edge is entirely within lastEmittedSeq, starving the
// pipe. BypassAdBreak must short-circuit instead.
func TestTwitchHLSStream_BypassAdBreak_SkipsContent(t *testing.T) {
	s := NewTwitchHLSStream(&hls.HLSStream{}, false)
	s.RefreshURL = func(ctx context.Context) (string, error) {
		t.Fatal("RefreshURL must not run during content playback")
		return "", nil
	}
	s.hadContent = true
	// lastWasAd defaults to false — content playing.

	err := s.BypassAdBreak(context.Background())
	if !errors.Is(err, ErrBypassNotInAd) {
		t.Errorf("BypassAdBreak during content err = %v, want ErrBypassNotInAd", err)
	}
}

// TestTwitchHLSStream_BypassAdBreak_Cooldown locks in the fix for a
// runaway bypass loop: without a cooldown, the pump ticker fires another
// BypassAdBreak before the new session has delivered its first segment
// (lastWasAd is held true through the swap), and the session gets
// replaced continuously, starving the player.
func TestTwitchHLSStream_BypassAdBreak_Cooldown(t *testing.T) {
	s := NewTwitchHLSStream(&hls.HLSStream{}, false)
	s.hadContent = true
	s.lastWasAd = true
	s.lastBypassAt = time.Now()
	s.RefreshURL = func(ctx context.Context) (string, error) {
		t.Fatal("RefreshURL must not run inside bypassCooldown")
		return "", nil
	}

	err := s.BypassAdBreak(context.Background())
	if !errors.Is(err, ErrBypassRecent) {
		t.Errorf("BypassAdBreak inside cooldown err = %v, want ErrBypassRecent", err)
	}
}

// TestTwitchHLSStream_ShouldFilter_StarvationFallback locks in the
// behaviour that prevents a permanent player freeze when bypass can't
// escape the ad pool: once the filter has been paused longer than
// maxAdPauseDuration, the next ad segment releases it and OnAdEnd
// fires so the pump stops and the UI shows Playing.
func TestTwitchHLSStream_ShouldFilter_StarvationFallback(t *testing.T) {
	s := NewTwitchHLSStream(&hls.HLSStream{}, false)

	// Simulate an already-running ad run that started long enough ago
	// to exceed the threshold.
	s.lastWasAd = true
	s.adPausedAt = time.Now().Add(-maxAdPauseDuration - time.Second)

	var endCalls int
	s.OnAdEnd = func() { endCalls++ }

	adSeg := hls.Segment{Num: 100, Title: "Amazon|xyz"}
	filtered := s.shouldFilter(adSeg)
	if filtered {
		t.Error("shouldFilter past maxAdPauseDuration = true, want false (degraded, let it through)")
	}
	if !s.adFilterDegraded {
		t.Error("adFilterDegraded = false after starvation, want true")
	}
	if endCalls != 1 {
		t.Errorf("OnAdEnd fired %d times, want 1", endCalls)
	}

	// Subsequent ad segments in the same run stay degraded; no further
	// OnAdEnd calls.
	filtered = s.shouldFilter(hls.Segment{Num: 101, Title: "Amazon|xyz"})
	if filtered {
		t.Error("degraded-mode ad segment filtered, want pass-through")
	}
	if endCalls != 1 {
		t.Errorf("OnAdEnd fired %d times after second ad, want 1", endCalls)
	}
}

func TestTwitchHLSStream_BypassAdBreak_SingleFlight(t *testing.T) {
	s := NewTwitchHLSStream(&hls.HLSStream{}, false)
	s.hadContent = true
	s.lastWasAd = true
	s.bypassInFlight = true
	s.RefreshURL = func(ctx context.Context) (string, error) {
		t.Fatal("RefreshURL must not run when another bypass is in flight")
		return "", nil
	}

	err := s.BypassAdBreak(context.Background())
	if !errors.Is(err, ErrBypassInFlight) {
		t.Errorf("BypassAdBreak in-flight err = %v, want ErrBypassInFlight", err)
	}
}
