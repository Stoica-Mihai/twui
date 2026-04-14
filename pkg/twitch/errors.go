package twitch

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrAccessDenied    = errors.New("twitch: access token denied")
	ErrChannelNotFound = errors.New("twitch: channel not found")
	ErrChannelOffline  = errors.New("twitch: channel is not currently live")
	ErrGQLServerError  = errors.New("twitch: gql server error")
)

// RateLimitedError is returned when Twitch responds with HTTP 429.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("twitch: rate limited (retry after %s)", e.RetryAfter)
	}
	return "twitch: rate limited"
}
