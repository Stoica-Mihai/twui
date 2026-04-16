package output

import (
	"slices"
	"strings"
	"testing"
)

func TestBuildArgs_StdinMarkerFirst(t *testing.T) {
	args := (&Player{Path: "mpv"}).buildArgs()
	if len(args) == 0 || args[0] != "-" {
		t.Errorf("args should start with '-' for stdin; got %v", args)
	}
}

func TestBuildArgs_MPVTitleFlag(t *testing.T) {
	p := &Player{Path: "mpv", Title: "streamer — twui"}
	args := p.buildArgs()
	want := "--force-media-title=streamer — twui"
	if !slices.Contains(args, want) {
		t.Errorf("expected %q in args, got %v", want, args)
	}
}

func TestBuildArgs_VLCTitleFlag(t *testing.T) {
	p := &Player{Path: "/usr/bin/vlc", Title: "streamer"}
	args := p.buildArgs()
	want := "--meta-title=streamer"
	if !slices.Contains(args, want) {
		t.Errorf("expected %q in args, got %v", want, args)
	}
}

func TestBuildArgs_TitleOmittedWhenEmpty(t *testing.T) {
	p := &Player{Path: "mpv"}
	for _, a := range p.buildArgs() {
		if strings.Contains(a, "--force-media-title") || strings.Contains(a, "--meta-title") {
			t.Errorf("should not emit a title flag when Title is empty, got %v", p.buildArgs())
		}
	}
}

func TestBuildArgs_NoTerminal_OnlyForMPV(t *testing.T) {
	mpv := (&Player{Path: "mpv", NoTerminal: true}).buildArgs()
	if !slices.Contains(mpv, "--no-terminal") {
		t.Errorf("mpv + NoTerminal should include --no-terminal, got %v", mpv)
	}

	vlc := (&Player{Path: "vlc", NoTerminal: true}).buildArgs()
	if slices.Contains(vlc, "--no-terminal") {
		t.Errorf("vlc + NoTerminal should NOT include --no-terminal (mpv-specific), got %v", vlc)
	}
}

func TestBuildArgs_AudioOnly_OnlyForMPV(t *testing.T) {
	mpv := (&Player{Path: "mpv", AudioOnly: true}).buildArgs()
	if !slices.Contains(mpv, "--vid=no") || !slices.Contains(mpv, "--force-window") {
		t.Errorf("mpv + AudioOnly should include --vid=no and --force-window, got %v", mpv)
	}

	vlc := (&Player{Path: "vlc", AudioOnly: true}).buildArgs()
	if slices.Contains(vlc, "--vid=no") {
		t.Errorf("vlc + AudioOnly should NOT include mpv-specific flags, got %v", vlc)
	}
}

func TestBuildArgs_ExtraArgsAppendedLast(t *testing.T) {
	p := &Player{
		Path:  "mpv",
		Title: "s",
		Args:  []string{"-af", "lavfi=[volume=2]"},
	}
	args := p.buildArgs()
	// Extra args must come after all synthesized flags.
	idx := slices.Index(args, "-af")
	if idx == -1 {
		t.Fatalf("-af not in args: %v", args)
	}
	if idx < slices.Index(args, "--force-media-title=s") {
		t.Errorf("extra args should come after synthesized flags; got %v", args)
	}
	if idx+1 >= len(args) || args[idx+1] != "lavfi=[volume=2]" {
		t.Errorf("extra-args pairs not preserved in order: %v", args)
	}
}

func TestBuildArgs_CaseInsensitiveBinaryMatch(t *testing.T) {
	// Path with capitalized binary name should still match mpv detection.
	args := (&Player{Path: "/opt/bin/MPV", Title: "s"}).buildArgs()
	if !slices.Contains(args, "--force-media-title=s") {
		t.Errorf("case-insensitive mpv detection failed: %v", args)
	}
}

func TestBuildArgs_AbsolutePathBinaryMatch(t *testing.T) {
	args := (&Player{Path: "/usr/local/bin/mpv", Title: "s", NoTerminal: true}).buildArgs()
	if !slices.Contains(args, "--no-terminal") {
		t.Errorf("absolute mpv path should still trigger mpv-specific flag: %v", args)
	}
}

func TestBuildArgs_UnknownBinaryGetsNoPlayerSpecificFlags(t *testing.T) {
	// Unrecognized player: should still include "-" and extra args, but no
	// mpv/vlc-specific synthesis.
	p := &Player{
		Path:       "ffplay",
		Title:      "s",
		NoTerminal: true,
		AudioOnly:  true,
		Args:       []string{"extra"},
	}
	args := p.buildArgs()
	if args[0] != "-" {
		t.Errorf("unknown player should still get '-' stdin marker, got %v", args)
	}
	if !slices.Contains(args, "extra") {
		t.Errorf("unknown player should still get extra Args, got %v", args)
	}
	for _, bad := range []string{"--force-media-title=s", "--meta-title=s", "--no-terminal", "--vid=no", "--force-window"} {
		if slices.Contains(args, bad) {
			t.Errorf("unknown player should not emit %q, got %v", bad, args)
		}
	}
}
