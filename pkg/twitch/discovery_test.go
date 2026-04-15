package twitch

import (
	"context"
	"net/http"
	"testing"
)

// --- Live API tests ---
// These make real HTTP calls to the Twitch GQL endpoint.
// Run with: go test ./pkg/twitch/ -run TestLive
// They are skipped under -short.

func TestLive_BrowseCategories(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live Twitch API test")
	}
	api := NewTwitchAPI(http.DefaultClient, "", "", nil, nil)
	cats, next, err := api.BrowseCategories(context.Background(), 5, "")
	if err != nil {
		t.Fatalf("BrowseCategories: %v", err)
	}
	if len(cats) == 0 {
		t.Fatal("expected at least one category, got none")
	}
	for _, c := range cats {
		if c.Name == "" {
			t.Errorf("category has empty name: %+v", c)
		}
		if c.ViewerCount <= 0 {
			t.Errorf("category %q has non-positive viewer count: %d", c.Name, c.ViewerCount)
		}
	}
	t.Logf("got %d categories, first: %q (%d viewers), nextCursor=%q",
		len(cats), cats[0].Name, cats[0].ViewerCount, next)
}

func TestLive_CategoryStreams(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live Twitch API test")
	}
	api := NewTwitchAPI(http.DefaultClient, "", "", nil, nil)
	// Use "Just Chatting" — reliably has many live streams at any hour.
	streams, next, err := api.CategoryStreams(context.Background(), "Just Chatting", 5, "")
	if err != nil {
		t.Fatalf("CategoryStreams: %v", err)
	}
	if len(streams) == 0 {
		t.Fatal("expected at least one stream in Just Chatting, got none")
	}
	for _, s := range streams {
		if s.Login == "" {
			t.Errorf("stream has empty login: %+v", s)
		}
		if s.ViewerCount <= 0 {
			t.Errorf("stream %q has non-positive viewer count: %d", s.Login, s.ViewerCount)
		}
	}
	t.Logf("got %d streams, first: %q (%d viewers), nextCursor=%q",
		len(streams), streams[0].DisplayName, streams[0].ViewerCount, next)
}

func TestLive_SearchChannels(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live Twitch API test")
	}
	api := NewTwitchAPI(http.DefaultClient, "", "", nil, nil)
	results, err := api.SearchChannels(context.Background(), "starcraft", 5)
	if err != nil {
		t.Fatalf("SearchChannels: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one search result for 'starcraft'")
	}
	t.Logf("got %d results, first: %q", len(results), results[0].DisplayName)
}

// --- SearchChannels ---

func TestSearchChannels_Results(t *testing.T) {
	api := newTestAPI(t, gqlOK(`{
		"searchFor": {
			"channels": {
				"items": [
					{
						"id": "123",
						"login": "teststreamer",
						"displayName": "TestStreamer",
						"profileImageURL": "https://example.com/avatar.jpg",
						"stream": {
							"id": "456",
							"title": "Playing games",
							"viewersCount": 1000,
							"createdAt": "2024-01-15T10:00:00Z",
							"game": {"id": "1", "name": "Fortnite"}
						}
					}
				]
			}
		}
	}`))

	results, err := api.SearchChannels(context.Background(), "test", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.Login != "teststreamer" {
		t.Errorf("Login = %q, want %q", r.Login, "teststreamer")
	}
	if r.DisplayName != "TestStreamer" {
		t.Errorf("DisplayName = %q, want %q", r.DisplayName, "TestStreamer")
	}
	if r.Title != "Playing games" {
		t.Errorf("Title = %q, want %q", r.Title, "Playing games")
	}
	if r.ViewerCount != 1000 {
		t.Errorf("ViewerCount = %d, want 1000", r.ViewerCount)
	}
	if r.Category != "Fortnite" {
		t.Errorf("Category = %q, want %q", r.Category, "Fortnite")
	}
	if r.StartedAt.IsZero() {
		t.Error("StartedAt should be set for live stream")
	}
}

func TestSearchChannels_Empty(t *testing.T) {
	api := newTestAPI(t, gqlOK(`{
		"searchFor": {
			"channels": {"items": []}
		}
	}`))

	results, err := api.SearchChannels(context.Background(), "nobody", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearchChannels_GQLError(t *testing.T) {
	api := newTestAPI(t, gqlErrors("search failed"))

	_, err := api.SearchChannels(context.Background(), "test", 10)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestSearchChannels_NoStream(t *testing.T) {
	// Channel with no active stream — StartedAt should be zero.
	api := newTestAPI(t, gqlOK(`{
		"searchFor": {
			"channels": {
				"items": [
					{
						"id": "789",
						"login": "offlineuser",
						"displayName": "OfflineUser",
						"profileImageURL": "",
						"stream": null
					}
				]
			}
		}
	}`))

	results, err := api.SearchChannels(context.Background(), "offline", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].StartedAt.IsZero() {
		t.Error("StartedAt should be zero for channel with no active stream")
	}
}

// --- BrowseCategories ---

func TestBrowseCategories_Results(t *testing.T) {
	api := newTestAPI(t, gqlOK(`{
		"games": {
			"edges": [
				{
					"cursor": "cursor1",
					"node": {
						"id": "1",
						"name": "Just Chatting",
						"viewersCount": 150000,
						"boxArtURL": "https://example.com/art.jpg"
					}
				},
				{
					"cursor": "cursor2",
					"node": {
						"id": "2",
						"name": "Fortnite",
						"viewersCount": 80000,
						"boxArtURL": "https://example.com/fn.jpg"
					}
				}
			],
			"pageInfo": {"hasNextPage": false}
		}
	}`))

	cats, next, err := api.BrowseCategories(context.Background(), 50, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cats) != 2 {
		t.Fatalf("expected 2 categories, got %d", len(cats))
	}
	if cats[0].Name != "Just Chatting" {
		t.Errorf("cats[0].Name = %q, want %q", cats[0].Name, "Just Chatting")
	}
	if cats[0].ViewerCount != 150000 {
		t.Errorf("cats[0].ViewerCount = %d, want 150000", cats[0].ViewerCount)
	}
	if cats[1].Name != "Fortnite" {
		t.Errorf("cats[1].Name = %q, want %q", cats[1].Name, "Fortnite")
	}
	if next != "" {
		t.Errorf("next cursor should be empty on last page, got %q", next)
	}
}

func TestBrowseCategories_HasNextPage(t *testing.T) {
	api := newTestAPI(t, gqlOK(`{
		"games": {
			"edges": [
				{
					"cursor": "nextpagecursor",
					"node": {"id": "1", "name": "Category1", "viewersCount": 1000, "boxArtURL": ""}
				}
			],
			"pageInfo": {"hasNextPage": true}
		}
	}`))

	_, next, err := api.BrowseCategories(context.Background(), 1, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != "nextpagecursor" {
		t.Errorf("next = %q, want %q", next, "nextpagecursor")
	}
}

func TestBrowseCategories_LastPage_ClearsNextCursor(t *testing.T) {
	api := newTestAPI(t, gqlOK(`{
		"games": {
			"edges": [
				{"cursor": "somecursor", "node": {"id": "1", "name": "Cat", "viewersCount": 0, "boxArtURL": ""}}
			],
			"pageInfo": {"hasNextPage": false}
		}
	}`))

	_, next, err := api.BrowseCategories(context.Background(), 1, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != "" {
		t.Errorf("next should be empty when hasNextPage=false, got %q", next)
	}
}

func TestBrowseCategories_NilData(t *testing.T) {
	api := newTestAPI(t, gqlOK(`{"games": null}`))

	cats, next, err := api.BrowseCategories(context.Background(), 10, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cats) != 0 {
		t.Errorf("expected 0 categories for null data, got %d", len(cats))
	}
	if next != "" {
		t.Errorf("expected empty cursor for null data, got %q", next)
	}
}

// --- CategoryStreams ---

func TestCategoryStreams_Results(t *testing.T) {
	api := newTestAPI(t, gqlOK(`{
		"game": {
			"id": "1",
			"name": "Just Chatting",
			"streams": {
				"edges": [
					{
						"cursor": "c1",
						"node": {
							"id": "999",
							"broadcaster": {
								"id": "111",
								"login": "streamer1",
								"displayName": "Streamer1",
								"profileImageURL": "https://example.com/a.jpg"
							},
							"title": "Hello stream",
							"viewersCount": 5000,
							"createdAt": "2024-01-15T08:00:00Z",
							"game": {"id": "1", "name": "Just Chatting"}
						}
					}
				],
				"pageInfo": {"hasNextPage": false}
			}
		}
	}`))

	streams, next, err := api.CategoryStreams(context.Background(), "Just Chatting", 30, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(streams) != 1 {
		t.Fatalf("expected 1 stream, got %d", len(streams))
	}
	s := streams[0]
	if s.Login != "streamer1" {
		t.Errorf("Login = %q, want %q", s.Login, "streamer1")
	}
	if s.DisplayName != "Streamer1" {
		t.Errorf("DisplayName = %q, want %q", s.DisplayName, "Streamer1")
	}
	if s.Title != "Hello stream" {
		t.Errorf("Title = %q, want %q", s.Title, "Hello stream")
	}
	if s.ViewerCount != 5000 {
		t.Errorf("ViewerCount = %d, want 5000", s.ViewerCount)
	}
	if s.Category != "Just Chatting" {
		t.Errorf("Category = %q, want %q", s.Category, "Just Chatting")
	}
	if next != "" {
		t.Errorf("next should be empty on last page, got %q", next)
	}
}

func TestCategoryStreams_LoadMore(t *testing.T) {
	api := newTestAPI(t, gqlOK(`{
		"game": {
			"id": "1",
			"name": "Cat",
			"streams": {
				"edges": [
					{
						"cursor": "page2cursor",
						"node": {
							"id": "1",
							"broadcaster": {"id": "2", "login": "s1", "displayName": "S1", "profileImageURL": ""},
							"title": "stream",
							"viewersCount": 100,
							"createdAt": "2024-01-15T00:00:00Z",
							"game": {"id": "1", "name": "Cat"}
						}
					}
				],
				"pageInfo": {"hasNextPage": true}
			}
		}
	}`))

	_, next, err := api.CategoryStreams(context.Background(), "Cat", 1, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != "page2cursor" {
		t.Errorf("next = %q, want %q", next, "page2cursor")
	}
}

// --- HostingChannels ---

func TestHostingChannels_Hosting(t *testing.T) {
	api := newTestAPI(t, gqlOK(`{
		"user": {
			"hosting": {
				"login": "hostedchan",
				"displayName": "HostedChan"
			}
		}
	}`))

	hosts, err := api.HostingChannels(context.Background(), "somechannel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hosts) != 1 {
		t.Fatalf("expected 1 host, got %d", len(hosts))
	}
	if hosts[0].Login != "hostedchan" {
		t.Errorf("Login = %q, want %q", hosts[0].Login, "hostedchan")
	}
	if hosts[0].DisplayName != "HostedChan" {
		t.Errorf("DisplayName = %q, want %q", hosts[0].DisplayName, "HostedChan")
	}
}

func TestHostingChannels_NotHosting(t *testing.T) {
	api := newTestAPI(t, gqlOK(`{
		"user": {
			"hosting": null
		}
	}`))

	hosts, err := api.HostingChannels(context.Background(), "somechannel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hosts) != 0 {
		t.Errorf("expected 0 hosts when not hosting, got %d", len(hosts))
	}
}

func TestHostingChannels_NullUser(t *testing.T) {
	api := newTestAPI(t, gqlOK(`{"user": null}`))

	hosts, err := api.HostingChannels(context.Background(), "ghost")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hosts) != 0 {
		t.Errorf("expected 0 hosts for null user, got %d", len(hosts))
	}
}
