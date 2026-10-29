package exfatgen

import (
	"crypto/rand"
	"encoding/binary"
	"io"
	"sort"
	"time"
	"unicode/utf16"
)

// The on-disk structures, after Microsoft's exFAT specification.

func upcaseTable() []byte {
	// The first 128 characters explicitly (a-z to A-Z), then the rest as
	// one compressed identity run (0xFFFF, count).
	var b []byte
	for c := uint16(0); c < 0x80; c++ {
		u := c
		if c >= 'a' && c <= 'z' {
			u = c - 'a' + 'A'
		}
		b = binary.LittleEndian.AppendUint16(b, u)
	}
	b = binary.LittleEndian.AppendUint16(b, 0xFFFF)
	b = binary.LittleEndian.AppendUint16(b, 0xFFFF-0x80+1)
	return b
}

func upcase(c uint16) uint16 {
	if c >= 'a' && c <= 'z' {
		return c - 'a' + 'A'
	}
	return c
}

func tableChecksum(b []byte) uint32 {
	var c uint32
	for _, x := range b {
		c = (c<<31 | c>>1) + uint32(x)
	}
	return c
}

func labelEntry(label string) []byte {
	e := make([]byte, entrySize)
	u := utf16.Encode([]rune(label))
	if len(u) > 11 {
		u = u[:11]
	}
	e[0], e[1] = 0x83, byte(len(u))
	for i, c := range u {
		binary.LittleEndian.PutUint16(e[2+2*i:], c)
	}
	return e
}

func bitmapEntry(first uint32, length int64) []byte {
	e := make([]byte, entrySize)
	e[0] = 0x81
	binary.LittleEndian.PutUint32(e[20:], first)
	binary.LittleEndian.PutUint64(e[24:], uint64(length))
	return e
}

func upcaseEntry(first uint32, table []byte) []byte {
	e := make([]byte, entrySize)
	e[0] = 0x82
	binary.LittleEndian.PutUint32(e[4:], tableChecksum(table))
	binary.LittleEndian.PutUint32(e[20:], first)
	binary.LittleEndian.PutUint64(e[24:], uint64(len(table)))
	return e
}

func timestamp(t time.Time) uint32 {
	if t.Year() < 1980 {
		t = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return uint32(t.Year()-1980)<<25 | uint32(t.Month())<<21 | uint32(t.Day())<<16 |
		uint32(t.Hour())<<11 | uint32(t.Minute())<<5 | uint32(t.Second()/2)
}

// entrySet is a File entry, its Stream Extension and File Name entries.
// The FAT describes every chain, so NoFatChain is never set.
func entrySet(name string, attr uint16, mod time.Time, first uint32, length int64, up []byte) []byte {
	u := utf16.Encode([]rune(name))
	nameEntries := (len(u) + 14) / 15
	set := make([]byte, entrySize*(2+nameEntries))
	f := set[:entrySize]
	f[0], f[1] = 0x85, byte(1+nameEntries)
	binary.LittleEndian.PutUint16(f[4:], attr)
	ts := timestamp(mod)
	for _, off := range []int{8, 12, 16} {
		binary.LittleEndian.PutUint32(f[off:], ts)
	}
	s := set[entrySize : 2*entrySize]
	s[0], s[1], s[3] = 0xC0, 0x01, byte(len(u)) // AllocationPossible
	var hash uint16
	for _, c := range u {
		c = upcase(c)
		for _, b := range []byte{byte(c), byte(c >> 8)} {
			hash = (hash<<15 | hash>>1) + uint16(b)
		}
	}
	binary.LittleEndian.PutUint16(s[4:], hash)
	binary.LittleEndian.PutUint64(s[8:], uint64(length))
	binary.LittleEndian.PutUint32(s[20:], first)
	binary.LittleEndian.PutUint64(s[24:], uint64(length))
	for i := 0; i < nameEntries; i++ {
		n := set[(2+i)*entrySize : (3+i)*entrySize]
		n[0] = 0xC1
		for j := 0; j < 15 && i*15+j < len(u); j++ {
			binary.LittleEndian.PutUint16(n[2+2*j:], u[i*15+j])
		}
	}
	var sum uint16
	for i, b := range set {
		if i == 2 || i == 3 {
			continue
		}
		sum = (sum<<15 | sum>>1) + uint16(b)
	}
	binary.LittleEndian.PutUint16(f[2:], sum)
	return set
}

// bootRegion is the 12 sectors of the main (and backup) boot region.
func bootRegion(volSectors, fatLen, heap int64, clusters, root uint32) []byte {
	b := make([]byte, 12*sectorSize)
	s := b[:sectorSize]
	copy(s, []byte{0xEB, 0x76, 0x90})
	copy(s[3:], "EXFAT   ")
	binary.LittleEndian.PutUint64(s[64:], partStart)
	binary.LittleEndian.PutUint64(s[72:], uint64(volSectors))
	binary.LittleEndian.PutUint32(s[80:], fatOffset)
	binary.LittleEndian.PutUint32(s[84:], uint32(fatLen))
	binary.LittleEndian.PutUint32(s[88:], uint32(heap))
	binary.LittleEndian.PutUint32(s[92:], clusters)
	binary.LittleEndian.PutUint32(s[96:], root)
	var serial [4]byte
	rand.Read(serial[:])
	copy(s[100:], serial[:])
	binary.LittleEndian.PutUint16(s[104:], 0x0100)
	s[108], s[109], s[110], s[111], s[112] = 9, clusterShift, 1, 0x80, 100
	s[510], s[511] = 0x55, 0xAA
	for i := 1; i <= 8; i++ { // extended boot sectors
		binary.LittleEndian.PutUint32(b[i*sectorSize+508:], 0xAA550000)
	}
	var sum uint32
	for i, x := range b[:11*sectorSize] {
		if i == 106 || i == 107 || i == 112 {
			continue
		}
		sum = (sum<<31 | sum>>1) + uint32(x)
	}
	for i := 11 * sectorSize; i < 12*sectorSize; i += 4 {
		binary.LittleEndian.PutUint32(b[i:], sum)
	}
	return b
}

func mbr(volSectors int64) []byte {
	b := make([]byte, sectorSize)
	rand.Read(b[440:444])
	p := b[446:462]
	p[1], p[2], p[3] = 0xFE, 0xFF, 0xFF
	p[4] = 0x07 // exFAT
	p[5], p[6], p[7] = 0xFE, 0xFF, 0xFF
	binary.LittleEndian.PutUint32(p[8:], partStart)
	binary.LittleEndian.PutUint32(p[12:], uint32(min(volSectors, 0xFFFFFFFF)))
	b[510], b[511] = 0x55, 0xAA
	return b
}

// ReadAt reads the generated disk.
func (v *Volume) ReadAt(p []byte, off int64) (int, error) {
	if off >= v.size {
		return 0, io.EOF
	}
	n := 0
	for len(p) > 0 && off < v.size {
		m, err := v.readSome(p, off)
		n += m
		off += int64(m)
		p = p[m:]
		if err != nil {
			return n, err
		}
	}
	if len(p) > 0 {
		return n, io.EOF
	}
	return n, nil
}

// readSome fills the start of p from one region and returns how much.
func (v *Volume) readSome(p []byte, off int64) (int, error) {
	zero := func(limit int64) int {
		n := int(min(int64(len(p)), limit-off))
		clear(p[:n])
		return n
	}
	from := func(src []byte, base, limit int64) int {
		n := int(min(int64(len(p)), limit-off))
		at := off - base
		m := 0
		if at < int64(len(src)) {
			m = copy(p[:n], src[at:])
		}
		clear(p[m:n])
		return n
	}
	part := int64(partStart) * sectorSize
	switch {
	case off < sectorSize:
		return from(v.mbr, 0, sectorSize), nil
	case off < part:
		return zero(part), nil
	}
	vo := off - part // offset in the volume
	boot := int64(len(v.boot))
	fatStart, fatEnd := int64(fatOffset)*sectorSize, (int64(fatOffset)+v.fatLen)*sectorSize
	heap := v.heap * sectorSize
	switch {
	case vo < boot:
		return from(v.boot, part, part+boot), nil
	case vo < 2*boot:
		return from(v.boot, part+boot, part+2*boot), nil
	case vo < fatStart:
		return zero(part + fatStart), nil
	case vo < fatEnd:
		n := int(min(int64(len(p)), part+fatEnd-off))
		v.readFAT(p[:n], vo-fatStart)
		return n, nil
	case vo < heap:
		return zero(part + heap), nil
	}
	c := uint32((vo-heap)/clusterSize) + 2
	i := sort.Search(len(v.chains), func(i int) bool { return v.chains[i].first+v.chains[i].n > c })
	if i == len(v.chains) || v.chains[i].first > c {
		next := v.size
		if i < len(v.chains) {
			next = part + heap + int64(v.chains[i].first-2)*clusterSize
		}
		return zero(next), nil
	}
	e := v.chains[i]
	base := part + heap + int64(e.first-2)*clusterSize
	end := base + int64(e.n)*clusterSize
	switch {
	case e.dir != nil:
		return from(e.dir, base, end), nil
	case e.bitmap:
		n := int(min(int64(len(p)), end-off))
		for j := range p[:n] {
			bit := uint32((off - base + int64(j)) * 8)
			switch {
			case bit+8 <= v.clusters:
				p[j] = 0xFF
			case bit < v.clusters:
				p[j] = byte(1<<(v.clusters-bit) - 1)
			default:
				p[j] = 0
			}
		}
		return n, nil
	}
	f := e.file
	at := off - base
	n := int(min(int64(len(p)), end-off))
	if at >= f.Size {
		clear(p[:n])
		return n, nil
	}
	want := int(min(int64(n), f.Size-at))
	m, err := f.ReadAt(p[:want], at)
	if m < want {
		if err == nil || err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return m, err
	}
	clear(p[want:n])
	return n, nil
}

// readFAT fills p with the FAT from byte at: every chain runs to the
// next cluster and ends with end-of-chain; the rest is free.
func (v *Volume) readFAT(p []byte, at int64) {
	var e [4]byte
	for j := range p {
		pos := at + int64(j)
		idx := uint32(pos / 4)
		if j == 0 || pos%4 == 0 {
			binary.LittleEndian.PutUint32(e[:], v.fatEntry(idx))
		}
		p[j] = e[pos%4]
	}
}

func (v *Volume) fatEntry(c uint32) uint32 {
	switch c {
	case 0:
		return 0xFFFFFFF8
	case 1:
		return 0xFFFFFFFF
	}
	i := sort.Search(len(v.chains), func(i int) bool { return v.chains[i].first+v.chains[i].n > c })
	if i == len(v.chains) || v.chains[i].first > c {
		return 0
	}
	if c == v.chains[i].first+v.chains[i].n-1 {
		return 0xFFFFFFFF
	}
	return c + 1
}
