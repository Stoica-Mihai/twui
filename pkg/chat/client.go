package chat

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"time"
)

const (
	// DefaultAddr is the TLS endpoint for Twitch IRC.
	DefaultAddr = "irc.chat.twitch.tv:6697"

	defaultInitialReconnectDelay = 1 * time.Second
	defaultMaxReconnectDelay     = 30 * time.Second
	defaultMsgBufferSize         = 64
)

// DialFunc produces a net.Conn for the given address. Overridable so tests
// can plug a net.Pipe server in place of a real TLS dial.
type DialFunc func(ctx context.Context, addr string) (net.Conn, error)

// Client is an anonymous, read-only IRC client scoped to a single Twitch
// channel. Create with NewClient; call Run(ctx) to connect and receive
// messages on Messages() until ctx is cancelled. Run reconnects forever
// with exponential backoff — it only returns when ctx expires.
type Client struct {
	// Channel is the channel to join (no leading '#').
	Channel string

	// Addr is the TCP endpoint to dial. Default: DefaultAddr.
	Addr string

	// Dial produces a net.Conn. nil ⇒ the package's TLS dialer.
	Dial DialFunc

	// Nick overrides the IRC nick. Empty ⇒ random justinfan<n>.
	Nick string

	// Reconnect backoff bounds. Zero values use defaults.
	InitialReconnectDelay time.Duration
	MaxReconnectDelay     time.Duration

	msgs  chan *Chat
	delay time.Duration
}

// NewClient constructs a Client with sensible defaults. Set Dial before
// calling Run to use an in-memory server (net.Pipe) in tests.
func NewClient(channel string) *Client {
	return &Client{
		Channel:               channel,
		Addr:                  DefaultAddr,
		InitialReconnectDelay: defaultInitialReconnectDelay,
		MaxReconnectDelay:     defaultMaxReconnectDelay,
		msgs:                  make(chan *Chat, defaultMsgBufferSize),
	}
}

// Messages returns a receive-only channel of parsed chat messages. The
// channel is closed when Run returns.
func (c *Client) Messages() <-chan *Chat {
	return c.msgs
}

// Run drives the connection: dial → handshake → read loop → reconnect on
// disconnect. Returns ctx.Err() when the caller cancels; otherwise runs
// indefinitely. The Messages() channel is closed on return.
func (c *Client) Run(ctx context.Context) error {
	defer close(c.msgs)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		if c.delay > 0 {
			t := time.NewTimer(c.delay)
			select {
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			case <-t.C:
			}
		}

		err := c.session(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Debug("chat: session ended; will reconnect",
			"channel", c.Channel, "err", err)
		c.bumpDelay()
	}
}

// session runs one connection lifetime: dial, handshake, read loop.
func (c *Client) session(ctx context.Context) error {
	dial := c.Dial
	if dial == nil {
		dial = defaultDialer
	}
	conn, err := dial(ctx, c.Addr)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	if err := c.handshake(conn); err != nil {
		return fmt.Errorf("handshake: %w", err)
	}
	c.resetDelay()

	return c.readLoop(ctx, conn)
}

// defaultDialer dials TLS against irc.chat.twitch.tv.
func defaultDialer(ctx context.Context, addr string) (net.Conn, error) {
	var d tls.Dialer
	return d.DialContext(ctx, "tcp", addr)
}

// handshake writes the three-line anon registration sequence.
func (c *Client) handshake(conn net.Conn) error {
	nick := c.Nick
	if nick == "" {
		nick = fmt.Sprintf("justinfan%d", rand.IntN(10_000_000))
	}
	lines := []string{
		"CAP REQ :twitch.tv/tags twitch.tv/commands",
		"NICK " + nick,
		"JOIN #" + c.Channel,
	}
	for _, l := range lines {
		if _, err := io.WriteString(conn, l+"\r\n"); err != nil {
			return err
		}
	}
	return nil
}

// readLoop reads lines until the conn errors or ctx is cancelled. Handles
// PING/PONG and forwards PRIVMSGs as *Chat onto c.msgs.
func (c *Client) readLoop(ctx context.Context, conn net.Conn) error {
	// Ctx cancellation closes the conn so the blocking read below unblocks.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stop:
		}
	}()

	r := bufio.NewReaderSize(conn, 4096)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return err
		}
		msg, perr := Parse(line)
		if perr != nil {
			continue
		}
		switch msg.Command {
		case "PING":
			resp := "PONG"
			if len(msg.Params) > 0 {
				resp += " :" + msg.Params[0]
			}
			if _, werr := io.WriteString(conn, resp+"\r\n"); werr != nil {
				return werr
			}
		case "RECONNECT":
			return errors.New("server requested reconnect")
		case "PRIVMSG":
			chat, ok := msg.AsChat()
			if !ok {
				continue
			}
			// Non-blocking send: if the consumer is slow, drop rather than
			// stall the read loop (and thus miss PINGs).
			select {
			case c.msgs <- chat:
			case <-ctx.Done():
				return ctx.Err()
			default:
				slog.Debug("chat: dropping message, buffer full",
					"channel", c.Channel)
			}
		}
	}
}

func (c *Client) bumpDelay() {
	initial := c.InitialReconnectDelay
	if initial == 0 {
		initial = defaultInitialReconnectDelay
	}
	maxDelay := c.MaxReconnectDelay
	if maxDelay == 0 {
		maxDelay = defaultMaxReconnectDelay
	}
	if c.delay == 0 {
		c.delay = initial
	} else {
		c.delay *= 2
	}
	if c.delay > maxDelay {
		c.delay = maxDelay
	}
}

func (c *Client) resetDelay() { c.delay = 0 }
