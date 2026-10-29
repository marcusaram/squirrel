package storage

import (
	"errors"
	"testing"
)

const gib = 1 << 30 / sectorSize

func noFS(string) bool { return false }

// A card as squirrel-maker writes it, after Raspberry Pi OS's first boot
// skipped growing root: root ends at about 5 GB, the empty data partition
// starts at 8 GiB, the card has 32 GiB.
var makerCard = `{"partitiontable":{"label":"dos","id":"0x1234abcd","device":"/dev/mmcblk0","unit":"sectors","sectorsize":512,
 "partitions":[{"node":"/dev/mmcblk0p1","start":16384,"size":1048576,"type":"c"},
 {"node":"/dev/mmcblk0p2","start":1064960,"size":10485760,"type":"83"},
 {"node":"/dev/mmcblk0p3","start":16777216,"size":2048,"type":"83"}]}}`

func TestMakerCard(t *testing.T) {
	tbl, err := ParseSfdisk([]byte(makerCard))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Decide(tbl, 32*gib, "/dev/mmcblk0p2", 0, 8*gib, noFS)
	if err != nil {
		t.Fatal(err)
	}
	if p.RootSize != 16777216-1064960 {
		t.Errorf("root size = %d", p.RootSize)
	}
	if p.DataNew || p.Data.Node != "/dev/mmcblk0p3" || p.Data.Start != 16777216 || p.Data.Size != 32*gib-16777216 {
		t.Errorf("data = %+v new=%v", p.Data, p.DataNew)
	}
	t.Log("\n" + p.String())

	// The same card where the "empty" partition turns out to hold data.
	if _, err := Decide(tbl, 32*gib, "/dev/mmcblk0p2", 0, 8*gib, func(string) bool { return true }); err == nil {
		t.Error("formatted a partition that holds a filesystem")
	}
	// A card too small for the layout.
	if _, err := Decide(tbl, 9*gib, "/dev/mmcblk0p2", 0, 8*gib, noFS); !errors.Is(err, ErrNoSpace) {
		t.Errorf("small card: %v", err)
	}
}

func TestFreeSpaceAfterRoot(t *testing.T) {
	tbl, _ := ParseSfdisk([]byte(`{"partitiontable":{"label":"dos","device":"/dev/mmcblk0","sectorsize":512,"partitions":[
	 {"node":"/dev/mmcblk0p1","start":8192,"size":524288,"type":"c"},
	 {"node":"/dev/mmcblk0p2","start":532480,"size":8000001,"type":"83"}]}}`))
	p, err := Decide(tbl, 64*gib, "/dev/mmcblk0p2", 0, 8*gib, noFS)
	if err != nil {
		t.Fatal(err)
	}
	if !p.DataNew || p.RootSize != 0 || p.Data.Start%align != 0 || p.Data.Start < 532480+8000001 || p.Data.Start+p.Data.Size != 64*gib {
		t.Errorf("plan = %+v", p)
	}
}

// The real layout of raspios-trixie-arm64-lite 2026-10-06 on a 32 GB card
// when the resize flag is removed: root stays 2.5 GB.
func TestMakerImageGrowsRoot(t *testing.T) {
	tbl, _ := ParseSfdisk([]byte(`{"partitiontable":{"label":"dos","device":"/dev/mmcblk0","sectorsize":512,"partitions":[
	 {"node":"/dev/mmcblk0p1","start":16384,"size":1048576,"type":"c"},
	 {"node":"/dev/mmcblk0p2","start":1064960,"size":4947968,"type":"83"}]}}`))
	p, err := Decide(tbl, 60751872, "/dev/mmcblk0p2", 8*gib, 8*gib, noFS)
	if err != nil {
		t.Fatal(err)
	}
	if p.RootSize != 8*gib || p.Data.Start != 1064960+8*gib || p.Data.Start+p.Data.Size != 60751872 || !p.DataNew {
		t.Errorf("plan = %+v", p)
	}
	// On a card too small for both, root keeps its size and data still fits.
	p, err = Decide(tbl, 1064960+4947968+9*gib, "/dev/mmcblk0p2", 8*gib, 8*gib, noFS)
	if err != nil || p.RootSize != 0 {
		t.Errorf("small card: plan=%+v err=%v", p, err)
	}
}

func TestStockCardRefused(t *testing.T) {
	// Root grown to the end of the card by Raspberry Pi OS's first boot.
	tbl, _ := ParseSfdisk([]byte(`{"partitiontable":{"label":"dos","device":"/dev/mmcblk0","sectorsize":512,"partitions":[
	 {"node":"/dev/mmcblk0p1","start":16384,"size":1048576,"type":"c"},
	 {"node":"/dev/mmcblk0p2","start":1064960,"size":66043904,"type":"83"}]}}`))
	if _, err := Decide(tbl, 32*gib, "/dev/mmcblk0p2", 0, 8*gib, noFS); !errors.Is(err, ErrNoSpace) {
		t.Errorf("err = %v", err)
	}
}

func TestNumber(t *testing.T) {
	for node, want := range map[string]int{"/dev/mmcblk0p3": 3, "/dev/sda2": 2, "/dev/loop12p1": 1, "/dev/nvme0n1p10": 10} {
		if n, err := Number(node); err != nil || n != want {
			t.Errorf("%s: %d %v", node, n, err)
		}
	}
}
