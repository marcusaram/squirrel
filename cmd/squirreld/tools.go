package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/marcusaram/squirrel/internal/archive"
	"github.com/marcusaram/squirrel/internal/config"
	"github.com/marcusaram/squirrel/internal/driveimage"
	"github.com/marcusaram/squirrel/internal/fatfs"
	"github.com/marcusaram/squirrel/internal/gadget"
	"github.com/marcusaram/squirrel/internal/preserve"
	"github.com/marcusaram/squirrel/internal/state"
	"github.com/marcusaram/squirrel/internal/storage"
)

func allocated(fi os.FileInfo) int64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int64(st.Blocks) * 512
	}
	return fi.Size()
}

// inspect lists what is on a drive image without changing anything, so it
// is safe to run against the image the car is using.
func inspect(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: squirreld inspect IMAGE")
	}
	f, err := os.Open(args[0])
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	v, err := fatfs.Open(f, fi.Size())
	if err != nil {
		return err
	}
	fmt.Printf("filesystem:   %s\ncluster size: %d\npartition at: %d\ndirty:        %v\n",
		v.Type, v.ClusterSize, v.Offset, v.Dirty)

	var files, aligned, fragmented int
	var bytes int64
	err = v.Walk(v.Root(), func(e fatfs.Entry, err error) error {
		if err != nil {
			fmt.Printf("  error reading %s: %v\n", e.Path, err)
			return nil
		}
		if e.IsDir {
			return nil
		}
		ext, err := v.Extents(e)
		if err != nil {
			fmt.Printf("  error mapping %s: %v\n", e.Path, err)
			return nil
		}
		files++
		bytes += e.Size
		if len(ext) > 1 {
			fragmented++
		}
		ok := true
		for _, x := range ext {
			ok = ok && x.ImageOffset%4096 == 0
		}
		if ok {
			aligned++
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Printf("files:        %d (%.1f GB), %d fragmented\n", files, float64(bytes)/(1<<30), fragmented)
	fmt.Printf("4K aligned:   %d of %d (aligned files can be reflinked)\n", aligned, files)
	for _, dir := range []string{"TeslaCam/RecentClips", "TeslaCam/SavedClips", "TeslaCam/SentryClips", "TeslaTrackMode"} {
		e, err := v.Lookup(dir)
		if err != nil {
			continue
		}
		n := 0
		v.Walk(e, func(e fatfs.Entry, err error) error {
			if err == nil && !e.IsDir {
				n++
			}
			return nil
		})
		fmt.Printf("  %-22s %d files\n", dir, n)
	}
	return nil
}

func parseSize(s string) (int64, error) {
	s = strings.TrimSuffix(strings.ToUpper(strings.TrimSpace(s)), "B")
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "T"):
		mult = 1 << 40
	case strings.HasSuffix(s, "G"):
		mult = 1 << 30
	case strings.HasSuffix(s, "M"):
		mult = 1 << 20
	}
	n, err := strconv.ParseInt(strings.TrimRight(s, "TGM"), 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	return n * mult, nil
}

// createImage makes a drive image for the car; see driveimage.Create.
func createImage(args []string) error {
	fs := flag.NewFlagSet("create-image", flag.ExitOnError)
	sizeStr := fs.String("size", "", "image size, e.g. 64G, or a percentage of the disk, e.g. 40%")
	fsType := fs.String("fs", "exfat", "exfat or fat32")
	label := fs.String("label", "CAM", "volume label")
	fs.Parse(args)
	if fs.NArg() != 1 || *sizeStr == "" {
		return errors.New("usage: squirreld create-image -size 64G|40% [-fs exfat|fat32] [-label CAM] IMAGE")
	}
	path := fs.Arg(0)
	var size int64
	var err error
	if pct, ok := strings.CutSuffix(*sizeStr, "%"); ok {
		n, perr := strconv.Atoi(pct)
		if perr != nil {
			return fmt.Errorf("invalid size %q", *sizeStr)
		}
		size, err = driveimage.PercentOf(filepath.Dir(path), n)
	} else {
		size, err = parseSize(*sizeStr)
	}
	if err != nil {
		return err
	}
	return driveimage.Create(path, size, *fsType, *label, os.Stdout)
}

// importImages runs the preserver over static images. It writes to the
// same archive as the daemon, so the daemon must not run at the same time.
func importImages(args []string) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultPath, "config file")
	fs.Parse(args)
	if fs.NArg() == 0 {
		return errors.New("usage: squirreld import [-config FILE] IMAGE...")
	}
	store, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	cfg := store.Get()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	tomb, err := state.Open(cfg.StateDir + "/tombstones")
	if err != nil {
		return err
	}
	uploaded, err := state.Open(cfg.StateDir + "/uploaded")
	if err != nil {
		return err
	}
	ix := archive.NewIndex(cfg.ArchiveDir, uploaded)
	if err := ix.Load(log); err != nil {
		return err
	}
	p := preserve.New(store.Get, ix, tomb, log)
	total := 0
	for _, img := range fs.Args() {
		n, skipped, err := p.Import(img)
		if err != nil {
			fmt.Printf("%s: %v\n", img, err)
			continue
		}
		total += n
		fmt.Printf("%s: %d clips preserved, %d unfinished skipped\n", img, n, skipped)
		if filepath.Base(img) == "snap.bin" {
			// Lets the space manager delete this teslausb snapshot first
			// when space runs low.
			os.WriteFile(filepath.Join(filepath.Dir(img), archive.ImportedMarker), nil, 0o644)
		}
	}
	st := p.Status()
	fmt.Printf("done: %d files, %.1f MB shared via reflink, %.1f MB copied\n",
		total, float64(st.BytesShared)/(1<<20), float64(st.BytesCopied)/(1<<20))
	return nil
}

func setupStorage(args []string) error {
	fs := flag.NewFlagSet("setup-storage", flag.ExitOnError)
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	rootSize := fs.String("root-size", "8G", "grow the root partition to this size when there is room")
	minData := fs.String("min-data", "8G", "smallest acceptable data partition")
	usb := fs.Bool("usb", false, "put the data on the USB drive (one that is empty or set up earlier) instead of the card")
	usbDevice := fs.String("usb-device", "", "put the data on this USB drive, erasing what is on it")
	root := fs.String("root", "", "root partition (default: the one mounted at /)")
	fstab := fs.String("fstab", "/etc/fstab", "fstab to add the mount to")
	mp := fs.String("mountpoint", "/backingfiles", "where to mount the data partition")
	fs.Parse(args)
	rt, err := parseSize(*rootSize)
	if err != nil {
		return err
	}
	md, err := parseSize(*minData)
	if err != nil {
		return err
	}
	o := storage.Options{
		Root: *root, Fstab: *fstab, Mountpoint: *mp, RootTarget: rt, MinData: md,
		Log: func(f string, a ...any) { fmt.Printf(f+"\n", a...) },
	}
	if !*yes {
		o.Confirm = func(plan string) bool {
			fmt.Print("Plan:\n" + plan + "Type 'yes' to go ahead: ")
			var answer string
			fmt.Scanln(&answer)
			return answer == "yes"
		}
	}
	if *usb || *usbDevice != "" {
		return storage.SetupUSB(o, *usbDevice)
	}
	return storage.Setup(o)
}

// putFile copies a file into the root of a drive image, for reading on any
// computer (the first-start log on boards without a FAT boot partition).
func putFile(args []string) error {
	if len(args) != 3 {
		return errors.New("usage: squirreld put-file IMAGE NAME FILE")
	}
	if gadget.MountedLocally(args[0]) {
		return fmt.Errorf("%s is mounted; not writing to it", args[0])
	}
	data, err := os.ReadFile(args[2])
	if err != nil {
		return err
	}
	// FAT and exFAT on the drive; Windows reads plain LF text fine now, but
	// older Notepad does not.
	data = bytes.ReplaceAll(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")), []byte("\n"), []byte("\r\n"))
	return driveimage.WriteFile(args[0], args[1], data)
}
