// Package clone writes a file out of byte ranges of another file. On XFS
// and btrfs the ranges are reflinked, so the "copy" shares blocks with the
// car's disk image and costs neither space nor SD card writes. When the car
// later overwrites those blocks, the filesystem keeps the old ones for us.
package clone

import (
	"io"
	"os"

	"github.com/marcusaram/squirrel/internal/fatfs"
)

// Result reports how the data ended up in the destination.
type Result struct {
	Reflinked int64 // bytes shared with the source
	Copied    int64 // bytes physically copied
}

// Extract fills dst with the file described by ext (offsets in src) and
// truncates it to size. dst must be empty and opened read-write.
func Extract(src, dst *os.File, ext []fatfs.Extent, size int64) (Result, error) {
	var res Result
	bs := blockSize(dst)
	for i, e := range ext {
		last := i == len(ext)-1
		if bs > 0 && e.ImageOffset%bs == 0 && e.FileOffset%bs == 0 && (last || e.Length%bs == 0) {
			// The tail of the last cluster is cloned along with the data
			// and cut off by the truncate below.
			n := (e.Length + bs - 1) / bs * bs
			if err := cloneRange(src, dst, e.ImageOffset, n, e.FileOffset); err == nil {
				res.Reflinked += e.Length
				continue
			}
		}
		if err := copyRange(src, dst, e.ImageOffset, e.Length, e.FileOffset); err != nil {
			return res, err
		}
		res.Copied += e.Length
	}
	if err := dst.Truncate(size); err != nil {
		return res, err
	}
	return res, nil
}

func copyPortable(src, dst *os.File, off, n, dstOff int64) error {
	_, err := io.Copy(io.NewOffsetWriter(dst, dstOff), io.NewSectionReader(src, off, n))
	return err
}
