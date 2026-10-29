//go:build linux

package archive

import (
	"crypto/rand"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/marcusaram/squirrel/internal/state"
	"github.com/marcusaram/squirrel/internal/sysinfo"
)

// Runs on XFS through `make test-linux` (TBX_TEST_DIR). It replays the
// migration from teslausb: archived clips share their blocks with the car's
// image and with an old snapshot, so deleting them frees nothing.
func TestPruneOnlyWhatFreesSpace(t *testing.T) {
	base := os.Getenv("TBX_TEST_DIR")
	if base == "" || runtime.GOOS != "linux" {
		t.Skip("needs TBX_TEST_DIR on XFS")
	}
	dir, err := os.MkdirTemp(base, "space")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	const size = 64 << 20
	cam := filepath.Join(dir, "cam_disk.bin")
	data := make([]byte, size)
	rand.Read(data)
	os.WriteFile(cam, data, 0o644)
	reflink := func(src, dst string) {
		os.MkdirAll(filepath.Dir(dst), 0o755)
		s, _ := os.Open(src)
		defer s.Close()
		d, _ := os.Create(dst)
		defer d.Close()
		if err := unix.IoctlFileClone(int(d.Fd()), int(s.Fd())); err != nil {
			t.Fatalf("reflink: %v", err)
		}
	}
	snapDir := filepath.Join(dir, "snapshots")
	reflink(cam, filepath.Join(snapDir, "snap-000001", "snap.bin"))
	os.WriteFile(filepath.Join(snapDir, "snap-000001", ImportedMarker), nil, 0o644)
	reflink(cam, filepath.Join(snapDir, "snap-000002", "snap.bin")) // never imported

	root := filepath.Join(dir, "archive")
	clip := filepath.Join(root, "RecentClips", "2026-10-28", "2026-10-28_10-00-00-front.mp4")
	reflink(cam, clip)

	if n, err := unsharedBytes(clip); err != nil || n != 0 {
		t.Fatalf("fully shared clip: unshared=%d err=%v", n, err)
	}

	tomb, _ := state.Open(filepath.Join(dir, "tomb"))
	ix := NewIndex(root, nil)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ix.Load(log)
	var reserve uint64
	m := &SpaceManager{Index: ix, Tomb: tomb, SnapshotDir: snapDir, Log: log, Reserve: func() uint64 { return reserve }}
	underReserve := func() {
		d, _ := sysinfo.DiskUsage(root)
		reserve = d.Free + 32<<20
	}

	// 1. Below the reserve: the imported snapshot goes first, the clip stays.
	underReserve()
	m.Check()
	if _, err := os.Stat(filepath.Join(snapDir, "snap-000001")); !os.IsNotExist(err) {
		t.Error("imported snapshot not removed")
	}
	if _, err := os.Stat(filepath.Join(snapDir, "snap-000002")); err != nil {
		t.Error("snapshot that was never imported was removed")
	}
	if !ix.Has("RecentClips/2026-10-28/2026-10-28_10-00-00-front.mp4", size) {
		t.Fatal("clip deleted while it was not the first thing to go")
	}

	// 2. Still below the reserve, but the clip shares every block with the
	// image and the remaining snapshot: it must be kept, with a warning.
	underReserve()
	m.Check()
	if !ix.Has("RecentClips/2026-10-28/2026-10-28_10-00-00-front.mp4", size) {
		t.Fatal("clip deleted although that frees nothing")
	}
	if m.Status().Warning == "" {
		t.Error("no warning when nothing can be freed")
	}

	// 3. The car overwrites its image and the old snapshot is gone: now the
	// clip owns its blocks, and deleting it frees space.
	os.RemoveAll(filepath.Join(snapDir, "snap-000002"))
	rand.Read(data)
	os.WriteFile(cam, data, 0o644)
	time.Sleep(200 * time.Millisecond)
	if n, _ := unsharedBytes(clip); n != size {
		t.Fatalf("after overwrite: unshared=%d, want %d", n, size)
	}
	underReserve()
	// Right after a pass that found nothing, it waits before looking again.
	m.Check()
	if !ix.Has("RecentClips/2026-10-28/2026-10-28_10-00-00-front.mp4", size) || m.Status().Warning == "" {
		t.Fatal("looked again during the wait, or dropped the warning")
	}
	m.idleUntil = time.Time{}
	m.Check()
	if ix.Has("RecentClips/2026-10-28/2026-10-28_10-00-00-front.mp4", size) {
		t.Error("clip that frees space was not pruned")
	}
	if !tomb.Has("RecentClips/2026-10-28/2026-10-28_10-00-00-front.mp4") {
		t.Error("pruned clip not tombstoned")
	}
}
