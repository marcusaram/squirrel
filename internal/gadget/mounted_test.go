package gadget

import (
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Needs root and loop devices; runs in `make test-linux`.
func TestMountedLocally(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := exec.LookPath("losetup"); err != nil {
		t.Skip("no losetup")
	}
	dir := t.TempDir()
	img := filepath.Join(dir, "cam_disk.bin")
	src, err := os.Open("../fatfs/testdata/fat32.img.gz")
	if err != nil {
		t.Fatal(err)
	}
	zr, _ := gzip.NewReader(src)
	out, _ := os.Create(img)
	io.Copy(out, zr)
	out.Close()
	src.Close()

	if mountedLocally(img) {
		t.Fatal("reported mounted before anything happened")
	}
	b, err := exec.Command("losetup", "--find", "--show", "--offset", "1048576", img).CombinedOutput()
	if err != nil {
		t.Fatalf("losetup: %v %s", err, b)
	}
	dev := strings.TrimSpace(string(b))
	defer exec.Command("losetup", "-d", dev).Run()
	if mountedLocally(img) {
		t.Error("a loop device alone is not a mount")
	}
	mnt := filepath.Join(dir, "mnt")
	os.Mkdir(mnt, 0o755)
	if b, err := exec.Command("mount", dev, mnt).CombinedOutput(); err != nil {
		t.Fatalf("mount: %v %s", err, b)
	}
	if !mountedLocally(img) {
		t.Error("mounted image not detected")
	}
	exec.Command("umount", mnt).Run()
	if mountedLocally(img) {
		t.Error("still reported mounted after umount")
	}
}
