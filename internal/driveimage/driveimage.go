// Package driveimage creates the disk images the car records to.
package driveimage

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/marcusaram/squirrel/internal/fatfs"
)

const startSector = 2048 // partition at 1 MiB keeps clusters 4K aligned

// PercentOf returns pct percent of the filesystem that holds dir.
func PercentOf(dir string, pct int) (int64, error) {
	if pct < 1 || pct > 90 {
		return 0, fmt.Errorf("invalid percentage %d", pct)
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Blocks) * int64(st.Bsize) * int64(pct) / 100, nil
}

func loop(path string, size int64) (string, error) {
	out, err := exec.Command("losetup", "--find", "--show", "--offset", strconv.Itoa(startSector*512),
		"--sizelimit", strconv.FormatInt(size-startSector*512, 10), path).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("losetup: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

// Create makes a sparse image with an MBR and one partition at 1 MiB,
// formatted for the car and with the TeslaCam folder the car needs before
// it records anything.
func Create(path string, size int64, fsType, label string, log io.Writer) error {
	size &^= 1<<20 - 1
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists; not overwriting", path)
	}
	var ptype byte
	var mkfs []string
	switch fsType {
	case "exfat":
		ptype, mkfs = 0x07, []string{"mkfs.exfat", "-L", label}
	case "fat32":
		ptype, mkfs = 0x0C, []string{"mkfs.vfat", "-F", "32", "-n", label}
	default:
		return fmt.Errorf("unknown filesystem %q", fsType)
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	mbr := make([]byte, 512)
	rand.Read(mbr[440:444]) // disk signature
	pe := mbr[446:462]
	pe[1], pe[2], pe[3] = 0xFE, 0xFF, 0xFF // CHS: use LBA
	pe[4] = ptype
	pe[5], pe[6], pe[7] = 0xFE, 0xFF, 0xFF
	binary.LittleEndian.PutUint32(pe[8:], startSector)
	binary.LittleEndian.PutUint32(pe[12:], uint32(size/512-startSector))
	mbr[510], mbr[511] = 0x55, 0xAA
	_, err = f.WriteAt(mbr, 0)
	f.Close()
	if err != nil {
		os.Remove(path)
		return err
	}

	dev, err := loop(path, size)
	if err != nil {
		os.Remove(path)
		return err
	}
	cmd := exec.Command(mkfs[0], append(mkfs[1:], dev)...)
	cmd.Stdout, cmd.Stderr = log, log
	err = cmd.Run()
	exec.Command("losetup", "-d", dev).Run()
	if err != nil {
		os.Remove(path)
		return fmt.Errorf("%s: %w", mkfs[0], err)
	}
	if err := EnsureTeslaCam(path); err != nil {
		os.Remove(path)
		return err
	}
	fmt.Fprintf(log, "created %s (%d GB, %s)\n", path, size>>30, fsType)
	return nil
}

// HasTeslaCam reports whether the image has the TeslaCam folder. It only
// reads.
func HasTeslaCam(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return false, err
	}
	v, err := fatfs.Open(f, fi.Size())
	if err != nil {
		return false, err
	}
	_, err = v.Lookup("TeslaCam")
	if errors.Is(err, fatfs.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// EnsureTeslaCam creates the TeslaCam folder when it is missing. The image
// must not be in use by the car: it is mounted here.
func EnsureTeslaCam(path string) error {
	if ok, err := HasTeslaCam(path); err != nil || ok {
		return err
	}
	return withMounted(path, func(mnt string) error {
		return os.Mkdir(filepath.Join(mnt, "TeslaCam"), 0o755)
	})
}

// WriteFile puts a file in the root of the image. Like EnsureTeslaCam it
// mounts the image, so the car must not be using it.
func WriteFile(path, name string, data []byte) error {
	return withMounted(path, func(mnt string) error {
		return os.WriteFile(filepath.Join(mnt, name), data, 0o644)
	})
}

func withMounted(path string, fn func(mnt string) error) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	dev, err := loop(path, fi.Size())
	if err != nil {
		return err
	}
	defer exec.Command("losetup", "-d", dev).Run()
	mnt, err := os.MkdirTemp("", "squirreld-cam")
	if err != nil {
		return err
	}
	defer os.Remove(mnt)
	if out, err := exec.Command("mount", dev, mnt).CombinedOutput(); err != nil {
		return fmt.Errorf("mount: %v: %s", err, out)
	}
	err = fn(mnt)
	if out, uerr := exec.Command("umount", mnt).CombinedOutput(); uerr != nil && err == nil {
		err = fmt.Errorf("umount: %v: %s", uerr, out)
	}
	return err
}
