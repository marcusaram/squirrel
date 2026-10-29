package fatfs

import (
	"encoding/binary"
	"fmt"
	"io"
	"path"
	"unicode/utf16"
)

func openExFAT(r io.ReaderAt, off, size int64, bs []byte) (*Volume, error) {
	bpsShift := bs[108]
	spcShift := bs[109]
	if bpsShift < 9 || bpsShift > 12 || int(spcShift) > 25-int(bpsShift) {
		return nil, fmt.Errorf("%w: exfat sector/cluster shift", ErrCorrupt)
	}
	bps := int64(1) << bpsShift
	v := &Volume{
		Type:         "exfat",
		ClusterSize:  bps << spcShift,
		Offset:       off,
		r:            r,
		size:         size,
		fatOffset:    off + int64(binary.LittleEndian.Uint32(bs[80:]))*bps,
		heapOffset:   off + int64(binary.LittleEndian.Uint32(bs[88:]))*bps,
		clusterCount: binary.LittleEndian.Uint32(bs[92:]),
		rootCluster:  binary.LittleEndian.Uint32(bs[96:]),
		eoc:          0xFFFFFFF7,
	}
	flags := binary.LittleEndian.Uint16(bs[106:])
	v.Dirty = flags&0x0002 != 0
	if bs[110] == 2 && flags&0x0001 != 0 {
		// second FAT is the active one
		v.fatOffset += int64(binary.LittleEndian.Uint32(bs[84:])) * bps
	}
	if v.heapOffset+int64(v.clusterCount)*v.ClusterSize > off+size {
		return nil, fmt.Errorf("%w: exfat cluster heap beyond end of image", ErrCorrupt)
	}
	v.readDir = v.readDirExFAT
	v.findBitmap()
	return v, nil
}

// findBitmap locates the allocation bitmap from its entry in the root
// directory. Without it, deleted files cannot be checked and stay lost.
func (v *Volume) findBitmap() {
	data, err := v.readDirData(v.rootCluster, 0, false)
	if err != nil {
		return
	}
	for i := 0; i+32 <= len(data) && data[i] != 0; i += 32 {
		if data[i] != 0x81 || data[i+1]&1 != 0 { // the first FAT's bitmap
			continue
		}
		off, err := v.clusterOffset(binary.LittleEndian.Uint32(data[i+20:]))
		if err == nil && int64(binary.LittleEndian.Uint64(data[i+24:]))*8 >= int64(v.clusterCount) {
			v.bitmap = off
		}
		return
	}
}

const (
	exfatEntryFile   = 0x85
	exfatEntryStream = 0xC0
	exfatEntryName   = 0xC1
)

func (v *Volume) readDirExFAT(dir Entry) ([]Entry, error) {
	size := dir.Size
	if dir.FirstCluster == v.rootCluster && dir.Path == "" {
		size = 0 // root has no size, follow the chain
	}
	data, err := v.readDirData(dir.FirstCluster, size, dir.contiguous)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for i := 0; i+32 <= len(data); {
		t := data[i]
		if t == 0x00 {
			break
		}
		if t != exfatEntryFile {
			i += 32
			continue
		}
		secondary := int(data[i+1])
		setLen := (secondary + 1) * 32
		if secondary < 2 || i+setLen > len(data) {
			i += 32
			continue
		}
		set := data[i : i+setLen]
		i += setLen
		if binary.LittleEndian.Uint16(set[2:]) != entrySetChecksum(set) {
			// half written or corrupt entry set; skip it this time
			continue
		}
		e, ok := parseExFATSet(set)
		if !ok {
			continue
		}
		e.Path = path.Join(dir.Path, e.Name)
		out = append(out, e)
	}
	return out, nil
}

func entrySetChecksum(set []byte) uint16 {
	var sum uint16
	for j, b := range set {
		if j == 2 || j == 3 {
			continue
		}
		sum = (sum<<15 | sum>>1) + uint16(b)
	}
	return sum
}

func parseExFATSet(set []byte) (Entry, bool) {
	stream := set[32:64]
	if stream[0] != exfatEntryStream {
		return Entry{}, false
	}
	nameLen := int(stream[3])
	var name []uint16
	for j := 64; j+32 <= len(set) && len(name) < nameLen; j += 32 {
		if set[j] != exfatEntryName {
			break
		}
		for k := 2; k < 32 && len(name) < nameLen; k += 2 {
			name = append(name, binary.LittleEndian.Uint16(set[j+k:]))
		}
	}
	if nameLen == 0 || len(name) != nameLen {
		return Entry{}, false
	}
	attr := binary.LittleEndian.Uint16(set[4:])
	vdl := int64(binary.LittleEndian.Uint64(stream[8:]))
	dataLen := int64(binary.LittleEndian.Uint64(stream[24:]))
	if vdl < 0 || dataLen < 0 {
		return Entry{}, false
	}
	return Entry{
		Name:         string(utf16.Decode(name)),
		IsDir:        attr&0x10 != 0,
		Size:         dataLen,
		FirstCluster: binary.LittleEndian.Uint32(stream[20:]),
		ModTime:      dosTime(binary.LittleEndian.Uint32(set[12:]), set[21]),
		Complete:     vdl == dataLen,
		contiguous:   stream[1]&0x02 != 0,
	}, true
}
