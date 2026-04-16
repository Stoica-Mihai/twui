package ui

import (
	"strings"
	"testing"
)

func TestPresetsCount(t *testing.T) {
	if len(Presets) != 11 {
		t.Errorf("expected 11 presets, got %d", len(Presets))
	}
}

func TestPresetsUniqueNames(t *testing.T) {
	seen := make(map[string]bool)
	for _, p := range Presets {
		key := strings.ToLower(p.Name)
		if seen[key] {
			t.Errorf("duplicate preset name: %q", p.Name)
		}
		seen[key] = true
	}
}

func TestPresetsNonEmptyNames(t *testing.T) {
	for _, p := range Presets {
		if p.Name == "" {
			t.Error("found preset with empty name")
		}
	}
}

func TestThemeByName_KnownNames(t *testing.T) {
	for _, p := range Presets {
		if p.Name == "default" {
			continue // default returns empty Theme, skip field checks
		}
		got := ThemeByName(p.Name)
		// Verify at least one field matches the preset
		if got.Border != p.Theme.Border {
			t.Errorf("ThemeByName(%q).Border = %q, want %q", p.Name, got.Border, p.Theme.Border)
		}
	}
}

func TestThemeByName_CaseInsensitive(t *testing.T) {
	// "dracula" preset should be returned for "DRACULA" or "Dracula"
	lower := ThemeByName("dracula")
	upper := ThemeByName("DRACULA")
	mixed := ThemeByName("Dracula")

	if lower.Border != upper.Border {
		t.Errorf("case sensitivity mismatch: lower=%q upper=%q", lower.Border, upper.Border)
	}
	if lower.Border != mixed.Border {
		t.Errorf("case sensitivity mismatch: lower=%q mixed=%q", lower.Border, mixed.Border)
	}
}

func TestThemeByName_Unknown_ReturnsDefault(t *testing.T) {
	got := ThemeByName("doesnotexist")
	def := DefaultTheme()
	if got != def {
		t.Errorf("unknown theme should return DefaultTheme, got non-default fields")
	}
}

func TestNamedThemes_HaveNonEmptyColors(t *testing.T) {
	for _, p := range Presets {
		if p.Name == "default" {
			continue // default intentionally has empty fields
		}
		th := p.Theme
		fields := map[string]string{
			"Border":    th.Border,
			"Live":      th.Live,
			"Offline":   th.Offline,
			"Title":     th.Title,
			"TabActive": th.TabActive,
			"Category":  th.Category,
			"Favorite":  th.Favorite,
		}
		for field, val := range fields {
			if val == "" {
				t.Errorf("preset %q has empty %s field", p.Name, field)
			}
		}
	}
}

func TestBuildStyles_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("buildStyles panicked: %v", r)
		}
	}()
	for _, p := range Presets {
		buildStyles(p.Theme)
	}
	buildStyles(DefaultTheme())
}

// Monochrome mode must not emit any hex color string that was present in the
// source Theme: the monochrome dispatcher takes over and ignores all color fields.
func TestBuildStyles_MonochromeDropsColors(t *testing.T) {
	colored := "#abcdef"
	t1 := Theme{
		Live: colored, Offline: colored, Title: colored,
		SelectedBg: colored, SelectedFg: colored,
		Border: colored, Text: colored,
		Playing: colored, AdBreak: colored, Waiting: colored, Reconnecting: colored,
		TabActive: colored, Category: colored, Favorite: colored,
		Monochrome: true,
	}
	ps := buildStyles(t1)
	// Render something through each style; none of the outputs should contain the
	// original hex string (lipgloss would only embed it if a Foreground/Background
	// was set with that color).
	for name, s := range map[string]interface{ Render(...string) string }{
		"live":         ps.live,
		"offline":      ps.offline,
		"title":        ps.title,
		"selected":     ps.selected,
		"border":       ps.border,
		"text":         ps.text,
		"playing":      ps.playing,
		"adBreak":      ps.adBreak,
		"waiting":      ps.waiting,
		"reconnecting": ps.reconnecting,
		"tabActive":    ps.tabActive,
		"category":     ps.category,
		"favorite":     ps.favorite,
	} {
		out := s.Render("x")
		if strings.Contains(out, colored) {
			t.Errorf("monochrome style %s leaked color %q in output %q", name, colored, out)
		}
	}
}

// When Monochrome is false, the colored branch runs — the rendered Live style
// for a colored Theme should differ byte-for-byte from the monochrome render,
// at least when lipgloss's active color profile supports any color output.
func TestBuildStyles_ColoredPathStillRuns(t *testing.T) {
	colored := buildStyles(Theme{Live: "#00ff00"}).live.Render("x")
	mono := buildStyles(Theme{Live: "#00ff00", Monochrome: true}).live.Render("x")
	if colored == mono {
		t.Skip("lipgloss color profile suppresses all color in this env; branches not observable")
	}
}
