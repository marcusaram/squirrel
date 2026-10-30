package maker

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

// A virtual disk as the card: listed, written, made a Raspberry Pi card,
// inspected and updated, on Windows itself. Opt-in, as administrator
// (GitHub's Windows runners are): SQUIRREL_TEST_VHD=C:\path\card.vhdx.
func TestWindowsCard(t *testing.T) {
	vhd := os.Getenv("SQUIRREL_TEST_VHD")
	if vhd == "" {
		t.Skip("set SQUIRREL_TEST_VHD to a path for a virtual disk")
	}
	if !elevated() {
		t.Skip("needs administrator rights")
	}
	AllowDiskImages = true
	defer func() { AllowDiskImages = false }()
	ctx := context.Background()

	diskpart := func(script string) {
		t.Helper()
		f := filepath.Join(t.TempDir(), "script.txt")
		os.WriteFile(f, []byte(script), 0o644)
		if out, err := exec.Command("diskpart", "/s", f).CombinedOutput(); err != nil {
			t.Fatalf("diskpart: %v\n%s", err, out)
		}
	}
	os.Remove(vhd)
	diskpart(fmt.Sprintf("create vdisk file=\"%s\" maximum=8400 type=expandable\nattach vdisk\n", vhd))
	defer diskpart(fmt.Sprintf("select vdisk file=\"%s\"\ndetach vdisk\n", vhd))
	out, err := powershell(ctx, fmt.Sprintf(`(Get-DiskImage -ImagePath '%s' | Get-Disk).Number`, vhd))
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("disk number %q", out)
	}
	id := fmt.Sprintf("PhysicalDrive%d", n)

	disks, err := ListDisks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range disks {
		found = found || d.ID == id
	}
	if !found {
		t.Fatalf("%s not among the cards: %+v", id, disks)
	}

	// Writing an image: WriteCard reads it back and compares.
	img := filepath.Join(t.TempDir(), "card.img")
	data := make([]byte, 64<<20)
	rand.New(rand.NewSource(1)).Read(data)
	os.WriteFile(img, data, 0o644)
	if err := WriteCard(ctx, img, id, func(string, float64) {}); err != nil {
		t.Fatal(err)
	}

	// A Raspberry Pi card from before: a FAT32 boot partition with
	// Squirrel's folder, started once.
	if _, err := powershell(ctx, fmt.Sprintf(`$d = Get-Disk -Number %[1]d
		if ($d.PartitionStyle -ne 'RAW') { Clear-Disk -Number %[1]d -RemoveData -RemoveOEM -Confirm:$false }
		Initialize-Disk -Number %[1]d -PartitionStyle MBR
		New-Partition -DiskNumber %[1]d -Size 512MB -MbrType FAT32 -AssignDriveLetter |
			Format-Volume -FileSystem FAT32 -NewFileSystemLabel bootfs -Confirm:$false | Out-Null`, n)); err != nil {
		t.Fatal(err)
	}
	parts, err := partitions(ctx, n)
	if err != nil || len(parts) == 0 || parts[0].Letter == "" {
		t.Fatalf("partitions: %+v %v", parts, err)
	}
	sq := parts[0].Letter + `:\squirrel`
	os.MkdirAll(sq, 0o755)
	arm64 := []byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 0, 0xB7, 0}
	os.WriteFile(filepath.Join(sq, "squirreld"), arm64, 0o644)
	os.WriteFile(filepath.Join(sq, "firstboot.sh"), []byte("old"), 0o644)
	os.WriteFile(filepath.Join(sq, "version"), []byte("0.1.0\n"), 0o644)
	os.WriteFile(filepath.Join(sq, "done"), nil, 0o644)

	info, err := InspectCard(ctx, id)
	if err != nil || info.Board != "rpi" || info.Version != "0.1.0" || info.Pending {
		t.Fatalf("inspect: %+v %v", info, err)
	}
	payload := fstest.MapFS{
		"files/squirreld":    {Data: append(arm64, []byte("new")...)},
		"files/firstboot.sh": {Data: []byte("new")},
		"files/version":      {Data: []byte("9.9.9\n")},
	}
	info, err = UpdateCard(ctx, id, payload, func(string, float64) {})
	if err != nil || info.Version != "9.9.9" || !info.Pending {
		t.Fatalf("update: %+v %v", info, err)
	}
	if got, _ := os.ReadFile(filepath.Join(sq, "firstboot.sh")); string(got) != "new" {
		t.Errorf("firstboot.sh: %q", got)
	}
}
