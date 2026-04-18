package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/mcs/twui/pkg/stream"
)

// mockStream satisfies stream.Stream for tests that only need quality selection.
type mockStream struct{ url string }

func (m mockStream) Open() (io.ReadCloser, error) { return nil, nil }
func (m mockStream) URL() string                  { return m.url }

func ms(url string) stream.Stream { return mockStream{url: url} }

// --- streamWeight ---

func TestStreamWeight_Ranking(t *testing.T) {
	ordered := []string{
		"source",
		"1080p60",
		"1080p",
		"720p60",
		"720p",
		"480p",
		"360p",
		"160p",
		"audio_only",
	}
	for i := 0; i < len(ordered)-1; i++ {
		hi := streamWeight(ordered[i])
		lo := streamWeight(ordered[i+1])
		if hi <= lo {
			t.Errorf("streamWeight(%q)=%f should be > streamWeight(%q)=%f",
				ordered[i], hi, ordered[i+1], lo)
		}
	}
}

func TestStreamWeight_Source(t *testing.T) {
	w := streamWeight("source")
	if w < 1e17 {
		t.Errorf("source weight %f should be very large", w)
	}
}

func TestStreamWeight_AudioOnly(t *testing.T) {
	w := streamWeight("audio_only")
	if w != 0 {
		t.Errorf("audio_only weight = %f, want 0", w)
	}
}

func TestStreamWeight_AltSuffix(t *testing.T) {
	base := streamWeight("720p60")
	alt1 := streamWeight("720p60_alt")
	alt2 := streamWeight("720p60_alt2")

	if base <= alt1 {
		t.Errorf("720p60 (%f) should outrank 720p60_alt (%f)", base, alt1)
	}
	if alt1 <= alt2 {
		t.Errorf("720p60_alt (%f) should outrank 720p60_alt2 (%f)", alt1, alt2)
	}
}

func TestStreamWeight_Kbps(t *testing.T) {
	high := streamWeight("3000k")
	low := streamWeight("500k")
	if high <= low {
		t.Errorf("3000k (%f) should outrank 500k (%f)", high, low)
	}
}

// --- selectBestQuality ---

func TestSelectBestQuality_PicksSource(t *testing.T) {
	streams := map[string]stream.Stream{
		"source":     ms(""),
		"720p60":     ms(""),
		"480p":       ms(""),
		"audio_only": ms(""),
	}
	got := selectBestQuality(streams)
	if got != "source" {
		t.Errorf("selectBestQuality = %q, want %q", got, "source")
	}
}

func TestSelectBestQuality_FallsToHighest(t *testing.T) {
	streams := map[string]stream.Stream{
		"720p60": ms(""),
		"480p":   ms(""),
		"360p":   ms(""),
	}
	got := selectBestQuality(streams)
	if got != "720p60" {
		t.Errorf("selectBestQuality = %q, want %q", got, "720p60")
	}
}

func TestSelectBestQuality_Single(t *testing.T) {
	streams := map[string]stream.Stream{
		"1080p": ms(""),
	}
	got := selectBestQuality(streams)
	if got != "1080p" {
		t.Errorf("selectBestQuality = %q, want %q", got, "1080p")
	}
}

// --- sortedStreamNames ---

func TestSortedStreamNames_Order(t *testing.T) {
	streams := map[string]stream.Stream{
		"audio_only": ms(""),
		"360p":       ms(""),
		"720p60":     ms(""),
		"source":     ms(""),
		"480p":       ms(""),
	}
	names := sortedStreamNames(streams)
	if len(names) != 5 {
		t.Fatalf("expected 5 names, got %d: %v", len(names), names)
	}
	if names[0] != "source" {
		t.Errorf("names[0] = %q, want %q", names[0], "source")
	}
	if names[len(names)-1] != "audio_only" {
		t.Errorf("last name = %q, want %q", names[len(names)-1], "audio_only")
	}
	// Verify descending order.
	for i := 0; i < len(names)-1; i++ {
		if streamWeight(names[i]) < streamWeight(names[i+1]) {
			t.Errorf("out of order: %q (%f) < %q (%f)",
				names[i], streamWeight(names[i]),
				names[i+1], streamWeight(names[i+1]))
		}
	}
}

// --- toSet ---

func TestToSet_Basic(t *testing.T) {
	s := toSet([]string{"a", "b", "c"})
	for _, k := range []string{"a", "b", "c"} {
		if !s[k] {
			t.Errorf("expected %q in set", k)
		}
	}
}

func TestToSet_Empty(t *testing.T) {
	s := toSet(nil)
	if len(s) != 0 {
		t.Errorf("expected empty set, got %v", s)
	}
}

func TestToSet_Dedup(t *testing.T) {
	s := toSet([]string{"x", "x", "x"})
	if len(s) != 1 {
		t.Errorf("expected 1 unique entry, got %d", len(s))
	}
}

// containsValue reports whether content contains a TOML-quoted string value.
// go-toml/v2 emits literal strings ('value') for plain content; both forms are valid.
func containsValue(content, value string) bool {
	return strings.Contains(content, `"`+value+`"`) || strings.Contains(content, `'`+value+`'`)
}

// --- writeConfigKey ---

func TestWriteConfigKey_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	err := writeConfigKey(path, "twitch.channels", []string{"alice", "bob"})
	if err != nil {
		t.Fatalf("writeConfigKey: %v", err)
	}

	raw, _ := os.ReadFile(path)
	content := string(raw)
	if !containsValue(content, "alice") {
		t.Errorf("config file missing alice: %s", content)
	}
	if !containsValue(content, "bob") {
		t.Errorf("config file missing bob: %s", content)
	}
}

func TestWriteConfigKey_ReplaceExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	initial := "[twitch]\nchannels = [\"old\"]\n"
	_ = os.WriteFile(path, []byte(initial), 0600)

	err := writeConfigKey(path, "twitch.channels", []string{"new1", "new2"})
	if err != nil {
		t.Fatalf("writeConfigKey: %v", err)
	}

	raw, _ := os.ReadFile(path)
	content := string(raw)
	if containsValue(content, "old") {
		t.Errorf("old value should be replaced: %s", content)
	}
	if !containsValue(content, "new1") || !containsValue(content, "new2") {
		t.Errorf("new values missing from config: %s", content)
	}
}

func TestWriteConfigKey_AppendWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	initial := "[twitch]\n"
	_ = os.WriteFile(path, []byte(initial), 0600)

	err := writeConfigKey(path, "twitch.channels", []string{"only"})
	if err != nil {
		t.Fatalf("writeConfigKey: %v", err)
	}

	raw, _ := os.ReadFile(path)
	content := string(raw)
	if !containsValue(content, "only") {
		t.Errorf("appended value missing: %s", content)
	}
}

func TestWriteConfigKey_EmptyList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	_ = os.WriteFile(path, []byte("[twitch]\nchannels = [\"old\"]\n"), 0600)

	err := writeConfigKey(path, "twitch.channels", []string{})
	if err != nil {
		t.Fatalf("writeConfigKey: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if containsValue(string(raw), "old") {
		t.Errorf("empty list should clear old value: %s", string(raw))
	}
}

// --- writeConfigStringKey ---

func TestWriteConfigStringKey_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	err := writeConfigStringKey(path, "theming.theme", "catppuccin")
	if err != nil {
		t.Fatalf("writeConfigStringKey: %v", err)
	}

	raw, _ := os.ReadFile(path)
	content := string(raw)
	if !strings.Contains(content, "catppuccin") {
		t.Errorf("theme name missing from config: %s", content)
	}
}

func TestWriteConfigStringKey_ReplaceExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	initial := "[theming]\ntheme = \"dracula\"\n"
	_ = os.WriteFile(path, []byte(initial), 0600)

	err := writeConfigStringKey(path, "theming.theme", "nord")
	if err != nil {
		t.Fatalf("writeConfigStringKey: %v", err)
	}

	raw, _ := os.ReadFile(path)
	content := string(raw)
	if strings.Contains(content, "dracula") {
		t.Errorf("old theme should be replaced: %s", content)
	}
	if !strings.Contains(content, "nord") {
		t.Errorf("new theme missing from config: %s", content)
	}
}

func TestWriteConfigStringKey_AppendWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	_ = os.WriteFile(path, []byte("[theming]\n"), 0600)

	err := writeConfigStringKey(path, "theming.theme", "gruvbox")
	if err != nil {
		t.Fatalf("writeConfigStringKey: %v", err)
	}

	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "gruvbox") {
		t.Errorf("appended value missing: %s", string(raw))
	}
}

// --- config key duplication bug ---

func TestWriteConfigKey_NoKeyDuplication(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	// First write creates the entry.
	if err := writeConfigKey(path, "twitch.channels", []string{"alice"}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// Second write should replace, not duplicate.
	if err := writeConfigKey(path, "twitch.channels", []string{"alice", "bob"}); err != nil {
		t.Fatalf("second write: %v", err)
	}

	raw, _ := os.ReadFile(path)
	content := string(raw)
	if n := strings.Count(content, "channels"); n != 1 {
		t.Errorf("expected exactly 1 'channels' line, got %d in:\n%s", n, content)
	}
	if !containsValue(content, "bob") {
		t.Errorf("second write value missing: %s", content)
	}
}

func TestWriteConfigKey_FixesDottedKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	// Simulate buggy file from old code that wrote the full dotted form.
	_ = os.WriteFile(path, []byte("twitch.channels = [\"old\"]\n"), 0600)

	if err := writeConfigKey(path, "twitch.channels", []string{"fixed"}); err != nil {
		t.Fatalf("writeConfigKey: %v", err)
	}

	raw, _ := os.ReadFile(path)
	content := string(raw)
	if containsValue(content, "old") {
		t.Errorf("old value should be replaced: %s", content)
	}
	if !containsValue(content, "fixed") {
		t.Errorf("new value missing: %s", content)
	}
	if n := strings.Count(content, "channels"); n != 1 {
		t.Errorf("expected 1 channels line, got %d in:\n%s", n, content)
	}
}

func TestWriteConfigKey_AppendsUnderSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	_ = os.WriteFile(path, []byte("[twitch]\n"), 0600)

	if err := writeConfigKey(path, "twitch.channels", []string{"a"}); err != nil {
		t.Fatalf("writeConfigKey: %v", err)
	}

	raw, _ := os.ReadFile(path)
	content := string(raw)
	// Should use short form under existing section.
	if strings.Contains(content, "twitch.channels") {
		t.Errorf("should use short form, got dotted key in:\n%s", content)
	}
	if !containsValue(content, "a") {
		t.Errorf("expected channels line to contain 'a', got:\n%s", content)
	}
}

func TestWriteConfigKey_CreatesSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := writeConfigKey(path, "twitch.channels", []string{"new"}); err != nil {
		t.Fatalf("writeConfigKey: %v", err)
	}

	raw, _ := os.ReadFile(path)
	content := string(raw)
	if !strings.Contains(content, "[twitch]") {
		t.Errorf("section header missing in:\n%s", content)
	}
	if !containsValue(content, "new") {
		t.Errorf("expected channels line to contain 'new': %s", content)
	}
	if strings.Contains(content, "twitch.channels") {
		t.Errorf("should not use dotted key in:\n%s", content)
	}
}

func TestWriteConfigStringKey_NoKeyDuplication(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := writeConfigStringKey(path, "theming.theme", "dracula"); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := writeConfigStringKey(path, "theming.theme", "nord"); err != nil {
		t.Fatalf("second write: %v", err)
	}

	raw, _ := os.ReadFile(path)
	content := string(raw)
	if n := strings.Count(content, "theme"); n != 1 {
		t.Errorf("expected exactly 1 'theme' line, got %d in:\n%s", n, content)
	}
	if !strings.Contains(content, "nord") {
		t.Errorf("second value missing: %s", content)
	}
}

func TestWriteConfigStringKey_FixesDottedKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	// Simulate buggy file.
	_ = os.WriteFile(path, []byte("theming.theme = \"old-theme\"\n"), 0600)

	if err := writeConfigStringKey(path, "theming.theme", "fixed"); err != nil {
		t.Fatalf("writeConfigStringKey: %v", err)
	}

	raw, _ := os.ReadFile(path)
	content := string(raw)
	if strings.Contains(content, "old-theme") {
		t.Errorf("old value should be replaced: %s", content)
	}
	if !strings.Contains(content, "fixed") {
		t.Errorf("new value missing: %s", content)
	}
	if n := strings.Count(content, "theme"); n != 1 {
		t.Errorf("expected 1 theme line, got %d in:\n%s", n, content)
	}
}

// --- B2 regression tests: cases the old text-replace writer broke ---

// Multi-line arrays must round-trip correctly. The old writer found the first
// line containing the key and replaced it, leaving the tail of the array behind.
func TestWriteConfigKey_MultilineArrayRewrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	initial := "[twitch]\nchannels = [\n  \"alice\",\n  \"bob\",\n]\n"
	_ = os.WriteFile(path, []byte(initial), 0600)

	if err := writeConfigKey(path, "twitch.channels", []string{"carol"}); err != nil {
		t.Fatalf("writeConfigKey: %v", err)
	}

	raw, _ := os.ReadFile(path)
	content := string(raw)
	if containsValue(content, "alice") || containsValue(content, "bob") {
		t.Errorf("old array entries should be replaced, got:\n%s", content)
	}
	if !containsValue(content, "carol") {
		t.Errorf("new value missing: %s", content)
	}
}

// A key written under one table must not be clobbered by writing a key of the
// same name under a different table. The old writer matched on the first occurrence
// of "name =" regardless of which table it belonged to.
func TestWriteConfigKey_DuplicateNameAcrossTables(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	initial := "[twitch]\nchannels = [\"alice\"]\n\n[other]\nchannels = [\"keep\"]\n"
	_ = os.WriteFile(path, []byte(initial), 0600)

	if err := writeConfigKey(path, "twitch.channels", []string{"bob"}); err != nil {
		t.Fatalf("writeConfigKey: %v", err)
	}

	raw, _ := os.ReadFile(path)
	content := string(raw)
	if !containsValue(content, "bob") {
		t.Errorf("new value missing from [twitch]: %s", content)
	}
	if !containsValue(content, "keep") {
		t.Errorf("[other].channels should be preserved: %s", content)
	}
	if containsValue(content, "alice") {
		t.Errorf("[twitch].channels not replaced: %s", content)
	}
}

// Values containing embedded quotes must survive a round-trip. The old writer
// used fmt.Sprintf("%q", v) which escapes correctly on the way out but the
// matcher at the top of the next call would false-match on the escaped form.
func TestWriteConfigKey_ValueWithQuote(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := writeConfigKey(path, "twitch.channels", []string{`has"quote`}); err != nil {
		t.Fatalf("writeConfigKey: %v", err)
	}
	// Second write should still parse the first, not corrupt on match.
	if err := writeConfigKey(path, "twitch.channels", []string{"plain"}); err != nil {
		t.Fatalf("writeConfigKey (second): %v", err)
	}

	raw, _ := os.ReadFile(path)
	content := string(raw)
	if !containsValue(content, "plain") {
		t.Errorf("second value missing: %s", content)
	}
	if strings.Contains(content, `has"quote`) || strings.Contains(content, `has\"quote`) {
		t.Errorf("first value should be gone: %s", content)
	}
}

// Writing one key must not drop unknown sibling sections. The map round-trip
// preserves data that twui's schema doesn't know about.
func TestWriteConfigKey_PreservesUnknownSections(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	initial := "[twitch]\nchannels = [\"alice\"]\n\n[unknown]\nfuture_field = \"value\"\n"
	_ = os.WriteFile(path, []byte(initial), 0600)

	if err := writeConfigKey(path, "twitch.channels", []string{"bob"}); err != nil {
		t.Fatalf("writeConfigKey: %v", err)
	}

	raw, _ := os.ReadFile(path)
	content := string(raw)
	if !strings.Contains(content, "[unknown]") {
		t.Errorf("[unknown] section should be preserved: %s", content)
	}
	if !strings.Contains(content, "future_field") {
		t.Errorf("unknown key should be preserved: %s", content)
	}
}

// --- parseRefreshInterval ---

func TestParseRefreshInterval(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr string
	}{
		{"empty disables refresh", "", 0, ""},
		{"explicit zero disables refresh", "0s", 0, ""},
		{"30s minimum allowed", "30s", 30 * time.Second, ""},
		{"1m valid", "1m", time.Minute, ""},
		{"2m30s valid", "2m30s", 2*time.Minute + 30*time.Second, ""},
		{"bare integer rejected", "60", 0, "invalid refresh interval"},
		{"garbage rejected", "soon", 0, "invalid refresh interval"},
		{"below minimum 5s rejected", "5s", 0, "must be at least 30s"},
		{"below minimum 29s rejected", "29s", 0, "must be at least 30s"},
		{"negative rejected", "-1m", 0, "must be at least 30s"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseRefreshInterval(c.input)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != c.want {
					t.Errorf("got %v, want %v", got, c.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), c.wantErr)
			}
		})
	}
}

// --- applyFlagFromViper ---

func TestApplyFlagFromViper_ValidValueApplied(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	var d time.Duration
	flags.DurationVar(&d, "refresh", 10*time.Second, "")
	viper.Set("general.refresh", "45s")

	applyFlagFromViper(flags.Lookup("refresh"), "general.refresh")

	if d != 45*time.Second {
		t.Errorf("expected flag to be 45s, got %v", d)
	}
}

func TestApplyFlagFromViper_InvalidValueLeavesDefault(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	// Capture slog so we can assert the warning fires.
	var buf strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	var d time.Duration
	flags.DurationVar(&d, "refresh", 10*time.Second, "")
	viper.Set("general.refresh", "not-a-duration")

	applyFlagFromViper(flags.Lookup("refresh"), "general.refresh")

	if d != 10*time.Second {
		t.Errorf("expected flag to remain at default 10s on invalid input, got %v", d)
	}
	if !strings.Contains(buf.String(), "invalid config value") {
		t.Errorf("expected warning, got log: %q", buf.String())
	}
}

func TestApplyFlagFromViper_NoFlagNoop(t *testing.T) {
	// Must not panic on nil flag.
	applyFlagFromViper(nil, "general.refresh")
}

// --- --refresh flag registration ---

func TestRefreshFlag_Registered(t *testing.T) {
	f := rootCmd.PersistentFlags().Lookup("refresh")
	if f == nil {
		// Cobra registers persistent flags on the root, but the actual --refresh might be on rootCmd directly.
		f = rootCmd.Flags().Lookup("refresh")
	}
	if f == nil {
		t.Fatal("--refresh flag not registered on rootCmd")
	}
	if f.Usage == "" {
		t.Error("--refresh flag has no usage string")
	}
	if !strings.Contains(f.Usage, "30s") && !strings.Contains(f.Usage, "1m") {
		t.Errorf("--refresh usage should give a duration example, got %q", f.Usage)
	}
}

// Silence unused import warning — slices is used implicitly via slices.DeleteFunc
// which is tested indirectly through ToggleFavorite/ToggleIgnore callbacks.
var _ = slices.Contains[[]string]
