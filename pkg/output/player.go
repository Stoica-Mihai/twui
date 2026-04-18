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

// playerKind classifies p.Path into one of the player families we emit
// specific flags for, so the per-family check only runs once.
type playerKind int

const (
	playerOther playerKind = iota
	playerMPV
	playerVLC
)

func (p *Player) kind() playerKind {
	base := strings.ToLower(filepath.Base(p.Path))
	switch {
	case strings.Contains(base, "mpv"):
		return playerMPV
	case strings.Contains(base, "vlc"):
		return playerVLC
	default:
		return playerOther
	}
}

// buildArgs assembles the command-line arguments for the configured player,
// including the leading "-" stdin marker, any player-specific title/terminal
// flags, and the caller's extra Args appended at the end. User-provided Args
// come last so they override any defaults we inject (mpv/vlc take the later
// value on repeated flags).
func (p *Player) buildArgs() []string {
	args := []string{"-"} // read from stdin

	kind := p.kind()
	if p.Title != "" {
		switch kind {
		case playerMPV:
			args = append(args, fmt.Sprintf("--force-media-title=%s", p.Title))
		case playerVLC:
			args = append(args, fmt.Sprintf("--meta-title=%s", p.Title))
		}
	}
	if p.NoTerminal && kind == playerMPV {
		args = append(args, "--no-terminal")
	}
	if p.AudioOnly && kind == playerMPV {
		args = append(args, "--vid=no", "--force-window")
	}
	// Default player cache: the piped HLS feed can stall briefly during
	// an ad-break bypass (new HLS session spins up before bytes flow),
	// and a larger cache absorbs that gap so playback stays smooth.
	// Overridable via --player-args (both players take the later value
	// on repeated flags).
	switch kind {
	case playerMPV:
		args = append(args, "--cache=yes", "--cache-secs=30")
	case playerVLC:
		// VLC's cache knobs take milliseconds. stdin goes through the
		// file-caching path; network-caching is also bumped so any
		// variant that VLC classifies as network-like gets the same
		// buffer.
		args = append(args, "--file-caching=30000", "--network-caching=30000")
	}

	return append(args, p.Args...)
}

// gracefulShutdownTimeout bounds each step of the SIGTERM → SIGKILL sequence
// we apply when the player hasn't exited on its own after EOF or cancel.
const gracefulShutdownTimeout = 5 * time.Second

// maybeCloseReader closes r unless NoClose is set. Kept as a helper so the
// error-path and happy-path cleanups stay in sync.
func (p *Player) maybeCloseReader(r io.ReadCloser) {
	if !p.NoClose {
		r.Close()
	}
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
		p.maybeCloseReader(r)
		return fmt.Errorf("player: failed to create stdin pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		p.maybeCloseReader(r)
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
			timer := time.NewTimer(gracefulShutdownTimeout)
			defer timer.Stop()
			select {
			case <-done:
			case <-timer.C:
				_ = cmd.Process.Kill()
			}
		case <-copyDone:
			// Stream ended (stdin closed). Give the player a few seconds to
			// exit on its own (e.g. drain buffered frames), then terminate.
			timer := time.NewTimer(gracefulShutdownTimeout)
			defer timer.Stop()
			select {
			case <-done:
				// Player exited normally after EOF.
			case <-timer.C:
				if cmd.Process != nil {
					_ = cmd.Process.Signal(syscall.SIGTERM)
				}
				killTimer := time.NewTimer(gracefulShutdownTimeout)
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
	p.maybeCloseReader(r)

	// Wait for the copy goroutine to finish cleanly.
	<-copyDone

	return waitErr
}
