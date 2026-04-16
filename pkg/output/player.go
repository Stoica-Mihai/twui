package output

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Player manages launching a media player process and feeding it stream data
// via stdin.
type Player struct {
	Path       string    // player binary: "mpv", "vlc", or an absolute path
	Args       []string  // extra arguments from --player-args
	Title      string    // window title from stream metadata
	NoClose    bool      // if true, keep the reader open after the player exits
	Stderr     io.Writer // player's stderr; defaults to os.Stderr when nil
	NoTerminal bool      // if true, pass --no-terminal to mpv to suppress /dev/tty writes
	AudioOnly  bool      // if true, pass flags to disable video decoding (mpv: --vid=no --force-window)
}

// buildArgs assembles the command-line arguments for the configured player,
// including the leading "-" stdin marker, any player-specific title/terminal
// flags, and the caller's extra Args appended at the end.
func (p *Player) buildArgs() []string {
	args := []string{"-"} // read from stdin

	base := strings.ToLower(filepath.Base(p.Path))
	if p.Title != "" {
		switch {
		case strings.Contains(base, "mpv"):
			args = append(args, fmt.Sprintf("--force-media-title=%s", p.Title))
		case strings.Contains(base, "vlc"):
			args = append(args, fmt.Sprintf("--meta-title=%s", p.Title))
		}
	}
	if p.NoTerminal && strings.Contains(base, "mpv") {
		args = append(args, "--no-terminal")
	}
	if p.AudioOnly && strings.Contains(base, "mpv") {
		args = append(args, "--vid=no", "--force-window")
	}

	return append(args, p.Args...)
}

// Play launches the player, pipes r into its stdin, and blocks until the player
// exits. Unless NoClose is true the reader is closed when the player finishes.
func (p *Player) Play(ctx context.Context, r io.ReadCloser) error {
	cmd := exec.Command(p.Path, p.buildArgs()...)
	cmd.Stdout = io.Discard
	if p.Stderr != nil {
		cmd.Stderr = p.Stderr
	} else {
		cmd.Stderr = os.Stderr
	}

	// Use StdinPipe so cmd.Wait() does not manage our stdin copy goroutine.
	// This lets us close r after cmd.Wait() returns, unblocking any paused
	// FilteredStream reads (e.g. during an ad break) without hanging.
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		if !p.NoClose {
			r.Close()
		}
		return fmt.Errorf("player: failed to create stdin pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		if !p.NoClose {
			r.Close()
		}
		return fmt.Errorf("player: failed to start player: %w", err)
	}

	// Feed the stream into the player's stdin in a goroutine we control.
	// Close stdinPipe when done so the player receives EOF on stdin.
	copyDone := make(chan struct{})
	go func() {
		defer close(copyDone)
		_, _ = io.Copy(stdinPipe, r)
		stdinPipe.Close()
	}()

	// done is closed when cmd.Wait() returns, signalling the signal goroutine.
	done := make(chan struct{})

	// Graceful shutdown on context cancel or stream EOF.
	go func() {
		select {
		case <-ctx.Done():
			// User-initiated or picker-initiated cancel.
			if cmd.Process == nil {
				return
			}
			_ = cmd.Process.Signal(syscall.SIGTERM)
			timer := time.NewTimer(5 * time.Second)
			defer timer.Stop()
			select {
			case <-done:
			case <-timer.C:
				_ = cmd.Process.Kill()
			}
		case <-copyDone:
			// Stream ended (stdin closed). Give the player a few seconds to
			// exit on its own (e.g. drain buffered frames), then terminate.
			timer := time.NewTimer(5 * time.Second)
			defer timer.Stop()
			select {
			case <-done:
				// Player exited normally after EOF.
			case <-timer.C:
				if cmd.Process != nil {
					_ = cmd.Process.Signal(syscall.SIGTERM)
				}
				killTimer := time.NewTimer(5 * time.Second)
				defer killTimer.Stop()
				select {
				case <-done:
				case <-killTimer.C:
					if cmd.Process != nil {
						_ = cmd.Process.Kill()
					}
				}
			}
		case <-done:
			// Process exited before context was cancelled.
		}
	}()

	waitErr := cmd.Wait()
	close(done)

	// Close r to unblock the copy goroutine if it is blocked in a paused
	// FilteredStream.Read() (e.g. during an ad break at the time mpv exits).
	if !p.NoClose {
		r.Close()
	}

	// Wait for the copy goroutine to finish cleanly.
	<-copyDone

	return waitErr
}
