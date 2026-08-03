package state_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ysk/dacast-watchfolder/internal/state"
)

func TestUploadLifecycle(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AppData", dir) // Windows UserConfigDir uses AppData
	// Force appdir via overriding - appdir uses os.UserConfigDir which on Windows is AppData
	// Ensure subdirectory can be created under temp by setting USERPROFILE-related vars.
	// UserConfigDir on Windows: %AppData%
	os.Setenv("APPDATA", dir)

	st, err := state.Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	path := filepath.Join(dir, "video.mp4")
	if err := st.UpsertQueued(path, 100, 123); err != nil {
		t.Fatal(err)
	}
	done, err := st.IsDoneSameIdentity(path, 100, 123)
	if err != nil || done {
		t.Fatalf("expected not done, done=%v err=%v", done, err)
	}
	if err := st.MarkUploading(path, "up1", "s3://x", 8<<20); err != nil {
		t.Fatal(err)
	}
	if err := st.SavePart(path, 1, `"etag1"`); err != nil {
		t.Fatal(err)
	}
	parts, err := st.Parts(path)
	if err != nil || parts[1] != `"etag1"` {
		t.Fatalf("parts=%v err=%v", parts, err)
	}
	if err := st.MarkDone(path, "vod-1"); err != nil {
		t.Fatal(err)
	}
	done, err = st.IsDoneSameIdentity(path, 100, 123)
	if err != nil || !done {
		t.Fatalf("expected done")
	}
	parts, _ = st.Parts(path)
	if len(parts) != 0 {
		t.Fatalf("parts should be cleared, got %v", parts)
	}
}
