package main

import (
	"context"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mcs/twui/pkg/chat"
	"github.com/mcs/twui/pkg/ui"
)

// demoFuncs returns a ui.DiscoveryFuncs wired against hardcoded fixtures so
// the TUI can be run (and screenshotted) with zero network. Every callback
// is deterministic and in-memory; favorites/ignore mutations persist for the
// lifetime of the process only.
func demoFuncs() ui.DiscoveryFuncs {
	start := time.Now()

	// Shape a stable snapshot of the watchlist. Uptimes are computed from
	// `start` so they tick forward naturally while the demo runs.
	watchlist := func() []ui.DiscoveryEntry {
		return []ui.DiscoveryEntry{
			{Kind: ui.EntryChannel, Login: "MeNotSanta", DisplayName: "MeNotSanta", Title: "blind playthrough — chapter 7 no deaths", Category: "Minecraft", ViewerCount: 892, IsLive: true, IsFavorite: true, StartedAt: start.Add(-47 * time.Minute)},
			{Kind: ui.EntryChannel, Login: "alpha", DisplayName: "Alpha", Title: "speedrun attempts until i forget to breathe", Category: "Software and Game Development", ViewerCount: 47200, IsLive: true, IsFavorite: true, StartedAt: start.Add(-5*time.Hour - 12*time.Minute)},
			{Kind: ui.EntryChannel, Login: "beta", DisplayName: "Beta", Title: "late-night chill stream — q&a", Category: "Just Chatting", ViewerCount: 12400, IsLive: true, IsFavorite: true, StartedAt: start.Add(-1*time.Hour - 47*time.Minute)},
			{Kind: ui.EntryChannel, Login: "gamma", DisplayName: "Gamma", Title: "ranked solo queue, climbing from gold", Category: "Counter-Strike 2", ViewerCount: 315, IsLive: true, IsFavorite: true, StartedAt: start.Add(-23 * time.Minute)},
			{Kind: ui.EntryChannel, Login: "delta", DisplayName: "Delta", Title: "building a mechanical keyboard from scratch", Category: "Software and Game Development", ViewerCount: 28, IsLive: true, IsFavorite: true, StartedAt: start.Add(-8*time.Hour - 4*time.Minute)},
			{Kind: ui.EntryChannel, Login: "epsilon", DisplayName: "Epsilon", IsFavorite: true, IsLive: false},
			{Kind: ui.EntryChannel, Login: "zeta", DisplayName: "Zeta", IsFavorite: true, IsLive: false},
		}
	}

	categoriesByViewers := []ui.DiscoveryEntry{
		{Kind: ui.EntryCategory, CategoryName: "Just Chatting", CategoryViewers: 428_000},
		{Kind: ui.EntryCategory, CategoryName: "Software and Game Development", CategoryViewers: 41_200},
		{Kind: ui.EntryCategory, CategoryName: "Counter-Strike 2", CategoryViewers: 126_500},
		{Kind: ui.EntryCategory, CategoryName: "Chess", CategoryViewers: 18_700},
		{Kind: ui.EntryCategory, CategoryName: "Minecraft", CategoryViewers: 83_400},
		{Kind: ui.EntryCategory, CategoryName: "Art", CategoryViewers: 9_240},
	}

	// In-memory favorite/ignore state. Seeded from the watchlist so hitting
	// `f` already shows the star toggled on.
	mu := sync.Mutex{}
	favs := map[string]bool{}
	ignored := map[string]bool{}
	for _, e := range watchlist() {
		if e.IsFavorite {
			favs[e.Login] = true
		}
	}

	// categoryStreams generates a stable set of fake streams for a category.
	// Names are drawn from a neutral pool; counts look realistic.
	categoryStreams := func(category string) []ui.DiscoveryEntry {
		logins := []string{"nebula", "cobalt", "quasar", "pixel_drift", "starlight", "ember", "juniper", "halcyon"}
		titles := []string{
			"day 12 — no-hit run",
			"casual session, come hang",
			"learning the ropes, clueless but enthusiastic",
			"back after a week off",
			"tournament prep — feedback welcome",
			"theorycrafting + tier list",
			"full playthrough marathon",
			"community night — viewer games",
		}
		out := make([]ui.DiscoveryEntry, 0, len(logins))
		for i, login := range logins {
			out = append(out, ui.DiscoveryEntry{
				Kind:        ui.EntryChannel,
				Login:       login,
				DisplayName: login,
				Title:       titles[i%len(titles)],
				Category:    category,
				ViewerCount: 100 + (i*17+len(category))*53%9000,
				StartedAt:   start.Add(-time.Duration((i+1)*23) * time.Minute),
				IsLive:      true,
				IsFavorite:  favs[login],
			})
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].ViewerCount > out[j].ViewerCount })
		return out
	}

	applyIgnored := func(entries []ui.DiscoveryEntry) []ui.DiscoveryEntry {
		mu.Lock()
		defer mu.Unlock()
		out := make([]ui.DiscoveryEntry, 0, len(entries))
		for _, e := range entries {
			if e.Kind == ui.EntryChannel && ignored[e.Login] {
				continue
			}
			if e.Kind == ui.EntryChannel {
				e.IsFavorite = favs[e.Login]
			}
			out = append(out, e)
		}
		return out
	}

	return ui.DiscoveryFuncs{
		WatchList: func(ctx context.Context) ([]ui.DiscoveryEntry, error) {
			return applyIgnored(watchlist()), nil
		},

		Search: func(ctx context.Context, query string) ([]ui.DiscoveryEntry, error) {
			q := strings.ToLower(strings.TrimSpace(query))
			if q == "" {
				return nil, nil
			}
			pool := append(watchlist(), categoryStreams("Just Chatting")...)
			out := make([]ui.DiscoveryEntry, 0, len(pool))
			seen := map[string]bool{}
			for _, e := range pool {
				if e.Kind != ui.EntryChannel || seen[e.Login] {
					continue
				}
				if strings.Contains(strings.ToLower(e.Login), q) || strings.Contains(strings.ToLower(e.DisplayName), q) {
					seen[e.Login] = true
					out = append(out, e)
				}
			}
			return applyIgnored(out), nil
		},

		BrowseCategories: func(ctx context.Context, cursor string) ([]ui.DiscoveryEntry, string, error) {
			cats := make([]ui.DiscoveryEntry, len(categoriesByViewers))
			copy(cats, categoriesByViewers)
			sort.SliceStable(cats, func(i, j int) bool { return cats[i].CategoryViewers > cats[j].CategoryViewers })
			return cats, "", nil
		},

		CategoryStreams: func(ctx context.Context, category, cursor string) ([]ui.DiscoveryEntry, string, error) {
			return applyIgnored(categoryStreams(category)), "", nil
		},

		Streams: func(ctx context.Context, channel string) ([]string, error) {
			return []string{"source", "1080p60", "720p60", "480p", "360p", "160p"}, nil
		},

		Launch: func(ctx context.Context, channel, quality, avatarURL string, send func(ui.Status, string), notice func(string)) {
			send(ui.StatusWaiting, "")
			select {
			case <-time.After(300 * time.Millisecond):
			case <-ctx.Done():
				return
			}
			detail := quality
			if detail == "" {
				detail = "1080p60"
			}
			send(ui.StatusPlaying, detail)
			<-ctx.Done()
		},

		ToggleFavorite: func(channel string, add bool) {
			mu.Lock()
			defer mu.Unlock()
			if add {
				favs[channel] = true
			} else {
				delete(favs, channel)
			}
		},

		ToggleIgnore: func(channel string, add bool) {
			mu.Lock()
			defer mu.Unlock()
			if add {
				ignored[channel] = true
			} else {
				delete(ignored, channel)
			}
		},

		IgnoreList: func() []string {
			mu.Lock()
			defer mu.Unlock()
			out := make([]string, 0, len(ignored))
			for ch := range ignored {
				out = append(out, ch)
			}
			sort.Strings(out)
			return out
		},

		RelatedChannels: func(ctx context.Context, channel, category string) ([]ui.DiscoveryEntry, error) {
			streams := categoryStreams(category)
			out := make([]ui.DiscoveryEntry, 0, len(streams))
			for _, e := range streams {
				if e.Login == channel {
					continue
				}
				out = append(out, e)
			}
			return applyIgnored(out), nil
		},

		WriteTheme: func(name string) {},
	}
}

// --- Demo chat source ---

// demoChatFactory returns a ui.ChatSource that emits scripted messages. The
// channel argument is echoed back in every message's Channel field so the
// renderer sees consistent routing.
func demoChatFactory(channel string) ui.ChatSource {
	return &demoChatClient{channel: channel, msgs: make(chan *chat.Chat, 16)}
}

type demoChatClient struct {
	channel string
	msgs    chan *chat.Chat
}

func (d *demoChatClient) Messages() <-chan *chat.Chat { return d.msgs }

// Run walks the demo script in a loop, emitting one message per ~1-2s with
// jitter, until ctx is cancelled. Sends are non-blocking so a slow consumer
// can't stall the loop (matches pkg/chat/client.go behaviour).
func (d *demoChatClient) Run(ctx context.Context) error {
	defer close(d.msgs)
	script := demoChatScript(d.channel)
	i := 0
	// Kick off with a short warm-up so the pane doesn't sit empty the first
	// second after opening.
	select {
	case <-time.After(150 * time.Millisecond):
	case <-ctx.Done():
		return ctx.Err()
	}
	for {
		msg := script[i%len(script)]
		msg.Sent = time.Now()
		select {
		case d.msgs <- &msg:
		default: // drop if consumer is behind
		}
		i++
		// 900ms–2100ms between messages.
		wait := 900*time.Millisecond + time.Duration(rand.Int64N(1200))*time.Millisecond
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// demoChatScript returns a deterministic-ish list of fake chat messages for
// a channel. Logins are neutral tokens (no real handles); badges exercise
// every rendered kind; one message is intentionally long to demonstrate
// wrapping across multiple lines.
func demoChatScript(channel string) []chat.Chat {
	bcaster := []chat.Badge{{Name: "broadcaster", Version: "1"}}
	mod := []chat.Badge{{Name: "moderator", Version: "1"}}
	vip := []chat.Badge{{Name: "vip", Version: "1"}}
	sub3 := []chat.Badge{{Name: "subscriber", Version: "3"}}
	sub12 := []chat.Badge{{Name: "subscriber", Version: "12"}}
	subT2 := []chat.Badge{{Name: "subscriber", Version: "2012"}}
	subT3 := []chat.Badge{{Name: "subscriber", Version: "3024"}}

	return []chat.Chat{
		{Channel: channel, Login: channel, DisplayName: channel, Badges: bcaster, Text: "welcome in, pull up a chair"},
		{Channel: channel, Login: "nebula", DisplayName: "Nebula", Color: "#4fc3f7", Badges: mod, Text: "o7 chat"},
		{Channel: channel, Login: "cobalt", DisplayName: "Cobalt", Color: "#9c27b0", Badges: sub12, Text: "first time catching this live, looks amazing"},
		{Channel: channel, Login: "quasar", DisplayName: "Quasar", Color: "#ff7043", Badges: vip, Text: "wait how did that even work"},
		{Channel: channel, Login: "pixel_drift", DisplayName: "pixel_drift", Color: "#66bb6a", Badges: sub3, Text: "gg"},
		{Channel: channel, Login: "starlight", DisplayName: "Starlight", Color: "#ef5350", Badges: subT2, Text: "honestly one of the cleanest runs I've seen in a while, the pacing decisions around the midgame transition were spot on and I think you're going to land a PB before the end of the session if you keep splits like that"},
		{Channel: channel, Login: "ember", DisplayName: "Ember", Color: "#ffa726", Text: "chat what did I miss"},
		{Channel: channel, Login: "juniper", DisplayName: "Juniper", Color: "#26a69a", Badges: subT3, Text: "let him cook"},
		{Channel: channel, Login: "halcyon", DisplayName: "Halcyon", Color: "#ab47bc", Badges: sub12, Text: "the theme honestly goes hard"},
		{Channel: channel, Login: "kappa_pro", DisplayName: "kappa_pro", Color: "#8d6e63", Text: "clutch"},
		{Channel: channel, Login: "eevee", DisplayName: "Eevee", Color: "#ec407a", Badges: mod, Text: "reminder: !commands for the list"},
		{Channel: channel, Login: "cobalt", DisplayName: "Cobalt", Color: "#9c27b0", Badges: sub12, Text: "how long have you been practicing this route?"},
		{Channel: channel, Login: "nebula", DisplayName: "Nebula", Color: "#4fc3f7", Badges: mod, Text: "W"},
		{Channel: channel, Login: "pixel_drift", DisplayName: "pixel_drift", Color: "#66bb6a", Badges: sub3, Text: "taking notes"},
		{Channel: channel, Login: "quasar", DisplayName: "Quasar", Color: "#ff7043", Badges: vip, Text: "kekw"},
	}
}
