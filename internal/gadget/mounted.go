package gadget

import (
	"os"
	"path/filepath"
	"strings"
)

// mountedLocally reports whether image is attached to a loop device that is
// mounted on this machine, directly or through one of its partitions. The
// car must never get the drive while that is the case: two writers on one
// FAT filesystem corrupt it.
func mountedLocally(image string) bool {
	mounts, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return false
	}
	if real, err := filepath.EvalSymlinks(image); err == nil {
		image = real
	}
	backing, _ := filepath.Glob("/sys/block/loop*/loop/backing_file")
	for _, b := range backing {
		if strings.TrimSuffix(read(b), " (deleted)") != image {
			continue
		}
		dev := "/dev/" + filepath.Base(filepath.Dir(filepath.Dir(b)))
		for _, line := range strings.Split(string(mounts), "\n") {
			f := strings.Fields(line)
			if len(f) > 0 && (f[0] == dev || strings.HasPrefix(f[0], dev+"p")) {
				return true
			}
		}
	}
	return false
}

// MountedLocally reports whether the image is mounted on this machine.
func MountedLocally(image string) bool { return mountedLocally(image) }
