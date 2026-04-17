package ui

// Status represents the current stream state for display.
type Status int

const (
	StatusWaiting Status = iota
	StatusPlaying
	StatusAdBreak
	StatusReconnecting
)
