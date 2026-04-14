package twitch

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// CategoryResult represents a Twitch game/category in browse results.
type CategoryResult struct {
	ID          string
	Name        string
	ViewerCount int
	BoxArtURL   string // resizable template URL e.g. ".../300x400.jpg"
}

// ChannelResult represents a channel returned by search or directory queries.
type ChannelResult struct {
	ID          string
	Login       string // lowercase login name
	DisplayName string
	Title       string
	Category    string
	ViewerCount int
	StartedAt   time.Time
	AvatarURL   string
}

// HostResult represents a channel that is hosting or related to another.
type HostResult struct {
	Login       string
	DisplayName string
}

// SearchChannels searches for live channels matching query.
// Returns up to limit results (Twitch default/max ~20).
func (a *TwitchAPI) SearchChannels(ctx context.Context, query string, limit int) ([]ChannelResult, error) {
	if limit <= 0 {
		limit = 20
	}

	variables := map[string]any{
		"query":                 query,
		"first":                 limit,
		"includeIsDJ":           false,
		"requestID":             "",
		"context":               "SEARCH",
		"platform":              "web",
		"isLiverailEnabled":     false,
		"isVodEnabled":          true,
		"isMobile":              false,
		"isGameEnabled":         false,
		"isChannelEnabled":      true,
		"shouldRenderPersonalized": false,
	}

	body, err := a.doGQL(ctx, "SearchResultsPage", "", variables, nil)
	if err != nil {
		return nil, fmt.Errorf("twitch: search channels: %w", err)
	}

	var data struct {
		SearchFor *struct {
			Channels *struct {
				Items []struct {
					ID              string `json:"id"`
					Login           string `json:"login"`
					DisplayName     string `json:"displayName"`
					ProfileImageURL string `json:"profileImageURL"`
					Stream          *struct {
						Title        string `json:"title"`
						ViewersCount int    `json:"viewersCount"`
						CreatedAt    string `json:"createdAt"`
						Game         *struct {
							Name string `json:"name"`
						} `json:"game"`
					} `json:"stream"`
				} `json:"items"`
			} `json:"channels"`
		} `json:"searchFor"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("twitch: parse search results: %w", err)
	}

	if data.SearchFor == nil || data.SearchFor.Channels == nil {
		return nil, nil
	}

	results := make([]ChannelResult, 0, len(data.SearchFor.Channels.Items))
	for _, item := range data.SearchFor.Channels.Items {
		r := ChannelResult{
			ID:          item.ID,
			Login:       item.Login,
			DisplayName: item.DisplayName,
			AvatarURL:   item.ProfileImageURL,
		}
		if item.Stream != nil {
			r.Title = item.Stream.Title
			r.ViewerCount = item.Stream.ViewersCount
			if item.Stream.CreatedAt != "" {
				if t, err := time.Parse(time.RFC3339, item.Stream.CreatedAt); err == nil {
					r.StartedAt = t
				}
			}
			if item.Stream.Game != nil {
				r.Category = item.Stream.Game.Name
			}
		}
		results = append(results, r)
	}

	return results, nil
}

// BrowseCategories returns top-level categories sorted by viewer count.
// cursor is empty on first call; pass the returned cursor for pagination.
func (a *TwitchAPI) BrowseCategories(ctx context.Context, limit int, cursor string) ([]CategoryResult, string, error) {
	if limit <= 0 {
		limit = 30
	}

	variables := map[string]any{
		"limit":                  limit,
		"options":                map[string]any{},
		"sortTypeIsRecency":      false,
		"freeformTagsEnabled":    false,
		"isPaginationEnabled":    true,
		"cursor":                 cursor,
	}

	body, err := a.doGQL(ctx, "BrowsePage_AllDirectories", "", variables, nil)
	if err != nil {
		return nil, "", fmt.Errorf("twitch: browse categories: %w", err)
	}

	var data struct {
		DirectoriesWithTags *struct {
			Edges []struct {
				Cursor string `json:"cursor"`
				Node   struct {
					ID          string `json:"id"`
					Name        string `json:"name"`
					ViewersCount int   `json:"viewersCount"`
					BoxArtURL   string `json:"boxArtURL"`
				} `json:"node"`
			} `json:"edges"`
			PageInfo *struct {
				HasNextPage bool `json:"hasNextPage"`
			} `json:"pageInfo"`
		} `json:"directoriesWithTags"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, "", fmt.Errorf("twitch: parse browse categories: %w", err)
	}

	if data.DirectoriesWithTags == nil {
		return nil, "", nil
	}

	results := make([]CategoryResult, 0, len(data.DirectoriesWithTags.Edges))
	var nextCursor string
	for _, edge := range data.DirectoriesWithTags.Edges {
		results = append(results, CategoryResult{
			ID:          edge.Node.ID,
			Name:        edge.Node.Name,
			ViewerCount: edge.Node.ViewersCount,
			BoxArtURL:   edge.Node.BoxArtURL,
		})
		nextCursor = edge.Cursor
	}

	if data.DirectoriesWithTags.PageInfo != nil && !data.DirectoriesWithTags.PageInfo.HasNextPage {
		nextCursor = ""
	}

	return results, nextCursor, nil
}

// CategoryStreams returns live streams within a category.
// cursor is empty on first call; returns next cursor for pagination.
func (a *TwitchAPI) CategoryStreams(ctx context.Context, categoryName string, limit int, cursor string) ([]ChannelResult, string, error) {
	if limit <= 0 {
		limit = 30
	}

	variables := map[string]any{
		"categoryName": categoryName,
		"options": map[string]any{
			"sort": "VIEWER_COUNT",
			"recommendationsContext": map[string]any{
				"platform": "web",
			},
			"requestID": "",
			"freeformTags": nil,
			"tags":         []any{},
		},
		"sortTypeIsRecency": false,
		"limit":             limit,
		"cursor":            cursor,
	}

	body, err := a.doGQL(ctx, "DirectoryPage_Game", "", variables, nil)
	if err != nil {
		return nil, "", fmt.Errorf("twitch: category streams: %w", err)
	}

	var data struct {
		Game *struct {
			Streams *struct {
				Edges []struct {
					Cursor string `json:"cursor"`
					Node   struct {
						ID        string `json:"id"`
						Title     string `json:"title"`
						ViewersCount int `json:"viewersCount"`
						CreatedAt string `json:"createdAt"`
						Broadcaster struct {
							ID              string `json:"id"`
							Login           string `json:"login"`
							DisplayName     string `json:"displayName"`
							ProfileImageURL string `json:"profileImageURL"`
						} `json:"broadcaster"`
						Game *struct {
							Name string `json:"name"`
						} `json:"game"`
					} `json:"node"`
				} `json:"edges"`
				PageInfo *struct {
					HasNextPage bool `json:"hasNextPage"`
				} `json:"pageInfo"`
			} `json:"streams"`
		} `json:"game"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, "", fmt.Errorf("twitch: parse category streams: %w", err)
	}

	if data.Game == nil || data.Game.Streams == nil {
		return nil, "", nil
	}

	results := make([]ChannelResult, 0, len(data.Game.Streams.Edges))
	var nextCursor string
	for _, edge := range data.Game.Streams.Edges {
		node := edge.Node
		r := ChannelResult{
			ID:          node.Broadcaster.ID,
			Login:       node.Broadcaster.Login,
			DisplayName: node.Broadcaster.DisplayName,
			AvatarURL:   node.Broadcaster.ProfileImageURL,
			Title:       node.Title,
			ViewerCount: node.ViewersCount,
		}
		if node.CreatedAt != "" {
			if t, err := time.Parse(time.RFC3339, node.CreatedAt); err == nil {
				r.StartedAt = t
			}
		}
		if node.Game != nil {
			r.Category = node.Game.Name
		}
		results = append(results, r)
		nextCursor = edge.Cursor
	}

	if data.Game.Streams.PageInfo != nil && !data.Game.Streams.PageInfo.HasNextPage {
		nextCursor = ""
	}

	return results, nextCursor, nil
}

// HostingChannels returns channels that are hosting the given channel.
func (a *TwitchAPI) HostingChannels(ctx context.Context, channel string) ([]HostResult, error) {
	variables := map[string]any{
		"channelLogin": channel,
	}

	body, err := a.doGQL(ctx, "ChannelPage_HostInfo", "", variables, nil)
	if err != nil {
		return nil, fmt.Errorf("twitch: hosting channels: %w", err)
	}

	var data struct {
		User *struct {
			Hosting *struct {
				Login       string `json:"login"`
				DisplayName string `json:"displayName"`
			} `json:"hosting"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("twitch: parse hosting channels: %w", err)
	}

	if data.User == nil || data.User.Hosting == nil {
		return nil, nil
	}

	return []HostResult{{
		Login:       data.User.Hosting.Login,
		DisplayName: data.User.Hosting.DisplayName,
	}}, nil
}

// fallback query text for discovery operations (hashes start empty → always fallback)
func init() {
	fallbackQueries["SearchResultsPage"] = `query SearchResultsPage($query: String!, $first: Int, $context: String, $cursor: String, $platform: String, $requestID: String, $includeIsDJ: Boolean!, $isVodEnabled: Boolean!, $isGameEnabled: Boolean!, $isChannelEnabled: Boolean!, $isMobile: Boolean!, $isLiverailEnabled: Boolean!, $shouldRenderPersonalized: Boolean!) {
  searchFor(userQuery: $query, platform: $platform, requestID: $requestID, target: {cursor: $cursor, index: CHANNEL, limit: $first, sessionID: ""}, shouldRenderPersonalized: $shouldRenderPersonalized) @include(if: $isChannelEnabled) {
    channels {
      items {
        id
        login
        displayName
        profileImageURL(width: 70)
        stream {
          id
          title
          viewersCount
          createdAt
          game {
            id
            name
            __typename
          }
          __typename
        }
        __typename
      }
      __typename
    }
    __typename
  }
}`

	fallbackQueries["BrowsePage_AllDirectories"] = `query BrowsePage_AllDirectories($limit: Int, $cursor: String, $options: DirectoryRowOptions, $sortTypeIsRecency: Boolean!, $freeformTagsEnabled: Boolean!, $isPaginationEnabled: Boolean!) {
  directoriesWithTags(first: $limit, after: $cursor, options: $options) {
    edges {
      cursor
      node {
        id
        name
        viewersCount
        boxArtURL(width: 188, height: 250)
        __typename
      }
      __typename
    }
    pageInfo {
      hasNextPage
    }
    __typename
  }
}`

	fallbackQueries["DirectoryPage_Game"] = `query DirectoryPage_Game($categoryName: String!, $options: StreamOptions, $sortTypeIsRecency: Boolean!, $limit: Int!, $cursor: String) {
  game(name: $categoryName) {
    id
    name
    streams(first: $limit, after: $cursor, options: $options) {
      edges {
        cursor
        node {
          id
          title
          viewersCount
          createdAt
          broadcaster {
            id
            login
            displayName
            profileImageURL(width: 70)
            __typename
          }
          game {
            id
            name
            __typename
          }
          __typename
        }
        __typename
      }
      pageInfo {
        hasNextPage
      }
      __typename
    }
    __typename
  }
}`

	fallbackQueries["ChannelPage_HostInfo"] = `query ChannelPage_HostInfo($channelLogin: String!) {
  user(login: $channelLogin) {
    id
    hosting {
      id
      login
      displayName
      __typename
    }
    __typename
  }
}`
}
