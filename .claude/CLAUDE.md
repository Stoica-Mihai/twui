# twui

Terminal UI for browsing and playing Twitch streams. Go 1.25, Bubble Tea v2, Lipgloss v2, Cobra + Viper.

## Layout

- `cmd/twui/main.go` — CLI entry, flag binding, Viper config, session wiring
- `pkg/ui/` — Bubble Tea `Model`, view-mode rendering, overlays, chat pane, keymap
- `pkg/twitch/` — GQL client, persisted queries, integrity tokens, discovery/search
- `pkg/stream/`, `pkg/stream/hls/` — HLS playlist parser, segment fetcher, ad detection, muxed reader
- `pkg/chat/` — IRCv3 parser + anonymous Twitch chat client
- `pkg/session/` — shared `*http.Client`, cache, Retry-After helper
- `pkg/output/` — media-player launch

## Build / test / run

- Build: `go build ./...`
- Test: `go test ./...`
- Vet: `go vet ./...`
- Run: `go run ./cmd/twui` (or `./twui` after build)
- Release: tag `vX.Y.Z` on `main` — goreleaser pipeline publishes GitHub release

## Conventions

- **Commits:** conventional style, lowercase prefix (`fix(chat): ...`, `feat(r): ...`, `refactor: ...`). No `Co-Authored-By` trailers, no "Generated with Claude Code" tags.
- **Version line in `cmd/twui/main.go`:** `version = "dev"` is ldflag-replaced by goreleaser at tag time — don't edit manually.
- **Config:** flags > `[section]` TOML keys > defaults. Viper is the source of truth for TOML; flag-vs-TOML merge happens in `cmd/twui/main.go` loaders.
- **UI helpers** live in `pkg/ui/render_list.go` (`pad`, `padRight`, `stripANSI`, `cellTruncate`, `formatViewers`, `formatUptime`) and `pkg/ui/overlay.go` (`overlayHeader`/`Row`/`Footer`/`Width`). Prefer reusing them over rolling new formatters.
- **ANSI-safe width:** measure visible width with `uniseg.StringWidth(stripANSI(s))` — `len()` counts bytes and will misalign Unicode/styled rows.

## Scoped rules

`.claude/rules/` holds topic-specific guidance that loads on matching files — check there for rules relevant to what you're editing (e.g. test fixtures).
