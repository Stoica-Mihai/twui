package twitch

import "time"

// Metadata holds stream and channel information.
type Metadata struct {
	ID          string
	Author      string
	AvatarURL   string
	Title       string
	Category    string
	ViewerCount int
	StartedAt   time.Time
}
