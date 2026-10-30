package maker

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/marcusaram/squirrel/internal/ext4"
)

// Armbian has no FAT partition: everything, /boot included, is on one
// ext4 root. The maker puts its files there with the ext4 package, which
// changes the file system without mounting it, on macOS and Windows
// alike.
//
// What goes on the card:
//   - /boot/squirrel: squirreld, the scripts, the packages Armbian minimal
//     lacks (NetworkManager, xfsprogs, exfatprogs), the USB overlay, and
//     setup.env with the user's settings for firstboot.sh;
//   - squirrel-firstboot.service, enabled;
//   - Armbian's own first start defused: no root filesystem resize (the
//     data partition goes after root), no interactive first login, no
//     console logged in as root, and root's password 1234 locked.

const armbianDir = "/boot/squirrel"

type ext4File struct {
	data []byte
	mode fs.FileMode
	gid  int
}

// armbianRootStart is where Armbian's root partition starts, in bytes.
// CheckArmbianLayout verifies it.
const armbianRootStart = 32768 * 512

// CheckArmbianLayout verifies the image is Armbian's layout for this
// board: an MBR with a single Linux partition at 16 MiB, the bootloader in
// front of it.
func CheckArmbianLayout(image string) error {
	f, err := os.Open(image)
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
	for i := 0; i < 4; i++ {
		e := mbr[446+16*i:]
		typ, start := e[4], binary.LittleEndian.Uint32(e[8:])
		switch {
		case i == 0 && (typ != 0x83 || int64(start)*512 != armbianRootStart):
			return fmt.Errorf("unexpected first partition (type %#x at sector %d)", typ, start)
		case i > 0 && typ != 0:
			return fmt.Errorf("unexpected partition %d (type %#x)", i+1, typ)
		}
	}
	return nil
}

// shq quotes a value for bash.
func shq(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

// ArmbianSetup is setup.env: the settings firstboot.sh applies on Armbian,
// where there is no cloud-init.
func (s Settings) ArmbianSetup() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	hash := ""
	if s.Password != "" {
		hash = HashPassword(s.Password)
	}
	var b strings.Builder
	b.WriteString("# Written by squirrel-maker; read by firstboot.sh and removed after.\n")
	for _, kv := range [][2]string{
		{"TUSB_HOSTNAME", s.Hostname}, {"TUSB_USER", s.User}, {"TUSB_PASSWORD_HASH", hash},
		{"TUSB_SSH_KEY", s.SSHKey}, {"TUSB_COUNTRY", s.Country}, {"TUSB_TIMEZONE", s.Timezone},
	} {
		fmt.Fprintf(&b, "%s=%s\n", kv[0], shq(kv[1]))
	}
	var ssids, psks []string
	for _, n := range s.WiFi {
		ssids = append(ssids, shq(n.SSID))
		psks = append(psks, shq(n.Password))
	}
	fmt.Fprintf(&b, "TUSB_WIFI_SSID=(%s)\nTUSB_WIFI_PSK=(%s)\n", strings.Join(ssids, " "), strings.Join(psks, " "))
	return []byte(b.String()), nil
}

// armbianFiles collects what goes on the root, by path. payload is the
// app's payload directory: files/ (shared with the Raspberry Pi) and
// armbian/.
func armbianFiles(s Settings, payload fs.FS) (map[string]ext4File, error) {
	files, err := armbianProgram(payload)
	if err != nil {
		return nil, err
	}
	setup, err := s.ArmbianSetup()
	if err != nil {
		return nil, err
	}
	files[armbianDir+"/setup.env"] = ext4File{data: setup, mode: 0o600}
	shared, err := s.Files()
	if err != nil {
		return nil, err
	}
	files[armbianDir+"/config.json"] = ext4File{data: shared["squirrel/config.json"], mode: 0o600}
	files[armbianDir+"/cam-size"] = ext4File{data: shared["squirrel/cam-size"], mode: 0o644}
	if st, ok := shared["squirrel/storage"]; ok {
		files[armbianDir+"/storage"] = ext4File{data: st, mode: 0o644}
	}
	// The data partition goes after root; Armbian must not grow root into
	// that space first.
	files["/root/.no_rootfs_resize"] = ext4File{mode: 0o644}
	return files, nil
}

// armbianProgram is Squirrel itself on the root, without settings: what a
// new card gets and an update replaces.
func armbianProgram(payload fs.FS) (map[string]ext4File, error) {
	return armbianProgramIn(payload, armbianDir, true)
}

// armbianProgramIn puts it in dir; unit adds the first-start unit, which a
// card from before Squirrel does not get (its own unit runs from dir).
func armbianProgramIn(payload fs.FS, dir string, unit bool) (map[string]ext4File, error) {
	armbianDir := dir
	files := map[string]ext4File{}
	add := func(dst, src string, mode fs.FileMode) error {
		data, err := fs.ReadFile(payload, src)
		if err != nil {
			return err
		}
		files[dst] = ext4File{data: data, mode: mode}
		return nil
	}
	for _, f := range []struct {
		name string
		mode fs.FileMode
	}{{"squirreld", 0o755}, {"firstboot.sh", 0o755}, {"install.sh", 0o755}, {"squirreld.service", 0o644}} {
		if err := add(armbianDir+"/"+f.name, "files/"+f.name, f.mode); err != nil {
			return nil, fmt.Errorf("this build of squirrel-maker lacks %s; build it with `make maker`: %w", f.name, err)
		}
	}
	// Which Squirrel is on the card, for a later update.
	if _, err := fs.Stat(payload, "files/version"); err == nil {
		if err := add(armbianDir+"/version", "files/version", 0o644); err != nil {
			return nil, err
		}
	}
	if err := add(armbianDir+"/dwc3-0-device.dts", "armbian/dwc3-0-device.dts", 0o644); err != nil {
		return nil, err
	}
	if unit {
		if err := add("/etc/systemd/system/squirrel-firstboot.service", "armbian/squirrel-firstboot.service", 0o644); err != nil {
			return nil, err
		}
	}
	debs, err := fs.Glob(payload, "armbian/debs/*.deb")
	if err != nil || len(debs) == 0 {
		return nil, errors.New("this build of squirrel-maker has no packages for Armbian; build it with `make maker`")
	}
	for _, d := range debs {
		if err := add(armbianDir+"/debs/"+path.Base(d), d, 0o644); err != nil {
			return nil, err
		}
	}
	return files, nil
}

// armbianRemove are Armbian's first-start steps that would get in the way:
// growing root over the whole card, the interactive first login, and the
// consoles that log in as root without a password.
var armbianRemove = []string{
	"/etc/systemd/system/multi-user.target.wants/armbian-resize-filesystem.service",
	"/root/.not_logged_in_yet",
	"/etc/systemd/system/getty@.service.d/override.conf",
	"/etc/systemd/system/serial-getty@.service.d/override.conf",
}

const (
	firstbootUnit  = "/etc/systemd/system/squirrel-firstboot.service"
	firstbootWants = "/etc/systemd/system/multi-user.target.wants/squirrel-firstboot.service"
)

// lockRoot locks root's password (1234 on Armbian) in /etc/shadow.
func lockRoot(shadow []byte) ([]byte, error) {
	lines := strings.Split(string(shadow), "\n")
	for i, l := range lines {
		f := strings.Split(l, ":")
		if len(f) > 2 && f[0] == "root" {
			if !strings.HasPrefix(f[1], "!") && f[1] != "*" {
				f[1] = "!" + f[1]
			}
			lines[i] = strings.Join(f, ":")
			return []byte(strings.Join(lines, "\n")), nil
		}
	}
	return nil, errors.New("no root entry in /etc/shadow")
}

// root is Armbian's ext4 root, on an image or a card, changed by the ext4
// package without mounting it.
type root struct {
	dev ext4.Device
	fs  *ext4.FS
}

// openRoot opens the root and refuses one that is not clean or uses ext4
// features the ext4 package does not handle.
func openRoot(dev ext4.Device) (*root, error) {
	f, err := ext4.Open(dev, armbianRootStart)
	if err != nil {
		return nil, err
	}
	if err := f.Clean(); err != nil {
		return nil, fmt.Errorf("the card's file system cannot be changed now: %w", err)
	}
	return &root{dev: dev, fs: f}, nil
}

func (r *root) exists(p string) bool {
	_, err := r.fs.Stat(p)
	return err == nil
}

// read returns a file from the root, or an error when it does not exist.
func (r *root) read(p string) ([]byte, error) { return r.fs.ReadFile(p) }

// CustomizeArmbian puts the settings and payload on the root of an
// uncompressed Armbian image.
func CustomizeArmbian(ctx context.Context, image string, s Settings, payload fs.FS, report Progress) error {
	if err := CheckArmbianLayout(image); err != nil {
		return err
	}
	files, err := armbianFiles(s, payload)
	if err != nil {
		return err
	}
	report("Adapting the image", -1)
	img, err := os.OpenFile(image, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer img.Close()
	r, err := openRoot(img)
	if err != nil {
		return err
	}
	release, err := r.read("/etc/armbian-release")
	if err != nil || !bytes.Contains(release, []byte("BOARD=rockpi-4cplus")) {
		return errors.New("this does not look like Armbian for the ROCK 4C+")
	}
	shadow, err := r.read("/etc/shadow")
	if err != nil {
		return err
	}
	if shadow, err = lockRoot(shadow); err != nil {
		return err
	}
	files["/etc/shadow"] = ext4File{data: shadow, mode: 0o640, gid: 42} // group shadow

	if err := r.put(files, armbianRemove, []string{armbianDir, armbianDir + "/debs"}, true, report); err != nil {
		return err
	}
	return img.Sync()
}

// put writes files onto the root, removes the paths in remove (missing
// ones are fine), and checks the result by opening the root afresh and
// reading everything back. dirs are created when missing.
func (r *root) put(files map[string]ext4File, remove, dirs []string, enable bool, report Progress) error {
	for _, dir := range dirs {
		if err := r.fs.Mkdir(dir, 0o755); err != nil {
			return err
		}
	}
	for _, p := range remove {
		if err := r.fs.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("removing %s: %w", p, err)
		}
	}
	names := make([]string, 0, len(files))
	for p := range files {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, p := range names {
		if err := r.fs.WriteFile(p, files[p].data, files[p].mode.Perm(), 0, uint32(files[p].gid)); err != nil {
			return fmt.Errorf("writing %s: %w", p, err)
		}
	}
	if enable {
		if err := r.fs.Remove(firstbootWants); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := r.fs.Symlink(firstbootUnit, firstbootWants); err != nil {
			return err
		}
	}
	if err := r.fs.Flush(); err != nil {
		return err
	}

	// Read back from the device, not from what is still in memory.
	report("Checking the image", -1)
	check, err := openRoot(r.dev)
	if err != nil {
		return fmt.Errorf("checking: %w", err)
	}
	for _, p := range names {
		got, err := check.read(p)
		if err != nil {
			return fmt.Errorf("checking %s: %w", p, err)
		}
		if !bytes.Equal(got, files[p].data) {
			return fmt.Errorf("checking %s: content differs after writing", p)
		}
	}
	for _, p := range remove {
		if check.exists(p) {
			return fmt.Errorf("checking: %s is still there", p)
		}
	}
	if !enable {
		return nil
	}
	if target, err := check.read(firstbootWants); err != nil || string(target) != firstbootUnit {
		return errors.New("checking: squirrel-firstboot.service is not enabled")
	}
	return nil
}
