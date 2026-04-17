<p align="center">
  <img src="assets/logo-wordmark.svg" alt="twui" width="280">
</p>

[![Release](https://img.shields.io/github/v/release/Stoica-Mihai/twui)](https://github.com/Stoica-Mihai/twui/releases/latest)
[![CI](https://github.com/Stoica-Mihai/twui/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/Stoica-Mihai/twui/actions/workflows/ci.yml)
[![Go version](https://img.shields.io/github/go-mod/go-version/Stoica-Mihai/twui)](go.mod)
[![Downloads](https://img.shields.io/github/downloads/Stoica-Mihai/twui/total)](https://github.com/Stoica-Mihai/twui/releases)
[![License](https://img.shields.io/github/license/Stoica-Mihai/twui)](LICENSE)

Anonymous Twitch TUI for browsing and watching live streams.

No Twitch account, no OAuth, no tracking. Built-in ad blocking. Runs mpv or vlc under the hood.

<p align="center">
  <img src="assets/demo.gif" alt="twui demo" width="800">
</p>

## Features

- Browse live streams by **category** or **search** channels by name
- **Favorites** (WatchList tab) with live status, viewer count, uptime
- **Ignore list** — hide channels everywhere with `x`
- **Ad blocking** via a filtered HLS stream
- **Quality picker** (`i`) or auto-best
- **Theme picker** (`t`) with 11 presets; honors `NO_COLOR`
- **Desktop notifications** on stream start / ad break / drop with channel avatars
- **Auto-refresh** at a configurable interval
- **ASCII fallback** for terminals without Unicode (`--ascii`, `TERM=linux`)
- **Live chat pane** per playing stream (anonymous, read-only); opens on demand with `C` or automatically via `[chat] auto-open`. Pause on scroll-back, cycle with `c`, hide with `C`

## Install

**Binary release (recommended):** download the archive for your platform from [Releases](https://github.com/Stoica-Mihai/twui/releases), extract, move `twui` to somewhere on your `$PATH`.

```sh
# example for linux/amd64
curl -LO https://github.com/Stoica-Mihai/twui/releases/latest/download/twui_<version>_linux_amd64.tar.gz
tar -xzf twui_<version>_linux_amd64.tar.gz
sudo mv twui /usr/local/bin/
```

**From source:**

```sh
git clone https://github.com/Stoica-Mihai/twui
cd twui
make build
./twui
```

Requires Go 1.25+ and one of `mpv` (default) or `vlc` on your `$PATH`.

## Usage

```sh
twui                 # launch the TUI
twui 1080p60         # launch with a default quality hint
twui --ascii         # 7-bit safe glyphs
twui --refresh 1m    # auto-refresh the current view every 60s
```

**Keys** (see `?` in the TUI for the full list):

| Key | Action |
|---|---|
| `j` / `k` | Navigate |
| `Tab` / `Shift+Tab` | Switch view |
| `Enter` | Launch stream / open category |
| `/` | Search |
| `f` | Toggle favorite |
| `x` | Toggle ignore |
| `i` | Quality picker |
| `t` | Theme picker |
| `r` | Related / host channels |
| `?` | Help overlay |
| `q` / `Ctrl+C` | Quit |

**Related overlay** (opened with `r`):

| Key | Action |
|---|---|
| `j` / `k` | Navigate |
| `Enter` | Launch the selected stream |
| `f` | Toggle favorite on the selected channel |
| `x` | Ignore the selected channel (drops from the overlay) |
| `Esc` / `r` | Close |

**Chat pane** (appears when a stream is playing):

| Key | Action |
|---|---|
| `c` | Cycle focus between live sessions |
| `C` | Toggle chat pane visibility |
| `[` / `]` | Scroll chat back / forward one line (back auto-pauses) |
| `{` / `}` | Scroll chat back / forward one page |
| Mouse wheel over pane | Scroll (auto-pauses) |
| `Space` | Resume autoscroll, jump to newest |
| `Esc` | Hide pane |

## Config

Lives at `~/.config/twui/config.toml` and is written in place on favorite / ignore / theme changes.

```toml
[twitch]
channels    = ["streamer1", "streamer2"]   # favorites
ignored     = ["channelX"]
low-latency = false

[general]
player      = "mpv"          # or "vlc"
player-args = []             # extra player args
refresh     = "30s"          # auto-refresh interval; 0 or empty = disabled

[theming]
theme = "catppuccin"         # or: default, dracula, nord, solarized-dark,
                             # gruvbox, tokyo-night, rose-pine, kanagawa,
                             # everforest, one-dark
# Optional per-field hex overrides:
# border = "#..."
# text   = "#..."
# live   = "#..."

[chat]
enabled     = true           # set false to never open IRC connections
max-backlog = 500            # per-session message cap
auto-open   = false          # true: connect + show pane on stream launch
                             # false (default): connect lazily when you press C
```

Environment variables:

| Variable | Effect |
|---|---|
| `NO_COLOR` | Disable all ANSI color output |
| `TWUI_ASCII` | Force ASCII glyphs (same as `--ascii`) |
| `TWUI_*` | Override any config key (Viper convention) |

## Development

```sh
make test            # go test ./...
make test-race       # with -race
make test-live       # network required; Twitch API canary
make coverage        # coverage.out + summary
make lint            # requires golangci-lint installed
make fmt             # gofmt + goimports
```
