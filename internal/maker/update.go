package maker

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Updating a card puts the new Squirrel on it and clears the "done"
// marker, so the next start runs firstboot.sh again: it installs the new
// squirreld and keeps everything else (settings and clips live on
// /backingfiles, the settings files on the card were consumed at the
// first start).

// CardInfo describes a card that holds Squirrel (or teslausbd, as it was
// called before).
type CardInfo struct {
	Disk
	Board   string `json:"board"`   // rpi, rpizerow or rock4cplus
	Version string `json:"version"` // empty when the card predates version files
	Pending bool   `json:"pending"` // the first start (or an update) has not run yet
}

// piPayload picks the payload folder by the architecture of the
// squirreld already on a Raspberry Pi card.
func piPayload(squirreld string) (board, payload string, err error) {
	f, err := os.Open(squirreld)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	var h [20]byte
	if _, err := f.ReadAt(h[:], 0); err != nil || string(h[:4]) != "\x7fELF" {
		return "", "", errors.New("the squirreld on this card is not a Linux program")
	}
	switch binary.LittleEndian.Uint16(h[18:]) {
	case 0xB7: // aarch64
		return "rpi", "files", nil
	case 0x28: // arm
		return "rpizerow", "files-armv6", nil
	}
	return "", "", errors.New("the squirreld on this card is for an unknown processor")
}

// Before Squirrel the folder on the card was teslausb and the program
// teslausbd. Such a card is updated in place: its first-start unit runs
// the folder it has.
var (
	bootDirs  = []string{"squirrel", "teslausb"}
	daemons   = []string{"squirreld", "teslausbd"}
	legacyOld = []string{"teslausbd", "teslausbd.service"}
)

// piFolder finds Squirrel's folder on a mounted boot partition and the
// program in it.
func piFolder(dir string) (folder, daemon string, err error) {
	for _, b := range bootDirs {
		tb := filepath.Join(dir, b)
		if _, err := os.Stat(filepath.Join(tb, "firstboot.sh")); err != nil {
			continue
		}
		for _, d := range daemons {
			if _, err := os.Stat(filepath.Join(tb, d)); err == nil {
				return tb, filepath.Join(tb, d), nil
			}
		}
	}
	return "", "", errors.New("this card has no Squirrel")
}

func readVersion(p string) string {
	b, _ := os.ReadFile(p)
	return strings.TrimSpace(string(b))
}

// inspectPiBoot looks at a mounted Raspberry Pi boot partition.
func inspectPiBoot(dir string) (CardInfo, error) {
	tb, daemon, err := piFolder(dir)
	if err != nil {
		return CardInfo{}, err
	}
	board, _, err := piPayload(daemon)
	if err != nil {
		return CardInfo{}, err
	}
	_, done := os.Stat(filepath.Join(tb, "done"))
	return CardInfo{Board: board, Version: readVersion(filepath.Join(tb, "version")), Pending: done != nil}, nil
}

// updatePiBoot puts the new Squirrel in the Squirrel (or teslausb) folder
// of a mounted Raspberry Pi boot partition. all is the app's whole payload.
func updatePiBoot(dir string, all fs.FS) error {
	tb, daemon, err := piFolder(dir)
	if err != nil {
		return err
	}
	_, payloadDir, err := piPayload(daemon)
	if err != nil {
		return err
	}
	payload, err := fs.Sub(all, payloadDir)
	if err != nil {
		return err
	}
	if _, err := fs.Stat(payload, "squirreld"); err != nil {
		return errors.New("this build of squirrel-maker has no squirreld inside; build it with `make maker`")
	}
	want := map[string][]byte{}
	err = fs.WalkDir(payload, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(payload, p)
		want[p] = data
		return err
	})
	if err != nil {
		return err
	}
	// Packages of the old version that the new one does not bring.
	old, _ := filepath.Glob(filepath.Join(tb, "debs", "*.deb"))
	for _, p := range old {
		if _, ok := want["debs/"+filepath.Base(p)]; !ok {
			if err := os.Remove(p); err != nil {
				return err
			}
		}
	}
	for p, data := range want {
		dst := filepath.Join(tb, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", p, err)
		}
	}
	for p, data := range want {
		got, err := os.ReadFile(filepath.Join(tb, filepath.FromSlash(p)))
		if err != nil || string(got) != string(data) {
			return fmt.Errorf("checking %s: content differs after writing", p)
		}
	}
	// The program under its earlier name; the new firstboot.sh installs
	// squirreld and retires teslausbd.
	for _, f := range legacyOld {
		if err := os.Remove(filepath.Join(tb, f)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Remove(filepath.Join(tb, "done")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// armbianDirs are where Squirrel lives on an Armbian card, newest first.
var armbianDirs = []string{armbianDir, "/boot/teslausb"}

func armbianFolder(r *root) (string, error) {
	for _, dir := range armbianDirs {
		if r.exists(dir + "/firstboot.sh") {
			return dir, nil
		}
	}
	return "", errors.New("this card has no Squirrel")
}

// inspectArmbian looks at an Armbian card's root.
func inspectArmbian(r *root) (CardInfo, error) {
	release, err := r.read("/etc/armbian-release")
	if err != nil || !strings.Contains(string(release), "BOARD=rockpi-4cplus") {
		return CardInfo{}, errors.New("this does not look like Armbian for the ROCK 4C+")
	}
	dir, err := armbianFolder(r)
	if err != nil {
		return CardInfo{}, err
	}
	v, _ := r.read(dir + "/version")
	return CardInfo{Board: "rock4cplus", Version: strings.TrimSpace(string(v)), Pending: !r.exists(dir + "/done")}, nil
}

// updateArmbian puts the new Squirrel on an Armbian card's root.
func updateArmbian(r *root, all fs.FS, report Progress) error {
	if _, err := inspectArmbian(r); err != nil {
		return err
	}
	dir, err := armbianFolder(r)
	if err != nil {
		return err
	}
	current := dir == armbianDirs[0]
	files, err := armbianProgramIn(all, dir, current)
	if err != nil {
		return err
	}
	remove := []string{dir + "/done"}
	for _, f := range legacyOld {
		remove = append(remove, dir+"/"+f)
	}
	// Packages of the old version that the new one does not bring.
	old, _ := r.fs.ReadDir(dir + "/debs")
	for _, e := range old {
		p := dir + "/debs/" + e.Name
		if _, ok := files[p]; !ok && strings.HasSuffix(e.Name, ".deb") && !e.IsDir() {
			remove = append(remove, p)
		}
	}
	report("Updating the card", -1)
	return r.put(files, remove, []string{dir + "/debs"}, current, report)
}
