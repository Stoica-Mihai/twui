package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"
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
		f := cmd.Root().PersistentFlags().Lookup(flag)
		if f != nil && !f.Changed && viper.IsSet(viperKey) {
			_ = f.Value.Set(viper.GetString(viperKey))
		}
	}
	bindFlag("twitch-client-id", "twitch.client-id")
	bindFlag("twitch-user-agent", "twitch.user-agent")
	bindFlag("player", "general.player")
	bindFlag("low-latency", "twitch.low-latency")

	return nil
}

func runTUI(cmd *cobra.Command, defaultQuality string) error {
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

	// Load theme
	themeName := viper.GetString("theming.theme")
	theme := ui.ThemeByName(themeName)
	// Apply hex overrides
	applyHexOverrides(&theme)

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
				isLive := !md.StartedAt.IsZero()
				entries = append(entries, ui.DiscoveryEntry{
					Kind:        ui.EntryChannel,
					Login:       ch,
					DisplayName: md.Author,
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
			cats, next, err := api.BrowseCategories(c, 50, cursor)
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
			return entries, next, nil
		},

		CategoryStreams: func(c context.Context, category, cursor string) ([]ui.DiscoveryEntry, string, error) {
			streams, next, err := api.CategoryStreams(c, category, 30, cursor)
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
			if next != "" {
				entries = append(entries, ui.DiscoveryEntry{
					Kind:   ui.EntryLoadMore,
					Cursor: next,
				})
			}
			return entries, next, nil
		},

		Streams: func(c context.Context, channel string) ([]string, error) {
			streams, err := client.Streams(c, channel)
			if err != nil {
				return nil, err
			}
			names := make([]string, 0, len(streams))
			for name := range streams {
				names = append(names, name)
			}
			return names, nil
		},

		Launch: func(c context.Context, channel, quality string, send func(ui.Status, string)) {
			send(ui.StatusWaiting, "")

			url := fmt.Sprintf("https://twitch.tv/%s", channel)
			_ = url

			streams, err := client.Streams(c, channel)
			if err != nil {
				slog.Error("Failed to get streams", "channel", channel, "err", err)
				return
			}

			q := quality
			if q == "" {
				q = defaultQuality
			}
			if q == "" {
				q = selectBestQuality(streams)
			}

			s, ok := streams[q]
			if !ok {
				// Fallback to best
				q = selectBestQuality(streams)
				s, ok = streams[q]
				if !ok {
					slog.Error("No stream available", "channel", channel)
					return
				}
			}

			reader, err := s.Open()
			if err != nil {
				slog.Error("Failed to open stream", "channel", channel, "quality", q, "err", err)
				return
			}

			send(ui.StatusPlaying, q)

			p := &output.Player{
				Path:    playerPath,
				Args:    playerArgs,
				Title:   fmt.Sprintf("%s — twui", channel),
				NoClose: false,
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
	}

	model := ui.NewModel(fns, theme)
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
	// Prefer "source", then highest resolution
	if _, ok := streams["source"]; ok {
		return "source"
	}
	// Simple: pick first non-audio-only
	for name := range streams {
		if name != "audio_only" {
			return name
		}
	}
	for name := range streams {
		return name
	}
	return ""
}

// writeConfigList persists a config list value to the config file.
func writeConfigList(key string, values []string) {
	viper.Set(key, values)
	configFile := viper.ConfigFileUsed()
	if configFile == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return
		}
		dir := filepath.Join(configDir, "twui")
		_ = os.MkdirAll(dir, 0700)
		configFile = filepath.Join(dir, "config.toml")
		viper.SetConfigFile(configFile)
	}
	if err := writeConfigKey(configFile, key, values); err != nil {
		slog.Warn("Failed to write config", "key", key, "err", err)
	}
}

// writeConfigKey does a targeted in-place text replacement of a config key.
// Handles TOML array format.
func writeConfigKey(path, key string, values []string) error {
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	// Build new value string
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	newLine := fmt.Sprintf("%s = [%s]", key, strings.Join(quoted, ", "))

	// Find the last dotted part as the TOML key
	parts := strings.SplitN(key, ".", 2)
	var tomlKey string
	if len(parts) == 2 {
		tomlKey = parts[1]
	} else {
		tomlKey = key
	}

	lines := strings.Split(string(raw), "\n")
	found := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, tomlKey+" =") || strings.HasPrefix(trimmed, tomlKey+"=") {
			lines[i] = tomlKey + " = [" + strings.Join(quoted, ", ") + "]"
			found = true
			break
		}
	}

	if !found {
		// Append under section header or at end
		lines = append(lines, newLine)
	}

	content := strings.Join(lines, "\n")
	_ = newLine // already used above

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

func toSet(list []string) map[string]bool {
	m := make(map[string]bool, len(list))
	for _, s := range list {
		m[s] = true
	}
	return m
}

