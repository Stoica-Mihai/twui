package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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

	"github.com/mcs/twui/pkg/notify"
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

var rootCmd = &cobra.Command{
	Use:   "twui [quality]",
	Short: "Anonymous Twitch TUI for browsing and watching live streams",
	Args:  cobra.MaximumNArgs(1),
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

	return nil
}

func runTUI(cmd *cobra.Command, defaultQuality string) error {
	// Redirect slog away from stderr before starting the TUI.
	// Any slog output (from hls.go, api.go, etc.) would otherwise appear
	// interleaved with Bubble Tea's terminal rendering.
	var logDest io.Writer = io.Discard
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
	lowLatency, _ := cmd.Root().PersistentFlags().GetBool("low-latency")
	client.LowLatency = lowLatency

	const notifyTimeoutMs = 5000
	notifier := notify.NewNotifier(notifyTimeoutMs)

	// Load theme
	themeName := viper.GetString("theming.theme")
	theme := ui.ThemeByName(themeName)
	// Apply hex overrides
	applyHexOverrides(&theme)
	// Honor the NO_COLOR convention (https://no-color.org/). Any non-empty value
	// disables colors regardless of theme; not persisted to config.
	if os.Getenv("NO_COLOR") != "" {
		theme.Monochrome = true
	}

	// Load favorites and ignored
	favorites := viper.GetStringSlice("twitch.channels")
	ignored := viper.GetStringSlice("twitch.ignored")
	favSet := toSet(favorites)
	ignSet := toSet(ignored)

	fns := ui.DiscoveryFuncs{
		WatchList: func(c context.Context) ([]ui.DiscoveryEntry, error) {
			if len(favorites) == 0 {
				return nil, nil
			}
			entries := make([]ui.DiscoveryEntry, 0, len(favorites))
			for _, ch := range favorites {
				if ignSet[ch] {
					continue
				}
				md, err := api.StreamMetadata(c, ch)
				if err != nil {
					slog.Debug("metadata error", "channel", ch, "err", err)
					entries = append(entries, ui.DiscoveryEntry{
						Kind:        ui.EntryChannel,
						Login:       ch,
						DisplayName: ch,
						IsFavorite:  true,
						IsLive:      false,
					})
					continue
				}
				displayName := md.Author
				if displayName == "" {
					displayName = ch
				}
				isLive := !md.StartedAt.IsZero()
				entries = append(entries, ui.DiscoveryEntry{
					Kind:        ui.EntryChannel,
					Login:       ch,
					DisplayName: displayName,
					Title:       md.Title,
					Category:    md.Category,
					ViewerCount: md.ViewerCount,
					StartedAt:   md.StartedAt,
					AvatarURL:   md.AvatarURL,
					IsFavorite:  true,
					IsLive:      isLive,
				})
			}
			return entries, nil
		},

		Search: func(c context.Context, query string) ([]ui.DiscoveryEntry, error) {
			results, err := api.SearchChannels(c, query, 30)
			if err != nil {
				return nil, err
			}
			entries := make([]ui.DiscoveryEntry, 0, len(results))
			for _, r := range results {
				e := ui.DiscoveryEntry{
					Kind:        ui.EntryChannel,
					Login:       r.Login,
					DisplayName: r.DisplayName,
					Title:       r.Title,
					Category:    r.Category,
					ViewerCount: r.ViewerCount,
					StartedAt:   r.StartedAt,
					AvatarURL:   r.AvatarURL,
					IsFavorite:  favSet[r.Login],
					IsLive:      !r.StartedAt.IsZero(),
				}
				entries = append(entries, e)
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
				e := ui.DiscoveryEntry{
					Kind:        ui.EntryChannel,
					Login:       r.Login,
					DisplayName: r.DisplayName,
					Title:       r.Title,
					Category:    r.Category,
					ViewerCount: r.ViewerCount,
					StartedAt:   r.StartedAt,
					AvatarURL:   r.AvatarURL,
					IsFavorite:  favSet[r.Login],
					IsLive:      true,
				}
				entries = append(entries, e)
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

			// Download avatar concurrently — don't block stream launch.
			var iconPath string
			var iconOnce sync.Once
			iconCh := make(chan string, 1)
			go func() {
				iconCh <- downloadAvatar(c, sess.HTTP, avatarURL, channel)
			}()
			getIcon := func() string {
				iconOnce.Do(func() { iconPath = <-iconCh })
				return iconPath
			}

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

			// Wire up notification hooks before opening.
			if abn, ok := s.(stream.AdBreakNotifier); ok {
				abn.SetOnAdBreak(func(duration float64, adType string) {
					notifier.SendWithIcon(channel, fmt.Sprintf("Ad break: %s", adType), getIcon())
					send(ui.StatusAdBreak, "")
				})
			}
			if aen, ok := s.(stream.AdEndNotifier); ok {
				aen.SetOnAdEnd(func() {
					notifier.SendWithIcon(channel, "Ad break ended", getIcon())
					send(ui.StatusPlaying, q)
				})
			}
			if prn, ok := s.(stream.PreRollNotifier); ok {
				prn.SetOnPreRoll(func() {
					send(ui.StatusWaiting, "preroll")
				})
			}
			if d, ok := s.(stream.Droppable); ok {
				d.SetOnDrop(func(err error) {
					notifier.SendWithIcon(channel, "Stream dropped", getIcon())
					send(ui.StatusReconnecting, "")
				})
			}

			reader, err := s.Open()
			if err != nil {
				slog.Error("Failed to open stream", "channel", channel, "quality", q, "err", err)
				return
			}

			notifier.SendWithIcon(channel, "Stream started", getIcon())
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
			notifier.SendWithIcon(channel, "Stream ended", getIcon())
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
			writeConfigList("twitch.channels", favorites)
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
			writeConfigList("twitch.ignored", ignored)
		},

		IgnoreList: func() []string {
			return ignored
		},

		HostingChannels: func(c context.Context, channel string) ([]ui.DiscoveryEntry, error) {
			hosts, err := api.HostingChannels(c, channel)
			if err != nil {
				return nil, err
			}
			entries := make([]ui.DiscoveryEntry, 0, len(hosts))
			for _, h := range hosts {
				entries = append(entries, ui.DiscoveryEntry{
					Kind:        ui.EntryChannel,
					Login:       h.Login,
					DisplayName: h.DisplayName,
				})
			}
			return entries, nil
		},

		WriteTheme: func(name string) {
			writeConfigString("theming.theme", name)
		},
	}

	refreshStr, _ := cmd.Root().PersistentFlags().GetString("refresh")
	refreshInterval, err := parseRefreshInterval(refreshStr)
	if err != nil {
		return fmt.Errorf("twui: %w", err)
	}

	model := ui.NewModel(fns, theme, refreshInterval)
	if useASCIISymbols(cmd) {
		model.SetSymbols(ui.ASCIISymbols())
	}
	p := tea.NewProgram(model)

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("twui: %w", err)
	}

	return nil
}

// applyHexOverrides reads hex color overrides from the [theming] config section.
func applyHexOverrides(t *ui.Theme) {
	if v := viper.GetString("theming.border"); v != "" {
		t.Border = v
	}
	if v := viper.GetString("theming.text"); v != "" {
		t.Text = v
	}
	if v := viper.GetString("theming.live"); v != "" {
		t.Live = v
	}
	if v := viper.GetString("theming.offline"); v != "" {
		t.Offline = v
	}
	if v := viper.GetString("theming.title"); v != "" {
		t.Title = v
	}
	if v := viper.GetString("theming.selected_bg"); v != "" {
		t.SelectedBg = v
	}
	if v := viper.GetString("theming.selected_fg"); v != "" {
		t.SelectedFg = v
	}
	if v := viper.GetString("theming.tab_active"); v != "" {
		t.TabActive = v
	}
	if v := viper.GetString("theming.category"); v != "" {
		t.Category = v
	}
	if v := viper.GetString("theming.favorite"); v != "" {
		t.Favorite = v
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

// avatarCache stores downloaded avatar file paths to avoid re-downloading.
var avatarCache sync.Map

// downloadAvatar downloads a channel avatar to a temp file and returns the path.
// Returns "" on any error. Caches per channel for the session lifetime.
func downloadAvatar(ctx context.Context, client *http.Client, url, channel string) string {
	if url == "" {
		return ""
	}
	if p, ok := avatarCache.Load(channel); ok {
		return p.(string)
	}

	dir := filepath.Join(os.TempDir(), "twui-avatars")
	_ = os.MkdirAll(dir, 0700)

	// Check if file exists on disk from a previous session.
	filePath := filepath.Join(dir, channel+".jpg")
	if _, err := os.Stat(filePath); err == nil {
		avatarCache.Store(channel, filePath)
		return filePath
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return ""
	}

	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return ""
	}
	const maxAvatarBytes = 512 << 10 // 512KB — avatars are typically <100KB
	if _, err := io.Copy(f, io.LimitReader(resp.Body, maxAvatarBytes)); err != nil {
		f.Close()
		os.Remove(filePath)
		return ""
	}
	f.Close()

	avatarCache.Store(channel, filePath)
	return filePath
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

// writeConfigString persists a single string config value to the config file.
func writeConfigString(key, value string) {
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
	if err := writeConfigStringKey(configFile, key, value); err != nil {
		slog.Warn("Failed to write config", "key", key, "err", err)
	}
}

// writeConfigStringKey sets a string value at a dotted TOML key and writes the file.
// Parses existing TOML into a map, updates the key, and marshals the result —
// unknown sections and sibling keys are preserved.
func writeConfigStringKey(path, key, value string) error {
	m, err := loadTomlMap(path)
	if err != nil {
		return err
	}
	setByDottedKey(m, key, value)
	return marshalAndWriteAtomic(path, m)
}

// writeConfigList persists a config list value to the config file.
func writeConfigList(key string, values []string) {
	viper.Set(key, values)
	configFile := viper.ConfigFileUsed()
	if configFile == "" {
		var err error
		configFile, err = resolveConfigFile()
		if err != nil {
			return
		}
		viper.SetConfigFile(configFile)
	}
	if err := writeConfigKey(configFile, key, values); err != nil {
		slog.Warn("Failed to write config", "key", key, "err", err)
	}
}

// writeConfigKey sets a string-list value at a dotted TOML key and writes the file.
// Parses existing TOML into a map, updates the key, and marshals the result —
// unknown sections and sibling keys are preserved.
func writeConfigKey(path, key string, values []string) error {
	m, err := loadTomlMap(path)
	if err != nil {
		return err
	}
	setByDottedKey(m, key, values)
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

