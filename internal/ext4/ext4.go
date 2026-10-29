// Package ext4 reads and changes an ext4 file system in an image or on a
// card without mounting it: just what Squirrel Maker needs to put its
// files on an Armbian root (write, replace and remove files, make
// directories and symlinks), for the features that root has.
//
// It reads and writes whole blocks only, which raw disks require, and
// keeps every change in memory until Flush, so an error halfway leaves
// the file system as it was. It refuses a file system it does not fully
// understand (an unknown feature) or that is not clean (a journal to
// replay, errors, orphans), rather than guess.
package ext4

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"
)

// Device is what the file system lives on: an image file or a card.
type Device interface {
	io.ReaderAt
	io.WriterAt
}

var (
	ErrNotExist    = fs.ErrNotExist
	ErrUnsupported = errors.New("ext4: unsupported file system")
	ErrCorrupt     = errors.New("ext4: corrupt file system")
	ErrNoSpace     = errors.New("ext4: no space left")
)

const (
	compatHasJournal  = 0x4
	compatExtAttr     = 0x8
	compatResizeInode = 0x10
	compatDirIndex    = 0x20

	incompatFiletype = 0x2
	incompatRecover  = 0x4
	incompatExtents  = 0x40
	incompat64bit    = 0x80
	incompatFlexBG   = 0x200
	incompatCsumSeed = 0x2000

	roSparseSuper  = 0x1
	roLargeFile    = 0x2
	roHugeFile     = 0x8
	roDirNlink     = 0x20
	roExtraIsize   = 0x40
	roMetadataCsum = 0x400

	knownCompat   = compatHasJournal | compatExtAttr | compatResizeInode | compatDirIndex
	knownIncompat = incompatFiletype | incompatExtents | incompat64bit | incompatFlexBG | incompatCsumSeed
	knownRO       = roSparseSuper | roLargeFile | roHugeFile | roDirNlink | roExtraIsize | roMetadataCsum

	stateValid  = 0x1
	stateErrors = 0x2

	bgInodeUninit = 0x1
	bgBlockUninit = 0x2

	inodeIndexFl   = 0x1000
	inodeExtentsFl = 0x80000
	inodeHugeFl    = 0x40000

	extentMagic = 0xF30A
	rootIno     = 2

	modeDir     = 0o040000
	modeRegular = 0o100000
	modeSymlink = 0o120000
	modeType    = 0o170000
)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// crc is crc32c as the kernel computes it: no inversion before or after.
func crc(seed uint32, b []byte) uint32 { return ^crc32.Update(^seed, castagnoli, b) }

func le16(b []byte) uint16     { return binary.LittleEndian.Uint16(b) }
func le32(b []byte) uint32     { return binary.LittleEndian.Uint32(b) }
func put16(b []byte, v uint16) { binary.LittleEndian.PutUint16(b, v) }
func put32(b []byte, v uint32) { binary.LittleEndian.PutUint32(b, v) }

// FS is an opened ext4 file system.
type FS struct {
	dev    Device
	offset int64 // where the file system starts on dev

	bs        int64 // block size
	blocks    uint64
	firstData uint64
	perGroup  uint32 // blocks per group
	inodesPer uint32
	inodeSize int
	descSize  int
	groups    uint32
	firstIno  uint32
	csum      bool   // metadata_csum
	csumSeed  uint32 // for every checksum but the superblock's
	wantExtra uint16 // i_extra_isize for new inodes
	sbBlock   uint64 // the block that holds the superblock
	sbOff     int    // where in that block
	gdtBlock  uint64 // first block of the group descriptors
	cache     map[uint64][]byte
	dirty     map[uint64]bool
	touched   map[uint32]bool // groups whose counts or bitmaps changed
	now       uint32          // the time stamped on what changes
}

// Open reads the superblock at offset on dev and checks that the file
// system is one this package can change safely.
func Open(dev Device, offset int64) (*FS, error) {
	f := &FS{dev: dev, offset: offset, bs: 1024, cache: map[uint64][]byte{}, dirty: map[uint64]bool{},
		now: uint32(time.Now().Unix())}
	// The superblock is 1024 bytes in; read the 4096 bytes around it
	// (whole sectors) before the block size is known.
	head := make([]byte, 4096)
	if _, err := dev.ReadAt(head, offset); err != nil {
		return nil, fmt.Errorf("ext4: reading the superblock: %w", err)
	}
	sb := head[1024:2048]
	if le16(sb[0x38:]) != 0xEF53 {
		return nil, fmt.Errorf("%w: no ext4 superblock", ErrUnsupported)
	}
	logBS := le32(sb[0x18:])
	if logBS > 6 {
		return nil, fmt.Errorf("%w: block size", ErrCorrupt)
	}
	f.bs = 1024 << logBS
	compat, incompat, ro := le32(sb[0x5C:]), le32(sb[0x60:]), le32(sb[0x64:])
	switch {
	case compat&^knownCompat != 0 || incompat&^knownIncompat != 0 || ro&^knownRO != 0:
		return nil, fmt.Errorf("%w: features %#x/%#x/%#x beyond %#x/%#x/%#x", ErrUnsupported,
			compat, incompat, ro, knownCompat, knownIncompat, knownRO)
	case incompat&incompatFiletype == 0 || incompat&incompatExtents == 0:
		return nil, fmt.Errorf("%w: needs filetype and extents", ErrUnsupported)
	case le32(sb[0x1C:]) != logBS:
		return nil, fmt.Errorf("%w: bigalloc", ErrUnsupported)
	}
	f.firstData = uint64(le32(sb[0x14:]))
	f.perGroup = le32(sb[0x20:])
	f.inodesPer = le32(sb[0x28:])
	f.inodeSize = int(le16(sb[0x58:]))
	f.firstIno = le32(sb[0x54:])
	f.blocks = uint64(le32(sb[0x4:]))
	f.descSize = 32
	if incompat&incompat64bit != 0 {
		f.blocks |= uint64(le32(sb[0x150:])) << 32
		f.descSize = int(le16(sb[0xFE:]))
	}
	if f.perGroup == 0 || f.inodesPer == 0 || f.inodeSize < 128 || f.descSize < 32 || f.inodesPer%8 != 0 {
		return nil, fmt.Errorf("%w: geometry", ErrCorrupt)
	}
	f.groups = uint32((f.blocks - f.firstData + uint64(f.perGroup) - 1) / uint64(f.perGroup))
	f.csum = ro&roMetadataCsum != 0
	if f.csum {
		if sb[0x175] != 1 {
			return nil, fmt.Errorf("%w: checksum type %d", ErrUnsupported, sb[0x175])
		}
		if crc(^uint32(0), sb[:0x3FC]) != le32(sb[0x3FC:]) {
			return nil, fmt.Errorf("%w: superblock checksum", ErrCorrupt)
		}
		if incompat&incompatCsumSeed != 0 {
			f.csumSeed = le32(sb[0x270:])
		} else {
			f.csumSeed = crc(^uint32(0), sb[0x68:0x78])
		}
	}
	f.wantExtra = 32
	if ro&roExtraIsize != 0 {
		if w := le16(sb[0x15E:]); w >= 4 {
			f.wantExtra = w
		}
	}
	if int(f.wantExtra)+128 > f.inodeSize {
		f.wantExtra = uint16(f.inodeSize - 128)
	}
	// The superblock is in block 0 (4 KB blocks) or block 1 (1 KB).
	f.sbBlock = uint64(1024 / f.bs)
	f.sbOff = int(1024 % f.bs)
	f.gdtBlock = f.firstData + 1
	return f, nil
}

// Clean reports why the file system must not be changed now, or nil: it
// needs fsck, has a journal to replay, or orphans to finish.
func (f *FS) Clean() error {
	sb, err := f.super()
	if err != nil {
		return err
	}
	state := le16(sb[0x3A:])
	switch {
	case state&stateValid == 0 || state&stateErrors != 0:
		return errors.New("ext4: the file system was not unmounted cleanly or has errors")
	case le32(sb[0x60:])&incompatRecover != 0:
		return errors.New("ext4: the journal has changes to replay")
	case le32(sb[0xE8:]) != 0:
		return errors.New("ext4: deleted files are still to be cleaned up")
	}
	return nil
}

// block returns block n, read once and kept for changes.
func (f *FS) block(n uint64) ([]byte, error) {
	if b, ok := f.cache[n]; ok {
		return b, nil
	}
	if n >= f.blocks {
		return nil, fmt.Errorf("%w: block %d beyond the end", ErrCorrupt, n)
	}
	b := make([]byte, f.bs)
	if _, err := f.dev.ReadAt(b, f.offset+int64(n)*f.bs); err != nil {
		return nil, err
	}
	f.cache[n] = b
	return b, nil
}

func (f *FS) markDirty(n uint64) { f.dirty[n] = true }

func (f *FS) super() ([]byte, error) {
	b, err := f.block(f.sbBlock)
	if err != nil {
		return nil, err
	}
	return b[f.sbOff : f.sbOff+1024], nil
}

// desc returns group g's descriptor and the block it lives in.
func (f *FS) desc(g uint32) ([]byte, uint64, error) {
	if g >= f.groups {
		return nil, 0, fmt.Errorf("%w: group %d", ErrCorrupt, g)
	}
	off := uint64(g) * uint64(f.descSize)
	n := f.gdtBlock + off/uint64(f.bs)
	b, err := f.block(n)
	if err != nil {
		return nil, 0, err
	}
	o := off % uint64(f.bs)
	return b[o : o+uint64(f.descSize)], n, nil
}

func (f *FS) hi(d []byte, lo, hi int, wide bool) uint64 {
	v := uint64(le32(d[lo:]))
	if wide && f.descSize >= 64 {
		v |= uint64(le32(d[hi:])) << 32
	}
	return v
}

func (f *FS) descBlockBitmap(d []byte) uint64 { return f.hi(d, 0x0, 0x20, true) }
func (f *FS) descInodeBitmap(d []byte) uint64 { return f.hi(d, 0x4, 0x24, true) }
func (f *FS) descInodeTable(d []byte) uint64  { return f.hi(d, 0x8, 0x28, true) }

func (f *FS) count16(d []byte, lo, hi int) uint32 {
	v := uint32(le16(d[lo:]))
	if f.descSize >= 64 {
		v |= uint32(le16(d[hi:])) << 16
	}
	return v
}

func (f *FS) setCount16(d []byte, lo, hi int, v uint32) {
	put16(d[lo:], uint16(v))
	if f.descSize >= 64 {
		put16(d[hi:], uint16(v>>16))
	}
}

// inode is a copy of an inode's bytes with its number; save writes it
// back.
type inode struct {
	ino uint32
	b   []byte
}

func (f *FS) inodeLoc(ino uint32) (uint64, int, error) {
	if ino == 0 || ino > f.inodesPer*f.groups {
		return 0, 0, fmt.Errorf("%w: inode %d", ErrCorrupt, ino)
	}
	g, i := (ino-1)/f.inodesPer, (ino-1)%f.inodesPer
	d, _, err := f.desc(g)
	if err != nil {
		return 0, 0, err
	}
	off := uint64(i) * uint64(f.inodeSize)
	return f.descInodeTable(d) + off/uint64(f.bs), int(off % uint64(f.bs)), nil
}

func (f *FS) inode(ino uint32) (*inode, error) {
	n, o, err := f.inodeLoc(ino)
	if err != nil {
		return nil, err
	}
	b, err := f.block(n)
	if err != nil {
		return nil, err
	}
	return &inode{ino: ino, b: append([]byte(nil), b[o:o+f.inodeSize]...)}, nil
}

func (in *inode) mode() uint16  { return le16(in.b[0x0:]) }
func (in *inode) size() uint64  { return uint64(le32(in.b[0x4:])) | uint64(le32(in.b[0x6C:]))<<32 }
func (in *inode) flags() uint32 { return le32(in.b[0x20:]) }
func (in *inode) links() uint16 { return le16(in.b[0x1A:]) }
func (in *inode) gen() uint32   { return le32(in.b[0x64:]) }
func (in *inode) uid() uint32 {
	return uint32(le16(in.b[0x2:])) | uint32(le16(in.b[0x78:]))<<16
}
func (in *inode) gid() uint32 {
	return uint32(le16(in.b[0x18:])) | uint32(le16(in.b[0x7A:]))<<16
}

// inodeSeed is the seed of the checksums of an inode and what belongs to
// it (directory blocks, extent blocks).
func (f *FS) inodeSeed(in *inode) uint32 {
	var b [4]byte
	put32(b[:], in.ino)
	s := crc(f.csumSeed, b[:])
	put32(b[:], in.gen())
	return crc(s, b[:])
}

// extent is a run of blocks of a file: logical start, physical start,
// length; unwritten runs read as zeros.
type extent struct {
	logical   uint32
	physical  uint64
	length    uint32
	unwritten bool
}

// extents lists a file's runs and the blocks its extent tree uses.
func (f *FS) extents(in *inode) (runs []extent, tree []uint64, err error) {
	if in.flags()&inodeExtentsFl == 0 {
		return nil, nil, fmt.Errorf("%w: inode %d does not use extents", ErrUnsupported, in.ino)
	}
	var walk func(node []byte, depth int) error
	walk = func(node []byte, depth int) error {
		if le16(node[0:]) != extentMagic || depth > 5 {
			return fmt.Errorf("%w: extent tree of inode %d", ErrCorrupt, in.ino)
		}
		n, d := int(le16(node[2:])), int(le16(node[6:]))
		if 12+12*n > len(node) {
			return fmt.Errorf("%w: extent tree of inode %d", ErrCorrupt, in.ino)
		}
		for i := 0; i < n; i++ {
			e := node[12+12*i:]
			if d == 0 {
				l := uint32(le16(e[4:]))
				x := extent{logical: le32(e[0:]), physical: uint64(le16(e[6:]))<<32 | uint64(le32(e[8:])), length: l}
				if l > 32768 {
					x.length, x.unwritten = l-32768, true
				}
				runs = append(runs, x)
				continue
			}
			child := uint64(le16(e[8:]))<<32 | uint64(le32(e[4:]))
			b, err := f.block(child)
			if err != nil {
				return err
			}
			tree = append(tree, child)
			if err := walk(b, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return runs, tree, walk(in.b[0x28:0x28+60], 0)
}

// read returns a file's content.
func (f *FS) read(in *inode) ([]byte, error) {
	size := in.size()
	if size > 1<<30 {
		return nil, fmt.Errorf("ext4: inode %d is too large to read", in.ino)
	}
	out := make([]byte, size)
	if in.mode()&modeType == modeSymlink && size < 60 && in.flags()&inodeExtentsFl == 0 {
		copy(out, in.b[0x28:])
		return out, nil
	}
	runs, _, err := f.extents(in)
	if err != nil {
		return nil, err
	}
	for _, r := range runs {
		if r.unwritten {
			continue
		}
		for i := uint32(0); i < r.length; i++ {
			at := (uint64(r.logical) + uint64(i)) * uint64(f.bs)
			if at >= size {
				break
			}
			b, err := f.block(r.physical + uint64(i))
			if err != nil {
				return nil, err
			}
			copy(out[at:], b)
		}
	}
	return out, nil
}

// DirEntry is one name in a directory.
type DirEntry struct {
	Name  string
	Inode uint32
	Type  byte // 1 file, 2 directory, 7 symlink
}

func (e DirEntry) IsDir() bool { return e.Type == 2 }

// dirBlocks returns a directory's blocks in order.
func (f *FS) dirBlocks(in *inode) ([]uint64, error) {
	runs, _, err := f.extents(in)
	if err != nil {
		return nil, err
	}
	n := int(in.size() / uint64(f.bs))
	out := make([]uint64, n)
	for _, r := range runs {
		for i := uint32(0); i < r.length; i++ {
			if l := int(r.logical + i); l < n {
				out[l] = r.physical + uint64(i)
			}
		}
	}
	for _, b := range out {
		if b == 0 {
			return nil, fmt.Errorf("%w: hole in directory %d", ErrCorrupt, in.ino)
		}
	}
	return out, nil
}

// entries walks the entries of one directory block; fn gets each entry's
// offset, record length and the entry itself (inode 0 for unused space).
func (f *FS) entries(b []byte, fn func(off, recLen int, e DirEntry) bool) error {
	for off := 0; off < len(b); {
		if off+8 > len(b) {
			return fmt.Errorf("%w: directory entry", ErrCorrupt)
		}
		ino, recLen, nameLen := le32(b[off:]), int(le16(b[off+4:])), int(b[off+6])
		if recLen < 8 || recLen%4 != 0 || off+recLen > len(b) || 8+nameLen > recLen {
			return fmt.Errorf("%w: directory entry", ErrCorrupt)
		}
		e := DirEntry{Inode: ino, Type: b[off+7], Name: string(b[off+8 : off+8+nameLen])}
		if !fn(off, recLen, e) {
			return nil
		}
		off += recLen
	}
	return nil
}

func isTail(e DirEntry, recLen int) bool {
	return e.Inode == 0 && recLen == 12 && e.Type == 0xDE && e.Name == ""
}

func (f *FS) readDir(in *inode) ([]DirEntry, error) {
	if in.mode()&modeType != modeDir {
		return nil, fmt.Errorf("ext4: inode %d is not a directory", in.ino)
	}
	blocks, err := f.dirBlocks(in)
	if err != nil {
		return nil, err
	}
	var out []DirEntry
	for _, n := range blocks {
		b, err := f.block(n)
		if err != nil {
			return nil, err
		}
		err = f.entries(b, func(_, recLen int, e DirEntry) bool {
			if e.Inode != 0 && e.Name != "." && e.Name != ".." && !isTail(e, recLen) {
				out = append(out, e)
			}
			return true
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// lookup follows an absolute path; symlinks are not followed.
func (f *FS) lookup(p string) (*inode, error) {
	p = path.Clean("/" + p)
	in, err := f.inode(rootIno)
	if err != nil {
		return nil, err
	}
	if p == "/" {
		return in, nil
	}
	for _, part := range strings.Split(p[1:], "/") {
		list, err := f.readDir(in)
		if err != nil {
			return nil, err
		}
		found := uint32(0)
		for _, e := range list {
			if e.Name == part {
				found = e.Inode
				break
			}
		}
		if found == 0 {
			return nil, fmt.Errorf("%s: %w", p, ErrNotExist)
		}
		if in, err = f.inode(found); err != nil {
			return nil, err
		}
	}
	return in, nil
}

// ReadFile returns the content of a file (or a symlink's target).
func (f *FS) ReadFile(p string) ([]byte, error) {
	in, err := f.lookup(p)
	if err != nil {
		return nil, err
	}
	if t := in.mode() & modeType; t != modeRegular && t != modeSymlink {
		return nil, fmt.Errorf("ext4: %s is not a file", p)
	}
	return f.read(in)
}

// ReadDir lists a directory, sorted by name.
func (f *FS) ReadDir(p string) ([]DirEntry, error) {
	in, err := f.lookup(p)
	if err != nil {
		return nil, err
	}
	return f.readDir(in)
}

// Info is what Stat tells about a path.
type Info struct {
	Mode     fs.FileMode
	UID, GID uint32
	Size     int64
	Links    int
}

// Stat describes a path without following a symlink at its end.
func (f *FS) Stat(p string) (Info, error) {
	in, err := f.lookup(p)
	if err != nil {
		return Info{}, err
	}
	m := fs.FileMode(in.mode() & 0o7777)
	switch in.mode() & modeType {
	case modeDir:
		m |= fs.ModeDir
	case modeSymlink:
		m |= fs.ModeSymlink
	}
	return Info{Mode: m, UID: in.uid(), GID: in.gid(), Size: int64(in.size()), Links: int(in.links())}, nil
}
