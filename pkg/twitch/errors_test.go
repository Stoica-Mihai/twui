package twitch

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestErrorSentinels_Messages(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{ErrAccessDenied, "twitch: access token denied"},
		{ErrChannelNotFound, "twitch: channel not found"},
		{ErrChannelOffline, "twitch: channel is not currently live"},
		{ErrGQLServerError, "twitch: gql server error"},
	}
	for _, c := range cases {
		if c.err.Error() != c.want {
			t.Errorf("got %q, want %q", c.err.Error(), c.want)
		}
	}
}

func TestRateLimitedError_MessageWithDuration(t *testing.T) {
	err := &RateLimitedError{RetryAfter: 5 * time.Second}
	if !strings.Contains(err.Error(), "5s") {
		t.Errorf("expected duration in error message, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("expected 'rate limited' in error message, got %q", err.Error())
	}
}

func TestRateLimitedError_MessageWithoutDuration(t *testing.T) {
	err := &RateLimitedError{}
	msg := err.Error()
	if msg != "twitch: rate limited" {
		t.Errorf("got %q, want %q", msg, "twitch: rate limited")
	}
}

func TestGQLServerError_Message(t *testing.T) {
	err := &gqlServerError{Message: `Unknown type "DirectoryRowOptions"`}
	if !strings.Contains(err.Error(), `Unknown type "DirectoryRowOptions"`) {
		t.Errorf("error message %q does not contain the wrapped message", err.Error())
	}
}

func TestGQLServerError_ErrorsIs(t *testing.T) {
	err := &gqlServerError{Message: "something"}
	if !errors.Is(err, ErrGQLServerError) {
		t.Error("errors.Is(gqlServerError, ErrGQLServerError) should be true")
	}
}

func TestGQLServerError_UnwrapReturnsErrGQLServerError(t *testing.T) {
	err := &gqlServerError{Message: "x"}
	if err.Unwrap() != ErrGQLServerError {
		t.Errorf("Unwrap() = %v, want ErrGQLServerError", err.Unwrap())
	}
}
