package hls

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"

	"github.com/mcs/twui/pkg/stream"
)

// MuxedHLSStream combines separate video and audio HLS streams into a single
// muxed MPEG-TS output using FFmpeg and named pipes.
type MuxedHLSStream struct {
	Video      *HLSStream
	Audio      *HLSStream
	FFmpegPath string
}

// URL returns the video stream URL.
func (m *MuxedHLSStream) URL() string {
	return m.Video.URL()
}

// Open starts both HLS streams and muxes them through FFmpeg.
// The returned io.ReadCloser produces muxed MPEG-TS data.
func (m *MuxedHLSStream) Open() (io.ReadCloser, error) {
	ffmpeg := m.FFmpegPath
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}

	videoReader, err := m.Video.Open()
	if err != nil {
		return nil, fmt.Errorf("muxed hls: open video: %w", err)
	}

	audioReader, err := m.Audio.Open()
	if err != nil {
		videoReader.Close()
		return nil, fmt.Errorf("muxed hls: open audio: %w", err)
	}

	// Create temp directory with named pipes
	tmpDir, err := os.MkdirTemp("", "twui-mux-*")
	if err != nil {
		videoReader.Close()
		audioReader.Close()
		return nil, fmt.Errorf("muxed hls: create temp dir: %w", err)
	}

	videoPipe, audioPipe, err := stream.CreatePipePair(tmpDir)
	if err != nil {
		videoReader.Close()
		audioReader.Close()
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("muxed hls: %w", err)
	}

	// Launch FFmpeg
	cmd := exec.Command(ffmpeg,
		"-i", videoPipe,
		"-i", audioPipe,
		"-c", "copy",
		"-f", "mpegts",
		"pipe:1",
	)
	cmd.Stderr = io.Discard

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		videoReader.Close()
		audioReader.Close()
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("muxed hls: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		videoReader.Close()
		audioReader.Close()
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("muxed hls: start ffmpeg: %w", err)
	}

	// Use the video stream's context if available, otherwise background.
	openCtx := m.Video.Ctx
	if openCtx == nil {
		openCtx = context.Background()
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine: copy video to named pipe
	go func() {
		defer wg.Done()
		f, err := stream.OpenPipeWithTimeout(openCtx, videoPipe, os.O_WRONLY, 0)
		if err != nil {
			slog.Error("Failed to open video pipe", "err", err)
			return
		}
		if _, err := io.Copy(f, videoReader); err != nil {
			slog.Error("Failed to copy video stream", "err", err)
		}
		f.Close()
		videoReader.Close()
	}()

	// Goroutine: copy audio to named pipe
	go func() {
		defer wg.Done()
		f, err := stream.OpenPipeWithTimeout(openCtx, audioPipe, os.O_WRONLY, 0)
		if err != nil {
			slog.Error("Failed to open audio pipe", "err", err)
			return
		}
		if _, err := io.Copy(f, audioReader); err != nil {
			slog.Error("Failed to copy audio stream", "err", err)
		}
		f.Close()
		audioReader.Close()
	}()

	return &muxedReadCloser{
		reader: stdout,
		cmd:    cmd,
		tmpDir: tmpDir,
		wg:     &wg,
	}, nil
}

// muxedReadCloser wraps FFmpeg stdout and cleans up resources on close.
type muxedReadCloser struct {
	reader io.ReadCloser
	cmd    *exec.Cmd
	tmpDir string
	wg     *sync.WaitGroup
}

func (r *muxedReadCloser) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

func (r *muxedReadCloser) Close() error {
	err := r.reader.Close()

	// Wait for copy goroutines to finish
	r.wg.Wait()

	// Kill FFmpeg if still running and wait
	if r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
	}
	_ = r.cmd.Wait()

	// Clean up temp directory
	os.RemoveAll(r.tmpDir)

	return err
}

// Ensure MuxedHLSStream satisfies the stream.Stream interface.
var _ stream.Stream = (*MuxedHLSStream)(nil)
