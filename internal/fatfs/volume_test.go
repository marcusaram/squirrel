package fatfs

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The images are built by scripts/mktestimages.sh with the Linux kernel
// drivers and exfatprogs/dosfstools, the same tools used on the Pi.

func loadImage(t *testing.T, name string) *bytes.Reader {
	t.Helper()
	f, err := os.Open("testdata/" + name + ".img.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(data)
}

type manifestEntry struct {
	size int64
	hash string
}

func loadManifest(t *testing.T, name string) map[string]manifestEntry {
	t.Helper()
	f, err := os.Open("testdata/" + name + ".manifest")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m := map[string]manifestEntry{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Split(sc.Text(), "\t")
		size, _ := strconv.ParseInt(parts[1], 10, 64)
		m[parts[0]] = manifestEntry{size, parts[2]}
	}
	return m
}

func TestImages(t *testing.T) {
	for _, name := range []string{"exfat", "fat32"} {
		t.Run(name, func(t *testing.T) {
			img := loadImage(t, name)
			v, err := Open(img, img.Size())
			if err != nil {
				t.Fatal(err)
			}
			if v.Type != name {
				t.Errorf("type = %q", v.Type)
			}
			if v.ClusterSize != 4096 {
				t.Errorf("cluster size = %d", v.ClusterSize)
			}
			if v.Offset != 1<<20 {
				t.Errorf("partition offset = %d", v.Offset)
			}
			if v.Dirty {
				t.Errorf("clean image reported dirty")
			}

			want := loadManifest(t, name)
			seen := map[string]bool{}
			err = v.Walk(v.Root(), func(e Entry, err error) error {
				if err != nil {
					t.Fatalf("walk %s: %v", e.Path, err)
				}
				if e.IsDir {
					return nil
				}
				w, ok := want[e.Path]
				if !ok {
					t.Errorf("unexpected file %q", e.Path)
					return nil
				}
				seen[e.Path] = true
				if e.Size != w.size {
					t.Errorf("%s: size %d, want %d", e.Path, e.Size, w.size)
				}
				if !e.Complete {
					t.Errorf("%s: not complete", e.Path)
				}
				fr, err := v.OpenFile(e)
				if err != nil {
					t.Fatalf("%s: %v", e.Path, err)
				}
				h := sha256.New()
				if _, err := io.Copy(h, fr.NewReader()); err != nil {
					t.Fatalf("%s: %v", e.Path, err)
				}
				if got := hex.EncodeToString(h.Sum(nil)); got != w.hash {
					t.Errorf("%s: content hash mismatch", e.Path)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			for p := range want {
				if !seen[p] {
					t.Errorf("missing file %q", p)
				}
			}

			// The interleaved writes must have produced fragmented files,
			// otherwise this test does not cover chain following.
			e, err := v.Lookup("teslacam/savedclips/2026-10-27_08-15-00/2026-10-27_08-14-00-front.mp4")
			if err != nil {
				t.Fatal(err)
			}
			ext, err := v.Extents(e)
			if err != nil {
				t.Fatal(err)
			}
			if len(ext) < 2 {
				t.Errorf("expected a fragmented file, got %d extent(s)", len(ext))
			}
			var total int64
			for i, x := range ext {
				if x.FileOffset != total {
					t.Errorf("extent %d starts at %d, want %d", i, x.FileOffset, total)
				}
				total += x.Length
			}
			if total != e.Size {
				t.Errorf("extents cover %d bytes, want %d", total, e.Size)
			}

			if _, err := v.Lookup("TeslaCam/RecentClips/deleted.mp4"); err == nil {
				t.Error("deleted file found")
			}
		})
	}
}

func TestCorruptEntrySetIsSkipped(t *testing.T) {
	img := loadImage(t, "exfat")
	data, _ := io.ReadAll(img)
	v, err := Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	dir, err := v.Lookup("TeslaCam/RecentClips")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := v.ReadDir(dir)

	// Flip a byte in the first file entry set of the directory, the way a
	// half-written update would look.
	runs, err := v.chain(dir.FirstCluster, 1, dir.contiguous)
	if err != nil {
		t.Fatal(err)
	}
	off, _ := v.clusterOffset(runs[0][0])
	for i := off; i < off+v.ClusterSize; i += 32 {
		if data[i] == exfatEntryFile {
			data[i+12] ^= 0xFF // modification time
			break
		}
	}
	v, _ = Open(bytes.NewReader(data), int64(len(data)))
	after, err := v.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before)-1 {
		t.Errorf("got %d entries after corruption, want %d", len(after), len(before)-1)
	}
}

func TestNotFAT(t *testing.T) {
	b := make([]byte, 1<<20)
	if _, err := Open(bytes.NewReader(b), int64(len(b))); err != ErrNotFAT {
		t.Errorf("err = %v", err)
	}
}
