//go:build !linux

package nbd

import (
	"errors"
	"io"
)

// Device is a running block device; only Linux has them.
type Device struct{ Path string }

func Serve(r io.ReaderAt, size int64) (*Device, error) {
	return nil, errors.New("nbd: block devices need Linux")
}

func (d *Device) Close() error { return nil }
