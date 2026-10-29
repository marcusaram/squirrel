package gadget

import (
	"os"
	"path/filepath"
	"testing"
)

// configfs creates and removes attribute files itself; a plain directory
// is close enough to check what we write and that a matching gadget is
// left alone.
func TestEnsure(t *testing.T) {
	dir := t.TempDir()
	cfs := filepath.Join(dir, "configfs")
	udc := filepath.Join(dir, "udc")
	os.MkdirAll(filepath.Join(cfs, "usb_gadget"), 0o755)
	os.MkdirAll(filepath.Join(udc, "fe980000.usb"), 0o755)
	os.WriteFile(filepath.Join(udc, "fe980000.usb", "state"), []byte("configured\n"), 0o644)
	cam := filepath.Join(dir, "cam_disk.bin")
	os.WriteFile(cam, nil, 0o644)

	g := &Gadget{ConfigFS: cfs, SysUDC: udc, MaxPower: 200, Luns: []Lun{{Name: "cam", Image: cam}}}
	if err := g.Ensure(); err != nil {
		t.Fatal(err)
	}
	r := g.root()
	if got := read(filepath.Join(r, "functions", function, "lun.0", "file")); got != cam {
		t.Errorf("lun.0/file = %q", got)
	}
	if got := read(filepath.Join(r, "UDC")); got != "fe980000.usb" {
		t.Errorf("UDC = %q", got)
	}
	if s := g.Status(); !s.Bound || s.State != "configured" {
		t.Errorf("status = %+v", s)
	}

	// A second Ensure must not rebind: mark the UDC file and check it
	// survives.
	os.WriteFile(filepath.Join(r, "UDC"), []byte("fe980000.usb\n"), 0o644)
	stamp, _ := os.Stat(filepath.Join(r, "UDC"))
	if err := g.Ensure(); err != nil {
		t.Fatal(err)
	}
	again, _ := os.Stat(filepath.Join(r, "UDC"))
	if !again.ModTime().Equal(stamp.ModTime()) {
		t.Error("Ensure rewrote the UDC of a matching gadget")
	}

	if err := g.Unbind(); err != nil {
		t.Fatal(err)
	}
	if g.Status().Bound {
		t.Error("still bound after Unbind")
	}
}

// A replaced image file must be handed to the drive again; the kernel
// keeps the old one open otherwise.
func TestReopen(t *testing.T) {
	dir := t.TempDir()
	cam := filepath.Join(dir, "cam_disk.bin")
	g := &Gadget{ConfigFS: dir, Luns: []Lun{{Name: "cam", Image: cam}}}
	lun := filepath.Join(g.root(), "functions", function, "lun.0")
	os.MkdirAll(lun, 0o755)
	os.WriteFile(filepath.Join(lun, "file"), []byte("/old\n"), 0o644)
	if err := g.Reopen(); err != nil {
		t.Fatal(err)
	}
	if got := read(filepath.Join(lun, "file")); got != cam {
		t.Errorf("lun.0/file = %q", got)
	}
	if got := read(filepath.Join(lun, "forced_eject")); got != "1" {
		t.Errorf("forced_eject = %q", got)
	}
}

// The original Zero W must not get the Zero 2 W's power budget.
func TestMaxPowerModels(t *testing.T) {
	for model, want := range map[string]int{
		"Raspberry Pi Zero W Rev 1.1":    100,
		"Raspberry Pi Zero 2 W Rev 1.0":  200,
		"Raspberry Pi 4 Model B Rev 1.5": 500,
		"Radxa ROCK 4C+":                 500,
	} {
		if got := maxPowerFor(model); got != want {
			t.Errorf("%q: %d mA, want %d", model, got, want)
		}
	}
}

// A gadget under the earlier name (teslausb's, or ours before Squirrel)
// is removed, so it no longer holds the drive image open.
func TestLegacyGadget(t *testing.T) {
	dir := t.TempDir()
	cfs := filepath.Join(dir, "configfs")
	udc := filepath.Join(dir, "udc")
	os.MkdirAll(filepath.Join(udc, "fe980000.usb"), 0o755)
	old := filepath.Join(cfs, "usb_gadget", "teslausb")
	os.MkdirAll(old, 0o755)
	os.WriteFile(filepath.Join(old, "UDC"), []byte("fe980000.usb\n"), 0o644)
	cam := filepath.Join(dir, "cam_disk.bin")
	os.WriteFile(cam, nil, 0o644)
	g := &Gadget{ConfigFS: cfs, SysUDC: udc, MaxPower: 200, Luns: []Lun{{Name: "cam", Image: cam}}}
	if err := g.Ensure(); err != nil {
		t.Fatal(err)
	}
	// configfs would remove the directory; a plain one keeps its files.
	if got := read(filepath.Join(old, "UDC")); got != "" {
		t.Errorf("old gadget still bound to %q", got)
	}
	if _, err := os.Stat(g.root()); err != nil {
		t.Error("new gadget missing")
	}
}
