//go:build !linux

package archive

import "os"

// unsharedBytes assumes no sharing where FIEMAP is not available.
func unsharedBytes(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}
