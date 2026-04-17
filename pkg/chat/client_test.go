package chat

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// pipeClient returns a Client wired to one side of a net.Pipe, along with the
// server side of the pipe for the test to read/write on. The Client has fast
// reconnect delays so retry tests finish in under a second.
func pipeClient(t *testing.T, channel string) (*Client, net.Conn, context.CancelFunc, <-chan error) {
	t.Helper()
	server, client := net.Pipe()

	c := NewClient(channel)
	c.Dial = func(ctx context.Context, addr string) (net.Conn, error) {
		return client, nil
	}
	c.InitialReconnectDelay = 5 * time.Millisecond
	c.MaxReconnectDelay = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	t.Cleanup(func() {
		cancel()
		_ = server.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("Run did not return within 1s after cancel")
		}
	})

	return c, server, cancel, done
}

// readLines reads up to n \n-terminated lines from r with a bounded deadline.
// Returns the lines (stripped of CRLF) or fails the test.
func readLines(t *testing.T, r io.Reader, n int) []string {
	t.Helper()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 4096), 8192)
	lines := make([]string, 0, n)
	for i := 0; i < n; i++ {
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				t.Fatalf("scan line %d: %v", i, err)
			}
			t.Fatalf("scan line %d: EOF after %d lines", i, len(lines))
		}
		lines = append(lines, strings.TrimRight(scanner.Text(), "\r"))
	}
	return lines
}

func TestClient_HandshakeSequence(t *testing.T) {
	_, server, _, _ := pipeClient(t, "MeNotSanta")

	lines := readLines(t, server, 3)
	if !strings.HasPrefix(lines[0], "CAP REQ") {
		t.Errorf("line 0: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "NICK justinfan") {
		t.Errorf("line 1: %q", lines[1])
	}
	if lines[2] != "JOIN #MeNotSanta" {
		t.Errorf("line 2: %q", lines[2])
	}
}

func TestClient_HandshakeUsesProvidedNick(t *testing.T) {
	server, client := net.Pipe()

	c := NewClient("test")
	c.Nick = "justinfan12345"
	c.Dial = func(ctx context.Context, addr string) (net.Conn, error) { return client, nil }
	c.InitialReconnectDelay = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
		<-done
	})

	lines := readLines(t, server, 3)
	if lines[1] != "NICK justinfan12345" {
		t.Errorf("NICK line = %q, want NICK justinfan12345", lines[1])
	}
}

func TestClient_RespondsToPingWithPong(t *testing.T) {
	_, server, _, _ := pipeClient(t, "c")
	readLines(t, server, 3) // drain handshake

	if _, err := fmt.Fprint(server, "PING :tmi.twitch.tv\r\n"); err != nil {
		t.Fatalf("write ping: %v", err)
	}

	lines := readLines(t, server, 1)
	if lines[0] != "PONG :tmi.twitch.tv" {
		t.Errorf("PONG reply = %q", lines[0])
	}
}

func TestClient_DeliversPrivmsgAsChat(t *testing.T) {
	c, server, _, _ := pipeClient(t, "c")
	readLines(t, server, 3)

	line := `@display-name=Alice;color=#00FF00 :alice!alice@alice.tmi.twitch.tv PRIVMSG #c :hello world` + "\r\n"
	if _, err := fmt.Fprint(server, line); err != nil {
		t.Fatalf("write privmsg: %v", err)
	}

	select {
	case chat := <-c.Messages():
		if chat.Text != "hello world" {
			t.Errorf("Text = %q", chat.Text)
		}
		if chat.Login != "alice" {
			t.Errorf("Login = %q", chat.Login)
		}
		if chat.DisplayName != "Alice" {
			t.Errorf("DisplayName = %q", chat.DisplayName)
		}
		if chat.Color != "#00FF00" {
			t.Errorf("Color = %q", chat.Color)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no message received in 2s")
	}
}

func TestClient_IgnoresNonPrivmsg(t *testing.T) {
	c, server, _, _ := pipeClient(t, "c")
	readLines(t, server, 3)

	// NOTICE and ROOMSTATE should be parsed but not forwarded to Messages().
	fmt.Fprint(server, ":tmi.twitch.tv NOTICE #c :welcome\r\n")
	fmt.Fprint(server, "@emote-only=0 :tmi.twitch.tv ROOMSTATE #c\r\n")
	fmt.Fprint(server, ":alice!alice@alice.tmi PRIVMSG #c :hi\r\n")

	select {
	case chat := <-c.Messages():
		if chat.Text != "hi" {
			t.Errorf("first forwarded message should be the privmsg, got %+v", chat)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no message received")
	}
}

func TestClient_ServerReconnectTriggersNewSession(t *testing.T) {
	// Custom Dial that hands out a fresh pipe each time Run reconnects. We
	// count dial attempts to verify the client retries.
	var (
		mu          sync.Mutex
		dialCount   atomic.Int32
		currentSrv  net.Conn
		newServerCh = make(chan net.Conn, 4)
	)

	c := NewClient("c")
	c.InitialReconnectDelay = 5 * time.Millisecond
	c.MaxReconnectDelay = 20 * time.Millisecond
	c.Dial = func(ctx context.Context, addr string) (net.Conn, error) {
		dialCount.Add(1)
		srv, cli := net.Pipe()
		mu.Lock()
		currentSrv = srv
		mu.Unlock()
		// Non-blocking publish; tests drain.
		select {
		case newServerCh <- srv:
		default:
		}
		return cli, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		mu.Lock()
		if currentSrv != nil {
			_ = currentSrv.Close()
		}
		mu.Unlock()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("Run didn't return")
		}
	})

	// First session.
	srv1 := <-newServerCh
	readLines(t, srv1, 3)

	// Simulate server dropping us.
	_ = srv1.Close()

	// Client should reconnect; second dial and handshake follow.
	select {
	case srv2 := <-newServerCh:
		readLines(t, srv2, 3)
	case <-time.After(2 * time.Second):
		t.Fatalf("no reconnect; dialCount=%d", dialCount.Load())
	}

	if dialCount.Load() < 2 {
		t.Errorf("dialCount = %d, want ≥2", dialCount.Load())
	}
}

func TestClient_ServerRECONNECTCommandForcesReconnect(t *testing.T) {
	var (
		dialCount   atomic.Int32
		newServerCh = make(chan net.Conn, 4)
	)

	c := NewClient("c")
	c.InitialReconnectDelay = 5 * time.Millisecond
	c.Dial = func(ctx context.Context, addr string) (net.Conn, error) {
		dialCount.Add(1)
		srv, cli := net.Pipe()
		select {
		case newServerCh <- srv:
		default:
		}
		return cli, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	srv1 := <-newServerCh
	readLines(t, srv1, 3)

	// Server sends RECONNECT; client should tear down and reconnect.
	fmt.Fprint(srv1, "RECONNECT\r\n")
	// Don't close srv1 — let the client detect the RECONNECT command. If the
	// pipe closes too quickly the error path masks the intended test.

	select {
	case srv2 := <-newServerCh:
		readLines(t, srv2, 3)
	case <-time.After(2 * time.Second):
		t.Fatalf("no reconnect after RECONNECT command; dialCount=%d", dialCount.Load())
	}
}

func TestClient_BumpDelayIsExponentialWithCeiling(t *testing.T) {
	c := NewClient("c")
	c.InitialReconnectDelay = 10 * time.Millisecond
	c.MaxReconnectDelay = 40 * time.Millisecond

	want := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		40 * time.Millisecond,
		40 * time.Millisecond, // clamped
	}
	for i, w := range want {
		c.bumpDelay()
		if c.delay != w {
			t.Errorf("step %d: delay = %v, want %v", i, c.delay, w)
		}
	}
	c.resetDelay()
	if c.delay != 0 {
		t.Errorf("resetDelay: delay = %v, want 0", c.delay)
	}
}

func TestClient_RunReturnsOnCancelWithErr(t *testing.T) {
	c := NewClient("c")
	c.Dial = func(ctx context.Context, addr string) (net.Conn, error) {
		return nil, errors.New("boom")
	}
	c.InitialReconnectDelay = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}
	// msgs channel must be closed.
	if _, ok := <-c.Messages(); ok {
		t.Error("Messages channel should be closed after Run returns")
	}
}
