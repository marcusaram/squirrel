package sysrw

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadOnly(t *testing.T) {
	p := filepath.Join(t.TempDir(), "mounts")
	os.WriteFile(p, []byte(`/dev/mmcblk0p2 / ext4 ro,noatime 0 0
/dev/mmcblk0p1 /boot/firmware vfat rw,relatime,fmask=0022 0 0
/dev/mmcblk0p3 /backingfiles xfs rw,noatime 0 0
tmpfs /var/log tmpfs rw,nosuid,nodev,size=32768k 0 0
`), 0o644)
	for mp, want := range map[string]bool{"/": true, "/boot/firmware": false, "/backingfiles": false, "/nope": false} {
		if got := ReadOnly(p, mp); got != want {
			t.Errorf("%s: %v, want %v", mp, got, want)
		}
	}
	// Remounted read-write later: the last entry counts.
	os.WriteFile(p, []byte("/dev/x / ext4 ro 0 0\n/dev/x / ext4 rw 0 0\n"), 0o644)
	if ReadOnly(p, "/") {
		t.Error("later rw entry ignored")
	}
}

// Needs root; runs in `make test-linux`.
func TestWritableRemounts(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := exec.LookPath("mkfs.ext4"); err != nil {
		t.Skip("no mkfs.ext4")
	}
	dir := t.TempDir()
	img := filepath.Join(dir, "root.img")
	os.WriteFile(img, nil, 0o644)
	os.Truncate(img, 32<<20)
	if out, err := exec.Command("mkfs.ext4", "-q", img).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	mnt := filepath.Join(dir, "mnt")
	os.Mkdir(mnt, 0o755)
	// Like the Pi's root: a writable device, mounted read-only.
	out, err := exec.Command("losetup", "--find", "--show", img).CombinedOutput()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	dev := strings.TrimSpace(string(out))
	defer exec.Command("losetup", "-d", dev).Run()
	if out, err := exec.Command("mount", "-o", "ro", dev, mnt).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	defer exec.Command("umount", mnt).Run()

	if err := os.WriteFile(filepath.Join(mnt, "x"), nil, 0o644); err == nil {
		t.Fatal("setup: mount is not read-only")
	}
	err = Writable(mnt, func() error { return os.WriteFile(filepath.Join(mnt, "wifi"), []byte("ok"), 0o644) })
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(mnt, "wifi")); string(b) != "ok" {
		t.Error("write inside Writable did not land")
	}
	if !ReadOnly("/proc/self/mounts", mnt) {
		t.Error("not read-only again afterwards")
	}
}
