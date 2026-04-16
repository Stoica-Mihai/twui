package session

import (
	"net/http"
	"testing"
)

func TestNew_ReturnsValidSession(t *testing.T) {
	s := New()
	if s == nil {
		t.Fatal("New() returned nil")
	}
	if s.HTTP == nil {
		t.Error("HTTP client is nil")
	}
	if s.Options == nil {
		t.Error("Options is nil")
	}
	// Cache may be nil if user config dir is unavailable, but the session
	// should still be usable.
}

func TestNew_SetsDefaultOptions(t *testing.T) {
	s := New()

	ua := s.Options.GetString("http-user-agent")
	if ua == "" {
		t.Error("default user-agent should be set")
	}
	if ua != defaultUserAgent {
		t.Errorf("user-agent = %q, want %q", ua, defaultUserAgent)
	}
}

func TestNew_HTTPClientHasTransport(t *testing.T) {
	s := New()
	if _, ok := s.HTTP.Transport.(*http.Transport); !ok {
		t.Error("HTTP Transport should be *http.Transport")
	}
}

func TestNew_HTTPClientTimeout(t *testing.T) {
	s := New()
	if s.HTTP.Timeout != defaultTimeout {
		t.Errorf("HTTP timeout = %v, want %v", s.HTTP.Timeout, defaultTimeout)
	}
}

func TestWithUserAgent_SetsUserAgent(t *testing.T) {
	s := New()
	s.WithUserAgent("custom-agent/1.0")

	got := s.Options.GetString("http-user-agent")
	if got != "custom-agent/1.0" {
		t.Errorf("user-agent = %q, want %q", got, "custom-agent/1.0")
	}
}

func TestWithUserAgent_OverridesDefault(t *testing.T) {
	s := New()
	s.WithUserAgent("override")

	got := s.Options.GetString("http-user-agent")
	if got == defaultUserAgent {
		t.Error("WithUserAgent should override the default user-agent")
	}
	if got != "override" {
		t.Errorf("user-agent = %q, want %q", got, "override")
	}
}

func TestClose_SafeToCall(t *testing.T) {
	s := New()
	// Close should not panic.
	s.Close()
}

func TestClose_SafeToCallTwice(t *testing.T) {
	s := New()
	s.Close()
	s.Close() // second call should not panic
}
