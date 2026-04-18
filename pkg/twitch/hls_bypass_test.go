package twitch

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

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
	// Get past the preroll guard so we reach the outer==nil check.
	s.hadContent = true

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

func TestTwitchHLSStream_BypassAdBreak_SingleFlight(t *testing.T) {
	s := NewTwitchHLSStream(&hls.HLSStream{}, false)
	s.hadContent = true
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
