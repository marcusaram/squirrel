package maker

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ulikunitz/xz"

	"github.com/marcusaram/squirrel/internal/fatfs"
)

// Image is an OS release the maker knows how to adapt.
type Image struct {
	Name   string
	URL    string
	SHA256 string
	Size   int64
}

// DefaultImage is the release this was tested with. A different release
// may change how the first boot works (it did between Bookworm and
// Trixie), so it is pinned rather than "latest".
var DefaultImage = Image{
	Name:   "Raspberry Pi OS Lite (64-bit) 2026-10-06",
	URL:    "https://downloads.raspberrypi.com/raspios_lite_arm64/images/raspios_lite_arm64-2026-10-06/2026-10-06-raspios-trixie-arm64-lite.img.xz",
	SHA256: "483db18a48da399b5b7022ffae9a07bc6d99daab30e593aedac09b69ff422843",
	Size:   550466056,
}

// PiOS32Image is the same release in 32 bits (armhf, built for ARMv6), for
// the original Pi Zero W, which cannot run the 64-bit one.
var PiOS32Image = Image{
	Name:   "Raspberry Pi OS Lite (32-bit) 2026-10-06",
	URL:    "https://downloads.raspberrypi.com/raspios_lite_armhf/images/raspios_lite_armhf-2026-10-06/2026-10-06-raspios-trixie-armhf-lite.img.xz",
	SHA256: "2c302771f34c67a0b7ae7aa2e0dcdd3eac660504be59193c011ba79d412e1632",
	Size:   569747828,
}

// Progress reports a step and how far along it is (0..1, or -1 when
// unknown).
type Progress func(step string, frac float64)

type countingWriter struct {
	n      int64
	total  int64
	step   string
	report Progress
	last   int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	if c.n-c.last > 4<<20 || c.n == c.total {
		c.last = c.n
		if c.total > 0 {
			c.report(c.step, float64(c.n)/float64(c.total))
		}
	}
	return len(p), nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Fetch downloads the compressed image into dir, unless a copy with the
// right hash is already there, and returns its path.
func Fetch(ctx context.Context, img Image, dir string, report Progress) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, filepath.Base(img.URL))
	if _, err := os.Stat(dst); err == nil {
		report("Checking the downloaded image", -1)
		if sum, err := fileSHA256(dst); err == nil && sum == img.SHA256 {
			return dst, nil
		}
		os.Remove(dst)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", img.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download: %s", resp.Status)
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	cw := &countingWriter{total: img.Size, step: "Downloading " + img.Name, report: report}
	_, err = io.Copy(io.MultiWriter(f, h, cw), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	if sum := hex.EncodeToString(h.Sum(nil)); sum != img.SHA256 {
		os.Remove(tmp)
		return "", fmt.Errorf("the download is corrupt (SHA-256 %s, expected %s)", sum, img.SHA256)
	}
	return dst, os.Rename(tmp, dst)
}

// Decompress unpacks the .img.xz. The xz tool is used when installed; it
// is about fifteen times faster than the Go decoder.
func Decompress(ctx context.Context, src, dst string, report Progress) error {
	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	in, err := os.Open(src)
	if err != nil {
		out.Close()
		return err
	}
	defer in.Close()
	fi, _ := in.Stat()
	// Progress follows the compressed input.
	cw := &countingWriter{total: fi.Size(), step: "Unpacking the image", report: report}
	input := io.TeeReader(in, cw)

	if xzPath, err := lookXZ(); err == nil {
		cmd := exec.CommandContext(ctx, xzPath, "-dc", "-T0")
		cmd.Stdin = input
		cmd.Stdout = out
		err = cmd.Run()
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("xz: %w", err)
		}
	} else {
		r, err := xz.NewReader(bufio.NewReaderSize(input, 1<<20))
		if err != nil {
			out.Close()
			return err
		}
		_, err = io.Copy(out, r)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return os.Rename(tmp, dst)
}

func lookXZ() (string, error) {
	if p, err := exec.LookPath("xz"); err == nil {
		return p, nil
	}
	for _, p := range []string{"/opt/homebrew/bin/xz", "/usr/local/bin/xz"} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", errors.New("xz not found")
}

// CheckLayout verifies the image is the Raspberry Pi OS layout this was
// built for: a FAT32 boot partition followed by a Linux root partition.
func CheckLayout(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	mbr := make([]byte, 512)
	if _, err := f.ReadAt(mbr, 0); err != nil {
		return err
	}
	if mbr[510] != 0x55 || mbr[511] != 0xAA {
		return errors.New("no MBR partition table")
	}
	p := func(i int) (byte, uint32) {
		e := mbr[446+16*i:]
		return e[4], binary.LittleEndian.Uint32(e[8:])
	}
	t1, _ := p(0)
	t2, _ := p(1)
	t3, _ := p(2)
	t4, _ := p(3)
	if (t1 != 0x0c && t1 != 0x0b) || t2 != 0x83 || t3 != 0 || t4 != 0 {
		return fmt.Errorf("unexpected partition layout (%#x %#x %#x %#x)", t1, t2, t3, t4)
	}
	return nil
}

// ReadBootFile reads a file from the boot partition of an image without
// mounting it, to check what was written.
func ReadBootFile(image, name string) ([]byte, error) {
	f, err := os.Open(image)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, _ := f.Stat()
	v, err := fatfs.Open(f, fi.Size())
	if err != nil {
		return nil, err
	}
	e, err := v.Lookup(name)
	if err != nil {
		return nil, err
	}
	r, err := v.OpenFile(e)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r.NewReader())
}

// bootFiles collects everything that goes on the boot partition: the
// generated settings plus the payload (daemon, scripts, packages) under
// squirrel/.
func bootFiles(s Settings, payload fs.FS) (map[string][]byte, error) {
	files, err := s.Files()
	if err != nil {
		return nil, err
	}
	err = fs.WalkDir(payload, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(payload, p)
		if err != nil {
			return err
		}
		files["squirrel/"+p] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	if _, ok := files["squirrel/squirreld"]; !ok {
		return nil, errors.New("this build of squirrel-maker has no squirreld inside; build it with `make maker`")
	}
	return files, nil
}
