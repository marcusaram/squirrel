package fatfs

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Changes a FAT32 volume made by mkfs.fat, with files Linux put there
// first, then lets fsck.fat judge it and, as root, Linux read it back.
// Needs dosfstools; runs in `make test-linux`.
func TestFAT32Write(t *testing.T) {
	for _, tool := range []string{"mkfs.fat", "fsck.fat"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s", tool)
		}
	}
	sh := func(name string, args ...string) {
		t.Helper()
		if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
		}
	}
	img := filepath.Join(t.TempDir(), "boot.img")
	sh("truncate", "-s", "128M", img)
	sh("mkfs.fat", "-F", "32", "-n", "BOOTFS", img)
	root := os.Getuid() == 0
	if root {
		// What Raspberry Pi OS has there before the maker comes.
		mnt := t.TempDir()
		sh("mount", "-o", "loop", img, mnt)
		os.WriteFile(filepath.Join(mnt, "cmdline.txt"), []byte("console=serial0,115200 root=PARTUUID=x rootwait quiet init=/usr/lib/raspberrypi-sys-mods/firstboot\n"), 0o644)
		os.WriteFile(filepath.Join(mnt, "config.txt"), []byte("[all]\ndtoverlay=vc4-kms-v3d\n"), 0o644)
		os.MkdirAll(filepath.Join(mnt, "overlays"), 0o755)
		for i := 0; i < 150; i++ {
			os.WriteFile(filepath.Join(mnt, "overlays", fmt.Sprintf("overlay-number-%03d.dtbo", i)), []byte{byte(i)}, 0o644)
		}
		sh("umount", mnt)
	}

	f, err := os.OpenFile(img, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := f.Stat()
	v, err := OpenRW(f, fi.Size())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	want := map[string][]byte{
		"cmdline.txt":                         []byte("console=serial0,115200 root=PARTUUID=x rootwait quiet cfg80211.ieee80211_regdom=NL\n"),
		"config.txt":                          bytes.Repeat([]byte("[all]\ndtoverlay=dwc2\n"), 400),
		"user-data":                           []byte("#cloud-config\nhostname: squirrel\n"),
		"network-config":                      []byte("network:\n  version: 2\n"),
		"ssh":                                 {},
		"squirrel/squirreld":                  bytes.Repeat([]byte("squirreld!"), 900_000),
		"squirrel/squirrel-firstboot.service": []byte("[Unit]\n"),
		"squirrel/debs/xfsprogs_6.13.0-2+deb13u1_arm64.deb": bytes.Repeat([]byte{1, 2, 3}, 350_000),
		"squirrel/debs/libinih1_59-1_arm64.deb":             bytes.Repeat([]byte{4}, 5000),
	}
	for i := 0; i < 120; i++ {
		want[fmt.Sprintf("many/a file with spaces and a long name %03d.txt", i)] = []byte(fmt.Sprint(i))
	}
	for _, d := range []string{"squirrel", "squirrel/debs", "many"} {
		if err := v.Mkdir(d, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := v.Mkdir("squirrel", now); err != nil {
		t.Errorf("making an existing directory: %v", err)
	}
	for p, data := range want {
		if err := v.WriteFile(p, data, now); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
	// Replacing again, with something smaller.
	want["squirrel/squirreld"] = []byte("small now")
	if err := v.WriteFile("squirrel/squirreld", want["squirrel/squirreld"], now); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if out, err := exec.Command("fsck.fat", "-n", "-V", img).CombinedOutput(); err != nil {
		t.Fatalf("fsck.fat found problems: %v\n%s", err, out)
	}

	f, _ = os.Open(img)
	defer f.Close()
	r, err := Open(f, fi.Size())
	if err != nil {
		t.Fatal(err)
	}
	for p, data := range want {
		e, err := r.Lookup(p)
		if err != nil {
			t.Errorf("%s: %v", p, err)
			continue
		}
		fr, _ := r.OpenFile(e)
		got, _ := io.ReadAll(fr.NewReader())
		if !bytes.Equal(got, data) {
			t.Errorf("%s: %d bytes, want %d", p, len(got), len(data))
		}
	}
	if !root {
		return
	}
	mnt := t.TempDir()
	sh("mount", "-o", "loop,ro", img, mnt)
	defer exec.Command("umount", mnt).Run()
	for p, data := range want {
		got, err := os.ReadFile(filepath.Join(mnt, p))
		if err != nil || !bytes.Equal(got, data) {
			t.Errorf("Linux reads %s: %d bytes, want %d: %v", p, len(got), len(data), err)
		}
	}
	if list, _ := os.ReadDir(filepath.Join(mnt, "overlays")); len(list) != 150 {
		t.Errorf("Linux lists %d overlays", len(list))
	}
}
