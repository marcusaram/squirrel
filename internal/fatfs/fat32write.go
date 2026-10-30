package fatfs

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
	"unicode/utf16"
)

// Writing FAT32: just what Squirrel Maker needs on Raspberry Pi OS's boot
// partition in an image: write and replace files and make directories.
// New names get a long name and a unique 8.3 alias, as Windows and Linux
// give them; every FAT copy and the FSInfo free count are kept up to date.

// ReadWriterAt is what OpenRW changes the volume on.
type ReadWriterAt interface {
	io.ReaderAt
	io.WriterAt
}

// OpenRW is Open for changing a FAT32 volume.
func OpenRW(rw ReadWriterAt, size int64) (*Volume, error) {
	v, err := Open(rw, size)
	if err != nil {
		return nil, err
	}
	if v.Type != "fat32" {
		return nil, fmt.Errorf("fatfs: changing %s is not supported", v.Type)
	}
	v.w = rw
	return v, nil
}

const (
	attrDir     = 0x10
	attrArchive = 0x20
	attrLFN     = 0x0F
	fatEOC      = 0x0FFFFFFF
)

// loadFAT reads the active FAT once; changes go to it and to every copy
// on the volume.
func (v *Volume) loadFAT() error {
	if v.fat != nil {
		return nil
	}
	fat := make([]byte, v.fatSize)
	if _, err := v.r.ReadAt(fat, v.fatOffset); err != nil {
		return err
	}
	v.fat = fat
	return nil
}

func (v *Volume) setFAT(c, val uint32) error {
	if err := v.loadFAT(); err != nil {
		return err
	}
	o := int64(c) * 4
	old := binary.LittleEndian.Uint32(v.fat[o:])
	binary.LittleEndian.PutUint32(v.fat[o:], old&0xF0000000|val&0x0FFFFFFF)
	for i := int64(0); i < v.numFATs; i++ {
		if _, err := v.w.WriteAt(v.fat[o:o+4], v.fatStart+i*v.fatSize+o); err != nil {
			return err
		}
	}
	return nil
}

func (v *Volume) getFAT(c uint32) uint32 {
	return binary.LittleEndian.Uint32(v.fat[int64(c)*4:]) & 0x0FFFFFFF
}

// adjustFree changes the FSInfo free cluster count, when it is kept.
func (v *Volume) adjustFree(d int64) error {
	if v.fsInfo == 0 {
		return nil
	}
	var b [4]byte
	if _, err := v.r.ReadAt(b[:], v.fsInfo+488); err != nil {
		return err
	}
	n := binary.LittleEndian.Uint32(b[:])
	if n == 0xFFFFFFFF {
		return nil // not kept
	}
	binary.LittleEndian.PutUint32(b[:], uint32(int64(n)+d))
	_, err := v.w.WriteAt(b[:], v.fsInfo+488)
	return err
}

// alloc takes n free clusters, links them into a chain, and zeroes them.
func (v *Volume) alloc(n int) ([]uint32, error) {
	if err := v.loadFAT(); err != nil {
		return nil, err
	}
	var got []uint32
	for c := uint32(2); c < v.clusterCount+2 && len(got) < n; c++ {
		if v.getFAT(c) == 0 {
			got = append(got, c)
		}
	}
	if len(got) < n {
		return nil, errors.New("fatfs: the volume is full")
	}
	for i, c := range got {
		next := uint32(fatEOC)
		if i+1 < len(got) {
			next = got[i+1]
		}
		if err := v.setFAT(c, next); err != nil {
			return nil, err
		}
	}
	return got, v.adjustFree(-int64(n))
}

func (v *Volume) freeChain(first uint32) error {
	if first < 2 {
		return nil
	}
	if err := v.loadFAT(); err != nil {
		return err
	}
	freed := int64(0)
	for c := first; c >= 2 && c < v.clusterCount+2; {
		next := v.getFAT(c)
		if err := v.setFAT(c, 0); err != nil {
			return err
		}
		freed++
		if next >= v.eoc || freed > int64(v.clusterCount) {
			break
		}
		c = next
	}
	return v.adjustFree(freed)
}

// writeClusters writes data over a chain of clusters, zero filled.
func (v *Volume) writeClusters(chain []uint32, data []byte) error {
	buf := make([]byte, v.ClusterSize)
	for i, c := range chain {
		clear(buf)
		if lo := int64(i) * v.ClusterSize; lo < int64(len(data)) {
			copy(buf, data[lo:])
		}
		off, err := v.clusterOffset(c)
		if err != nil {
			return err
		}
		if _, err := v.w.WriteAt(buf, off); err != nil {
			return err
		}
	}
	return nil
}

// dir is a directory's raw entries and the clusters they are in.
type dir struct {
	first    uint32
	clusters []uint32
	data     []byte
}

func (v *Volume) loadDir(first uint32) (*dir, error) {
	runs, err := v.chain(first, 0, false)
	if err != nil {
		return nil, err
	}
	d := &dir{first: first}
	for _, r := range runs {
		for i := uint32(0); i < r[1]; i++ {
			d.clusters = append(d.clusters, r[0]+i)
		}
	}
	if d.data, err = v.readDirData(first, 0, false); err != nil {
		return nil, err
	}
	return d, nil
}

func (v *Volume) slotOffset(d *dir, slot int) (int64, error) {
	at := int64(slot) * 32
	c := at / v.ClusterSize
	if c >= int64(len(d.clusters)) {
		return 0, fmt.Errorf("%w: directory slot %d", ErrCorrupt, slot)
	}
	off, err := v.clusterOffset(d.clusters[c])
	return off + at%v.ClusterSize, err
}

func (v *Volume) writeSlot(d *dir, slot int, e []byte) error {
	copy(d.data[slot*32:], e)
	off, err := v.slotOffset(d, slot)
	if err != nil {
		return err
	}
	_, err = v.w.WriteAt(e, off)
	return err
}

// find returns the slot of name's short entry in d, case-insensitively.
func (v *Volume) find(d *dir, name string) (int, bool) {
	var lfn []uint16
	var sum byte
	for i := 0; i+32 <= len(d.data); i += 32 {
		e := d.data[i : i+32]
		if e[0] == 0x00 {
			break
		}
		if e[0] == 0xE5 {
			lfn = nil
			continue
		}
		if e[11]&0x3F == attrLFN {
			ord := int(e[0] & 0x1F)
			if e[0]&0x40 != 0 {
				lfn, sum = make([]uint16, ord*13), e[13]
			}
			if lfn == nil || ord == 0 || ord*13 > len(lfn) {
				lfn = nil
				continue
			}
			p := (ord - 1) * 13
			for _, r := range [][2]int{{1, 11}, {14, 26}, {28, 32}} {
				for k := r[0]; k < r[1]; k += 2 {
					lfn[p] = binary.LittleEndian.Uint16(e[k:])
					p++
				}
			}
			continue
		}
		if e[11]&0x08 != 0 {
			lfn = nil
			continue
		}
		n := shortName(e)
		if lfn != nil && sfnChecksum(e[:11]) == sum {
			n = decodeLFN(lfn)
		}
		lfn = nil
		if strings.EqualFold(n, name) || strings.EqualFold(shortName(e), name) {
			return i / 32, true
		}
	}
	return 0, false
}

// alias makes a unique 8.3 name for name in d: BASE~N.EXT.
func alias(d *dir, name string) ([11]byte, error) {
	clean := func(s string, n int) string {
		var b strings.Builder
		for _, r := range strings.ToUpper(s) {
			if b.Len() >= n {
				break
			}
			switch {
			case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', strings.ContainsRune("$%'-_@~`!(){}^#&", r):
				b.WriteRune(r)
			case r == ' ' || r == '.':
			default:
				b.WriteRune('_')
			}
		}
		return b.String()
	}
	base, ext := name, ""
	if i := strings.LastIndex(name, "."); i > 0 {
		base, ext = name[:i], name[i+1:]
	}
	b, x := clean(base, 8), clean(ext, 3)
	if b == "" {
		b = "FILE"
	}
	taken := map[string]bool{}
	for i := 0; i+32 <= len(d.data); i += 32 {
		e := d.data[i : i+32]
		if e[0] == 0x00 {
			break
		}
		if e[0] != 0xE5 && e[11]&0x3F != attrLFN {
			taken[string(e[:11])] = true
		}
	}
	for n := 1; n < 1000000; n++ {
		tail := fmt.Sprintf("~%d", n)
		var sfn [11]byte
		copy(sfn[:], fmt.Sprintf("%-8s%-3s", b[:min(len(b), 8-len(tail))]+tail, x))
		if !taken[string(sfn[:])] {
			return sfn, nil
		}
	}
	return [11]byte{}, errors.New("fatfs: no free short name")
}

func dosStamp(t time.Time) (date, tm uint16) {
	if t.Year() < 1980 {
		t = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	date = uint16((t.Year()-1980)<<9 | int(t.Month())<<5 | t.Day())
	tm = uint16(t.Hour()<<11 | t.Minute()<<5 | t.Second()/2)
	return
}

func shortEntry(sfn [11]byte, attr byte, cluster uint32, size uint32, t time.Time) []byte {
	e := make([]byte, 32)
	copy(e, sfn[:])
	e[11] = attr
	date, tm := dosStamp(t)
	binary.LittleEndian.PutUint16(e[14:], tm)
	binary.LittleEndian.PutUint16(e[16:], date)
	binary.LittleEndian.PutUint16(e[18:], date)
	binary.LittleEndian.PutUint16(e[20:], uint16(cluster>>16))
	binary.LittleEndian.PutUint16(e[22:], tm)
	binary.LittleEndian.PutUint16(e[24:], date)
	binary.LittleEndian.PutUint16(e[26:], uint16(cluster))
	binary.LittleEndian.PutUint32(e[28:], size)
	return e
}

// lfnEntries are the long name entries for name, in on-disk order.
func lfnEntries(name string, sum byte) [][]byte {
	u := utf16.Encode([]rune(name))
	n := (len(u) + 12) / 13
	padded := make([]uint16, n*13)
	for i := range padded {
		switch {
		case i < len(u):
			padded[i] = u[i]
		case i == len(u):
			padded[i] = 0
		default:
			padded[i] = 0xFFFF
		}
	}
	var out [][]byte
	for ord := n; ord >= 1; ord-- {
		e := make([]byte, 32)
		e[0] = byte(ord)
		if ord == n {
			e[0] |= 0x40
		}
		e[11], e[13] = attrLFN, sum
		p := (ord - 1) * 13
		for _, r := range [][2]int{{1, 11}, {14, 26}, {28, 32}} {
			for k := r[0]; k < r[1]; k += 2 {
				binary.LittleEndian.PutUint16(e[k:], padded[p])
				p++
			}
		}
		out = append(out, e)
	}
	return out
}

// add puts a new name with its short entry into d, in free slots or a
// new cluster.
func (v *Volume) add(d *dir, name string, attr byte, cluster, size uint32, t time.Time) error {
	if name == "" || len(name) > 255 || strings.ContainsAny(name, `/\:*?"<>|`) {
		return fmt.Errorf("fatfs: name %q", name)
	}
	sfn, err := alias(d, name)
	if err != nil {
		return err
	}
	entries := append(lfnEntries(name, sfnChecksum(sfn[:])), shortEntry(sfn, attr, cluster, size, t))
	need := len(entries)
	start, run := -1, 0
	for i := 0; i+32 <= len(d.data); i += 32 {
		if b := d.data[i]; b == 0x00 || b == 0xE5 {
			if run == 0 {
				start = i / 32
			}
			run++
			if run == need {
				break
			}
		} else {
			run = 0
		}
	}
	if run < need {
		// Grow the directory by a cluster.
		c, err := v.alloc(1)
		if err != nil {
			return err
		}
		if err := v.writeClusters(c, nil); err != nil {
			return err
		}
		if err := v.setFAT(d.clusters[len(d.clusters)-1], c[0]); err != nil {
			return err
		}
		if run == 0 {
			start = len(d.data) / 32
		}
		d.clusters = append(d.clusters, c[0])
		d.data = append(d.data, make([]byte, v.ClusterSize)...)
	}
	for i, e := range entries {
		if err := v.writeSlot(d, start+i, e); err != nil {
			return err
		}
	}
	return nil
}

// lookupDir follows a path of directories from the root.
func (v *Volume) lookupDir(p string) (*dir, error) {
	cur := v.rootCluster
	for _, part := range strings.Split(strings.Trim(path.Clean("/"+p), "/"), "/") {
		if part == "" {
			continue
		}
		d, err := v.loadDir(cur)
		if err != nil {
			return nil, err
		}
		slot, ok := v.find(d, part)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, p)
		}
		e := d.data[slot*32:]
		if e[11]&attrDir == 0 {
			return nil, fmt.Errorf("fatfs: %s is not a directory", part)
		}
		cur = uint32(binary.LittleEndian.Uint16(e[20:]))<<16 | uint32(binary.LittleEndian.Uint16(e[26:]))
	}
	return v.loadDir(cur)
}

// Mkdir makes a directory whose parent exists; one that is there already
// is fine.
func (v *Volume) Mkdir(p string, t time.Time) error {
	p = path.Clean("/" + p)
	parent, err := v.lookupDir(path.Dir(p))
	if err != nil {
		return err
	}
	if slot, ok := v.find(parent, path.Base(p)); ok {
		if parent.data[slot*32+11]&attrDir != 0 {
			return nil
		}
		return fmt.Errorf("fatfs: %s exists and is not a directory", p)
	}
	c, err := v.alloc(1)
	if err != nil {
		return err
	}
	dotdot := parent.first
	if dotdot == v.rootCluster {
		dotdot = 0
	}
	body := make([]byte, v.ClusterSize)
	copy(body, shortEntry([11]byte{'.', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' '}, attrDir, c[0], 0, t))
	copy(body[32:], shortEntry([11]byte{'.', '.', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' '}, attrDir, dotdot, 0, t))
	if err := v.writeClusters(c, body); err != nil {
		return err
	}
	return v.add(parent, path.Base(p), attrDir, c[0], 0, t)
}

// WriteFile writes a file whose directory exists, replacing one that is
// there (in its directory entry).
func (v *Volume) WriteFile(p string, data []byte, t time.Time) error {
	if int64(len(data)) > 0xFFFFFFFF {
		return errors.New("fatfs: file too large for FAT32")
	}
	p = path.Clean("/" + p)
	d, err := v.lookupDir(path.Dir(p))
	if err != nil {
		return err
	}
	n := int((int64(len(data)) + v.ClusterSize - 1) / v.ClusterSize)
	var chain []uint32
	if n > 0 {
		if chain, err = v.alloc(n); err != nil {
			return err
		}
		if err := v.writeClusters(chain, data); err != nil {
			return err
		}
	}
	first := uint32(0)
	if len(chain) > 0 {
		first = chain[0]
	}
	if slot, ok := v.find(d, path.Base(p)); ok {
		e := append([]byte(nil), d.data[slot*32:slot*32+32]...)
		if e[11]&attrDir != 0 {
			return fmt.Errorf("fatfs: %s is a directory", p)
		}
		old := uint32(binary.LittleEndian.Uint16(e[20:]))<<16 | uint32(binary.LittleEndian.Uint16(e[26:]))
		var sfn [11]byte
		copy(sfn[:], e[:11])
		ne := shortEntry(sfn, e[11]|attrArchive, first, uint32(len(data)), t)
		ne[12] = e[12] // keep the lower case flags of a plain 8.3 name
		copy(ne[13:20], e[13:20])
		if err := v.writeSlot(d, slot, ne); err != nil {
			return err
		}
		return v.freeChain(old)
	}
	return v.add(d, path.Base(p), attrArchive, first, uint32(len(data)), t)
}
