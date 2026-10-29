package maker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// plist runs a macOS tool that prints a property list and decodes it.
func plist(ctx context.Context, v any, name string, args ...string) error {
	out, err := command(ctx, name, args...)
	if err != nil {
		return err
	}
	conv := exec.CommandContext(ctx, "plutil", "-convert", "json", "-o", "-", "-")
	conv.Stdin = bytes.NewReader(out)
	js, err := conv.Output()
	if err != nil {
		return fmt.Errorf("plutil: %w", err)
	}
	return json.Unmarshal(js, v)
}

// Customize writes the settings and payload onto the boot partition of an
// uncompressed Raspberry Pi OS image. macOS mounts the FAT partition
// itself, so no root rights or extra tools are needed.
func Customize(ctx context.Context, image string, s Settings, payload fs.FS, report Progress) error {
	if err := CheckLayout(image); err != nil {
		return err
	}
	files, err := bootFiles(s, payload)
	if err != nil {
		return err
	}
	report("Adapting the image", -1)
	out, err := command(ctx, "hdiutil", "attach", "-imagekey", "diskimage-class=CRawDiskImage", "-nomount", image)
	if err != nil {
		return err
	}
	disk := strings.Fields(string(out))[0] // e.g. /dev/disk7
	defer command(context.Background(), "hdiutil", "detach", "-force", disk)

	if _, err := command(ctx, "diskutil", "mount", disk+"s1"); err != nil {
		return err
	}
	var info struct{ MountPoint string }
	if err := plist(ctx, &info, "diskutil", "info", "-plist", disk+"s1"); err != nil {
		return err
	}
	if info.MountPoint == "" {
		return fmt.Errorf("boot partition of %s did not mount", disk)
	}
	mp := info.MountPoint
	if _, err := os.Stat(filepath.Join(mp, "cmdline.txt")); err != nil {
		return fmt.Errorf("%s does not look like a Raspberry Pi boot partition", mp)
	}

	cmdline, err := os.ReadFile(filepath.Join(mp, "cmdline.txt"))
	if err != nil {
		return err
	}
	newCmdline, err := EditCmdline(string(cmdline), s.Country)
	if err != nil {
		return err
	}
	files["cmdline.txt"] = []byte(newCmdline)
	cfgTxt, err := os.ReadFile(filepath.Join(mp, "config.txt"))
	if err != nil {
		return err
	}
	files["config.txt"] = []byte(EditConfig(string(cfgTxt), s.SDOverclock))

	for name, data := range files {
		p := filepath.Join(mp, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
	}
	// macOS stores extended attributes on FAT as ._ files; the Pi does not
	// need them.
	exec.Command("dot_clean", "-m", mp).Run()
	for _, junk := range []string{".fseventsd", ".Spotlight-V100", ".Trashes"} {
		os.RemoveAll(filepath.Join(mp, junk))
	}
	exec.Command("sync").Run()
	if _, err := command(ctx, "diskutil", "unmount", disk+"s1"); err != nil {
		return err
	}
	if _, err := command(ctx, "hdiutil", "detach", disk); err != nil {
		return err
	}

	// Read back through our own FAT reader, independent of macOS.
	for _, name := range []string{"cmdline.txt", "user-data", "network-config", "squirrel/squirreld"} {
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
