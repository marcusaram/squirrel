package maker

import (
	"context"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Writes to an attached, empty disk image standing in for an SD card.
// Needs SQUIRREL_MAKER_ALLOW_DISK_IMAGES=1; no root is needed for images.
func TestWriteCardToDiskImage(t *testing.T) {
	if !AllowDiskImages {
		t.Skip("set SQUIRREL_MAKER_ALLOW_DISK_IMAGES=1")
	}
	dir := t.TempDir()
	card := filepath.Join(dir, "card.img")
	if err := os.Truncate(card, 0); err != nil {
		os.WriteFile(card, nil, 0o644)
	}
	os.Truncate(card, 8<<30)
	out, err := exec.Command("hdiutil", "attach", "-imagekey", "diskimage-class=CRawDiskImage", "-nomount", card).Output()
	if err != nil {
		t.Fatal(err)
	}
	dev := strings.Fields(string(out))[0]
	id := strings.TrimPrefix(dev, "/dev/")
	defer exec.Command("hdiutil", "detach", "-force", dev).Run()

	ctx := context.Background()
	disks, err := ListDisks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range disks {
		found = found || d.ID == id
		if d.ID == "disk0" || d.ID == "disk1" || d.ID == "disk3" {
			t.Errorf("internal disk listed: %+v", d)
		}
	}
	if !found {
		t.Fatalf("%s not listed in %+v", id, disks)
	}
	if _, err := checkDisk(ctx, "disk0"); err == nil {
		t.Fatal("disk0 accepted as a card")
	}
	for _, d := range disks {
		if d.Protocol != "Disk Image" && d.Protocol != "Secure Digital" && d.Protocol != "USB" {
			t.Errorf("unexpected kind of disk listed: %+v", d)
		}
	}

	// An image whose size is not a multiple of the 4 MiB chunk or of 512.
	img := filepath.Join(dir, "src.img")
	data := make([]byte, 9<<20+1000)
	rand.Read(data)
	os.WriteFile(img, data, 0o644)
	if err := WriteCard(ctx, img, id, func(string, float64) {}); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(data))
	f, _ := os.Open(card)
	f.ReadAt(got, 0)
	f.Close()
	if string(got) != string(data) {
		t.Error("card content differs from the image")
	}
}

// TBX_LIST_DISKS=1 prints the cards this Mac offers; it only reads.
func TestListDisksHere(t *testing.T) {
	if os.Getenv("TBX_LIST_DISKS") != "1" {
		t.Skip("set TBX_LIST_DISKS=1")
	}
	disks, err := ListDisks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range disks {
		t.Logf("%s %q %d GB %s", d.ID, d.Name, d.Size>>30, d.Protocol)
		if d.ID == "disk0" {
			t.Error("internal SSD listed")
		}
	}
}
