package fatfs

import (
	"encoding/binary"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode/utf16"
)

func openFAT32(r io.ReaderAt, off, size int64, bs []byte) (*Volume, error) {
	bps := int64(binary.LittleEndian.Uint16(bs[11:]))
	spc := int64(bs[13])
	rsvd := int64(binary.LittleEndian.Uint16(bs[14:]))
	nfats := int64(bs[16])
	fatsz := int64(binary.LittleEndian.Uint32(bs[36:]))
	totsec := int64(binary.LittleEndian.Uint16(bs[19:]))
	if totsec == 0 {
		totsec = int64(binary.LittleEndian.Uint32(bs[32:]))
	}
	if (bps != 512 && bps != 1024 && bps != 2048 && bps != 4096) ||
		spc == 0 || spc&(spc-1) != 0 || rsvd == 0 || nfats == 0 || fatsz == 0 {
		return nil, fmt.Errorf("%w: fat32 BPB", ErrCorrupt)
	}
	firstData := rsvd + nfats*fatsz
	if totsec <= firstData {
		return nil, fmt.Errorf("%w: fat32 sizes", ErrCorrupt)
	}
	clusters := (totsec - firstData) / spc
	if maxByFAT := fatsz*bps/4 - 2; clusters > maxByFAT {
		clusters = maxByFAT
	}
	v := &Volume{
		Type:         "fat32",
		ClusterSize:  bps * spc,
		Offset:       off,
		r:            r,
		size:         size,
		fatOffset:    off + rsvd*bps,
		heapOffset:   off + firstData*bps,
		clusterCount: uint32(clusters),
		rootCluster:  binary.LittleEndian.Uint32(bs[44:]),
		eoc:          0x0FFFFFF7,
	}
	if ext := binary.LittleEndian.Uint16(bs[40:]); ext&0x80 != 0 {
		// FAT mirroring disabled; use the active FAT
		v.fatOffset += int64(ext&0x0F) * fatsz * bps
	}
	if fat1, err := v.fatEntry(1); err == nil {
		v.Dirty = fat1&0x08000000 == 0
	}
	v.readDir = v.readDirFAT32
	return v, nil
}

func (v *Volume) readDirFAT32(dir Entry) ([]Entry, error) {
	data, err := v.readDirData(dir.FirstCluster, 0, false)
	if err != nil {
		return nil, err
	}
	var out []Entry
	var lfn []uint16
	var lfnSum byte
	var lfnLeft int
	for i := 0; i+32 <= len(data); i += 32 {
		d := data[i : i+32]
		if d[0] == 0x00 {
			break
		}
		if d[0] == 0xE5 {
			lfn, lfnLeft = nil, 0
			continue
		}
		attr := d[11]
		if attr&0x3F == 0x0F {
			ord := int(d[0] & 0x1F)
			if d[0]&0x40 != 0 {
				lfn = make([]uint16, ord*13)
				lfnSum = d[13]
				lfnLeft = ord
			}
			if lfn == nil || ord == 0 || ord > len(lfn)/13 || d[13] != lfnSum || ord != lfnLeft {
				lfn, lfnLeft = nil, 0
				continue
			}
			p := (ord - 1) * 13
			for _, r := range [][2]int{{1, 11}, {14, 26}, {28, 32}} {
				for k := r[0]; k < r[1]; k += 2 {
					lfn[p] = binary.LittleEndian.Uint16(d[k:])
					p++
				}
			}
			lfnLeft--
			continue
		}
		if attr&0x08 != 0 {
			lfn, lfnLeft = nil, 0
			continue
		}
		name := ""
		if lfn != nil && lfnLeft == 0 && sfnChecksum(d[:11]) == lfnSum {
			name = decodeLFN(lfn)
		}
		lfn, lfnLeft = nil, 0
		if name == "" {
			name = shortName(d)
		}
		if name == "." || name == ".." || name == "" {
			continue
		}
		e := Entry{
			Name:         name,
			Path:         path.Join(dir.Path, name),
			IsDir:        attr&0x10 != 0,
			Size:         int64(binary.LittleEndian.Uint32(d[28:])),
			FirstCluster: uint32(binary.LittleEndian.Uint16(d[20:]))<<16 | uint32(binary.LittleEndian.Uint16(d[26:])),
			ModTime:      dosTime(uint32(binary.LittleEndian.Uint16(d[24:]))<<16|uint32(binary.LittleEndian.Uint16(d[22:])), 0),
			Complete:     true,
		}
		if e.IsDir {
			e.Size = 0
		}
		out = append(out, e)
	}
	return out, nil
}

func sfnChecksum(n []byte) byte {
	var sum byte
	for _, c := range n {
		sum = (sum&1)<<7 + sum>>1 + c
	}
	return sum
}

func decodeLFN(u []uint16) string {
	for i, c := range u {
		if c == 0x0000 || c == 0xFFFF {
			u = u[:i]
			break
		}
	}
	return string(utf16.Decode(u))
}

func shortName(d []byte) string {
	b := make([]byte, 11)
	copy(b, d[:11])
	if b[0] == 0x05 {
		b[0] = 0xE5
	}
	base := strings.TrimRight(string(b[:8]), " ")
	ext := strings.TrimRight(string(b[8:11]), " ")
	if d[12]&0x08 != 0 {
		base = strings.ToLower(base)
	}
	if d[12]&0x10 != 0 {
		ext = strings.ToLower(ext)
	}
	if ext == "" {
		return base
	}
	return base + "." + ext
}
