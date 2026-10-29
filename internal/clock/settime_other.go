//go:build !linux

package clock

import (
	"errors"
	"time"
)

func setTime(time.Time) error { return errors.ErrUnsupported }
