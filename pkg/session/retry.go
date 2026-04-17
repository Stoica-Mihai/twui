package session

import (
	"strconv"
	"strings"
	"time"
)

// MaxRetryAfter caps how long to honor a server-supplied Retry-After header.
const MaxRetryAfter = 30 * time.Second

// ParseRetryAfter parses a Retry-After header value as integer seconds.
// Returns 0 for empty, negative, zero, or non-integer values.
// The result is capped at MaxRetryAfter.
func ParseRetryAfter(header string) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || n <= 0 {
		return 0
	}
	d := time.Duration(n) * time.Second
	if d > MaxRetryAfter {
		d = MaxRetryAfter
	}
	return d
}
