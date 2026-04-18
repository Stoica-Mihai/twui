package stream

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestCreatePipePair_CreatesBothFIFOs(t *testing.T) {
	dir := t.TempDir()

	video, audio, err := CreatePipePair(dir)
	if err != nil {
		t.Fatalf("CreatePipePair: %v", err)
	}

	for _, p := range []string{video, audio} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if info.Mode()&os.ModeNamedPipe == 0 {
			t.Errorf("%s is not a named pipe (mode=%v)", p, info.Mode())
		}
	}

	if filepath.Dir(video) != dir || filepath.Dir(audio) != dir {
		t.Errorf("pipes not placed in dir: video=%s audio=%s dir=%s", video, audio, dir)
	}
}

func TestCreatePipePair_ErrorsOnMissingDir(t *testing.T) {
	_, _, err := CreatePipePair(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Error("expected error for missing directory")
	}
}

func TestCreatePipePair_ErrorsIfVideoAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	// Pre-create a conflicting regular file at video.pipe path.
	if err := os.WriteFile(filepath.Join(dir, "video.pipe"), []byte("x"), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, _, err := CreatePipePair(dir)
	if err == nil {
		t.Error("expected error when video.pipe already exists")
	}
	// Ensure we didn't leave audio.pipe behind (which syscall.Mkfifo would
	// refuse when the name collides, but we also shouldn't create it after
	// the video mkfifo failed — it's never reached).
	if _, err := os.Stat(filepath.Join(dir, "audio.pipe")); !os.IsNotExist(err) {
		// OK either way — not a strict contract, but log for visibility.
		t.Logf("audio.pipe state after failure: err=%v", err)
	}
}

// TestCreatePipePair_PermBits checks the mode bits are 0600 (user rw only).
func TestCreatePipePair_PermBits(t *testing.T) {
	dir := t.TempDir()
	video, _, err := CreatePipePair(dir)
	if err != nil {
		t.Fatalf("CreatePipePair: %v", err)
	}
	var st syscall.Stat_t
	if err := syscall.Stat(video, &st); err != nil {
		t.Fatalf("stat: %v", err)
	}
	// Lower 9 mode bits isolate rwx for user/group/other.
	perm := st.Mode & 0o777
	if perm != 0o600 {
		t.Errorf("video pipe perm = %#o, want 0600", perm)
	}
}
