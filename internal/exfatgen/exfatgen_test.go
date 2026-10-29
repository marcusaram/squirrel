package exfatgen

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// content is deterministic data for a file of n bytes.
func content(seed string, n int64) []byte {
	b := make([]byte, 0, n+32)
	for i := 0; int64(len(b)) < n; i++ {
		h := sha256.Sum256([]byte(fmt.Sprintf("%s-%d", seed, i)))
		b = append(b, h[:]...)
	}
	return b[:n]
}

func file(name string, n int64) *File {
	data := content(name, n)
	return &File{Name: name, Size: n, Mod: time.Date(2026, 10, 29, 8, 36, 0, 0, time.UTC),
		ReadAt: func(p []byte, off int64) (int, error) { return bytes.NewReader(data).ReadAt(p, off) }}
}

// testTree has nested folders, long and non-ASCII names, empty and
// multi-cluster files, and a folder with more entries than one cluster.
func testTree() *Dir {
	many := &Dir{Name: "Drives", Mod: time.Now()}
	for i := 0; i < 150; i++ {
		d := &Dir{Name: fmt.Sprintf("2026-10-%02d 08.%02d #%d", i%28+1, i%60, i), Mod: time.Now(),
			Files: []*File{file(fmt.Sprintf("front-%d.mp4", i), int64(1000+i*37))}}
		many.Dirs = append(many.Dirs, d)
	}
	ev := &Dir{Name: "2026-10-27 22.08 Utrecht – São Paulo straat met een heel lange naam", Mod: time.Now(),
		Files: []*File{file("front.mp4", 3*clusterSize+123), file("back.mp4", clusterSize), file("event.json", 0), file("left_repeater.mp4", 77)}}
	return &Dir{Mod: time.Now(), Dirs: []*Dir{many, {Name: "Sentry", Mod: time.Now(), Dirs: []*Dir{ev}}},
		Files: []*File{file("Read me.txt", 400)}}
}

func TestBuild(t *testing.T) {
	v, err := Build(testTree(), "Squirrel")
	if err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(t.TempDir(), "disk.img")
	if out := os.Getenv("EXFATGEN_OUT"); out != "" {
		img = out
	}
	f, err := os.Create(img)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(f, io.NewSectionReader(v, 0, v.Size())); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if v.Size()%sectorSize != 0 {
		t.Errorf("size %d", v.Size())
	}
}

func TestBadNames(t *testing.T) {
	for _, n := range []string{"", "a:b", "x/y", "..", "q?"} {
		if _, err := Build(&Dir{Files: []*File{file(n, 1)}}, "x"); err == nil {
			t.Errorf("%q accepted", n)
		}
	}
	if _, err := Build(&Dir{Files: []*File{file("A.mp4", 1), file("a.MP4", 1)}}, "x"); err == nil {
		t.Error("names differing only in case accepted")
	}
}
