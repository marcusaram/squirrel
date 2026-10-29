package clock

import (
	"time"

	"golang.org/x/sys/unix"
)

func setTime(t time.Time) error {
	tv := unix.NsecToTimeval(t.UnixNano())
	return unix.Settimeofday(&tv)
}
