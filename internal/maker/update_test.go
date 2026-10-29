package maker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func elf(machine byte) []byte {
	h := make([]byte, 64)
	copy(h, "\x7fELF")
	h[18] = machine
	return h
}

func TestUpdatePiBoot(t *testing.T) {
	dir := t.TempDir()
	tb := filepath.Join(dir, "teslausb")
	os.MkdirAll(filepath.Join(tb, "debs"), 0o755)
	// A Zero W card from before Squirrel: folder teslausb, program teslausbd.
	os.WriteFile(filepath.Join(tb, "teslausbd"), elf(0x28), 0o644)
	os.WriteFile(filepath.Join(tb, "teslausbd.service"), []byte("old unit"), 0o644)
	os.WriteFile(filepath.Join(tb, "firstboot.sh"), []byte("old"), 0o644)
	os.WriteFile(filepath.Join(tb, "done"), nil, 0o644)
	os.WriteFile(filepath.Join(tb, "cam-size"), []byte("40%\n"), 0o644)
	os.WriteFile(filepath.Join(tb, "debs", "xfsprogs_6.12_armhf.deb"), []byte("old deb"), 0o644)
	os.WriteFile(filepath.Join(tb, "debs", "libinih1_59-1_armhf.deb"), []byte("same deb"), 0o644)

	info, err := inspectPiBoot(dir)
	if err != nil || info.Board != "rpizerow" || info.Version != "" || info.Pending {
		t.Fatalf("before: %+v %v", info, err)
	}
	all := fstest.MapFS{
		"files/squirreld":                          {Data: elf(0xB7)},
		"files-armv6/squirreld":                    {Data: append(elf(0x28), "new"...)},
		"files-armv6/firstboot.sh":                 {Data: []byte("new")},
		"files-armv6/version":                      {Data: []byte("0.9.0\n")},
		"files-armv6/debs/xfsprogs_6.13_armhf.deb": {Data: []byte("new deb")},
		"files-armv6/debs/libinih1_59-1_armhf.deb": {Data: []byte("same deb")},
	}
	if err := updatePiBoot(dir, all); err != nil {
		t.Fatal(err)
	}
	info, err = inspectPiBoot(dir)
	if err != nil || info.Version != "0.9.0" || !info.Pending || info.Board != "rpizerow" {
		t.Errorf("after: %+v %v", info, err)
	}
	for _, old := range []string{"teslausbd", "teslausbd.service"} {
		if _, err := os.Stat(filepath.Join(tb, old)); err == nil {
			t.Errorf("%s left behind", old)
		}
	}
	if _, err := os.Stat(filepath.Join(tb, "squirreld")); err != nil {
		t.Error("squirreld not in the card's folder")
	}
	if b, _ := os.ReadFile(filepath.Join(tb, "firstboot.sh")); string(b) != "new" {
		t.Errorf("firstboot.sh = %q", b)
	}
	if _, err := os.Stat(filepath.Join(tb, "debs", "xfsprogs_6.12_armhf.deb")); err == nil {
		t.Error("old package left behind")
	}
	if b, _ := os.ReadFile(filepath.Join(tb, "cam-size")); string(b) != "40%\n" {
		t.Error("settings file touched")
	}
	// Not a Squirrel card.
	if _, err := inspectPiBoot(t.TempDir()); err == nil {
		t.Error("empty boot partition accepted")
	}
}

// Updates a card made from the real Armbian image, through an open file as
// on a real card. Opt-in like TestCustomizeRealArmbian.
func TestUpdateRealArmbian(t *testing.T) {
	src := os.Getenv("ARMBIAN_IMG")
	if src == "" {
		t.Skip("set ARMBIAN_IMG to the uncompressed Armbian image")
	}
	payload := os.DirFS("../makerui/payload")
	img := filepath.Join(t.TempDir(), "a.img")
	if out, err := exec.Command("cp", src, img).CombinedOutput(); err != nil {
		t.Fatalf("copy: %v %s", err, out)
	}
	s := testSettings()
	s.Board = "rock4cplus"
	ctx := context.Background()
	if err := CustomizeArmbian(ctx, img, s, payload, func(string, float64) {}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(img, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// As after a first start: done, and a package that is no longer bundled.
	r, err := openRoot(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{armbianDir + "/done", armbianDir + "/debs/old_1.0_arm64.deb"} {
		if err := r.fs.WriteFile(p, nil, 0o644, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.fs.Flush(); err != nil {
		t.Fatal(err)
	}
	if r, err = openRoot(f); err != nil {
		t.Fatal(err)
	}
	if info, err := inspectArmbian(r); err != nil || info.Pending {
		t.Fatalf("before: %+v %v", info, err)
	}
	if err := updateArmbian(r, payload, func(string, float64) {}); err != nil {
		t.Fatal(err)
	}
	fsckRoot(t, img)
	if r, err = openRoot(f); err != nil {
		t.Fatal(err)
	}
	info, err := inspectArmbian(r)
	if err != nil || !info.Pending {
		t.Errorf("after: %+v %v", info, err)
	}
	if r.exists(armbianDir + "/debs/old_1.0_arm64.deb") {
		t.Error("old package left behind")
	}
	if !r.exists(armbianDir + "/setup.env") {
		t.Error("settings removed")
	}
}
