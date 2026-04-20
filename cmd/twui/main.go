package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/mcs/twui/pkg/output"
	"github.com/mcs/twui/pkg/session"
	"github.com/mcs/twui/pkg/stream"
	"github.com/mcs/twui/pkg/twitch"
	"github.com/mcs/twui/pkg/ui"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var (
	cfgFile string
	verbose bool
)

const (
	// relatedPoolSize is how many filtered entries we return to the UI so
	// it has headroom to drop rows when the user ignores them and still
	// keep the visible window populated without refetching.
	relatedPoolSize = 30
	// relatedFetchLimit is what we ask the API for. Much larger than the
	// pool size on purpose: after dropping the subject channel and any
	// ignored logins we still want the full pool populated. 100 is the
	// Twitch API maximum for this endpoint.
	relatedFetchLimit = 100
)

// Injected at release time by goreleaser via -ldflags. Defaults describe an
// unreleased dev build; see .goreleaser.yaml.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var rootCmd = &cobra.Command{
	Use:     "twui [quality]",
	Short:   "Anonymous Twitch TUI for browsing and watching live streams",
	Version: fmt.Sprintf("%s (commit %s, built %s)", version, commit, date),
	Args:    cobra.MaximumNArgs(1),
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return initConfig(cmd)
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		quality := ""
		if len(args) > 0 {
			quality = args[0]
		}
		return runTUI(cmd, quality)
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default ~/.config/twui/config.toml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable debug logging")
	rootCmd.PersistentFlags().String("twitch-client-id", "", "Twitch Client-ID override")
	rootCmd.PersistentFlags().String("twitch-user-agent", "", "Twitch User-Agent override")
	rootCmd.PersistentFlags().String("player", "mpv", "Media player binary (mpv or vlc)")
	rootCmd.PersistentFlags().StringSlice("player-args", nil, "Extra arguments for the media player")
	rootCmd.PersistentFlags().Bool("low-latency", false, "Enable Twitch low-latency mode")
	rootCmd.PersistentFlags().String("refresh", "", "Auto-refresh interval (e.g. 30s, 1m, 2m30s; 0 = off)")
	rootCmd.PersistentFlags().Bool("ascii", false, "Use ASCII-only glyphs (auto-enabled for TERM=linux or TWUI_ASCII)")
	rootCmd.PersistentFlags().Bool("chat", true, "Connect to Twitch chat for playing streams")
	rootCmd.PersistentFlags().Bool("chat-auto-open", false, "Open the chat pane automatically when a stream launches")
	rootCmd.PersistentFlags().Bool("demo", false, "Run with mock data; skip Twitch API, media player, and real IRC")
}

// loadChatConfig builds a ui.ChatConfig from the --chat* flags and the [chat]
// TOML section. Defaults: enabled=true, max-backlog=500, auto-open=false.
// Flag values already reflect any TOML fallback via applyFlagFromViper in
// initConfig; max-backlog has no flag and is read from Viper directly.
func loadChatConfig(cmd *cobra.Command) ui.ChatConfig {
	cfg := ui.DefaultChatConfig()
	flags := cmd.Root().PersistentFlags()
	if v, err := flags.GetBool("chat"); err == nil {
		cfg.Enabled = v
	}
	if v, err := flags.GetBool("chat-auto-open"); err == nil {
		cfg.AutoOpen = v
	}
	if viper.IsSet("chat.max-backlog") {
		if n := viper.GetInt("chat.max-backlog"); n > 0 {
			cfg.MaxBacklog = n
		}
	}
	return cfg
}

// useASCIISymbols reports whether the TUI should render ASCII-only glyphs.
// True when --ascii is passed, TWUI_ASCII is set to a non-empty value, or
// TERM is "linux" (the kernel VT, which lacks a Unicode font).
func useASCIISymbols(cmd *cobra.Command) bool {
	if v, err := cmd.Root().PersistentFlags().GetBool("ascii"); err == nil && v {
		return true
	}
	if os.Getenv("TWUI_ASCII") != "" {
		return true
	}
	if os.Getenv("TERM") == "linux" {
		return true
	}
	return false
}

func initConfig(cmd *cobra.Command) error {
	if verbose {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}

	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		configDir, err := os.UserConfigDir()
		if err == nil {
			viper.AddConfigPath(filepath.Join(configDir, "twui"))
		}
		viper.SetConfigName("config")
		viper.SetConfigType("toml")
	}

	viper.SetEnvPrefix("TWUI")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			slog.Warn("Config file read error", "err", err)
		}
	}

	// Apply flag overrides from config when not explicitly set on CLI.
	bindFlag := func(flag, viperKey string) {
		applyFlagFromViper(cmd.Root().PersistentFlags().Lookup(flag), viperKey)
	}
	bindFlag("twitch-client-id", "twitch.client-id")
	bindFlag("twitch-user-agent", "twitch.user-agent")
	bindFlag("player", "general.player")
	bindFlag("low-latency", "twitch.low-latency")
	bindFlag("refresh", "general.refresh")
	bindFlag("chat", "chat.enabled")
	bindFlag("chat-auto-open", "chat.auto-open")

	return nil
}

func runTUI(cmd *cobra.Command, defaultQuality string) error {
	// Redirect slog away from stderr before starting the TUI.
	// Any slog output (from hls.go, api.go, etc.) would otherwise appear
	// interleaved with Bubble Tea's terminal rendering.
	var logDest = io.Discard
	if verbose {
		cacheDir, err := os.UserCacheDir()
		if err == nil {
			logPath := filepath.Join(cacheDir, "twui", "debug.log")
			_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
			if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
				logDest = f
				defer f.Close()
			}
		}
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(logDest, &slog.HandlerOptions{Level: slog.LevelDebug})))

	demo, _ := cmd.Root().PersistentFlags().GetBool("demo")
	if demo {
		return runDemoTUI(cmd)
	}

	sess := session.New()
	defer sess.Close()

	// Build Twitch client
	clientID := viper.GetString("twitch.client-id")
	userAgent := viper.GetString("twitch.user-agent")
	api := twitch.NewTwitchAPI(sess.HTTP, clientID, userAgent, nil, nil)
	usher := twitch.NewUsherService(sess.HTTP)
	client := twitch.New(sess.HTTP, api, usher)

	playerPath, _ := cmd.Root().PersistentFlags().GetString("player")
	playerArgs, _ := cmd.Root().PersistentFlags().GetStringSlice("player-args")
	// applyFlagFromViper does not carry TOML slices back into pflag
	// StringSlice flags, so fall back to Viper directly when the flag
	// wasn't given on the CLI.
	if f := cmd.Root().PersistentFlags().Lookup("player-args"); f != nil && !f.Changed && viper.IsSet("general.player-args") {
		playerArgs = viper.GetStringSlice("general.player-args")
	}
	lowLatency, _ := cmd.Root().PersistentFlags().GetBool("low-latency")
	client.LowLatency = lowLatency

	theme := loadTheme()

	// Load favorites and ignored
	favorites := viper.GetStringSlice("twitch.channels")
	ignored := viper.GetStringSlice("twitch.ignored")
	favSet := toSet(favorites)
	ignSet := toSet(ignored)

	fns := ui.DiscoveryFuncs{
		WatchList: func(c context.Context) (<-chan ui.DiscoveryEntry, error) {
			out := make(chan ui.DiscoveryEntry)
			pending := make([]string, 0, len(favorites))
			for _, ch := range favorites {
				if !ignSet[ch] {
					pending = append(pending, ch)
				}
			}
			go func() {
				defer close(out)
				// Bounded fan-out: one slow channel no longer blocks the rest,
				// and the rate-limit surface stays small even with hundreds of
				// favorites. 6 is empirical — fast enough to hide most
				// round-trip latency without tripping Twitch's per-client
				// burst limits on the metadata endpoint.
				const maxParallel = 6
				sem := make(chan struct{}, maxParallel)
				var wg sync.WaitGroup
				for _, ch := range pending {
					wg.Add(1)
					sem <- struct{}{}
					go func(ch string) {
						defer wg.Done()
						defer func() { <-sem }()
						entry := buildFavoriteEntry(c, api, ch)
						select {
						case out <- entry:
						case <-c.Done():
						}
					}(ch)
				}
				wg.Wait()
			}()
			return out, nil
		},

		Search: func(c context.Context, query string) ([]ui.DiscoveryEntry, error) {
			results, err := api.SearchChannels(c, query, 30)
			if err != nil {
				return nil, err
			}
			entries := make([]ui.DiscoveryEntry, 0, len(results))
			for _, r := range results {
				entries = append(entries, channelEntry(r, favSet, !r.StartedAt.IsZero()))
			}
			return entries, nil
		},

		BrowseCategories: func(c context.Context, cursor string) ([]ui.DiscoveryEntry, string, error) {
			// Twitch caps `first` at 100 and rejects cursor-based pagination with an
			// integrity challenge anonymous clients can't solve. Always request the max.
			cats, _, err := api.BrowseCategories(c, 100, "")
			if err != nil {
				return nil, "", err
			}
			entries := make([]ui.DiscoveryEntry, 0, len(cats))
			for _, cat := range cats {
				entries = append(entries, ui.DiscoveryEntry{
					Kind:            ui.EntryCategory,
					CategoryName:    cat.Name,
					CategoryViewers: cat.ViewerCount,
					BoxArtURL:       cat.BoxArtURL,
				})
			}
			return entries, "", nil
		},

		CategoryStreams: func(c context.Context, category, cursor string) ([]ui.DiscoveryEntry, string, error) {
			// Always request the API maximum (100); cursor-based pagination is blocked
			// by Twitch's integrity challenge for anonymous clients.
			streams, _, err := api.CategoryStreams(c, category, 100, "")
			if err != nil {
				return nil, "", err
			}
			entries := make([]ui.DiscoveryEntry, 0, len(streams))
			for _, r := range streams {
				entries = append(entries, channelEntry(r, favSet, true))
			}
			return entries, "", nil
		},

		Streams: func(c context.Context, channel string) ([]string, error) {
			streams, err := client.Streams(c, channel)
			if err != nil {
				return nil, err
			}
			return sortedStreamNames(streams), nil
		},

		Launch: func(c context.Context, channel, quality, avatarURL string, send func(ui.Status, string), notice func(string)) {
			send(ui.StatusWaiting, "")

			streams, err := client.Streams(c, channel)
			if err != nil {
				slog.Error("Failed to get streams", "channel", channel, "err", err)
				return
			}

			// requested is what the user asked for (via CLI arg, env, quality
			// picker overlay, or config); empty means "pick best available".
			requested := quality
			if requested == "" {
				requested = defaultQuality
			}
			q := requested
			if q == "" {
				q = selectBestQuality(streams)
			}

			s, ok := streams[q]
			if !ok {
				// Fallback to best.
				q = selectBestQuality(streams)
				s, ok = streams[q]
				if !ok {
					slog.Error("No stream available", "channel", channel)
					return
				}
			}
			if requested != "" && requested != q {
				notice(fmt.Sprintf("%s unavailable — using %s", requested, q))
			}

			// Bypass pump: fires BypassAdBreak on a ticker while we're
			// seeing ad-break detections. Starts on OnAdBreak, stops on
			// OnAdEnd — the latter fires when shouldFilter sees the
			// first content segment after a run of ads, which is exactly
			// when bypassing stops being useful (and starts being
			// harmful: a bypass during content opens a new session whose
			// live edge is inside lastEmittedSeq and starves the pipe).
			// pumpStopDebounce is a safety net in case OnAdEnd never
			// fires — e.g. the ad run ends via stream drop.
			const pumpStopDebounce = 60 * time.Second
			const bypassPumpInterval = 1 * time.Second
			// maxAdRunDuration bounds the total time we spend trying to
			// bypass a continuous ad run. Tracked at outer scope so it
			// survives OnAdEnd/OnAdBreak thrash — brief content blips
			// between swaps fire OnAdEnd, which would otherwise reset a
			// goroutine-local timer and let the pump run indefinitely
			// while the player starves.
			const maxAdRunDuration = 25 * time.Second
			// adRunResetGrace is the sustained-content window required
			// before we consider the current ad run "over". Shorter than
			// the bypass cadence so flickery ad↔content patterns keep
			// the clock advancing.
			const adRunResetGrace = 8 * time.Second
			var (
				pumpMu          sync.Mutex
				pumpStopTimer   *time.Timer
				pumpStop        chan struct{}
				adRunStartedAt  time.Time
				adRunResetTimer *time.Timer
			)

			stopPump := func() {
				pumpMu.Lock()
				if pumpStopTimer != nil {
					pumpStopTimer.Stop()
					pumpStopTimer = nil
				}
				stop := pumpStop
				pumpStop = nil
				pumpMu.Unlock()
				if stop != nil {
					close(stop)
				}
			}

			resetAdRun := func() {
				pumpMu.Lock()
				adRunStartedAt = time.Time{}
				if adRunResetTimer != nil {
					adRunResetTimer.Stop()
					adRunResetTimer = nil
				}
				pumpMu.Unlock()
			}

			bypasser, _ := s.(stream.AdBypasser)
			runBypass := func() {
				if bypasser == nil {
					return
				}
				err := bypasser.BypassAdBreak(c)
				switch {
				case err == nil:
					slog.Info("Ad-break bypass applied", "channel", channel)
				case errors.Is(err, twitch.ErrBypassInFlight),
					errors.Is(err, twitch.ErrBypassPreContent),
					errors.Is(err, twitch.ErrBypassNotInAd),
					errors.Is(err, twitch.ErrBypassRecent):
					slog.Debug("Ad-break bypass skipped", "channel", channel, "reason", err)
				default:
					slog.Warn("Ad-break bypass failed", "channel", channel, "err", err)
				}
			}

			if abn, ok := s.(stream.AdBreakNotifier); ok {
				abn.SetOnAdBreak(func(duration float64, adType string) {
					send(ui.StatusAdBreak, "")
					if bypasser == nil {
						return
					}
					// Preroll can't be bypassed — hadContent is false, so
					// BypassAdBreak always returns ErrBypassPreContent
					// until the preroll finishes. Starting the pump here
					// just logs a skip every tick for 15+ seconds. Wait
					// for a midroll before firing up.
					if strings.EqualFold(adType, "PREROLL") {
						return
					}
					pumpMu.Lock()
					// Cancel any pending ad-run reset — we're still
					// getting ads, the earlier OnAdEnd was a blip.
					if adRunResetTimer != nil {
						adRunResetTimer.Stop()
						adRunResetTimer = nil
					}
					if adRunStartedAt.IsZero() {
						adRunStartedAt = time.Now()
					}
					runAge := time.Since(adRunStartedAt)
					if pumpStopTimer != nil {
						pumpStopTimer.Stop()
					}
					pumpStopTimer = time.AfterFunc(pumpStopDebounce, stopPump)
					var startPump bool
					var localStopCh chan struct{}
					if pumpStop == nil {
						pumpStop = make(chan struct{})
						localStopCh = pumpStop
						startPump = true
					}
					pumpMu.Unlock()

					// Past the budget already — don't even try. Degrade
					// immediately so the new ads play through instead of
					// queuing up behind a paused filter.
					if runAge > maxAdRunDuration {
						slog.Info("Ad run past budget; letting ads play", "channel", channel, "age", runAge)
						if d, ok := s.(stream.AdFilterDegrader); ok {
							d.DegradeAdFilter()
						}
						return
					}

					if startPump {
						go func(stopCh chan struct{}) {
							runBypass()
							ticker := time.NewTicker(bypassPumpInterval)
							defer ticker.Stop()
							for {
								select {
								case <-stopCh:
									return
								case <-c.Done():
									return
								case <-ticker.C:
									pumpMu.Lock()
									start := adRunStartedAt
									pumpMu.Unlock()
									if !start.IsZero() && time.Since(start) > maxAdRunDuration {
										slog.Info("Bypass pump gave up; letting ads play", "channel", channel)
										if d, ok := s.(stream.AdFilterDegrader); ok {
											d.DegradeAdFilter()
										}
										resetAdRun()
										stopPump()
										return
									}
									runBypass()
								}
							}
						}(localStopCh)
					}
				})
			}
			if aen, ok := s.(stream.AdEndNotifier); ok {
				aen.SetOnAdEnd(func() {
					send(ui.StatusPlaying, q)
					// Debounce the clear: a brief content blip between
					// bypass swaps shouldn't reset the ad-run budget
					// (and stop the pump) — only sustained content does.
					// If another OnAdBreak fires within the grace
					// window, it cancels this timer.
					pumpMu.Lock()
					if adRunResetTimer != nil {
						adRunResetTimer.Stop()
					}
					adRunResetTimer = time.AfterFunc(adRunResetGrace, func() {
						pumpMu.Lock()
						adRunStartedAt = time.Time{}
						adRunResetTimer = nil
						pumpMu.Unlock()
						stopPump()
					})
					pumpMu.Unlock()
				})
			}
			if prn, ok := s.(stream.PreRollNotifier); ok {
				prn.SetOnPreRoll(func() {
					send(ui.StatusWaiting, "preroll")
				})
			}
			if d, ok := s.(stream.Droppable); ok {
				d.SetOnDrop(func(err error) {
					send(ui.StatusReconnecting, "")
				})
			}

			reader, err := s.Open()
			if err != nil {
				slog.Error("Failed to open stream", "channel", channel, "quality", q, "err", err)
				return
			}

			send(ui.StatusPlaying, q)

			p := &output.Player{
				Path:       playerPath,
				Args:       playerArgs,
				Title:      fmt.Sprintf("%s — twui", channel),
				NoClose:    false,
				NoTerminal: true,
				Stderr:     io.Discard,
			}

			if err := p.Play(c, reader); err != nil {
				if c.Err() == nil {
					slog.Error("Player exited with error", "err", err)
				}
			}
		},

		ToggleFavorite: func(channel string, add bool) {
			if add {
				if !favSet[channel] {
					favorites = append(favorites, channel)
					favSet[channel] = true
				}
			} else {
				favorites = slices.DeleteFunc(favorites, func(s string) bool { return s == channel })
				delete(favSet, channel)
			}
			writeConfigValue("twitch.channels", favorites)
		},

		ToggleIgnore: func(channel string, add bool) {
			if add {
				if !ignSet[channel] {
					ignored = append(ignored, channel)
					ignSet[channel] = true
				}
			} else {
				ignored = slices.DeleteFunc(ignored, func(s string) bool { return s == channel })
				delete(ignSet, channel)
			}
			writeConfigValue("twitch.ignored", ignored)
		},

		IgnoreList: func() []string {
			return ignored
		},

		RelatedChannels: func(c context.Context, channel, category string) ([]ui.DiscoveryEntry, error) {
			// Twitch removed the Host feature in Oct 2022, so "related" now
			// means other live channels in the same category. Fetch the
			// API maximum so we have headroom to drop the subject channel
			// and anyone already ignored and still have ~maxRelatedStreams
			// rows left to display.
			streams, _, err := api.CategoryStreams(c, category, relatedFetchLimit, "")
			if err != nil {
				return nil, err
			}
			entries := make([]ui.DiscoveryEntry, 0, len(streams))
			for _, s := range streams {
				if strings.EqualFold(s.Login, channel) {
					continue
				}
				if ignSet[s.Login] {
					continue
				}
				entries = append(entries, channelEntry(s, favSet, true))
			}
			// Twitch's category-streams order isn't strictly by viewer count
			// (featured slots etc.), so re-sort locally so users always see
			// the biggest streams first.
			sort.SliceStable(entries, func(i, j int) bool {
				return entries[i].ViewerCount > entries[j].ViewerCount
			})
			if len(entries) > relatedPoolSize {
				entries = entries[:relatedPoolSize]
			}
			return entries, nil
		},

		WriteTheme: func(name string) {
			writeConfigValue("theming.theme", name)
		},
	}

	refreshInterval, err := loadRefreshInterval(cmd)
	if err != nil {
		return err
	}

	model := ui.NewModel(fns, theme, refreshInterval)
	if useASCIISymbols(cmd) {
		model.SetSymbols(ui.ASCIISymbols())
	}
	model.SetChatConfig(loadChatConfig(cmd))
	p := tea.NewProgram(model)

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("twui: %w", err)
	}

	return nil
}

// runDemoTUI is the --demo variant of runTUI: no Twitch API, no media
// player, no IRC connect. Every discovery callback and the chat source are
// backed by hardcoded fixtures in demo.go.
func runDemoTUI(cmd *cobra.Command) error {
	theme := loadTheme()

	refreshInterval, err := loadRefreshInterval(cmd)
	if err != nil {
		return err
	}

	model := ui.NewModel(demoFuncs(), theme, refreshInterval)
	if useASCIISymbols(cmd) {
		model.SetSymbols(ui.ASCIISymbols())
	}
	model.SetChatConfig(loadChatConfig(cmd))
	model.SetChatFactory(demoChatFactory)

	if _, err := tea.NewProgram(model).Run(); err != nil {
		return fmt.Errorf("twui: %w", err)
	}
	return nil
}

func buildFavoriteEntry(ctx context.Context, api *twitch.TwitchAPI, ch string) ui.DiscoveryEntry {
	md, err := api.StreamMetadata(ctx, ch)
	if err != nil {
		slog.Debug("metadata error", "channel", ch, "err", err)
		return ui.DiscoveryEntry{
			Kind:        ui.EntryChannel,
			Login:       ch,
			DisplayName: ch,
			IsFavorite:  true,
			IsLive:      false,
		}
	}
	displayName := md.Author
	if displayName == "" {
		displayName = ch
	}
	return ui.DiscoveryEntry{
		Kind:        ui.EntryChannel,
		Login:       ch,
		DisplayName: displayName,
		Title:       md.Title,
		Category:    md.Category,
		ViewerCount: md.ViewerCount,
		StartedAt:   md.StartedAt,
		AvatarURL:   md.AvatarURL,
		IsFavorite:  true,
		IsLive:      !md.StartedAt.IsZero(),
	}
}

// channelEntry adapts a twitch.ChannelResult into a ui.DiscoveryEntry.
// isLive is caller-supplied because Search relies on StartedAt presence while
// CategoryStreams and RelatedChannels know the endpoint returns live streams.
func channelEntry(r twitch.ChannelResult, favSet map[string]bool, isLive bool) ui.DiscoveryEntry {
	return ui.DiscoveryEntry{
		Kind:        ui.EntryChannel,
		Login:       r.Login,
		DisplayName: r.DisplayName,
		Title:       r.Title,
		Category:    r.Category,
		ViewerCount: r.ViewerCount,
		StartedAt:   r.StartedAt,
		AvatarURL:   r.AvatarURL,
		IsFavorite:  favSet[r.Login],
		IsLive:      isLive,
	}
}

// loadTheme resolves the active theme from config: the named preset, then
// hex overrides from [theming], then NO_COLOR (https://no-color.org/) which
// forces monochrome regardless of what's configured.
func loadTheme() ui.Theme {
	theme := ui.ThemeByName(viper.GetString("theming.theme"))
	applyHexOverrides(&theme)
	if os.Getenv("NO_COLOR") != "" {
		theme.Monochrome = true
	}
	return theme
}

// loadRefreshInterval resolves the auto-refresh interval from the --refresh
// flag, validated by parseRefreshInterval. Both entry points (real TUI and
// --demo) apply the same validation so bad input fails the same way.
func loadRefreshInterval(cmd *cobra.Command) (time.Duration, error) {
	refreshStr, _ := cmd.Root().PersistentFlags().GetString("refresh")
	d, err := parseRefreshInterval(refreshStr)
	if err != nil {
		return 0, fmt.Errorf("twui: %w", err)
	}
	return d, nil
}

// applyHexOverrides reads hex color overrides from the [theming] config section.
// The field-to-key mapping lives in one place so adding a new themable color
// is a single-line table change.
func applyHexOverrides(t *ui.Theme) {
	overrides := map[string]*string{
		"theming.border":      &t.Border,
		"theming.text":        &t.Text,
		"theming.live":        &t.Live,
		"theming.offline":     &t.Offline,
		"theming.title":       &t.Title,
		"theming.selected_bg": &t.SelectedBg,
		"theming.selected_fg": &t.SelectedFg,
		"theming.tab_active":  &t.TabActive,
		"theming.category":    &t.Category,
		"theming.favorite":    &t.Favorite,
	}
	for key, dst := range overrides {
		if v := viper.GetString(key); v != "" {
			*dst = v
		}
	}
}

// selectBestQuality returns the quality name with the highest weight.
func selectBestQuality(streams map[string]stream.Stream) string {
	best := ""
	bestWeight := -1.0
	for name := range streams {
		w := streamWeight(name)
		if w > bestWeight {
			bestWeight = w
			best = name
		}
	}
	return best
}

// sortedStreamNames returns stream names sorted by weight descending (for quality picker).
func sortedStreamNames(streams map[string]stream.Stream) []string {
	type entry struct {
		name   string
		weight float64
	}
	entries := make([]entry, 0, len(streams))
	for name := range streams {
		entries = append(entries, entry{name: name, weight: streamWeight(name)})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].weight > entries[j].weight
	})
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.name
	}
	return names
}

// streamWeight computes a numeric weight for a stream quality name.
func streamWeight(name string) float64 {
	lower := strings.ToLower(name)
	if lower == "source" {
		return 1e18
	}
	if lower == "audio_only" {
		return 0
	}

	baseName := lower
	altPenalty := 0.0
	if idx := strings.Index(lower, "_alt"); idx >= 0 {
		altSuffix := lower[idx+4:]
		altNum := 1
		if altSuffix != "" {
			if n, err := strconv.Atoi(altSuffix); err == nil {
				altNum = n
			}
		}
		altPenalty = 0.01 * float64(altNum)
		baseName = lower[:idx]
	}

	if strings.Contains(baseName, "p") {
		parts := strings.SplitN(baseName, "p", 2)
		if len(parts) >= 1 {
			pixels, err := strconv.ParseFloat(parts[0], 64)
			if err == nil {
				fps := 0.0
				if len(parts) == 2 && parts[1] != "" {
					fps, _ = strconv.ParseFloat(parts[1], 64)
				}
				return pixels + fps - altPenalty
			}
		}
	}

	if strings.HasSuffix(baseName, "k") {
		bitrateStr := strings.TrimSuffix(baseName, "k")
		bitrate, err := strconv.ParseFloat(bitrateStr, 64)
		if err == nil {
			return bitrate/2.8 - altPenalty
		}
	}

	return 1 - altPenalty
}

// parseRefreshInterval parses a Go duration string for the auto-refresh interval.
// Empty string and zero return 0 (disabled). Bare integers are rejected. Non-zero
// values below 30s are rejected to avoid hammering the Twitch API.
func parseRefreshInterval(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid refresh interval %q (e.g. 30s, 1m, 2m30s)", s)
	}
	if d == 0 {
		return 0, nil
	}
	if d < 30*time.Second {
		return 0, fmt.Errorf("refresh interval must be at least 30s, got %s", d)
	}
	return d, nil
}

// applyFlagFromViper copies a Viper key into a cobra/pflag flag when the user
// hasn't set it on the CLI. Logs a warning if the flag rejects the value so
// bad config values don't silently fall through to default behavior. Some
// pflag types (e.g., durationValue) clobber the target before returning the
// parse error; we snapshot and restore on failure so the CLI default survives.
func applyFlagFromViper(f *pflag.Flag, viperKey string) {
	if f == nil || f.Changed || !viper.IsSet(viperKey) {
		return
	}
	val := viper.GetString(viperKey)
	prev := f.Value.String()
	if err := f.Value.Set(val); err != nil {
		slog.Warn("invalid config value", "key", viperKey, "value", val, "err", err)
		_ = f.Value.Set(prev)
	}
}

// resolveConfigFile returns the config file path, creating the directory if needed.
func resolveConfigFile() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(configDir, "twui")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

func atomicWriteFile(path, content string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "twui-config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	tmp.Close()
	return os.Rename(tmpName, path)
}

// writeConfigValue persists any TOML-encodable value at a dotted key, routing
// through viper's tracked config file. Write failures are logged — the UI
// can't surface them usefully mid-render.
func writeConfigValue(key string, value any) {
	viper.Set(key, value)
	configFile := viper.ConfigFileUsed()
	if configFile == "" {
		var err error
		configFile, err = resolveConfigFile()
		if err != nil {
			return
		}
		viper.SetConfigFile(configFile)
	}
	if err := writeTomlValue(configFile, key, value); err != nil {
		slog.Warn("Failed to write config", "key", key, "err", err)
	}
}

// writeTomlValue sets a value at a dotted TOML key inside path and writes the
// file atomically. Existing sections and sibling keys are preserved. Exposed
// at file-level so tests can exercise the TOML merge logic without viper.
func writeTomlValue(path, key string, value any) error {
	m, err := loadTomlMap(path)
	if err != nil {
		return err
	}
	setByDottedKey(m, key, value)
	return marshalAndWriteAtomic(path, m)
}

// loadTomlMap reads a TOML file into a nested map. A missing or empty file
// yields an empty map rather than an error — the caller is about to write to it.
func loadTomlMap(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	m := map[string]any{}
	if err := toml.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// setByDottedKey walks a nested map following dotted path segments, creating
// intermediate tables as needed, and assigns value at the leaf.
func setByDottedKey(m map[string]any, dotted string, value any) {
	parts := strings.Split(dotted, ".")
	cur := m
	for i, p := range parts {
		if i == len(parts)-1 {
			cur[p] = value
			return
		}
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
}

// marshalAndWriteAtomic serializes the map as TOML and writes it atomically.
func marshalAndWriteAtomic(path string, m map[string]any) error {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(m); err != nil {
		return err
	}
	return atomicWriteFile(path, buf.String())
}

func toSet(list []string) map[string]bool {
	m := make(map[string]bool, len(list))
	for _, s := range list {
		m[s] = true
	}
	return m
}
