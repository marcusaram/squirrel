// Package nbd turns an io.ReaderAt into a read-only block device
// (/dev/nbdN) through the kernel's network block device driver, served
// from this process over a socket pair. No nbd-client is needed.
package nbd

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// ioctls from linux/nbd.h.
const (
	nbdSetSock       = 0xab00
	nbdSetBlksize    = 0xab01
	nbdDoIt          = 0xab03
	nbdClearSock     = 0xab04
	nbdClearQue      = 0xab05
	nbdSetSizeBlocks = 0xab07
	nbdDisconnect    = 0xab08
	nbdSetFlags      = 0xab0a

	flagHasFlags = 1 << 0
	flagReadOnly = 1 << 1

	requestMagic = 0x25609513
	replyMagic   = 0x67446698
	cmdRead      = 0
	cmdDisc      = 2
	blockSize    = 512
	maxRequest   = 32 << 20
)

// Device is a running block device.
type Device struct {
	Path string // e.g. /dev/nbd0

	dev  *os.File
	sock *os.File
	done chan error
	once sync.Once
}

// Serve makes r, of size bytes, available read-only at a free /dev/nbdN.
func Serve(r io.ReaderAt, size int64) (*Device, error) {
	if size%blockSize != 0 {
		return nil, fmt.Errorf("nbd: size %d is not a multiple of %d", size, blockSize)
	}
	if _, err := os.Stat("/sys/module/nbd"); err != nil {
		if out, err := exec.Command("modprobe", "nbd").CombinedOutput(); err != nil {
			return nil, fmt.Errorf("nbd: modprobe: %v: %s", err, out)
		}
	}
	path, err := freeDevice()
	if err != nil {
		return nil, err
	}
	dev, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		dev.Close()
		return nil, err
	}
	kernel, ours := os.NewFile(uintptr(fds[0]), "nbd-kernel"), os.NewFile(uintptr(fds[1]), "nbd-server")
	fd := int(dev.Fd())
	setup := []struct {
		req uint
		arg int
	}{
		{nbdClearSock, 0},
		{nbdSetBlksize, blockSize},
		{nbdSetSizeBlocks, int(size / blockSize)},
		{nbdSetFlags, flagHasFlags | flagReadOnly},
		{nbdSetSock, int(kernel.Fd())},
	}
	for _, s := range setup {
		if err := unix.IoctlSetInt(fd, s.req, s.arg); err != nil {
			dev.Close()
			kernel.Close()
			ours.Close()
			return nil, fmt.Errorf("nbd: ioctl %#x on %s: %w", s.req, path, err)
		}
	}
	d := &Device{Path: path, dev: dev, sock: ours, done: make(chan error, 1)}
	go func() {
		// NBD_DO_IT runs the device until it is disconnected.
		runtime.LockOSThread()
		err := unix.IoctlSetInt(fd, nbdDoIt, 0)
		unix.IoctlSetInt(fd, nbdClearQue, 0)
		unix.IoctlSetInt(fd, nbdClearSock, 0)
		kernel.Close()
		d.done <- err
	}()
	go d.serve(r, size)
	// The kernel sets the size once NBD_DO_IT runs, and only after the
	// device's pid shows: until the size is there, the device is empty
	// (and the USB gadget refuses it as "too small", errno 525).
	for i := 0; i < 100; i++ {
		if n, err := Size(path); err == nil && n == size {
			return d, nil
		}
		select {
		case err := <-d.done:
			ours.Close()
			dev.Close()
			return nil, fmt.Errorf("nbd: %s did not start: %v", path, err)
		case <-time.After(50 * time.Millisecond):
		}
	}
	d.Close()
	return nil, fmt.Errorf("nbd: %s did not start", path)
}

// freeDevice finds an nbd device that is not in use.
func freeDevice() (string, error) {
	devs, _ := filepath.Glob("/sys/block/nbd*")
	for _, s := range devs {
		if _, err := os.Stat(filepath.Join(s, "pid")); err == nil {
			continue // in use
		}
		if b, err := os.ReadFile(filepath.Join(s, "size")); err == nil && string(b) != "0\n" {
			continue
		}
		return "/dev/" + filepath.Base(s), nil
	}
	return "", errors.New("nbd: no free device")
}

func (d *Device) serve(r io.ReaderAt, size int64) {
	var hdr [28]byte
	buf := make([]byte, 0, 1<<20)
	for {
		if _, err := io.ReadFull(d.sock, hdr[:]); err != nil {
			return
		}
		if binary.BigEndian.Uint32(hdr[0:]) != requestMagic {
			return
		}
		typ := binary.BigEndian.Uint32(hdr[4:]) & 0xffff
		handle := hdr[8:16]
		off := int64(binary.BigEndian.Uint64(hdr[16:]))
		n := int64(binary.BigEndian.Uint32(hdr[24:]))
		if typ == cmdDisc {
			return
		}
		errno := uint32(0)
		var data []byte
		switch {
		case typ != cmdRead:
			errno = uint32(unix.EPERM) // read-only
		case n > maxRequest || off < 0 || off+n > size:
			errno = uint32(unix.EINVAL)
		default:
			if int64(cap(buf)) < n {
				buf = make([]byte, n)
			}
			data = buf[:n]
			if m, err := r.ReadAt(data, off); int64(m) < n && err != nil {
				errno, data = uint32(unix.EIO), nil
			}
		}
		var rep [16]byte
		binary.BigEndian.PutUint32(rep[0:], replyMagic)
		binary.BigEndian.PutUint32(rep[4:], errno)
		copy(rep[8:], handle)
		if _, err := d.sock.Write(rep[:]); err != nil {
			return
		}
		if _, err := d.sock.Write(data); err != nil {
			return
		}
	}
}

// Close disconnects the device and waits for the kernel to let go.
func (d *Device) Close() error {
	var err error
	d.once.Do(func() {
		unix.IoctlSetInt(int(d.dev.Fd()), nbdDisconnect, 0)
		d.sock.Close()
		<-d.done
		err = d.dev.Close()
	})
	return err
}

// Size reports the size the kernel sees, for checks.
func Size(path string) (int64, error) {
	b, err := os.ReadFile(filepath.Join("/sys/block", filepath.Base(path), "size"))
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(string(b[:len(b)-1]), 10, 64)
	return n * 512, err
}
