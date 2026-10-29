package ext4

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io/fs"
	"path"
)

// Changes: files are written whole, into blocks allocated in one go;
// every changed block stays in memory and Flush writes them, after the
// checksums of the groups and the superblock are brought up to date.

func (f *FS) tailSize() int {
	if f.csum {
		return 12
	}
	return 0
}

func (f *FS) touch(g uint32) {
	if f.touched == nil {
		f.touched = map[uint32]bool{}
	}
	f.touched[g] = true
}

// sbAdd adds d to the superblock's free block (or inode) count.
func (f *FS) sbAdd(blocks bool, d int64) error {
	sb, err := f.super()
	if err != nil {
		return err
	}
	if blocks {
		v := uint64(le32(sb[0xC:])) | uint64(le32(sb[0x158:]))<<32
		v = uint64(int64(v) + d)
		put32(sb[0xC:], uint32(v))
		if f.descSize >= 64 {
			put32(sb[0x158:], uint32(v>>32))
		}
	} else {
		put32(sb[0x10:], uint32(int64(le32(sb[0x10:]))+d))
	}
	f.markDirty(f.sbBlock)
	return nil
}

func (f *FS) groupStart(g uint32) uint64 { return f.firstData + uint64(g)*uint64(f.perGroup) }

func (f *FS) groupLen(g uint32) uint32 {
	start := f.groupStart(g)
	if start+uint64(f.perGroup) > f.blocks {
		return uint32(f.blocks - start)
	}
	return f.perGroup
}

func bit(b []byte, i uint32) bool { return b[i/8]&(1<<(i%8)) != 0 }
func setBit(b []byte, i uint32, on bool) {
	if on {
		b[i/8] |= 1 << (i % 8)
	} else {
		b[i/8] &^= 1 << (i % 8)
	}
}

// allocBlocks finds n free blocks: one run if it can, else up to max
// runs, in groups whose bitmap is initialized.
func (f *FS) allocBlocks(n uint32, max int) ([]extent, error) {
	if n == 0 {
		return nil, nil
	}
	var free []run
	for g := uint32(0); g < f.groups; g++ {
		d, _, err := f.desc(g)
		if err != nil {
			return nil, err
		}
		if le16(d[0x12:])&bgBlockUninit != 0 || f.count16(d, 0xC, 0x2C) == 0 {
			continue
		}
		bm, err := f.block(f.descBlockBitmap(d))
		if err != nil {
			return nil, err
		}
		l := f.groupLen(g)
		for i := uint32(0); i < l; {
			if bit(bm, i) {
				i++
				continue
			}
			j := i
			for j < l && !bit(bm, j) {
				j++
			}
			free = append(free, run{g, i, j - i})
			if j-i >= n {
				return f.take([]run{{g, i, n}})
			}
			i = j
		}
	}
	// No single run: the largest runs first.
	var picked []run
	need := n
	for len(picked) < max && need > 0 {
		best := -1
		for k, r := range free {
			if r.len > 0 && (best < 0 || r.len > free[best].len) {
				best = k
			}
		}
		if best < 0 {
			break
		}
		r := free[best]
		r.len = min(r.len, need)
		picked = append(picked, r)
		need -= r.len
		free[best].len = 0
	}
	if need > 0 {
		return nil, ErrNoSpace
	}
	return f.take(picked)
}

// run is a stretch of free blocks in a group's bitmap.
type run struct {
	g     uint32
	start uint32
	len   uint32
}

// take marks runs as used.
func (f *FS) take(runs []run) ([]extent, error) {
	var out []extent
	for _, r := range runs {
		d, n, err := f.desc(r.g)
		if err != nil {
			return nil, err
		}
		bmBlock := f.descBlockBitmap(d)
		bm, err := f.block(bmBlock)
		if err != nil {
			return nil, err
		}
		for i := r.start; i < r.start+r.len; i++ {
			setBit(bm, i, true)
		}
		f.markDirty(bmBlock)
		f.setCount16(d, 0xC, 0x2C, f.count16(d, 0xC, 0x2C)-r.len)
		f.markDirty(n)
		f.touch(r.g)
		if err := f.sbAdd(true, -int64(r.len)); err != nil {
			return nil, err
		}
		out = append(out, extent{physical: f.groupStart(r.g) + uint64(r.start), length: r.len})
	}
	return out, nil
}

func (f *FS) freeBlocks(start uint64, length uint32) error {
	for b := start; b < start+uint64(length); b++ {
		g := uint32((b - f.firstData) / uint64(f.perGroup))
		i := uint32((b - f.firstData) % uint64(f.perGroup))
		d, n, err := f.desc(g)
		if err != nil {
			return err
		}
		bmBlock := f.descBlockBitmap(d)
		bm, err := f.block(bmBlock)
		if err != nil {
			return err
		}
		if !bit(bm, i) {
			return fmt.Errorf("%w: block %d freed twice", ErrCorrupt, b)
		}
		setBit(bm, i, false)
		f.markDirty(bmBlock)
		f.setCount16(d, 0xC, 0x2C, f.count16(d, 0xC, 0x2C)+1)
		f.markDirty(n)
		f.touch(g)
		if err := f.sbAdd(true, 1); err != nil {
			return err
		}
	}
	return nil
}

// allocInode takes a free inode, near group pref if it can.
func (f *FS) allocInode(pref uint32, dir bool) (uint32, error) {
	for k := uint32(0); k < f.groups; k++ {
		g := (pref + k) % f.groups
		d, n, err := f.desc(g)
		if err != nil {
			return 0, err
		}
		if le16(d[0x12:])&bgInodeUninit != 0 || f.count16(d, 0xE, 0x2E) == 0 {
			continue
		}
		bmBlock := f.descInodeBitmap(d)
		bm, err := f.block(bmBlock)
		if err != nil {
			return 0, err
		}
		for i := uint32(0); i < f.inodesPer; i++ {
			ino := g*f.inodesPer + i + 1
			if ino < f.firstIno || bit(bm, i) {
				continue
			}
			setBit(bm, i, true)
			f.markDirty(bmBlock)
			f.setCount16(d, 0xE, 0x2E, f.count16(d, 0xE, 0x2E)-1)
			if dir {
				f.setCount16(d, 0x10, 0x30, f.count16(d, 0x10, 0x30)+1)
			}
			// Inodes past the last one in use are not checked; this one is
			// in use now.
			if unused := f.count16(d, 0x1C, 0x32); i >= f.inodesPer-unused {
				f.setCount16(d, 0x1C, 0x32, f.inodesPer-i-1)
			}
			f.markDirty(n)
			f.touch(g)
			return ino, f.sbAdd(false, -1)
		}
	}
	return 0, ErrNoSpace
}

func (f *FS) freeInode(in *inode) error {
	g, i := (in.ino-1)/f.inodesPer, (in.ino-1)%f.inodesPer
	d, n, err := f.desc(g)
	if err != nil {
		return err
	}
	bmBlock := f.descInodeBitmap(d)
	bm, err := f.block(bmBlock)
	if err != nil {
		return err
	}
	setBit(bm, i, false)
	f.markDirty(bmBlock)
	f.setCount16(d, 0xE, 0x2E, f.count16(d, 0xE, 0x2E)+1)
	if in.mode()&modeType == modeDir {
		f.setCount16(d, 0x10, 0x30, f.count16(d, 0x10, 0x30)-1)
	}
	f.markDirty(n)
	f.touch(g)
	put16(in.b[0x1A:], 0)
	put32(in.b[0x14:], f.now) // dtime
	if err := f.save(in); err != nil {
		return err
	}
	return f.sbAdd(false, 1)
}

// save writes an inode back into the inode table, with its checksum.
func (f *FS) save(in *inode) error {
	if f.csum {
		c := append([]byte(nil), in.b...)
		put16(c[0x7C:], 0)
		extra := 0
		if f.inodeSize > 128 {
			extra = int(le16(c[0x80:]))
		}
		if extra >= 4 {
			put16(c[0x82:], 0)
		}
		v := crc(f.inodeSeed(in), c)
		put16(in.b[0x7C:], uint16(v))
		if extra >= 4 {
			put16(in.b[0x82:], uint16(v>>16))
		}
	}
	n, o, err := f.inodeLoc(in.ino)
	if err != nil {
		return err
	}
	b, err := f.block(n)
	if err != nil {
		return err
	}
	copy(b[o:o+f.inodeSize], in.b)
	f.markDirty(n)
	return nil
}

func (f *FS) newInode(ino uint32, mode uint16, uid, gid uint32) *inode {
	in := &inode{ino: ino, b: make([]byte, f.inodeSize)}
	put16(in.b[0x0:], mode)
	put16(in.b[0x2:], uint16(uid))
	put16(in.b[0x78:], uint16(uid>>16))
	put16(in.b[0x18:], uint16(gid))
	put16(in.b[0x7A:], uint16(gid>>16))
	for _, o := range []int{0x8, 0xC, 0x10} {
		put32(in.b[o:], f.now)
	}
	put16(in.b[0x1A:], 1)
	var g [4]byte
	rand.Read(g[:])
	put32(in.b[0x64:], binary.LittleEndian.Uint32(g[:]))
	if f.inodeSize > 128 {
		put16(in.b[0x80:], f.wantExtra)
		if f.wantExtra >= 0x18 {
			put32(in.b[0x90:], f.now) // crtime
		}
	}
	return in
}

func (in *inode) setSize(n uint64) {
	put32(in.b[0x4:], uint32(n))
	put32(in.b[0x6C:], uint32(n>>32))
}

func (f *FS) setBlockCount(in *inode, blocks uint64) {
	sectors := blocks * uint64(f.bs) / 512
	put32(in.b[0x1C:], uint32(sectors))
	put16(in.b[0x74:], uint16(sectors>>32))
	put32(in.b[0x20:], in.flags()&^inodeHugeFl)
}

// freeData gives back a file's blocks, the extent tree's included.
func (f *FS) freeData(in *inode) error {
	if in.flags()&inodeExtentsFl == 0 {
		return nil // a fast symlink: the target is in the inode
	}
	runs, tree, err := f.extents(in)
	if err != nil {
		return err
	}
	for _, r := range runs {
		if err := f.freeBlocks(r.physical, r.length); err != nil {
			return err
		}
	}
	for _, b := range tree {
		if err := f.freeBlocks(b, 1); err != nil {
			return err
		}
	}
	return nil
}

// setExtents puts runs (logical order) into an inode: up to four in the
// inode itself, more in one leaf block.
func (f *FS) setExtents(in *inode, runs []extent) (treeBlocks uint64, err error) {
	ib := in.b[0x28 : 0x28+60]
	clear(ib)
	put32(in.b[0x20:], in.flags()|inodeExtentsFl)
	put16(ib[0:], extentMagic)
	put16(ib[4:], 4)
	putExtent := func(e []byte, r extent) {
		put32(e[0:], r.logical)
		put16(e[4:], uint16(r.length))
		put16(e[6:], uint16(r.physical>>32))
		put32(e[8:], uint32(r.physical))
	}
	for _, r := range runs {
		if r.length > 32768 {
			return 0, fmt.Errorf("ext4: run of %d blocks", r.length)
		}
	}
	if len(runs) <= 4 {
		put16(ib[2:], uint16(len(runs)))
		for i, r := range runs {
			putExtent(ib[12+12*i:], r)
		}
		return 0, nil
	}
	max := (int(f.bs) - 12) / 12
	if len(runs) > max {
		return 0, fmt.Errorf("ext4: %d runs is too fragmented", len(runs))
	}
	leaf, err := f.allocBlocks(1, 1)
	if err != nil {
		return 0, err
	}
	lb, err := f.block(leaf[0].physical)
	if err != nil {
		return 0, err
	}
	clear(lb)
	put16(lb[0:], extentMagic)
	put16(lb[2:], uint16(len(runs)))
	put16(lb[4:], uint16(max))
	for i, r := range runs {
		putExtent(lb[12+12*i:], r)
	}
	if f.csum {
		put32(lb[12+12*max:], crc(f.inodeSeed(in), lb[:12+12*max]))
	}
	f.markDirty(leaf[0].physical)
	put16(ib[2:], 1)
	put16(ib[6:], 1) // depth
	put32(ib[12:], 0)
	put32(ib[16:], uint32(leaf[0].physical))
	put16(ib[20:], uint16(leaf[0].physical>>32))
	return 1, nil
}

// fill allocates blocks for data, writes it and sets the inode's extents,
// size and block count.
func (f *FS) fill(in *inode, data []byte) error {
	n := uint32((int64(len(data)) + f.bs - 1) / f.bs)
	runs, err := f.allocBlocks(n, 64)
	if err != nil {
		return err
	}
	var logical uint32
	for i := range runs {
		runs[i].logical = logical
		for k := uint32(0); k < runs[i].length; k++ {
			b, err := f.block(runs[i].physical + uint64(k))
			if err != nil {
				return err
			}
			clear(b)
			copy(b, data[min(int64(logical+k)*f.bs, int64(len(data))):])
			f.markDirty(runs[i].physical + uint64(k))
		}
		logical += runs[i].length
	}
	tree, err := f.setExtents(in, runs)
	if err != nil {
		return err
	}
	in.setSize(uint64(len(data)))
	f.setBlockCount(in, uint64(n)+tree)
	return nil
}

func (f *FS) group(ino uint32) uint32 { return (ino - 1) / f.inodesPer }

func fileType(mode uint16) byte {
	switch mode & modeType {
	case modeDir:
		return 2
	case modeSymlink:
		return 7
	}
	return 1
}

// dirBlockCsum sets the checksum tail of a directory block.
func (f *FS) dirBlockCsum(dir *inode, b []byte) {
	if !f.csum {
		return
	}
	t := b[len(b)-12:]
	put32(t[0:], 0)
	put16(t[4:], 12)
	t[6], t[7] = 0, 0xDE
	put32(t[8:], crc(f.inodeSeed(dir), b[:len(b)-12]))
}

func recLenFor(name string) int { return (8 + len(name) + 3) &^ 3 }

func putEntry(b []byte, ino uint32, recLen int, name string, typ byte) {
	put32(b[0:], ino)
	put16(b[4:], uint16(recLen))
	b[6], b[7] = byte(len(name)), typ
	copy(b[8:], name)
}

// unindex turns an indexed (htree) directory into a plain one, so an
// entry can go into any block. Its leaf blocks are plain blocks already;
// the root and the index nodes become empty ones.
func (f *FS) unindex(dir *inode) error {
	blocks, err := f.dirBlocks(dir)
	if err != nil {
		return err
	}
	root, err := f.block(blocks[0])
	if err != nil {
		return err
	}
	levels := int(root[30])
	if levels > 2 {
		return fmt.Errorf("%w: directory index of %d levels", ErrUnsupported, levels)
	}
	index := map[uint32]bool{}
	children := func(b []byte, at int) []uint32 {
		count := int(le16(b[at+2:]))
		out := []uint32{le32(b[at+4:])}
		for i := 1; i < count && at+8*i+8 <= len(b); i++ {
			out = append(out, le32(b[at+8*i+4:]))
		}
		return out
	}
	level := children(root, 0x20)
	for l := 0; l < levels; l++ {
		var next []uint32
		for _, c := range level {
			if int(c) >= len(blocks) {
				return fmt.Errorf("%w: directory index", ErrCorrupt)
			}
			index[c] = true
			b, err := f.block(blocks[c])
			if err != nil {
				return err
			}
			next = append(next, children(b, 8)...)
		}
		level = next
	}
	parent := le32(root[12:])
	clear(root)
	putEntry(root, dir.ino, 12, ".", 2)
	putEntry(root[12:], parent, len(root)-12-f.tailSize(), "..", 2)
	f.dirBlockCsum(dir, root)
	f.markDirty(blocks[0])
	for c := range index {
		b, err := f.block(blocks[c])
		if err != nil {
			return err
		}
		clear(b)
		put16(b[4:], uint16(len(b)-f.tailSize()))
		f.dirBlockCsum(dir, b)
		f.markDirty(blocks[c])
	}
	put32(dir.b[0x20:], dir.flags()&^inodeIndexFl)
	return f.save(dir)
}

// dirAdd adds name -> ino to a directory, in a free gap or a new block.
func (f *FS) dirAdd(dir *inode, name string, ino uint32, typ byte) error {
	if len(name) == 0 || len(name) > 255 {
		return fmt.Errorf("ext4: name %q", name)
	}
	if dir.flags()&inodeIndexFl != 0 {
		if err := f.unindex(dir); err != nil {
			return err
		}
	}
	blocks, err := f.dirBlocks(dir)
	if err != nil {
		return err
	}
	need := recLenFor(name)
	for _, n := range blocks {
		b, err := f.block(n)
		if err != nil {
			return err
		}
		done := false
		err = f.entries(b, func(off, recLen int, e DirEntry) bool {
			if isTail(e, recLen) {
				return true
			}
			used := 0
			if e.Inode != 0 {
				used = recLenFor(e.Name)
			}
			if recLen-used < need {
				return true
			}
			if used > 0 {
				put16(b[off+4:], uint16(used))
			}
			putEntry(b[off+used:], ino, recLen-used, name, typ)
			done = true
			return false
		})
		if err != nil {
			return err
		}
		if done {
			f.dirBlockCsum(dir, b)
			f.markDirty(n)
			return nil
		}
	}
	// A new block at the end of the directory.
	runs, tree, err := f.extents(dir)
	if err != nil {
		return err
	}
	nb, err := f.allocBlocks(1, 1)
	if err != nil {
		return err
	}
	for _, t := range tree {
		if err := f.freeBlocks(t, 1); err != nil {
			return err
		}
	}
	nb[0].logical = uint32(len(blocks))
	if last := len(runs) - 1; last >= 0 && runs[last].physical+uint64(runs[last].length) == nb[0].physical &&
		runs[last].logical+runs[last].length == nb[0].logical && !runs[last].unwritten && runs[last].length < 32768 {
		runs[last].length++
	} else {
		runs = append(runs, nb[0])
	}
	b, err := f.block(nb[0].physical)
	if err != nil {
		return err
	}
	clear(b)
	putEntry(b, ino, len(b)-f.tailSize(), name, typ)
	f.dirBlockCsum(dir, b)
	f.markDirty(nb[0].physical)
	t, err := f.setExtents(dir, runs)
	if err != nil {
		return err
	}
	dir.setSize(uint64(len(blocks)+1) * uint64(f.bs))
	f.setBlockCount(dir, uint64(len(blocks)+1)+t)
	return f.save(dir)
}

// dirRemove takes name out of a directory and returns its inode number.
func (f *FS) dirRemove(dir *inode, name string) (uint32, error) {
	if dir.flags()&inodeIndexFl != 0 {
		if err := f.unindex(dir); err != nil {
			return 0, err
		}
	}
	blocks, err := f.dirBlocks(dir)
	if err != nil {
		return 0, err
	}
	for _, n := range blocks {
		b, err := f.block(n)
		if err != nil {
			return 0, err
		}
		var found uint32
		prev := -1
		err = f.entries(b, func(off, recLen int, e DirEntry) bool {
			if e.Inode != 0 && e.Name == name && !isTail(e, recLen) {
				found = e.Inode
				if prev >= 0 {
					put16(b[prev+4:], le16(b[prev+4:])+uint16(recLen))
				} else {
					put32(b[off:], 0)
				}
				return false
			}
			prev = off
			return true
		})
		if err != nil {
			return 0, err
		}
		if found != 0 {
			f.dirBlockCsum(dir, b)
			f.markDirty(n)
			return found, nil
		}
	}
	return 0, ErrNotExist
}

func (f *FS) parent(p string) (*inode, string, error) {
	p = path.Clean("/" + p)
	if p == "/" {
		return nil, "", fmt.Errorf("ext4: cannot change /")
	}
	dir, err := f.lookup(path.Dir(p))
	if err != nil {
		return nil, "", err
	}
	if dir.mode()&modeType != modeDir {
		return nil, "", fmt.Errorf("ext4: %s is not a directory", path.Dir(p))
	}
	return dir, path.Base(p), nil
}

// WriteFile writes a file, replacing one that is there (in place, keeping
// its inode, so its directory does not change).
func (f *FS) WriteFile(p string, data []byte, perm fs.FileMode, uid, gid uint32) error {
	dir, name, err := f.parent(p)
	if err != nil {
		return err
	}
	mode := modeRegular | uint16(perm&0o7777)
	if old, err := f.lookup(p); err == nil {
		if old.mode()&modeType == modeRegular {
			if err := f.freeData(old); err != nil {
				return err
			}
			put16(old.b[0x0:], mode)
			put16(old.b[0x2:], uint16(uid))
			put16(old.b[0x78:], uint16(uid>>16))
			put16(old.b[0x18:], uint16(gid))
			put16(old.b[0x7A:], uint16(gid>>16))
			put32(old.b[0xC:], f.now)
			put32(old.b[0x10:], f.now)
			if err := f.fill(old, data); err != nil {
				return err
			}
			return f.save(old)
		}
		if err := f.Remove(p); err != nil {
			return err
		}
	}
	ino, err := f.allocInode(f.group(dir.ino), false)
	if err != nil {
		return err
	}
	in := f.newInode(ino, mode, uid, gid)
	if err := f.fill(in, data); err != nil {
		return err
	}
	if err := f.save(in); err != nil {
		return err
	}
	return f.dirAdd(dir, name, ino, 1)
}

// Mkdir makes a directory; one that is there already is fine.
func (f *FS) Mkdir(p string, perm fs.FileMode) error {
	if old, err := f.lookup(p); err == nil {
		if old.mode()&modeType == modeDir {
			return nil
		}
		return fmt.Errorf("ext4: %s exists and is not a directory", p)
	}
	dir, name, err := f.parent(p)
	if err != nil {
		return err
	}
	ino, err := f.allocInode(f.group(dir.ino), true)
	if err != nil {
		return err
	}
	in := f.newInode(ino, modeDir|uint16(perm&0o7777), 0, 0)
	put16(in.b[0x1A:], 2)
	blk, err := f.allocBlocks(1, 1)
	if err != nil {
		return err
	}
	b, err := f.block(blk[0].physical)
	if err != nil {
		return err
	}
	clear(b)
	putEntry(b, ino, 12, ".", 2)
	putEntry(b[12:], dir.ino, len(b)-12-f.tailSize(), "..", 2)
	f.dirBlockCsum(in, b)
	f.markDirty(blk[0].physical)
	if _, err := f.setExtents(in, blk); err != nil {
		return err
	}
	in.setSize(uint64(f.bs))
	f.setBlockCount(in, 1)
	if err := f.save(in); err != nil {
		return err
	}
	if err := f.dirAdd(dir, name, ino, 2); err != nil {
		return err
	}
	// The new directory's ".." links to the parent.
	if l := dir.links(); l > 1 && l < 64999 {
		put16(dir.b[0x1A:], l+1)
	}
	put32(dir.b[0xC:], f.now)
	put32(dir.b[0x10:], f.now)
	return f.save(dir)
}

// Symlink makes p a symlink to target; p must not exist.
func (f *FS) Symlink(target, p string) error {
	if _, err := f.lookup(p); err == nil {
		return fmt.Errorf("ext4: %s exists", p)
	}
	dir, name, err := f.parent(p)
	if err != nil {
		return err
	}
	ino, err := f.allocInode(f.group(dir.ino), false)
	if err != nil {
		return err
	}
	in := f.newInode(ino, modeSymlink|0o777, 0, 0)
	if len(target) < 60 {
		copy(in.b[0x28:], target) // a fast symlink, in the inode
		in.setSize(uint64(len(target)))
	} else if err := f.fill(in, []byte(target)); err != nil {
		return err
	}
	if err := f.save(in); err != nil {
		return err
	}
	return f.dirAdd(dir, name, ino, 7)
}

// Remove removes a file or symlink; ErrNotExist when it is not there.
func (f *FS) Remove(p string) error {
	in, err := f.lookup(p)
	if err != nil {
		return err
	}
	if in.mode()&modeType == modeDir {
		return fmt.Errorf("ext4: %s is a directory", p)
	}
	dir, name, err := f.parent(p)
	if err != nil {
		return err
	}
	if _, err := f.dirRemove(dir, name); err != nil {
		return err
	}
	put32(dir.b[0xC:], f.now)
	put32(dir.b[0x10:], f.now)
	if err := f.save(dir); err != nil {
		return err
	}
	if l := in.links(); l > 1 {
		put16(in.b[0x1A:], l-1)
		return f.save(in)
	}
	if err := f.freeData(in); err != nil {
		return err
	}
	return f.freeInode(in)
}

// Flush brings the checksums of the changed groups and the superblock up
// to date and writes every changed block.
func (f *FS) Flush() error {
	for g := range f.touched {
		d, n, err := f.desc(g)
		if err != nil {
			return err
		}
		if f.csum {
			bb, err := f.block(f.descBlockBitmap(d))
			if err != nil {
				return err
			}
			v := crc(f.csumSeed, bb[:f.perGroup/8])
			put16(d[0x18:], uint16(v))
			if f.descSize >= 0x3A {
				put16(d[0x38:], uint16(v>>16))
			}
			ib, err := f.block(f.descInodeBitmap(d))
			if err != nil {
				return err
			}
			v = crc(f.csumSeed, ib[:f.inodesPer/8])
			put16(d[0x1A:], uint16(v))
			if f.descSize >= 0x3C {
				put16(d[0x3A:], uint16(v>>16))
			}
			var gb [4]byte
			put32(gb[:], g)
			c := crc(f.csumSeed, gb[:])
			c = crc(c, d[:0x1E])
			c = crc(c, []byte{0, 0})
			if f.descSize > 0x20 {
				c = crc(c, d[0x20:f.descSize])
			}
			put16(d[0x1E:], uint16(c))
		}
		f.markDirty(n)
	}
	sb, err := f.super()
	if err != nil {
		return err
	}
	put32(sb[0x30:], f.now) // last write
	if f.csum {
		put32(sb[0x3FC:], crc(^uint32(0), sb[:0x3FC]))
	}
	f.markDirty(f.sbBlock)
	for n := range f.dirty {
		if _, err := f.dev.WriteAt(f.cache[n], f.offset+int64(n)*f.bs); err != nil {
			return fmt.Errorf("ext4: writing block %d: %w", n, err)
		}
	}
	f.dirty = map[uint64]bool{}
	f.touched = nil
	return nil
}
