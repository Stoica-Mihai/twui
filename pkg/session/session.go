package session

import (
	"net"
	"net/http"
	"time"
)

const (
	defaultTimeout   = 60 * time.Second
	defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36"
)

// Session holds the shared HTTP client, option store, and file-backed cache.
type Session struct {
	HTTP    *http.Client
	Options *Options
	Cache   *Cache
}

// New creates a new Session with sensible defaults.
func New() *Session {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConns:        100,
		// Default is 2, which causes connection churn when several goroutines
		// hit the same Twitch host concurrently (e.g. streaming favorites).
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}

	options := NewOptions()
	options.SetDefault("http-user-agent", defaultUserAgent)

	cache, _ := NewCache()

	return &Session{
		HTTP: &http.Client{
			Timeout:   defaultTimeout,
			Transport: transport,
		},
		Options: options,
		Cache:   cache,
	}
}

// WithUserAgent returns a copy of the session with a custom User-Agent.
func (s *Session) WithUserAgent(ua string) {
	s.Options.Set("http-user-agent", ua)
}

// Close releases idle HTTP connections.
func (s *Session) Close() {
	if t, ok := s.HTTP.Transport.(*http.Transport); ok {
		t.CloseIdleConnections()
	}
}
