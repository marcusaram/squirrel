// Package fatfs is a small read-only parser for the FAT32 and exFAT
// filesystems the car writes to its USB drive.
//
// It reads the disk image directly (no loop device, no mount, no fsck) and
// can report, for every file, which byte ranges of the image hold its data.
// That lets the caller clone those ranges into a separate file with a
// reflink instead of copying the data.
//
// The image is being written by the car while we read it, so every
// structure is bounds-checked and errors are returned instead of panics.
package fatfs

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
)

// Extent is a contiguous range of file data inside the image.
type Extent struct {
	ImageOffset int64 // absolute byte offset in the image file
	FileOffset  int64 // byte offset within the file
	Length      int64 // bytes of file data in this extent
}

// Entry is a file or directory.
type Entry struct {
	Name         string
	Path         string // slash separated, relative to the volume root
	IsDir        bool
	Size         int64
	FirstCluster uint32
	ModTime      time.Time // wall clock time as written by the car, in UTC
	// Complete is false when the filesystem says part of the allocated data
	// is not valid yet (exFAT ValidDataLength < DataLength).
	Complete bool

	contiguous bool // exFAT NoFatChain
}

// Volume is an opened FAT32 or exFAT filesystem.
type Volume struct {
	Type        string // "exfat" or "fat32"
	ClusterSize int64
	// Dirty is set when the filesystem was not cleanly unmounted.
	Dirty bool
	// Offset of the filesystem inside the image (partition start).
	Offset int64

	r            io.ReaderAt
	size         int64 // size of the filesystem in bytes
	fatOffset    int64 // absolute offset of the FAT
	heapOffset   int64 // absolute offset of cluster 2
	clusterCount uint32
	rootCluster  uint32
	eoc          uint32 // first end-of-chain value
	bitmap       int64  // absolute offset of the exFAT allocation bitmap, 0 if unknown

	readDir func(dir Entry) ([]Entry, error)

	// For changing a FAT32 volume (OpenRW).
	w        io.WriterAt
	fat      []byte // the active FAT, read once
	fatStart int64  // the first FAT copy
	fatSize  int64  // bytes per copy
	numFATs  int64
	fsInfo   int64 // the FSInfo sector, 0 when there is none
}

var (
	ErrNotFAT    = errors.New("fatfs: no FAT32 or exFAT filesystem found")
	ErrNotFound  = errors.New("fatfs: not found")
	ErrCorrupt   = errors.New("fatfs: corrupt filesystem structure")
	errChainLoop = fmt.Errorf("%w: cluster chain too long", ErrCorrupt)
)

// Limits that keep a corrupt or half-written filesystem from making us
// allocate unbounded memory.
const (
	maxDirBytes = 64 << 20
	maxDepth    = 16
)

// Open detects the filesystem in an image. The image may either start
// directly with a boot sector or contain an MBR partition table, in which
// case the first partition is used.
func Open(r io.ReaderAt, imageSize int64) (*Volume, error) {
	sec := make([]byte, 512)
	if _, err := r.ReadAt(sec, 0); err != nil {
		return nil, fmt.Errorf("fatfs: reading sector 0: %w", err)
	}
	if v, err := openAt(r, 0, imageSize, sec); err == nil {
		return v, nil
	}
	if sec[510] != 0x55 || sec[511] != 0xAA {
		return nil, ErrNotFAT
	}
	for i := 0; i < 4; i++ {
		pe := sec[446+16*i : 446+16*(i+1)]
		ptype := pe[4]
		start := int64(binary.LittleEndian.Uint32(pe[8:])) * 512
		length := int64(binary.LittleEndian.Uint32(pe[12:])) * 512
		if ptype == 0 || start == 0 || start >= imageSize {
			continue
		}
		if length == 0 || start+length > imageSize {
			length = imageSize - start
		}
		bs := make([]byte, 512)
		if _, err := r.ReadAt(bs, start); err != nil {
			continue
		}
		if v, err := openAt(r, start, length, bs); err == nil {
			return v, nil
		}
	}
	return nil, ErrNotFAT
}

func openAt(r io.ReaderAt, off, size int64, bs []byte) (*Volume, error) {
	if bs[510] != 0x55 || bs[511] != 0xAA {
		return nil, ErrNotFAT
	}
	switch {
	case string(bs[3:11]) == "EXFAT   ":
		return openExFAT(r, off, size, bs)
	case string(bs[82:90]) == "FAT32   ":
		return openFAT32(r, off, size, bs)
	}
	return nil, ErrNotFAT
}

// clusterOffset returns the absolute image offset of a data cluster.
func (v *Volume) clusterOffset(c uint32) (int64, error) {
	if c < 2 || c-2 >= v.clusterCount {
		return 0, fmt.Errorf("%w: cluster %d out of range", ErrCorrupt, c)
	}
	return v.heapOffset + int64(c-2)*v.ClusterSize, nil
}

// fatEntry reads one 32-bit FAT entry.
func (v *Volume) fatEntry(c uint32) (uint32, error) {
	var b [4]byte
	if _, err := v.r.ReadAt(b[:], v.fatOffset+int64(c)*4); err != nil {
		return 0, err
	}
	n := binary.LittleEndian.Uint32(b[:])
	if v.Type == "fat32" {
		n &= 0x0FFFFFFF
	}
	return n, nil
}

// chain follows the FAT from first and returns runs of contiguous clusters
// as [start, count] pairs, covering at least need clusters (or the whole
// chain when need is 0).
func (v *Volume) chain(first uint32, need uint32, contiguous bool) ([][2]uint32, error) {
	if first == 0 {
		return nil, nil
	}
	if contiguous {
		if need == 0 || first < 2 || uint64(first-2)+uint64(need) > uint64(v.clusterCount) {
			return nil, fmt.Errorf("%w: contiguous run out of range", ErrCorrupt)
		}
		return [][2]uint32{{first, need}}, nil
	}
	var runs [][2]uint32
	c := first
	var n uint32
	for {
		if c < 2 || c-2 >= v.clusterCount {
			return nil, fmt.Errorf("%w: cluster %d in chain out of range", ErrCorrupt, c)
		}
		if l := len(runs); l > 0 && runs[l-1][0]+runs[l-1][1] == c {
			runs[l-1][1]++
		} else {
			runs = append(runs, [2]uint32{c, 1})
		}
		n++
		if need != 0 && n >= need {
			return runs, nil
		}
		if n > v.clusterCount {
			return nil, errChainLoop
		}
		next, err := v.fatEntry(c)
		if err != nil {
			return nil, err
		}
		if next >= v.eoc {
			break
		}
		c = next
	}
	if need != 0 && n < need {
		return nil, fmt.Errorf("%w: chain shorter than file size", ErrCorrupt)
	}
	return runs, nil
}

// Extents returns where the data of a file lives in the image.
func (v *Volume) Extents(e Entry) ([]Extent, error) {
	if e.IsDir {
		return nil, errors.New("fatfs: extents of a directory")
	}
	if e.Size == 0 {
		return nil, nil
	}
	need := uint32((e.Size + v.ClusterSize - 1) / v.ClusterSize)
	runs, err := v.chain(e.FirstCluster, need, e.contiguous)
	if err != nil {
		return nil, err
	}
	var out []Extent
	var fileOff int64
	for _, run := range runs {
		off, err := v.clusterOffset(run[0])
		if err != nil {
			return nil, err
		}
		l := int64(run[1]) * v.ClusterSize
		if fileOff+l > e.Size {
			l = e.Size - fileOff
		}
		out = append(out, Extent{ImageOffset: off, FileOffset: fileOff, Length: l})
		fileOff += l
	}
	return out, nil
}

// OpenFile returns a reader for the content of a file.
func (v *Volume) OpenFile(e Entry) (*FileReader, error) {
	ext, err := v.Extents(e)
	if err != nil {
		return nil, err
	}
	return &FileReader{r: v.r, ext: ext, size: e.Size}, nil
}

// readDirData returns the raw bytes of a directory.
func (v *Volume) readDirData(first uint32, size int64, contiguous bool) ([]byte, error) {
	var need uint32
	if size > 0 {
		if size > maxDirBytes {
			return nil, fmt.Errorf("%w: directory too large", ErrCorrupt)
		}
		need = uint32((size + v.ClusterSize - 1) / v.ClusterSize)
	}
	runs, err := v.chain(first, need, contiguous)
	if err != nil {
		return nil, err
	}
	var total int64
	for _, run := range runs {
		total += int64(run[1]) * v.ClusterSize
	}
	if total > maxDirBytes {
		return nil, fmt.Errorf("%w: directory too large", ErrCorrupt)
	}
	buf := make([]byte, total)
	var pos int64
	for _, run := range runs {
		off, err := v.clusterOffset(run[0])
		if err != nil {
			return nil, err
		}
		l := int64(run[1]) * v.ClusterSize
		if _, err := v.r.ReadAt(buf[pos:pos+l], off); err != nil {
			return nil, err
		}
		pos += l
	}
	return buf, nil
}

// Root returns the root directory entry.
func (v *Volume) Root() Entry {
	return Entry{Name: "", Path: "", IsDir: true, FirstCluster: v.rootCluster, Complete: true}
}

// ReadDir lists a directory.
func (v *Volume) ReadDir(dir Entry) ([]Entry, error) {
	if !dir.IsDir {
		return nil, errors.New("fatfs: not a directory")
	}
	return v.readDir(dir)
}

// Reach returns e with Size set to the most its data can span: the rest of
// the cluster heap for a contiguous file, its cluster chain otherwise. It
// is for reading a deleted file whose size is no longer known.
func (v *Volume) Reach(e Entry) (Entry, error) {
	if e.FirstCluster < 2 || e.FirstCluster-2 >= v.clusterCount {
		return Entry{}, fmt.Errorf("%w: cluster %d out of range", ErrCorrupt, e.FirstCluster)
	}
	if e.contiguous {
		e.Size = int64(v.clusterCount-(e.FirstCluster-2)) * v.ClusterSize
		return e, nil
	}
	runs, err := v.chain(e.FirstCluster, 0, false)
	if err != nil {
		return Entry{}, err
	}
	e.Size = 0
	for _, run := range runs {
		e.Size += int64(run[1]) * v.ClusterSize
	}
	return e, nil
}

// Free tells whether none of a file's clusters is allocated, i.e. whether
// the data of a deleted file can still be there. exFAT only.
func (v *Volume) Free(e Entry) (bool, error) {
	if v.bitmap == 0 {
		return false, errors.New("fatfs: no allocation bitmap")
	}
	if e.Size == 0 {
		return true, nil
	}
	need := uint32((e.Size + v.ClusterSize - 1) / v.ClusterSize)
	runs, err := v.chain(e.FirstCluster, need, e.contiguous)
	if err != nil {
		return false, err
	}
	for _, run := range runs {
		first, last := run[0]-2, run[0]-2+run[1]-1
		b := make([]byte, last/8-first/8+1)
		if _, err := v.r.ReadAt(b, v.bitmap+int64(first/8)); err != nil {
			return false, err
		}
		for c := first; c <= last; c++ {
			if b[c/8-first/8]&(1<<(c%8)) != 0 {
				return false, nil
			}
		}
	}
	return true, nil
}

// Lookup finds an entry by slash separated path. Names are matched case
// insensitively, like the car does.
func (v *Volume) Lookup(p string) (Entry, error) {
	cur := v.Root()
	for _, part := range strings.Split(strings.Trim(path.Clean("/"+p), "/"), "/") {
		if part == "" {
			continue
		}
		entries, err := v.ReadDir(cur)
		if err != nil {
			return Entry{}, err
		}
		found := false
		for _, e := range entries {
			if strings.EqualFold(e.Name, part) {
				cur, found = e, true
				break
			}
		}
		if !found {
			return Entry{}, fmt.Errorf("%w: %s", ErrNotFound, p)
		}
	}
	return cur, nil
}

// Walk calls fn for every entry below dir (not dir itself). A directory that
// cannot be read is reported to fn through err and then skipped.
func (v *Volume) Walk(dir Entry, fn func(e Entry, err error) error) error {
	return v.walk(dir, fn, 0)
}

func (v *Volume) walk(dir Entry, fn func(e Entry, err error) error, depth int) error {
	if depth > maxDepth {
		return fn(dir, fmt.Errorf("%w: directory nesting too deep", ErrCorrupt))
	}
	entries, err := v.ReadDir(dir)
	if err != nil {
		return fn(dir, err)
	}
	for _, e := range entries {
		if err := fn(e, nil); err != nil {
			return err
		}
		if e.IsDir {
			if err := v.walk(e, fn, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// FileReader reads file content through its extents.
type FileReader struct {
	r    io.ReaderAt
	ext  []Extent
	size int64
}

func (f *FileReader) Size() int64 { return f.size }

func (f *FileReader) ReadAt(p []byte, off int64) (int, error) {
	if off >= f.size {
		return 0, io.EOF
	}
	want := len(p)
	n := 0
	for _, e := range f.ext {
		if len(p) == 0 {
			break
		}
		if off >= e.FileOffset+e.Length || off+int64(len(p)) <= e.FileOffset {
			continue
		}
		within := off - e.FileOffset
		chunk := p
		if rem := e.Length - within; int64(len(chunk)) > rem {
			chunk = chunk[:rem]
		}
		m, err := f.r.ReadAt(chunk, e.ImageOffset+within)
		n += m
		if err != nil {
			return n, err
		}
		p = p[m:]
		off += int64(m)
	}
	// A read that ends exactly at the end of the file is complete, not an
	// error: readers of the last box would otherwise fail.
	if n < want {
		return n, io.EOF
	}
	return n, nil
}

func (f *FileReader) NewReader() *io.SectionReader {
	return io.NewSectionReader(f, 0, f.size)
}

// dosTime decodes a FAT/exFAT date+time stamp (date in the high 16 bits).
func dosTime(ts uint32, centis uint8) time.Time {
	date, tm := uint16(ts>>16), uint16(ts)
	year := int(date>>9) + 1980
	month := time.Month((date >> 5) & 0x0F)
	day := int(date & 0x1F)
	hour := int(tm >> 11)
	minute := int((tm >> 5) & 0x3F)
	sec := int(tm&0x1F) * 2
	if month < 1 || month > 12 || day < 1 {
		return time.Time{}
	}
	t := time.Date(year, month, day, hour, minute, sec, 0, time.UTC)
	return t.Add(time.Duration(centis) * 10 * time.Millisecond)
}
