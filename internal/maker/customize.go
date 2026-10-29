package maker

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"time"

	"github.com/marcusaram/squirrel/internal/fatfs"
)

// Customize writes the settings and payload onto the boot partition of an
// uncompressed Raspberry Pi OS image, with the fatfs package: no mounting,
// no root rights and no tools, on macOS and Windows alike.
func Customize(ctx context.Context, image string, s Settings, payload fs.FS, report Progress) error {
	if err := CheckLayout(image); err != nil {
		return err
	}
	files, err := bootFiles(s, payload)
	if err != nil {
		return err
	}
	report("Adapting the image", -1)
	cmdline, err := ReadBootFile(image, "cmdline.txt")
	if err != nil {
		return fmt.Errorf("this does not look like a Raspberry Pi boot partition: %w", err)
	}
	newCmdline, err := EditCmdline(string(cmdline), s.Country)
	if err != nil {
		return err
	}
	files["cmdline.txt"] = []byte(newCmdline)
	cfgTxt, err := ReadBootFile(image, "config.txt")
	if err != nil {
		return err
	}
	files["config.txt"] = []byte(EditConfig(string(cfgTxt), s.SDOverclock))

	f, err := os.OpenFile(image, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	v, err := fatfs.OpenRW(f, fi.Size())
	if err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	dirs := map[string]bool{}
	for name := range files {
		names = append(names, name)
		for d := path.Dir(name); d != "."; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	// Parents before children.
	var dirList []string
	for d := range dirs {
		dirList = append(dirList, d)
	}
	sort.Strings(dirList)
	sort.Strings(names)
	now := time.Now()
	for _, d := range dirList {
		if err := v.Mkdir(d, now); err != nil {
			return fmt.Errorf("making %s: %w", d, err)
		}
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := v.WriteFile(name, files[name], now); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
	}
	if err := f.Sync(); err != nil {
		return err
	}

	// Read everything back through a fresh open.
	for _, name := range names {
		got, err := ReadBootFile(image, name)
		if err != nil {
			return fmt.Errorf("checking %s: %w", name, err)
		}
		if !bytes.Equal(got, files[name]) {
			return fmt.Errorf("checking %s: content differs after writing", name)
		}
	}
	return nil
}
