package notify

import (
	"errors"
	"strings"
	"testing"
)

// captureExec replaces execRun + execLookPath for the duration of a test.
// Returns a getter for the last captured invocation and a restore function.
type capturedCall struct {
	binary string
	args   []string
}

func captureExec(t *testing.T, lookPathErr error) (*capturedCall, func()) {
	t.Helper()
	cap := &capturedCall{}
	origRun := execRun
	origLookPath := execLookPath
	execRun = func(name string, args ...string) error {
		cap.binary = name
		cap.args = append([]string(nil), args...)
		return nil
	}
	execLookPath = func(file string) (string, error) {
		if lookPathErr != nil {
			return "", lookPathErr
		}
		return "/usr/bin/" + file, nil
	}
	return cap, func() {
		execRun = origRun
		execLookPath = origLookPath
	}
}

func TestLinuxNotifier_Send_BinaryAndArgs(t *testing.T) {
	cap, restore := captureExec(t, nil)
	defer restore()

	n := &linuxNotifier{timeoutMs: 0}
	n.Send("title", "body")

	if cap.binary != "notify-send" {
		t.Errorf("binary = %q, want notify-send", cap.binary)
	}
	if got, want := cap.args, []string{"title", "body"}; !equalSlices(got, want) {
		t.Errorf("args = %v, want %v", got, want)
	}
}

func TestLinuxNotifier_Send_WithTimeout(t *testing.T) {
	cap, restore := captureExec(t, nil)
	defer restore()

	n := &linuxNotifier{timeoutMs: 5000}
	n.Send("title", "body")

	if got, want := cap.args, []string{"-t", "5000", "title", "body"}; !equalSlices(got, want) {
		t.Errorf("args = %v, want %v", got, want)
	}
}

func TestLinuxNotifier_SendWithIcon_WithPath(t *testing.T) {
	cap, restore := captureExec(t, nil)
	defer restore()

	n := &linuxNotifier{timeoutMs: 5000}
	n.SendWithIcon("t", "b", "/tmp/icon.png")

	want := []string{"-t", "5000", "-i", "/tmp/icon.png", "t", "b"}
	if !equalSlices(cap.args, want) {
		t.Errorf("args = %v, want %v", cap.args, want)
	}
}

func TestLinuxNotifier_SendWithIcon_EmptyPathOmitsIconFlag(t *testing.T) {
	cap, restore := captureExec(t, nil)
	defer restore()

	n := &linuxNotifier{timeoutMs: 0}
	n.SendWithIcon("t", "b", "")

	for _, a := range cap.args {
		if a == "-i" {
			t.Errorf("expected no -i flag for empty iconPath, got %v", cap.args)
		}
	}
}

func TestLinuxNotifier_SkipsWhenBinaryAbsent(t *testing.T) {
	cap, restore := captureExec(t, errors.New("not found"))
	defer restore()

	n := &linuxNotifier{}
	n.Send("t", "b")

	if cap.binary != "" {
		t.Errorf("should not invoke binary when lookPath fails, got %q", cap.binary)
	}
}

func TestMacNotifier_Send_BuildsAppleScript(t *testing.T) {
	cap, restore := captureExec(t, nil)
	defer restore()

	n := &macNotifier{}
	n.Send(`Break time`, `has "quotes" and a \ backslash`)

	if cap.binary != "osascript" {
		t.Errorf("binary = %q, want osascript", cap.binary)
	}
	if len(cap.args) != 2 || cap.args[0] != "-e" {
		t.Fatalf("expected [-e, <script>], got %v", cap.args)
	}
	script := cap.args[1]
	if !strings.Contains(script, `with title "Break time"`) {
		t.Errorf("title not interpolated: %s", script)
	}
	if !strings.Contains(script, `\"quotes\"`) {
		t.Errorf("double quotes not escaped in body: %s", script)
	}
	if !strings.Contains(script, `\\ backslash`) {
		t.Errorf("backslash not escaped in body: %s", script)
	}
}

func TestMacNotifier_SendWithIcon_IgnoresIcon(t *testing.T) {
	cap, restore := captureExec(t, nil)
	defer restore()

	n := &macNotifier{}
	n.SendWithIcon("t", "b", "/tmp/icon.png")

	if !strings.Contains(cap.args[1], `with title "t"`) {
		t.Errorf("expected title-only, got: %s", cap.args[1])
	}
	// osascript notifications can't render custom icons; ensure the path
	// doesn't leak into the AppleScript literal.
	if strings.Contains(cap.args[1], "icon.png") {
		t.Errorf("icon path should not appear in AppleScript: %s", cap.args[1])
	}
}

func TestMacNotifier_SkipsWhenBinaryAbsent(t *testing.T) {
	cap, restore := captureExec(t, errors.New("not found"))
	defer restore()

	n := &macNotifier{}
	n.Send("t", "b")

	if cap.binary != "" {
		t.Errorf("should not invoke binary when lookPath fails, got %q", cap.binary)
	}
}

func TestNop_DoesNotInvokeExec(t *testing.T) {
	cap, restore := captureExec(t, nil)
	defer restore()

	n := Nop()
	n.Send("t", "b")
	n.SendWithIcon("t", "b", "/icon")

	if cap.binary != "" {
		t.Errorf("Nop notifier should not invoke exec; got %q", cap.binary)
	}
}

func TestNewNotifier_ReturnsPlatformImpl(t *testing.T) {
	// The concrete type depends on the test host's GOOS — we only check that
	// NewNotifier returns something non-nil that implements the interface and
	// doesn't panic when called. Platform-specific arg assertions are covered
	// by the linuxNotifier / macNotifier tests above.
	n := NewNotifier(5000)
	if n == nil {
		t.Fatal("NewNotifier returned nil")
	}

	_, restore := captureExec(t, nil)
	defer restore()
	n.Send("t", "b")
	n.SendWithIcon("t", "b", "/icon")
}

func TestEscapeAppleScript(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{``, ``},
		{`plain`, `plain`},
		{`with "quote"`, `with \"quote\"`},
		{`back\slash`, `back\\slash`},
		{`\"both"`, `\\\"both\"`},
	}
	for _, c := range cases {
		if got := escapeAppleScript(c.in); got != c.want {
			t.Errorf("escapeAppleScript(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
