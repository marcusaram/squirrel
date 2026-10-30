package maker

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/marcusaram/squirrel/internal/fatfs"
)

// Set TBX_PIOS_IMG to an uncompressed Raspberry Pi OS image to run this;
// the image is copied, not changed.
func TestCustomizeRealImage(t *testing.T) {
	src := os.Getenv("TBX_PIOS_IMG")
	if src == "" {
		t.Skip("set TBX_PIOS_IMG")
	}
	img := filepath.Join(t.TempDir(), "card.img")
	in, _ := os.Open(src)
	out, _ := os.Create(img)
	io.Copy(out, in)
	in.Close()
	out.Close()

	payload := fstest.MapFS{
		"squirreld":    {Data: []byte("fake daemon")},
		"firstboot.sh": {Data: []byte("#!/bin/bash\n")},
		"debs/x.deb":   {Data: []byte("deb")},
	}
	var steps []string
	err := Customize(context.Background(), img, testSettings(), payload, func(s string, f float64) { steps = append(steps, s) })
	if err != nil {
		t.Fatal(err)
	}
	cmd, _ := ReadBootFile(img, "cmdline.txt")
	if strings.Contains(string(cmd), "resize") || !strings.Contains(string(cmd), "cfg80211.ieee80211_regdom=NL") {
		t.Errorf("cmdline.txt = %q", cmd)
	}
	cfg, _ := ReadBootFile(img, "config.txt")
	if !strings.Contains(string(cfg), "dtoverlay=dwc2") {
		t.Error("config.txt lacks dwc2")
	}
	if b, err := ReadBootFile(img, "squirrel/debs/x.deb"); err != nil || string(b) != "deb" {
		t.Errorf("payload: %q %v", b, err)
	}
	if _, err := ReadBootFile(img, "ssh"); err != nil {
		t.Errorf("ssh marker: %v", err)
	}
	if err := CheckLayout(img); err != nil {
		t.Errorf("partition table changed: %v", err)
	}
	f, _ := os.Open(img)
	defer f.Close()
	fi, _ := f.Stat()
	v, err := fatfs.Open(f, fi.Size())
	if err != nil {
		t.Fatal(err)
	}
	v.Walk(v.Root(), func(e fatfs.Entry, err error) error {
		if strings.HasPrefix(e.Name, "._") || e.Name == ".fseventsd" || e.Name == ".Spotlight-V100" {
			t.Errorf("macOS leftover on the boot partition: %s", e.Path)
		}
		return nil
	})
	// fsck.fat, where it is installed, judges the boot partition.
	if _, err := exec.LookPath("fsck.fat"); err == nil {
		part := filepath.Join(t.TempDir(), "boot.img")
		if out, err := exec.Command("dd", "if="+img, "of="+part, fmt.Sprintf("bs=%d", 512), fmt.Sprintf("skip=%d", v.Offset/512),
			fmt.Sprintf("count=%d", 1024*1024)).CombinedOutput(); err != nil {
			t.Fatalf("dd: %v %s", err, out)
		}
		if out, err := exec.Command("fsck.fat", "-n", part).CombinedOutput(); err != nil {
			t.Errorf("fsck.fat: %v\n%s", err, out)
		}
	}
}
