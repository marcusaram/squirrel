package sysinfo

import "golang.org/x/sys/unix"

type Disk struct {
	Total uint64 `json:"total"`
	Free  uint64 `json:"free"` // available to unprivileged writers
}

func DiskUsage(path string) (Disk, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return Disk{}, err
	}
	bs := uint64(st.Bsize)
	return Disk{Total: st.Blocks * bs, Free: st.Bavail * bs}, nil
}
