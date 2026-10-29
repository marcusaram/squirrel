package maker

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

const chunk = 4 << 20

// headLast is how much of the start of the image (the partition table and
// what follows) is written last: until then the card has no partitions a
// computer would try to mount while the rest is written.
const headLast = 1 << 20

// WriteRaw copies image onto an opened raw disk, the first megabyte last,
// and reads it back to compare. Writes are kept in whole 512 byte sectors
// at sector offsets, which raw disks on macOS and Windows require.
func WriteRaw(image string, out *os.File, report Progress) error {
	device := out.Name()
	in, err := os.Open(image)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	size := fi.Size()
	buf := make([]byte, chunk)
	var done int64
	write := func(from, to int64) error {
		for at := from; at < to; {
			n := min(int64(chunk), to-at)
			m, err := in.ReadAt(buf[:n], at)
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			w := (int64(m) + 511) / 512 * 512
			clear(buf[m:w])
			if _, err := out.WriteAt(buf[:w], at); err != nil {
				return fmt.Errorf("writing to %s: %w", device, err)
			}
			at += n
			done += n
			report("Writing the SD card", float64(done)/float64(size))
		}
		return nil
	}
	head := min(int64(headLast), size)
	if err := write(head, size); err != nil {
		return err
	}
	if err := write(0, head); err != nil {
		return err
	}
	// Raw disk nodes on macOS are unbuffered and do not support fsync.
	if err := out.Sync(); err != nil && !errors.Is(err, syscall.ENOTTY) {
		return err
	}

	want, got := sha256.New(), sha256.New()
	cmp := make([]byte, chunk)
	var read int64
	for read < size {
		n := min(int64(chunk), size-read)
		w := (n + 511) / 512 * 512
		if _, err := in.ReadAt(buf[:n], read); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		m, err := out.ReadAt(cmp[:w], read)
		if int64(m) < n {
			return fmt.Errorf("reading back: %w", err)
		}
		want.Write(buf[:n])
		got.Write(cmp[:n])
		read += n
		report("Verifying", float64(read)/float64(size))
	}
	if !bytes.Equal(got.Sum(nil), want.Sum(nil)) {
		return errors.New("the card does not hold what was written; try another card")
	}
	return nil
}
