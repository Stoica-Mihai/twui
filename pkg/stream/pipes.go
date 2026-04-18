package stream

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// NamedPipeOpenTimeout bounds how long OpenPipeWithTimeout will wait for the
// other end of a FIFO to be opened before giving up.
const NamedPipeOpenTimeout = 30 * time.Second

// OpenPipeWithTimeout opens a file in a goroutine and selects on context
// cancellation or NamedPipeOpenTimeout, preventing indefinite blocking
// when the other end of a named pipe never opens. On timeout or ctx.Done,
// it unblocks the stuck open by opening the read end of the FIFO itself —
// removing the file does not unblock a process already inside open(2).
func OpenPipeWithTimeout(ctx context.Context, path string, flag int, perm os.FileMode) (*os.File, error) {
	type result struct {
		f   *os.File
		err error
	}
	ch := make(chan result, 1)
	go func() {
		f, err := os.OpenFile(path, flag, perm)
		ch <- result{f, err}
	}()

	timeout := time.NewTimer(NamedPipeOpenTimeout)
	defer timeout.Stop()

	select {
	case r := <-ch:
		return r.f, r.err
	case <-timeout.C:
		unblockPipeOpen(path)
		return nil, fmt.Errorf("timed out opening pipe %s after %s", path, NamedPipeOpenTimeout)
	case <-ctx.Done():
		unblockPipeOpen(path)
		return nil, ctx.Err()
	}
}

// unblockPipeOpen releases a goroutine blocked inside os.OpenFile on a FIFO
// by briefly opening the read end. Errors are intentionally ignored — the
// caller has already decided to abandon the open.
func unblockPipeOpen(path string) {
	go func() {
		f, err := os.Open(path)
		if err == nil {
			f.Close()
		}
	}()
}

// CreatePipePair creates two named pipes inside dir named "video.pipe" and
// "audio.pipe" and returns their absolute paths. Callers are responsible for
// removing dir on error and after use. Both pipes are created with mode 0600.
func CreatePipePair(dir string) (video, audio string, err error) {
	video = filepath.Join(dir, "video.pipe")
	audio = filepath.Join(dir, "audio.pipe")
	if err := syscall.Mkfifo(video, 0600); err != nil {
		return "", "", fmt.Errorf("mkfifo video: %w", err)
	}
	if err := syscall.Mkfifo(audio, 0600); err != nil {
		return "", "", fmt.Errorf("mkfifo audio: %w", err)
	}
	return video, audio, nil
}
