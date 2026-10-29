//go:build linux

package nbd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcusaram/squirrel/internal/exfatgen"
)

// Needs root and the nbd module; runs in `make test-linux`.
func TestServe(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("needs root")
	}
	data := make([]byte, 4<<20)
	for i := range data {
		data[i] = byte(i * 7)
	}
	d, err := Serve(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Skip(err)
	}
	defer d.Close()
	// The USB gadget opens the device right away; it must have its size
	// by then, or the gadget refuses it as too small (errno 525).
	if n, err := Size(d.Path); err != nil || n != int64(len(data)) {
		t.Fatalf("size right after Serve: %d %v, want %d", n, err, len(data))
	}
	f, err := os.Open(d.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got := make([]byte, 100000)
	if _, err := f.ReadAt(got, 1234567); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data[1234567:1234567+100000]) {
		t.Error("data differs")
	}
	if _, err := os.OpenFile(d.Path, os.O_WRONLY, 0); err == nil {
		// Opening may work; writing must not.
		w, _ := os.OpenFile(d.Path, os.O_WRONLY, 0)
		if _, err := w.WriteAt([]byte{1}, 0); err == nil {
			if err := w.Sync(); err == nil {
				t.Error("write to a read-only device succeeded")
			}
		}
		w.Close()
	}
}

// A generated exFAT disk served over nbd mounts and reads like a real one.
func TestExFATOverNBD(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("needs root")
	}
	want := make([]byte, 3<<20+77)
	for i := range want {
		want[i] = byte(i % 251)
	}
	root := &exfatgen.Dir{Mod: time.Now(), Dirs: []*exfatgen.Dir{{Name: "Drives", Mod: time.Now(), Dirs: []*exfatgen.Dir{{
		Name: "2026-10-29 08.36", Mod: time.Now(),
		Files: []*exfatgen.File{{Name: "front.mp4", Size: int64(len(want)), Mod: time.Now(),
			ReadAt: func(p []byte, off int64) (int, error) { return bytes.NewReader(want).ReadAt(p, off) }}},
	}}}}}
	v, err := exfatgen.Build(root, "Squirrel")
	if err != nil {
		t.Fatal(err)
	}
	d, err := Serve(v, v.Size())
	if err != nil {
		t.Skip(err)
	}
	defer d.Close()
	mnt := t.TempDir()
	if out, err := exec.Command("mount", "-t", "exfat", "-o", "ro,offset=1048576", d.Path, mnt).CombinedOutput(); err != nil {
		t.Fatalf("mount %s: %v %s", d.Path, err, out)
	}
	defer exec.Command("umount", mnt).Run()
	got, err := os.ReadFile(filepath.Join(mnt, "Drives", "2026-10-29 08.36", "front.mp4"))
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("read %d bytes, err %v, equal %v", len(got), err, bytes.Equal(got, want))
	}
}
