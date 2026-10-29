// Package storage creates the XFS data partition for /backingfiles on the
// boot disk.
//
// Two layouts are supported, and nothing else is touched:
//
//   - Unallocated space after the root partition, as on a card written by
//     squirrel-maker: that image has the "resize" flag removed from
//     cmdline.txt, so Raspberry Pi OS leaves root at its image size. Root is
//     grown to a target size (online, like Raspberry Pi OS does itself) and
//     a new partition fills the rest.
//   - An empty partition directly after root that someone prepared: root
//     is grown up to it and it is extended to the end of the disk.
//
// Existing partitions are never moved or shrunk, and a partition that holds
// any filesystem signature is never formatted.
package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// MountOptions are /backingfiles' mount options. noatime: reading a clip
// does not write. logbsize=256k: the filesystem's log is written in fewer,
// larger pieces, which suits an SD card. No discard: freed space is
// trimmed in one go now and then (squirreld), not at every delete.
const MountOptions = "noatime,logbsize=256k,nofail"

const (
	Label      = "backingfiles"
	align      = 2048 // sectors, 1 MiB
	sectorSize = 512
)

type Partition struct {
	Node  string `json:"node"`
	Start int64  `json:"start"`
	Size  int64  `json:"size"`
	Type  string `json:"type"`
}

func (p Partition) End() int64 { return p.Start + p.Size } // first sector after

type Table struct {
	Label      string      `json:"label"` // dos or gpt
	ID         string      `json:"id"`
	Device     string      `json:"device"`
	SectorSize int64       `json:"sectorsize"`
	LastLBA    int64       `json:"lastlba"` // gpt only
	Partitions []Partition `json:"partitions"`
}

func ParseSfdisk(data []byte) (Table, error) {
	var w struct {
		T Table `json:"partitiontable"`
	}
	if err := json.Unmarshal(data, &w); err != nil {
		return Table{}, fmt.Errorf("sfdisk output: %w", err)
	}
	if w.T.SectorSize != 0 && w.T.SectorSize != sectorSize {
		return Table{}, fmt.Errorf("unsupported sector size %d", w.T.SectorSize)
	}
	return w.T, nil
}

// Number returns the partition number from a node like /dev/mmcblk0p3.
func Number(node string) (int, error) {
	i := len(node)
	for i > 0 && node[i-1] >= '0' && node[i-1] <= '9' {
		i--
	}
	return strconv.Atoi(node[i:])
}

type Plan struct {
	Disk     string
	Root     Partition
	RootSize int64 // new size in sectors; 0 keeps it
	Data     Partition
	DataNew  bool // the data partition is added, not resized
}

func (p Plan) String() string {
	gb := func(sectors int64) string { return fmt.Sprintf("%.1f GB", float64(sectors*sectorSize)/(1<<30)) }
	var b strings.Builder
	fmt.Fprintf(&b, "disk %s\n", p.Disk)
	if p.RootSize > 0 {
		fmt.Fprintf(&b, "  grow root %s from %s to %s (stays mounted, nothing is moved)\n", p.Root.Node, gb(p.Root.Size), gb(p.RootSize))
	}
	if p.DataNew {
		fmt.Fprintf(&b, "  add partition at %s of %s in unallocated space\n", gb(p.Data.Start), gb(p.Data.Size))
	} else {
		fmt.Fprintf(&b, "  resize empty partition %s to %s, up to the end of the disk\n", p.Data.Node, gb(p.Data.Size))
	}
	fmt.Fprintf(&b, "  format it as XFS with label %q and mount it\n", Label)
	return b.String()
}

var ErrNoSpace = errors.New("no room for a data partition")

// Decide works out what to do. diskEnd is the first sector after the usable
// area; hasFS reports whether a partition holds a filesystem signature.
func Decide(t Table, diskEnd int64, rootNode string, rootTarget, minData int64, hasFS func(node string) bool) (Plan, error) {
	if t.Label == "gpt" && t.LastLBA > 0 {
		diskEnd = t.LastLBA + 1
	}
	plan := Plan{Disk: t.Device}
	ri := -1
	for i, p := range t.Partitions {
		if p.Node == rootNode {
			ri = i
		}
	}
	if ri < 0 {
		return plan, fmt.Errorf("root partition %s not found on %s", rootNode, t.Device)
	}
	root := t.Partitions[ri]
	plan.Root = root
	var after []Partition
	for _, p := range t.Partitions {
		if p.Start > root.Start {
			after = append(after, p)
		}
	}
	switch len(after) {
	case 0:
		end := root.End()
		if want := (rootTarget + align - 1) / align * align; root.Size < want && diskEnd-(root.Start+want) >= minData {
			plan.RootSize = want
			end = root.Start + want
		}
		start := (end + align - 1) / align * align
		if diskEnd-start < minData {
			return plan, fmt.Errorf("%w: root %s fills the disk. Write the card with squirrel-maker, or use a card with free space after the root partition", ErrNoSpace, root.Node)
		}
		if t.Label == "dos" && len(t.Partitions) >= 4 {
			return plan, errors.New("the MBR already has four partitions")
		}
		plan.Data = Partition{Start: start, Size: diskEnd - start, Type: "83"}
		plan.DataNew = true
	case 1:
		d := after[0]
		if hasFS(d.Node) {
			return plan, fmt.Errorf("partition %s after root already holds a filesystem; not touching it", d.Node)
		}
		if t.Label == "dos" && d.Type != "83" {
			return plan, fmt.Errorf("partition %s has type %s, expected an empty Linux partition (83)", d.Node, d.Type)
		}
		if diskEnd-d.Start < minData {
			return plan, fmt.Errorf("%w: the disk is too small for the data partition at %s", ErrNoSpace, d.Node)
		}
		if gap := d.Start - root.End(); gap >= align {
			plan.RootSize = d.Start - root.Start
		}
		plan.Data = d
		plan.Data.Size = diskEnd - d.Start
	default:
		return plan, fmt.Errorf("unexpected partitions after root on %s; not touching the disk", t.Device)
	}
	return plan, nil
}
