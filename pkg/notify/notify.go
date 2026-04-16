// Package notify provides desktop notification support for twui.
package notify

import (
	"fmt"
	"log/slog"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// execRun runs a named binary with the given args. It's a package-level seam
// so tests can capture the binary+args instead of spawning a real process.
var execRun = func(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

// execLookPath is the hook for binary discovery; overridden by tests to
// simulate presence or absence of notify-send / osascript.
var execLookPath = exec.LookPath

// escapeAppleScript escapes backslashes and double quotes for safe
// interpolation into AppleScript string literals.
func escapeAppleScript(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// Notifier sends desktop notifications.
type Notifier interface {
	Send(title, body string)
	SendWithIcon(title, body, iconPath string)
}

// nopNotifier is a no-op notifier used when notifications are disabled.
type nopNotifier struct{}

func (nopNotifier) Send(_, _ string) {}

func (nopNotifier) SendWithIcon(_, _, _ string) {}

// Nop returns a no-op Notifier that silently discards all notifications.
func Nop() Notifier { return nopNotifier{} }

// linuxNotifier sends notifications via notify-send.
type linuxNotifier struct {
	once      sync.Once
	available bool
	timeoutMs int // 0 = permanent
}

func (n *linuxNotifier) send(args []string) {
	n.once.Do(func() {
		_, err := execLookPath("notify-send")
		n.available = err == nil
		if !n.available {
			slog.Warn("notify-send not found in PATH; install libnotify-bin for desktop notifications")
		}
	})
	if !n.available {
		return
	}
	if n.timeoutMs > 0 {
		args = append([]string{"-t", fmt.Sprintf("%d", n.timeoutMs)}, args...)
	}
	if err := execRun("notify-send", args...); err != nil {
		slog.Debug("notify-send failed", "err", err)
	}
}

func (n *linuxNotifier) Send(title, body string) {
	n.send([]string{title, body})
}

func (n *linuxNotifier) SendWithIcon(title, body, iconPath string) {
	if iconPath == "" {
		n.send([]string{title, body})
		return
	}
	n.send([]string{"-i", iconPath, title, body})
}

// macNotifier sends notifications via osascript.
type macNotifier struct {
	once      sync.Once
	available bool
}

func (n *macNotifier) Send(title, body string) {
	n.once.Do(func() {
		_, err := execLookPath("osascript")
		n.available = err == nil
		if !n.available {
			slog.Warn("osascript not found in PATH; desktop notifications disabled")
		}
	})
	if !n.available {
		return
	}
	script := `display notification "` + escapeAppleScript(body) + `" with title "` + escapeAppleScript(title) + `"`
	if err := execRun("osascript", "-e", script); err != nil {
		slog.Debug("osascript failed", "err", err)
	}
}

func (n *macNotifier) SendWithIcon(title, body, _ string) {
	n.Send(title, body)
}

// NewNotifier returns a platform-appropriate Notifier.
// timeoutMs controls how long notifications stay visible (milliseconds).
// Pass 0 for permanent notifications (user must dismiss manually).
// Returns a no-op notifier on unsupported platforms.
func NewNotifier(timeoutMs int) Notifier {
	switch runtime.GOOS {
	case "linux":
		return &linuxNotifier{timeoutMs: timeoutMs}
	case "darwin":
		return &macNotifier{}
	default:
		slog.Debug("desktop notifications not supported on this platform", "os", runtime.GOOS)
		return nopNotifier{}
	}
}
