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
