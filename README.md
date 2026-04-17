# twui

Anonymous Twitch TUI for browsing and watching live streams.

No Twitch account, no OAuth, no tracking. Built-in ad blocking. Runs mpv or vlc under the hood.

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
