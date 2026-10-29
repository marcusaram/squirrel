package archive

import (
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// FIEMAP, from linux/fiemap.h and linux/fs.h.
const (
	fsIocFiemap        = 0xC020660B
	fiemapExtentLast   = 0x0001
	fiemapExtentShared = 0x2000
	fiemapBatch        = 64
)

type fiemapHeader struct {
	Start, Length        uint64
	Flags, Mapped, Count uint32
	_                    uint32
}

type fiemapExtent struct {
	Logical, Physical, Length uint64
	_                         [2]uint64
	Flags                     uint32
	_                         [3]uint32
}

// unsharedBytes returns how much space deleting the file would free now:
// the bytes in extents no other file (the car's image, a snapshot, another
// clip) shares.
func unsharedBytes(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	hdrSize := unsafe.Sizeof(fiemapHeader{})
	extSize := unsafe.Sizeof(fiemapExtent{})
	buf := make([]uint64, (hdrSize+fiemapBatch*extSize)/8)
	hdr := (*fiemapHeader)(unsafe.Pointer(&buf[0]))
	var total int64
	var start uint64
	for {
		*hdr = fiemapHeader{Start: start, Length: ^uint64(0), Count: fiemapBatch}
		if _, _, e := unix.Syscall(unix.SYS_IOCTL, f.Fd(), fsIocFiemap, uintptr(unsafe.Pointer(&buf[0]))); e != 0 {
			return 0, e
		}
		if hdr.Mapped == 0 {
			return total, nil
		}
		for i := uintptr(0); i < uintptr(hdr.Mapped); i++ {
			x := (*fiemapExtent)(unsafe.Pointer(uintptr(unsafe.Pointer(&buf[0])) + hdrSize + i*extSize))
			if x.Flags&fiemapExtentShared == 0 {
				total += int64(x.Length)
			}
			start = x.Logical + x.Length
			if x.Flags&fiemapExtentLast != 0 {
				return total, nil
			}
		}
	}
}
