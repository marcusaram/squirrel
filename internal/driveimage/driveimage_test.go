package driveimage

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/marcusaram/squirrel/internal/fatfs"
)

// Needs root, loop devices and mkfs.exfat; runs in `make test-linux`.
func TestCreateHasTeslaCam(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := exec.LookPath("mkfs.exfat"); err != nil {
		t.Skip("no mkfs.exfat")
	}
	dir := t.TempDir()
	img := filepath.Join(dir, "cam_disk.bin")
	if err := Create(img, 256<<20, "exfat", "CAM", io.Discard); err != nil {
		t.Fatal(err)
	}
	if ok, err := HasTeslaCam(img); err != nil || !ok {
		t.Fatalf("new drive has no TeslaCam folder: %v %v", ok, err)
	}
	if err := Create(img, 256<<20, "exfat", "CAM", io.Discard); err == nil {
		t.Error("overwrote an existing image")
	}
	// The diagnostics report goes in the root, next to TeslaCam.
	if err := WriteFile(img, "squirrel-log.txt", []byte("hallo\r\n")); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(img)
	defer f.Close()
	fi, _ := f.Stat()
	v, err := fatfs.Open(f, fi.Size())
	if err != nil {
		t.Fatal(err)
	}
	e, err := v.Lookup("squirrel-log.txt")
	if err != nil || e.Size != 7 {
		t.Errorf("report on the drive: %+v %v", e, err)
	}
}

// A drive made before the folder was added, like the first cards.
func TestEnsureTeslaCamOnEmptyDrive(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := exec.LookPath("mkfs.exfat"); err != nil {
		t.Skip("no mkfs.exfat")
	}
	img := filepath.Join(t.TempDir(), "cam_disk.bin")
	if err := Create(img, 256<<20, "exfat", "CAM", io.Discard); err != nil {
		t.Fatal(err)
	}
	// Remove the folder again to get the old situation.
	dev, err := loop(img, 256<<20)
	if err != nil {
		t.Fatal(err)
	}
	mnt := t.TempDir()
	exec.Command("mount", dev, mnt).Run()
	os.Remove(filepath.Join(mnt, "TeslaCam"))
	exec.Command("umount", mnt).Run()
	exec.Command("losetup", "-d", dev).Run()
	if ok, _ := HasTeslaCam(img); ok {
		t.Fatal("setup: folder still there")
	}

	if err := EnsureTeslaCam(img); err != nil {
		t.Fatal(err)
	}
	if ok, err := HasTeslaCam(img); err != nil || !ok {
		t.Fatalf("folder not added: %v %v", ok, err)
	}
	if err := EnsureTeslaCam(img); err != nil {
		t.Errorf("second call: %v", err)
	}
}
