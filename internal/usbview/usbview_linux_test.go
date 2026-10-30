//go:build linux

package usbview

import (
	"bytes"
	"crypto/sha256"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/marcusaram/squirrel/internal/archive"
	"github.com/marcusaram/squirrel/internal/exfatgen"
	"github.com/marcusaram/squirrel/internal/mp4"
	"github.com/marcusaram/squirrel/internal/nbd"
)

// The whole computer-mode drive from an archive, served over nbd and
// mounted. Needs root, nbd and USBVIEW_ARCHIVE (e.g. the demo archive).
func TestDriveOverNBD(t *testing.T) {
	root := os.Getenv("USBVIEW_ARCHIVE")
	if root == "" || os.Getuid() != 0 {
		t.Skip("set USBVIEW_ARCHIVE and run as root")
	}
	ix := archive.NewIndex(root, nil)
	ix.Load(slog.New(slog.NewTextHandler(io.Discard, nil)))
	longs := &Longs{}
	defer longs.Close()
	tree, files, err := Tree(ix, "nl", "kmh", longs, nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err := exfatgen.Build(tree, "Squirrel")
	if err != nil {
		t.Fatal(err)
	}
	d, err := nbd.Serve(v, v.Size())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	mnt := t.TempDir()
	if out, err := exec.Command("mount", "-t", "exfat", "-o", "ro,offset=1048576", d.Path, mnt).CombinedOutput(); err != nil {
		t.Fatalf("mount: %v %s", err, out)
	}
	defer exec.Command("umount", mnt).Run()
	out, _ := exec.Command("find", mnt, "-type", "f").Output()
	t.Logf("%d files in the tree; on the drive:\n%s", files, out)

	// Every camera file equals the long video built directly.
	g := ix.Groups()[0]
	for _, cam := range g.Cameras {
		var paths []string
		for _, s := range g.Segments {
			p := ""
			if rel := s.Cams[cam]; rel != "" {
				p = filepath.Join(root, rel)
			}
			paths = append(paths, p)
		}
		l, err := mp4.Concat(paths)
		if err != nil {
			t.Fatal(err)
		}
		r := l.Open()
		want := sha256.New()
		io.Copy(want, r)
		r.Close()
		matches, _ := filepath.Glob(filepath.Join(mnt, "*", "*", cam+".mp4"))
		found := false
		for _, m := range matches {
			data, _ := os.ReadFile(m)
			got := sha256.Sum256(data)
			if bytes.Equal(got[:], want.Sum(nil)) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s of %s: no file on the drive equals the long video", cam, g.ID)
		}
	}
	if out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", matchFirst(mnt)).CombinedOutput(); err == nil {
		t.Logf("ffprobe duration of a file on the drive: %s", out)
	}
}

func matchFirst(mnt string) string {
	m, _ := filepath.Glob(filepath.Join(mnt, "*", "*", "front.mp4"))
	if len(m) == 0 {
		return ""
	}
	return m[0]
}
