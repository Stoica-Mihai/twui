package session

import (
	"testing"
	"time"
)

func TestParseRetryAfter_ValidSeconds(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"5", 5 * time.Second},
		{"1", 1 * time.Second},
		{"30", 30 * time.Second},
	}
	for _, tt := range tests {
		got := ParseRetryAfter(tt.input)
		if got != tt.want {
			t.Errorf("ParseRetryAfter(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestParseRetryAfter_CapsAtMax(t *testing.T) {
	if got := ParseRetryAfter("120"); got != MaxRetryAfter {
		t.Errorf("ParseRetryAfter(\"120\") = %v, want %v", got, MaxRetryAfter)
	}
	if got := ParseRetryAfter("999"); got != MaxRetryAfter {
		t.Errorf("ParseRetryAfter(\"999\") = %v, want %v", got, MaxRetryAfter)
	}
}

func TestParseRetryAfter_Empty(t *testing.T) {
	if got := ParseRetryAfter(""); got != 0 {
		t.Errorf("ParseRetryAfter(\"\") = %v, want 0", got)
	}
}

func TestParseRetryAfter_Whitespace(t *testing.T) {
	if got := ParseRetryAfter("  10  "); got != 10*time.Second {
		t.Errorf("ParseRetryAfter(\"  10  \") = %v, want 10s", got)
	}
}

func TestParseRetryAfter_Negative(t *testing.T) {
	if got := ParseRetryAfter("-3"); got != 0 {
		t.Errorf("ParseRetryAfter(\"-3\") = %v, want 0", got)
	}
}

func TestParseRetryAfter_Zero(t *testing.T) {
	if got := ParseRetryAfter("0"); got != 0 {
		t.Errorf("ParseRetryAfter(\"0\") = %v, want 0", got)
	}
}

func TestParseRetryAfter_NonInteger(t *testing.T) {
	tests := []string{"abc", "3.5", "1h", "Thu, 01 Dec 1994 16:00:00 GMT"}
	for _, s := range tests {
		if got := ParseRetryAfter(s); got != 0 {
			t.Errorf("ParseRetryAfter(%q) = %v, want 0", s, got)
		}
	}
}
