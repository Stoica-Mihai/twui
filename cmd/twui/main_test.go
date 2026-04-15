package main

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
		"source":  ms(""),
		"720p60":  ms(""),
		"480p":    ms(""),
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
	if !strings.Contains(content, `"alice"`) {
		t.Errorf("config file missing alice: %s", content)
	}
	if !strings.Contains(content, `"bob"`) {
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
	if strings.Contains(content, `"old"`) {
		t.Errorf("old value should be replaced: %s", content)
	}
	if !strings.Contains(content, `"new1"`) || !strings.Contains(content, `"new2"`) {
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
	if !strings.Contains(content, `"only"`) {
		t.Errorf("appended value missing: %s", content)
	}
}

func TestWriteConfigKey_EmptyList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	_ = os.WriteFile(path, []byte("channels = [\"old\"]\n"), 0600)

	err := writeConfigKey(path, "twitch.channels", []string{})
	if err != nil {
		t.Fatalf("writeConfigKey: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), `"old"`) {
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

// Silence unused import warning — slices is used implicitly via slices.DeleteFunc
// which is tested indirectly through ToggleFavorite/ToggleIgnore callbacks.
var _ = slices.Contains[[]string]
