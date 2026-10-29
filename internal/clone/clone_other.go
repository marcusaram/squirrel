//go:build !linux

package clone

import (
	"errors"
	"os"
)

func blockSize(*os.File) int64 { return 0 }

func cloneRange(_, _ *os.File, _, _, _ int64) error {
	return errors.ErrUnsupported
}

func copyRange(src, dst *os.File, off, n, dstOff int64) error {
	return copyPortable(src, dst, off, n, dstOff)
}
