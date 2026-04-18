package stream

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"
)

// MuxedStream combines a video and audio stream by muxing them through FFmpeg
// using named pipes, producing a single mpegts output on stdout.
type MuxedStream struct {
	Video     Stream
	Audio     Stream
	FFmpeg    string // path to ffmpeg binary; defaults to "ffmpeg"
	AudioOnly bool   // if true, use explicit -map to extract audio track from Audio (for combined formats)
}

func (m *MuxedStream) Open() (io.ReadCloser, error) {
	ffmpegPath := m.FFmpeg
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}

	resolvedPath, err := exec.LookPath(ffmpegPath)
	if err != nil {
		return nil, fmt.Errorf("muxed: ffmpeg not found: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "twui-mux-*")
	if err != nil {
		return nil, fmt.Errorf("muxed: failed to create temp directory: %w", err)
	}

	videoPipe, audioPipe, err := CreatePipePair(tmpDir)
	if err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("muxed: %w", err)
	}

	videoReader, err := m.Video.Open()
	if err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("muxed: failed to open video stream: %w", err)
	}

	audioReader, err := m.Audio.Open()
	if err != nil {
		videoReader.Close()
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("muxed: failed to open audio stream: %w", err)
	}

	args := []string{"-i", videoPipe, "-i", audioPipe}
	if m.AudioOnly {
		// Audio input is a combined video+audio source; extract only its audio track.
		args = append(args, "-map", "0:v:0", "-map", "1:a:0")
	}
	args = append(args, "-c", "copy", "-f", "mpegts", "pipe:1")
	cmd := exec.Command(resolvedPath, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		videoReader.Close()
		audioReader.Close()
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("muxed: failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		videoReader.Close()
		audioReader.Close()
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("muxed: failed to start ffmpeg: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 2)

	var wg sync.WaitGroup
	wg.Add(2)

	mrc := &muxedReadCloser{
		cmd:     cmd,
		tmpDir:  tmpDir,
		closers: []io.Closer{videoReader, audioReader},
		wg:      &wg,
		errCh:   errCh,
		ctx:     ctx,
		cancel:  cancel,
	}
	mrc.Reader = &errAwareReader{r: stdout, m: mrc}

	// Background watcher: on feeder error, store it, kill FFmpeg, close readers, cancel ctx.
	go func() {
		select {
		case err := <-errCh:
			mrc.mu.Lock()
			mrc.feederErr = err
			mrc.mu.Unlock()
			slog.Error("Feeder goroutine error", "err", err)
			// Drain any remaining errors from other feeders.
			for {
				select {
				case extra := <-errCh:
					slog.Error("Additional feeder goroutine error", "err", extra)
				default:
					goto drained
				}
			}
		drained:
			mrc.cancel()
			if mrc.cmd.Process != nil {
				_ = mrc.cmd.Process.Kill()
			}
			for _, c := range mrc.closers {
				c.Close()
			}
		case <-ctx.Done():
			// Normal shutdown via Close().
		}
	}()

	// Feed video stream into the video named pipe.
	go func() {
		defer wg.Done()
		f, err := OpenPipeWithTimeout(ctx, videoPipe, os.O_WRONLY, 0)
		if err != nil {
			slog.Error("Failed to open video pipe", "err", err)
			select {
			case errCh <- err:
			default:
			}
			return
		}
		defer f.Close()
		if _, err := io.Copy(f, videoReader); err != nil {
			slog.Error("Failed to copy video stream", "err", err)
			select {
			case errCh <- err:
			default:
			}
		}
	}()

	// Feed audio stream into the audio named pipe.
	go func() {
		defer wg.Done()
		f, err := OpenPipeWithTimeout(ctx, audioPipe, os.O_WRONLY, 0)
		if err != nil {
			slog.Error("Failed to open audio pipe", "err", err)
			select {
			case errCh <- err:
			default:
			}
			return
		}
		defer f.Close()
		if _, err := io.Copy(f, audioReader); err != nil {
			slog.Error("Failed to copy audio stream", "err", err)
			select {
			case errCh <- err:
			default:
			}
		}
	}()

	return mrc, nil
}

func (m *MuxedStream) URL() string {
	return fmt.Sprintf("muxed(%s, %s)", m.Video.URL(), m.Audio.URL())
}

type muxedReadCloser struct {
	io.Reader
	cmd       *exec.Cmd
	tmpDir    string
	closers   []io.Closer
	wg        *sync.WaitGroup
	errCh     chan error
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	feederErr error
}

func (m *muxedReadCloser) Close() error {
	m.cancel()
	if m.cmd.Process != nil {
		_ = m.cmd.Process.Kill()
		_ = m.cmd.Wait()
	}
	for _, c := range m.closers {
		c.Close()
	}
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		slog.Warn("muxed stream cleanup timed out after 10s")
	}
	os.RemoveAll(m.tmpDir)
	return nil
}

// errAwareReader wraps an io.Reader and returns the feeder error (if any)
// when the muxed stream's context is cancelled due to a feeder failure.
type errAwareReader struct {
	r io.Reader
	m *muxedReadCloser
}

func (e *errAwareReader) Read(p []byte) (int, error) {
	if e.m.ctx.Err() != nil {
		e.m.mu.Lock()
		err := e.m.feederErr
		e.m.mu.Unlock()
		if err != nil {
			return 0, err
		}
		return 0, e.m.ctx.Err()
	}
	return e.r.Read(p)
}
