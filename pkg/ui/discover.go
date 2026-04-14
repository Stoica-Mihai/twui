package ui

import (
	"context"
	"time"

	"github.com/mcs/twui/pkg/twitch"
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
	Login       string
	DisplayName string
	Title       string
	Category    string
	ViewerCount int
	StartedAt   time.Time
	AvatarURL   string
	IsFavorite  bool
	IsLive      bool

	// Category fields (EntryCategory)
	CategoryName string
	CategoryViewers int
	BoxArtURL    string

	// Pagination cursor for LoadMore rows
	Cursor string
}

// DiscoveryFuncs holds callbacks that the picker model calls for data fetching.
// Each function must be non-nil.
type DiscoveryFuncs struct {
	// WatchList returns current favorites with their live status.
	// Channels returned are always the favorites list; IsLive may be false.
	WatchList func(ctx context.Context) ([]DiscoveryEntry, error)

	// Search returns live channels matching the query string.
	Search func(ctx context.Context, query string) ([]DiscoveryEntry, error)

	// BrowseCategories returns top categories, with optional cursor for pagination.
	BrowseCategories func(ctx context.Context, cursor string) ([]DiscoveryEntry, string, error)

	// CategoryStreams returns live streams within a category (paginated).
	CategoryStreams func(ctx context.Context, categoryName string, cursor string) ([]DiscoveryEntry, string, error)

	// Streams fetches available quality names for a channel.
	Streams func(ctx context.Context, channel string) ([]string, error)

	// Launch starts playback for channel at quality. send reports status updates.
	Launch func(ctx context.Context, channel string, quality string, send func(Status, string))

	// ToggleFavorite adds or removes a channel from favorites.
	ToggleFavorite func(channel string, add bool)

	// ToggleIgnore adds or removes a channel from the ignore list.
	ToggleIgnore func(channel string, add bool)

	// IgnoreList returns all currently ignored channels.
	IgnoreList func() []string
}

// channelToEntry converts a twitch.ChannelResult to a DiscoveryEntry.
func channelToEntry(r twitch.ChannelResult, isLive bool) DiscoveryEntry {
	return DiscoveryEntry{
		Kind:        EntryChannel,
		Login:       r.Login,
		DisplayName: r.DisplayName,
		Title:       r.Title,
		Category:    r.Category,
		ViewerCount: r.ViewerCount,
		StartedAt:   r.StartedAt,
		AvatarURL:   r.AvatarURL,
		IsLive:      isLive,
	}
}

// categoryToEntry converts a twitch.CategoryResult to a DiscoveryEntry.
func categoryToEntry(r twitch.CategoryResult) DiscoveryEntry {
	return DiscoveryEntry{
		Kind:            EntryCategory,
		CategoryName:    r.Name,
		CategoryViewers: r.ViewerCount,
		BoxArtURL:       r.BoxArtURL,
	}
}
