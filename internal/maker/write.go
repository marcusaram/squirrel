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

// WriteRaw copies image onto an opened raw disk and reads it back to
// compare. Writes are kept in whole 512 byte sectors.
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
	want := sha256.New()
	buf := make([]byte, chunk)
	var done int64
	for done < size {
		n, err := io.ReadFull(in, buf)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			return err
		}
		if n == 0 {
			break
		}
		want.Write(buf[:n])
		w := (n + 511) / 512 * 512
		clear(buf[n:w])
		if _, err := out.Write(buf[:w]); err != nil {
			return fmt.Errorf("writing to %s: %w", device, err)
		}
		done += int64(n)
		report("Writing the SD card", float64(done)/float64(size))
	}
	// Raw disk nodes on macOS are unbuffered and do not support fsync.
	if err := out.Sync(); err != nil && !errors.Is(err, syscall.ENOTTY) {
		return err
	}
	back := out
	if _, err := back.Seek(0, io.SeekStart); err != nil {
		return err
	}
	got := sha256.New()
	var read int64
	for read < size {
		w := min(int64(chunk), (size-read+511)/512*512)
		n, err := io.ReadFull(back, buf[:w])
		if err != nil && n == 0 {
			return fmt.Errorf("reading back: %w", err)
		}
		got.Write(buf[:min(int64(n), size-read)])
		read += int64(n)
		report("Verifying", float64(read)/float64(size))
	}
	if !bytes.Equal(got.Sum(nil), want.Sum(nil)) {
		return errors.New("the card does not hold what was written; try another card")
	}
	return nil
}
