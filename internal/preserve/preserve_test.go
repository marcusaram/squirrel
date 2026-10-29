package preserve

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marcusaram/squirrel/internal/archive"
	"github.com/marcusaram/squirrel/internal/config"
	"github.com/marcusaram/squirrel/internal/fatfs"
	"github.com/marcusaram/squirrel/internal/state"
)

// testDir returns where to put the image and archive. Set TBX_TEST_DIR to a
// directory on XFS (see `make test-linux`) to exercise real reflinks.
func testDir(t *testing.T) string {
	if d := os.Getenv("TBX_TEST_DIR"); d != "" {
		d, err := os.MkdirTemp(d, "preserve")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(d) })
		return d
	}
	return t.TempDir()
}

func unpack(t *testing.T, name, dst string) {
	t.Helper()
	f, err := os.Open("../fatfs/testdata/" + name + ".img.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, zr); err != nil {
		t.Fatal(err)
	}
	out.Close()
}

func TestPreserve(t *testing.T) {
	for _, fs := range []string{"exfat", "fat32"} {
		t.Run(fs, func(t *testing.T) { testPreserve(t, fs) })
	}
}

func testPreserve(t *testing.T, fs string) {
	dir := testDir(t)
	img := filepath.Join(dir, "cam_disk.bin")
	unpack(t, fs, img)

	cfg := config.Default()
	cfg.Drives = []config.Drive{{Name: "cam", Image: img}}
	cfg.ArchiveDir = filepath.Join(dir, "archive")
	tomb, err := state.Open(filepath.Join(dir, "state", "tombstones"))
	if err != nil {
		t.Fatal(err)
	}
	ix := archive.NewIndex(cfg.ArchiveDir, nil)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ix.Load(log)
	p := New(func() config.Config { return cfg }, ix, tomb, log)
	clock := time.Date(2026, 10, 28, 19, 0, 0, 0, time.UTC)
	p.now = func() time.Time { clock = clock.Add(time.Minute); return clock }

	type file struct {
		size int64
		hash string
	}
	want := map[string]file{} // by archive path
	mf, _ := os.Open("../fatfs/testdata/" + fs + ".manifest")
	sc := bufio.NewScanner(mf)
	for sc.Scan() {
		parts := strings.Split(sc.Text(), "\t")
		if !strings.HasPrefix(parts[0], "TeslaCam/") && !strings.HasPrefix(parts[0], "TeslaTrackMode/") {
			continue
		}
		n, _ := strconv.ParseInt(parts[1], 10, 64)
		want[destPath(parts[0])] = file{n, parts[2]}
	}
	mf.Close()

	ctx := context.Background()
	// First scan only records what is there.
	if err := p.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if n := ix.Stats().Files; n != 0 {
		t.Fatalf("first scan extracted %d files", n)
	}
	// Second scan takes the stable files. The test .mp4 files are random
	// bytes, so they wait until they have been unchanged for keepBroken.
	p.Scan(ctx)
	for rel, f := range want {
		mp4 := strings.HasSuffix(rel, ".mp4")
		if got := ix.Has(rel, f.size); got == mp4 {
			t.Errorf("after 2nd scan: %s archived=%v", rel, got)
		}
	}
	p.Scan(ctx)
	p.Scan(ctx)
	for rel, f := range want {
		data, err := os.ReadFile(filepath.Join(cfg.ArchiveDir, rel))
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != f.hash {
			t.Errorf("%s: content differs", rel)
		}
	}
	st := p.Status()
	if st.Extracted != len(want) {
		t.Errorf("extracted %d, want %d", st.Extracted, len(want))
	}
	if os.Getenv("TBX_EXPECT_REFLINK") == "1" {
		if st.BytesShared == 0 || st.BytesCopied != 0 {
			t.Errorf("expected only reflinks: shared=%d copied=%d", st.BytesShared, st.BytesCopied)
		}
	}
	t.Logf("%s: reflinked %d bytes, copied %d bytes", fs, st.BytesShared, st.BytesCopied)

	// The car rotating clips away overwrites their clusters in place. The
	// archived copies share those blocks and must not change.
	w, err := os.OpenFile(img, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := w.Stat()
	junk := make([]byte, 1<<20)
	for i := range junk {
		junk[i] = 0xA5
	}
	for off := int64(0); off < fi.Size(); off += int64(len(junk)) {
		if _, err := w.WriteAt(junk, off); err != nil {
			t.Fatal(err)
		}
	}
	w.Sync()
	w.Close()
	for rel, f := range want {
		data, _ := os.ReadFile(filepath.Join(cfg.ArchiveDir, rel))
		if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != f.hash {
			t.Errorf("%s changed after the image was overwritten", rel)
		}
	}
	unpack(t, fs, img)

	// Deleting a group must stick: it is not extracted again.
	g, ok := ix.Group("SentryClips/2026-10-28_18-49-12")
	if !ok {
		t.Fatal("sentry group missing")
	}
	if g.Event == nil || g.Event.City != "Utrecht" || g.Thumb == "" || len(g.Cameras) != 4 {
		t.Errorf("sentry group = %+v", g)
	}
	if _, err := ix.DeleteGroup(g.ID, tomb); err != nil {
		t.Fatal(err)
	}
	p.Scan(ctx)
	p.Scan(ctx)
	if _, ok := ix.Group(g.ID); ok {
		t.Error("deleted group came back")
	}
}

// The car deletes what it recorded while parked when it drives off, and
// the Linux exFAT driver wipes size and first cluster from the deleted
// entry. A recording seen while being written is rescued from where it
// was, if its clusters are still free.
func TestRescueDeletedRecording(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	for _, reused := range []bool{false, true} {
		dir := testDir(t)
		clip := filepath.Join(dir, "clip.mp4")
		if out, err := exec.Command("ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=64x48:rate=10", "-t", "1",
			"-c:v", "libx264", "-pix_fmt", "yuv420p", clip).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg: %v %s", err, out)
		}
		video, _ := os.ReadFile(clip)
		imgPath := filepath.Join(dir, "cam_disk.bin")
		unpack(t, "exfat", imgPath)
		img, _ := os.ReadFile(imgPath)

		// The victim: a recording in RecentClips, made to hold the real
		// video with its entry saying only half of it is written yet.
		name := "TeslaCam/RecentClips/2026-10-28_18-40-00-front.mp4"
		v, err := fatfs.Open(bytes.NewReader(img), int64(len(img)))
		if err != nil {
			t.Fatal(err)
		}
		e, err := v.Lookup(name)
		if err != nil || e.Size < int64(len(video)) {
			t.Fatalf("victim %+v %v (video %d bytes)", e, err, len(video))
		}
		ext, _ := v.Extents(e)
		if len(ext) != 1 {
			t.Fatalf("victim in %d pieces", len(ext))
		}
		copy(img[ext[0].ImageOffset:], video)
		set := entrySet(t, img, e)
		stream := img[set+32:]
		binary.LittleEndian.PutUint64(stream[8:], uint64(len(video)/2))
		binary.LittleEndian.PutUint64(stream[24:], uint64(len(video)/2))
		binary.LittleEndian.PutUint16(img[set+2:], checksum(img[set:set+(int64(img[set+1])+1)*32]))
		os.WriteFile(imgPath, img, 0o644)

		cfg := config.Default()
		cfg.Drives = []config.Drive{{Name: "cam", Image: imgPath}}
		cfg.ArchiveDir = filepath.Join(dir, "archive")
		tomb, _ := state.Open(filepath.Join(dir, "state", "tombstones"))
		ix := archive.NewIndex(cfg.ArchiveDir, nil)
		p := New(func() config.Config { return cfg }, ix, tomb, slog.New(slog.NewTextHandler(io.Discard, nil)))
		ctx := context.Background()
		p.Scan(ctx)
		dest := destPath(name)
		if ix.Has(dest, int64(len(video))) || ix.Has(dest, int64(len(video)/2)) {
			t.Fatal("half a recording was taken")
		}

		// Finished and deleted before the next scan, as Linux does it.
		for j := int64(0); j <= int64(img[set+1]); j++ {
			img[set+j*32] &^= 0x80
		}
		binary.LittleEndian.PutUint32(stream[20:], 0)
		binary.LittleEndian.PutUint64(stream[8:], 0)
		binary.LittleEndian.PutUint64(stream[24:], 0)
		if !reused {
			first := e.FirstCluster - 2
			for c := first; c < first+uint32((e.Size+v.ClusterSize-1)/v.ClusterSize); c++ {
				img[bitmap(t, img, v.Offset)+int64(c/8)] &^= 1 << (c % 8)
			}
		}
		os.WriteFile(imgPath, img, 0o644)
		p.Scan(ctx)

		got, err := os.ReadFile(filepath.Join(cfg.ArchiveDir, dest))
		switch {
		case reused && err == nil:
			t.Error("rescued a recording whose clusters are in use again")
		case !reused && !bytes.Equal(got, video):
			t.Errorf("rescued %d bytes, want the %d of the recording: %v", len(got), len(video), err)
		}
	}
}

// entrySet finds the offset of a file's exFAT entry set in the image.
func entrySet(t *testing.T, img []byte, e fatfs.Entry) int64 {
	for off := 0; off+64 <= len(img); off += 32 {
		s := img[off+32:]
		if img[off] == 0x85 && s[0] == 0xC0 && binary.LittleEndian.Uint32(s[20:]) == e.FirstCluster &&
			int64(binary.LittleEndian.Uint64(s[24:])) == e.Size {
			return int64(off)
		}
	}
	t.Fatal("entry set not found")
	return 0
}

func checksum(set []byte) uint16 {
	var sum uint16
	for j, b := range set {
		if j != 2 && j != 3 {
			sum = (sum<<15 | sum>>1) + uint16(b)
		}
	}
	return sum
}

// bitmap returns the offset of the allocation bitmap of an exFAT volume
// that starts at base in the image.
func bitmap(t *testing.T, img []byte, base int64) int64 {
	bs := img[base:]
	bps := int64(1) << bs[108]
	cluster := bps << bs[109]
	heap := base + int64(binary.LittleEndian.Uint32(bs[88:]))*bps
	root := heap + int64(binary.LittleEndian.Uint32(bs[96:])-2)*cluster
	for off := root; off < root+cluster; off += 32 {
		if img[off] == 0x81 {
			return heap + int64(binary.LittleEndian.Uint32(img[off+20:])-2)*cluster
		}
	}
	t.Fatal("no bitmap")
	return 0
}
