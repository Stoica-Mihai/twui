package ui

import (
	"context"
	"time"
)

// EntryKind distinguishes between row types in the TUI.
type EntryKind int

const (
	EntryChannel  EntryKind = iota // a live or offline channel
	EntryCategory                  // a Twitch category/game
	EntryLoadMore                  // sentinel: load next page
)

// DiscoveryEntry is a unified row type for the TUI table.
type DiscoveryEntry struct {
	Kind EntryKind

	// Channel fields (EntryChannel)
	Login           string
	DisplayName     string
	Title           string
	Category        string
	ViewerCount     int
	StartedAt       time.Time
	AvatarURL       string
	IsFavorite      bool
	IsLive          bool

	// Category fields (EntryCategory)
	CategoryName    string
	CategoryViewers int
	BoxArtURL       string

	// Pagination cursor for LoadMore rows
	Cursor string
}

// DiscoveryFuncs holds callbacks that the picker model calls for data fetching.
type DiscoveryFuncs struct {
	// WatchList returns current favorites with their live status.
	WatchList func(ctx context.Context) ([]DiscoveryEntry, error)

	// Search returns live channels matching the query string.
	Search func(ctx context.Context, query string) ([]DiscoveryEntry, error)

	// BrowseCategories returns top categories, with optional cursor for pagination.
	BrowseCategories func(ctx context.Context, cursor string) ([]DiscoveryEntry, string, error)

	// CategoryStreams returns live streams within a category (paginated).
	CategoryStreams func(ctx context.Context, categoryName string, cursor string) ([]DiscoveryEntry, string, error)

	// Streams fetches available quality names for a channel.
	Streams func(ctx context.Context, channel string) ([]string, error)

	// Launch starts playback for channel at quality. send reports status updates;
	// notice pushes a transient one-line footer message (e.g. "1080p60 unavailable
	// — using 720p60"). avatarURL is the channel's profile image URL for
	// notification icons (may be empty).
	Launch func(ctx context.Context, channel, quality, avatarURL string, send func(Status, string), notice func(string))

	// ToggleFavorite adds or removes a channel from favorites.
	ToggleFavorite func(channel string, add bool)

	// ToggleIgnore adds or removes a channel from the ignore list.
	ToggleIgnore func(channel string, add bool)

	// IgnoreList returns all currently ignored channels.
	IgnoreList func() []string

	// HostingChannels returns channels that are hosting the given channel.
	HostingChannels func(ctx context.Context, channel string) ([]DiscoveryEntry, error)

	// WriteTheme persists the selected theme name to the config file.
	WriteTheme func(name string)
}
