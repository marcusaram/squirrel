package clone

import (
	"os"

	"golang.org/x/sys/unix"
)

func blockSize(f *os.File) int64 {
	var st unix.Statfs_t
	if err := unix.Fstatfs(int(f.Fd()), &st); err != nil {
		return 0
	}
	return int64(st.Bsize)
}

func cloneRange(src, dst *os.File, off, n, dstOff int64) error {
	return unix.IoctlFileCloneRange(int(dst.Fd()), &unix.FileCloneRange{
		Src_fd:      int64(src.Fd()),
		Src_offset:  uint64(off),
		Src_length:  uint64(n),
		Dest_offset: uint64(dstOff),
	})
}

// copyRange copies inside the kernel. copy_file_range still reflinks
// whatever part of the range is block aligned.
func copyRange(src, dst *os.File, off, n, dstOff int64) error {
	for n > 0 {
		chunk := n
		if chunk > 1<<30 {
			chunk = 1 << 30
		}
		w, err := unix.CopyFileRange(int(src.Fd()), &off, int(dst.Fd()), &dstOff, int(chunk), 0)
		if err != nil {
			if err == unix.EXDEV || err == unix.ENOSYS || err == unix.EOPNOTSUPP || err == unix.EINVAL {
				return copyPortable(src, dst, off, n, dstOff)
			}
			return err
		}
		if w == 0 {
			return copyPortable(src, dst, off, n, dstOff)
		}
		n -= int64(w)
	}
	return nil
}
