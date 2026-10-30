package ext4

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The tests make a file system the way Armbian's is made (mkfs.ext4 with
// metadata_csum, a checksum seed, 64bit, 4 KB blocks), change it, and let
// e2fsck judge the result; as root they also mount it and compare what
// Linux reads. They need e2fsprogs and run in `make test-linux`.

func need(t *testing.T, tools ...string) {
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s", tool)
		}
	}
}

func sh(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

// mkfs makes an image of size MB from the files in src.
func mkfs(t *testing.T, src string, mb int) string {
	img := filepath.Join(t.TempDir(), "root.img")
	sh(t, "mkfs.ext4", "-q", "-F", "-b", "4096", "-L", "test",
		"-O", "metadata_csum,metadata_csum_seed,64bit,^orphan_file", "-d", src, img, fmt.Sprintf("%dM", mb))
	return img
}

// fsck runs e2fsck without changing anything; any finding fails the test.
func fsck(t *testing.T, img string) {
	t.Helper()
	out, err := exec.Command("e2fsck", "-fn", img).CombinedOutput()
	if err != nil {
		t.Fatalf("e2fsck found problems: %v\n%s", err, out)
	}
}

func openImage(t *testing.T, img string) (*FS, *os.File) {
	t.Helper()
	dev, err := os.OpenFile(img, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	f, err := Open(dev, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Clean(); err != nil {
		t.Fatal(err)
	}
	return f, dev
}

func TestChanges(t *testing.T) {
	need(t, "mkfs.ext4", "e2fsck")
	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, "etc/systemd/system/multi-user.target.wants"), 0o755)
	os.MkdirAll(filepath.Join(src, "boot"), 0o755)
	os.MkdirAll(filepath.Join(src, "root"), 0o700)
	os.WriteFile(filepath.Join(src, "etc/shadow"), []byte("root:$y$old:19000:0:99999:7:::\n"), 0o640)
	os.WriteFile(filepath.Join(src, "root/.not_logged_in_yet"), []byte("1\n"), 0o644)
	os.Symlink("/lib/systemd/system/armbian-resize-filesystem.service",
		filepath.Join(src, "etc/systemd/system/multi-user.target.wants/armbian-resize-filesystem.service"))
	// A directory too large for one block, which e2fsck -D indexes.
	big := filepath.Join(src, "usr/share/many")
	os.MkdirAll(big, 0o755)
	for i := 0; i < 400; i++ {
		os.WriteFile(filepath.Join(big, fmt.Sprintf("file-with-a-longish-name-%04d", i)), []byte{byte(i)}, 0o644)
	}
	img := mkfs(t, src, 96)
	sh(t, "e2fsck", "-fyD", img) // index the large directory
	fsck(t, img)

	f, dev := openImage(t, img)
	if in, err := f.lookup("/usr/share/many"); err != nil || in.flags()&inodeIndexFl == 0 {
		t.Fatalf("the large directory is not indexed: %v", err)
	}
	large := bytes.Repeat([]byte("0123456789abcdef"), 10<<20/16+3) // 10 MB and a bit
	want := map[string][]byte{
		"/boot/squirrel/squirreld":                       large,
		"/boot/squirrel/debs/xfsprogs_6.13.0_arm64.deb":  bytes.Repeat([]byte{7}, 1<<20),
		"/boot/squirrel/setup.env":                       []byte("TUSB_HOSTNAME='squirrel'\n"),
		"/boot/squirrel/empty":                           {},
		"/etc/systemd/system/squirrel-firstboot.service": []byte("[Unit]\nDescription=first start\n"),
		"/etc/shadow":                                    []byte("root:!$y$old:19000:0:99999:7:::\nother:x:1:2::::\n"),
		"/root/.no_rootfs_resize":                        {},
		"/usr/share/many/a-new-one":                      []byte("in an indexed directory"),
	}
	for _, d := range []string{"/boot/squirrel", "/boot/squirrel/debs"} {
		if err := f.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for p, data := range want {
		mode, gid := os.FileMode(0o644), uint32(0)
		if p == "/etc/shadow" {
			mode, gid = 0o640, 42
		}
		if err := f.WriteFile(p, data, mode, 0, gid); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
	for _, p := range []string{"/root/.not_logged_in_yet", "/etc/systemd/system/multi-user.target.wants/armbian-resize-filesystem.service"} {
		if err := f.Remove(p); err != nil {
			t.Fatalf("removing %s: %v", p, err)
		}
	}
	if err := f.Remove("/root/not-there"); err == nil {
		t.Error("removing a missing file succeeded")
	}
	wants := "/etc/systemd/system/multi-user.target.wants/squirrel-firstboot.service"
	if err := f.Symlink("/etc/systemd/system/squirrel-firstboot.service", wants); err != nil {
		t.Fatal(err)
	}
	long := "/" + strings.Repeat("a-long-target/", 6)
	if err := f.Symlink(long, "/boot/squirrel/long-link"); err != nil {
		t.Fatal(err)
	}
	// Replacing a file again, with something smaller.
	want["/boot/squirrel/setup.env"] = []byte("x")
	if err := f.WriteFile("/boot/squirrel/setup.env", want["/boot/squirrel/setup.env"], 0o600, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := f.Flush(); err != nil {
		t.Fatal(err)
	}
	dev.Close()
	fsck(t, img)

	// Read back by a fresh open.
	g, dev2 := openImage(t, img)
	defer dev2.Close()
	for p, data := range want {
		got, err := g.ReadFile(p)
		if err != nil || !bytes.Equal(got, data) {
			t.Errorf("%s: %d bytes, want %d: %v", p, len(got), len(data), err)
		}
	}
	if st, err := g.Stat("/etc/shadow"); err != nil || st.GID != 42 || st.Mode.Perm() != 0o640 {
		t.Errorf("shadow: %+v %v", st, err)
	}
	for _, p := range []string{"/root/.not_logged_in_yet", "/etc/systemd/system/multi-user.target.wants/armbian-resize-filesystem.service"} {
		if _, err := g.Stat(p); err == nil {
			t.Errorf("%s is still there", p)
		}
	}
	if got, _ := g.ReadFile(wants); string(got) != "/etc/systemd/system/squirrel-firstboot.service" {
		t.Errorf("symlink reads %q", got)
	}
	if many, _ := g.ReadDir("/usr/share/many"); len(many) != 401 {
		t.Errorf("indexed directory has %d entries", len(many))
	}

	// What Linux itself reads.
	if os.Getuid() != 0 {
		return
	}
	mnt := t.TempDir()
	sh(t, "mount", "-o", "loop,ro", img, mnt)
	defer exec.Command("umount", mnt).Run()
	for p, data := range want {
		got, err := os.ReadFile(filepath.Join(mnt, p))
		if err != nil || !bytes.Equal(got, data) {
			t.Errorf("Linux reads %s: %d bytes, want %d: %v", p, len(got), len(data), err)
		}
	}
	if target, err := os.Readlink(filepath.Join(mnt, "boot/squirrel/long-link")); err != nil || target != long {
		t.Errorf("Linux reads the long link as %q: %v", target, err)
	}
	if many, err := os.ReadDir(filepath.Join(mnt, "usr/share/many")); err != nil || len(many) != 401 {
		t.Errorf("Linux lists %d entries: %v", len(many), err)
	}
}

// A file system with a journal to replay, or one that is not clean, is
// left alone.
func TestRefusesUnclean(t *testing.T) {
	need(t, "mkfs.ext4", "debugfs")
	img := mkfs(t, t.TempDir(), 16)
	sh(t, "debugfs", "-w", "-R", "ssv state 0", img)
	dev, _ := os.Open(img)
	defer dev.Close()
	f, err := Open(dev, 0)
	if err != nil {
		t.Fatal(err)
	}
	if f.Clean() == nil {
		t.Error("a file system that is not clean counts as clean")
	}
}

// A feature this package does not know is refused rather than ignored.
func TestRefusesUnknownFeatures(t *testing.T) {
	need(t, "mkfs.ext4")
	img := filepath.Join(t.TempDir(), "x.img")
	sh(t, "mkfs.ext4", "-q", "-F", "-b", "4096", "-O", "inline_data,^orphan_file", img, "16M")
	dev, _ := os.Open(img)
	defer dev.Close()
	if _, err := Open(dev, 0); err == nil {
		t.Error("inline_data accepted")
	}
}
