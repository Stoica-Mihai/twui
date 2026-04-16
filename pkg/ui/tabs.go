package ui

import (
	"fmt"
	"strings"
	"time"
)

// renderTabBar draws the row of tab labels plus the optional auto-refresh
// countdown on the right. Called once per frame from render().
func (m Model) renderTabBar() string {
	tabs := []struct {
		label string
		mode  viewMode
	}{
		{"Watch List", viewModeWatchList},
		{"Browse", viewModeBrowse},
		{"Search", viewModeSearch},
		{"Ignored", viewModeIgnored},
	}

	var parts []string
	for _, tab := range tabs {
		label := " " + tab.label + " "
		if m.mode == tab.mode {
			parts = append(parts, m.styles.tabActive.Render("["+label+"]"))
		} else {
			parts = append(parts, m.styles.text.Render(" "+label+" "))
		}
	}

	result := strings.Join(parts, m.styles.text.Render(" · "))
	if m.refreshInterval > 0 {
		if m.refreshing {
			result += m.styles.text.Render("  ↻ refreshing…")
		} else {
			result += m.styles.text.Render(fmt.Sprintf("  ↻ %s", m.refreshCountdown.Truncate(time.Second)))
		}
	}
	return result
}
