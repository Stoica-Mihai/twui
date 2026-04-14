# twui — Anonymous Twitch TUI

A standalone Go TUI for browsing and watching Twitch streams anonymously. Discovery-first: browse
categories, search channels, manage favorites and an ignore list — no Twitch account required.
Ad blocking included.

---

## Decisions

| # | Decision |
|---|----------|
| Implementation scope | **Tier 1 only** first; Tier 2+ incrementally after |
| Go module path | `github.com/mcs/twui` |
| Player support | **mpv + vlc** (same as ghyll) |
| Default quality | **best available** (highest weight stream); quality picker for manual override |
| Session simplification | Strip `pkg/plugin` registry — session holds HTTP client, options, cache only |

---

## What to Reuse from ghyll

Copy and adapt from `/home/mcs/Documents/git/ghyll/` (do not import as a module):

| ghyll source | twui destination | Notes |
|---|---|---|
| `plugins/twitch/` | `pkg/twitch/` | Rename package; add discovery methods |
| `pkg/session/` | `pkg/session/` | Strip `pkg/plugin` registry import; HTTP client + options + cache only |
| `pkg/stream/` | `pkg/stream/` | Unchanged |
| `pkg/output/` | `pkg/output/` | Unchanged |
| `pkg/notify/` | `pkg/notify/` | Unchanged |
| `pkg/ui/theme.go` | `pkg/ui/theme.go` | Adapt |
| `pkg/ui/status.go` | `pkg/ui/status.go` | Unchanged |

---

## Feature Inventory

### Tier 1 — Core

| # | Feature | Notes |
|---|---------|-------|
| 1 | **Channel search** | GQL `SearchResultsPage`, `CHANNEL` target index |
| 2 | **Category browser** | GQL `BrowsePage_AllDirectories` → `DirectoryPage_Game` |
| 3 | **Favorites** | `f` to toggle; writes to `twitch.channels` in config |
| 4 | **Related/host channels** | GQL `HostingInfo` / `ChannelPage_HostInfo` |
| 5 | **Ignore list** | `x` to hide permanently; writes to `twitch.ignored` in config; filtered everywhere; dedicated **Ignored** tab to review/undo (`x` un-ignore, `f` un-ignore + favorite) |
| 6 | **Ad blocking** | `FilteredStream` copied from ghyll |

### Tier 2 — High-value additions

| # | Feature | Notes |
|---|---------|-------|
| 7 | **Stream thumbnails** | `https://static-cdn.jtvnw.net/previews-ttv/live_user_{login}-320x180.jpg`; sixel/kitty if supported |
| 8 | **Stream tags filter** | GQL `DirectoryPage_Game` `tags` option |
| 9 | **Go-live notifications** | Poll favorites on refresh; `pkg/notify` on offline→live |
| 10 | **Multi-stream toggle** | Side-by-side mpv instances (`m` key) |

### Tier 3 — Nice to have

| # | Feature | Notes |
|---|---------|-------|
| 11 | **Clip browsing** | GQL `ClipsCards_Game`; MP4 URLs → mpv |
| 12 | **Category search** | `SearchResultsPage` `GAME` target index |
| 13 | **Category art** | `chafa` CLI if installed |

### Not feasible anonymously

Followed channels sync, subscriptions, channel points, predictions, PubSub, whispers — all require OAuth.

---

## Themes

Copied and extended from ghyll's `pkg/ui/theme.go`. Same `Theme` struct + `buildStyles()` +
`Presets` slice pattern. twui adds three extra fields for elements ghyll doesn't have:

```go
type Theme struct {
    // inherited from ghyll
    Border, Text, Live, Offline, Title string
    SelectedBg, SelectedFg             string
    Playing, AdBreak, Waiting, Reconnecting string

    // twui additions
    TabActive string // active tab label color       (default: purple/accent)
    Category  string // category name in browse view (default: cyan)
    Favorite  string // ★ favorite star color         (default: yellow)
}
```

**Presets** (same 11 as ghyll): default, dracula, nord, solarized-dark, gruvbox, catppuccin,
tokyo-night, rose-pine, kanagawa, everforest, one-dark.

**Theme picker overlay** — press `t` from any view. Live-preview on cursor move (same pattern as
ghyll). Enter applies and writes `theming.theme = "name"` to config via `writeThemeToConfig()`
(same targeted text-replacement pattern). Esc reverts.

**Config:**
```toml
[theming]
theme = "catppuccin"
```

Custom hex overrides also supported per-field (same as ghyll):
```toml
[theming]
border = "#ff0000"
```

---

## Stack

| Concern | Library | Version |
|---------|---------|---------|
| TUI framework | `charm.land/bubbletea/v2` | v2.0.5 |
| TUI styling | `charm.land/lipgloss/v2` | v2.0.3 |
| Unicode cell width | `github.com/mattn/go-runewidth` | v0.0.23 |
| CLI flags | `github.com/spf13/cobra` | v1.10.2 |
| Config file | `github.com/spf13/viper` | v1.21.0 |
| Concurrency helpers | `golang.org/x/sync` | v0.20.0 |
| Terminal detection | `golang.org/x/term` | v0.42.0 |

`charm.land` is the vanity import path for the charmbracelet repos (same as ghyll uses).  
No proxy support — use a host-level proxy if needed.  
**Not needed:** `bogdanfinn/tls-client`, YouTube deps, `golang.org/x/net` (SOCKS5), `golang.org/x/sys` (SO_BINDTODEVICE).  
**Go version:** 1.25.

---

## Project Layout

```
twui/
  cmd/twui/
    main.go              ← Cobra CLI, Viper config, picker launch
  pkg/twitch/
    api.go               ← TwitchAPI struct, 3 existing GQL ops
    discovery.go         ← New GQL ops: search, browse, category streams, host/related
    discovery_test.go
    errors.go
    hls.go               ← TwitchHLSStream (ad filtering)
    twitch.go            ← Plugin entry point
    usher.go
  pkg/session/           ← copied from ghyll
  pkg/stream/            ← copied from ghyll
  pkg/output/            ← copied from ghyll
  pkg/notify/            ← copied from ghyll
  pkg/ui/
    picker.go            ← NEW: browse-first Bubble Tea model
    discover.go          ← NEW: DiscoveryFuncs, DiscoveryEntry, browse/search views
    theme.go             ← adapted from ghyll
    status.go            ← copied from ghyll
  go.mod
  go.sum
```

---

## TUI Architecture

### View Modes

```
viewModeWatchList   favorites list (default if favorites exist)
viewModeBrowse      category browser (default if no favorites)
viewModeSearch      channel/stream search
viewModeIgnored     ignored channel list (review/undo ignores)
```

### Navigation

```
Tab / Shift+Tab  → cycle WatchList ↔ Browse ↔ Search
/                → in WatchList: filter list; in Browse: jump to Search
Enter            → launch stream (live rows) / open category (category rows)
f                → toggle favorite (writes twitch.channels to config)
x                → toggle ignore (writes twitch.ignored to config; hides everywhere)
r                → related/host channels overlay
i                → quality picker overlay
t                → theme picker overlay
Esc              → back one level
j/k / arrows     → navigate
PgUp/PgDn        → page scroll
g/G              → top/bottom
?                → help overlay
q / Ctrl+C       → quit
```

### Header Tab Bar

```
 [ Watch List ]  [ Browse ]  [ Search ]
```

---

## Config

```toml
[twitch]
channels = ["streamer1", "streamer2"]   # favorites
ignored  = ["channelX"]                  # ignore list
```

Config file: `~/.config/twui/config.{toml,yaml,json}`
Cache file:  `~/.config/twui/cache.json`

---

## New GQL Operations

All use `doGQL` + `fallbackQueries` (hashes start empty → always use full query fallback text).

| Operation | Purpose |
|-----------|---------|
| `SearchResultsPage` | Channel search |
| `BrowsePage_AllDirectories` | Top categories with viewer counts |
| `DirectoryPage_Game` | Live streams within a category (paginated, tag-filterable) |
| `HostingInfo` / `ChannelPage_HostInfo` | Related/host channels |

---

## Favorites & Ignore List

Both use targeted text-replacement config writes (same pattern as ghyll's `writeThemeToConfig`):
- Read config → find/replace key in-place → atomic write via temp file
- Handles TOML, YAML, JSON
- `favorites map[string]bool` and `ignored map[string]bool` on Bubble Tea model
- Populated at startup from Viper; writes are async; next refresh picks up changes

---

## Debounced Search

```go
type discoverSearchDebounceMsg struct{ query string }

func discoverSearchDebounceCmd(query string) tea.Cmd {
    return tea.Tick(400*time.Millisecond, func(time.Time) tea.Msg {
        return discoverSearchDebounceMsg{query: query}
    })
}
```

Fire GQL only when `msg.query == m.discoverQuery` (latest keystroke wins).

---

## Pagination

Cursor-based GQL pagination in category browse and stream list. `[Load more...]` sentinel row at
the bottom; select with Enter or `n` to append next page.

---

## Implementation Order

1. `go mod init` + copy packages from ghyll (`session`, `stream`, `output`, `notify`)
2. `pkg/twitch/` — copy and adapt from ghyll's `plugins/twitch/`
3. `pkg/twitch/discovery.go` — new GQL methods + types
4. `pkg/twitch/discovery_test.go`
5. `pkg/ui/theme.go`, `pkg/ui/status.go`
6. `pkg/ui/discover.go` — DiscoveryFuncs, DiscoveryEntry, browse/search rendering
7. `pkg/ui/picker.go` — main Bubble Tea model
8. `cmd/twui/main.go` — CLI, config, wiring
9. Tier 2 features incrementally

---

## Verification

```bash
go mod tidy
go build -o twui ./cmd/twui
go test ./pkg/twitch/ -run TestSearch
go test ./pkg/twitch/ -run TestBrowse
go vet ./...
./twui
```
