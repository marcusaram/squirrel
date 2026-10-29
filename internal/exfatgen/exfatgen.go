// Package exfatgen presents a tree of files as a read-only exFAT disk,
// without writing it anywhere: the boot region, FAT, allocation bitmap and
// directories are generated, and file data is read from the files' own
// sources whenever that part of the disk is read.
//
// squirreld uses it for computer mode: plugged into a computer, the Pi is a
// USB drive with each camera of each drive or event as one long video.
package exfatgen

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf16"
)

// File is a file on the disk. ReadAt is called for its data only while the
// disk is read; it must return exactly Size bytes over the file.
type File struct {
	Name   string
	Size   int64
	Mod    time.Time
	ReadAt func(p []byte, off int64) (int, error)
}

type Dir struct {
	Name  string
	Mod   time.Time
	Dirs  []*Dir
	Files []*File
}

const (
	sectorSize   = 512
	clusterShift = 8 // sectors per cluster: 256, so 128 KiB clusters
	clusterSize  = sectorSize << clusterShift
	partStart    = 2048 // sectors; the volume sits in an MBR partition at 1 MiB
	fatOffset    = 2048 // sectors into the volume
	entrySize    = 32
)

// Volume is a generated disk image.
type Volume struct {
	size       int64 // whole disk, bytes
	mbr        []byte
	boot       []byte // main boot region, 12 sectors
	fatLen     int64  // sectors
	heap       int64  // cluster heap offset in the volume, sectors
	clusters   uint32 // cluster count
	chains     []extent
	upcase     []byte
	dirData    map[uint32][]byte // directory contents by first cluster
	bitmapFrom uint32
}

// extent is a run of clusters holding one thing.
type extent struct {
	first, n uint32
	dir      []byte // directory or table contents
	file     *File
	bitmap   bool
}

// Build lays out root as a disk with the given volume label (up to 11
// characters).
func Build(root *Dir, label string) (*Volume, error) {
	v := &Volume{dirData: map[uint32][]byte{}}
	v.upcase = upcaseTable()

	// Every directory's size depends only on its entries' names.
	type laid struct {
		d     *Dir
		bytes int64
		first uint32
	}
	var dirs []*laid
	var walk func(d *Dir, isRoot bool) error
	walk = func(d *Dir, isRoot bool) error {
		n := int64(0)
		if isRoot {
			n = 3 // label, bitmap, up-case table
		}
		seen := map[string]bool{}
		for _, name := range names(d) {
			if err := checkName(name); err != nil {
				return err
			}
			key := strings.ToUpper(name)
			if seen[key] {
				return fmt.Errorf("exfatgen: %q twice in %q", name, d.Name)
			}
			seen[key] = true
			n += 2 + int64(len(utf16.Encode([]rune(name)))+14)/15
		}
		dirs = append(dirs, &laid{d: d, bytes: n * entrySize})
		for _, c := range d.Dirs {
			if err := walk(c, false); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, true); err != nil {
		return nil, err
	}

	clustersOf := func(bytes int64) uint32 {
		return uint32(max(1, (bytes+clusterSize-1)/clusterSize))
	}
	// Up-case table, then directories, then files; the bitmap goes last
	// because its size depends on the total.
	next := uint32(2)
	upFirst := next
	next += clustersOf(int64(len(v.upcase)))
	for _, l := range dirs {
		l.first = next
		next += clustersOf(l.bytes)
	}
	var files []*File
	var fileFirst []uint32
	for _, l := range dirs {
		for _, f := range l.d.Files {
			if f.Size < 0 {
				return nil, fmt.Errorf("exfatgen: %q has negative size", f.Name)
			}
			files = append(files, f)
			fileFirst = append(fileFirst, next)
			if f.Size > 0 {
				next += clustersOf(f.Size)
			}
		}
	}
	used := next - 2
	bm := uint32(1)
	for {
		need := clustersOf(int64((used + bm + 7) / 8))
		if need == bm {
			break
		}
		bm = need
	}
	v.bitmapFrom = next
	v.clusters = used + bm
	if uint64(v.clusters) > 0xFFFFFFF5 {
		return nil, errors.New("exfatgen: too large")
	}
	bitmapBytes := int64((v.clusters + 7) / 8)

	firstOf := map[*Dir]uint32{}
	dirSize := map[*Dir]int64{}
	for _, l := range dirs {
		firstOf[l.d] = l.first
		dirSize[l.d] = int64(clustersOf(l.bytes)) * clusterSize
	}
	fileAt := map[*File]uint32{}
	for i, f := range files {
		fileAt[f] = fileFirst[i]
	}
	for i, l := range dirs {
		var e []byte
		if i == 0 {
			e = append(e, labelEntry(label)...)
			e = append(e, bitmapEntry(v.bitmapFrom, bitmapBytes)...)
			e = append(e, upcaseEntry(upFirst, v.upcase)...)
		}
		for _, c := range l.d.Dirs {
			e = append(e, entrySet(c.Name, 0x10, c.Mod, firstOf[c], dirSize[c], v.upcase)...)
		}
		for _, f := range l.d.Files {
			first := uint32(0)
			if f.Size > 0 {
				first = fileAt[f]
			}
			e = append(e, entrySet(f.Name, 0x01, f.Mod, first, f.Size, v.upcase)...)
		}
		size := int64(clustersOf(l.bytes)) * clusterSize
		buf := make([]byte, size)
		copy(buf, e)
		v.dirData[l.first] = buf
		v.chains = append(v.chains, extent{first: l.first, n: clustersOf(l.bytes), dir: buf})
	}
	v.chains = append(v.chains, extent{first: upFirst, n: clustersOf(int64(len(v.upcase))), dir: v.upcase})
	for i, f := range files {
		if f.Size > 0 {
			v.chains = append(v.chains, extent{first: fileFirst[i], n: clustersOf(f.Size), file: f})
		}
	}
	v.chains = append(v.chains, extent{first: v.bitmapFrom, n: bm, bitmap: true})
	sort.Slice(v.chains, func(i, j int) bool { return v.chains[i].first < v.chains[j].first })

	v.fatLen = (int64(v.clusters+2)*4 + sectorSize - 1) / sectorSize
	cs := int64(1) << clusterShift
	v.heap = (fatOffset + v.fatLen + cs - 1) / cs * cs
	volSectors := v.heap + int64(v.clusters)*cs
	v.size = (partStart + volSectors) * sectorSize
	v.boot = bootRegion(volSectors, v.fatLen, v.heap, v.clusters, dirs[0].first)
	v.mbr = mbr(volSectors)
	return v, nil
}

func names(d *Dir) []string {
	var out []string
	for _, c := range d.Dirs {
		out = append(out, c.Name)
	}
	for _, f := range d.Files {
		out = append(out, f.Name)
	}
	return out
}

func checkName(n string) error {
	if n == "" || n == "." || n == ".." || len(utf16.Encode([]rune(n))) > 255 || strings.ContainsAny(n, "\"*/:<>?\\|") {
		return fmt.Errorf("exfatgen: invalid name %q", n)
	}
	for _, r := range n {
		if r < 0x20 {
			return fmt.Errorf("exfatgen: invalid name %q", n)
		}
	}
	return nil
}

// Size is the size of the whole disk in bytes.
func (v *Volume) Size() int64 { return v.size }
