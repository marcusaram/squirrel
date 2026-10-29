package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Data on an external USB drive (an SSD on a Pi 4 or ROCK 4C+): the card
// keeps root as it is, and the whole drive becomes one XFS filesystem.
//
// The drive is never guessed at. A first start takes the one USB drive
// that is either labelled backingfiles from an earlier setup, or entirely
// empty: no partition table and no filesystem. Anything else, two drives,
// or a drive that holds data, stops with a message; erasing such a drive
// takes an explicit `squirreld setup-storage -usb-device /dev/sdX`.

// BlockDev is one line of lsblk's tree.
type BlockDev struct {
	Name     string     `json:"name"`
	Path     string     `json:"path"`
	Type     string     `json:"type"` // disk, part, ...
	Tran     string     `json:"tran"` // usb, mmc, ...
	Size     int64      `json:"size"`
	Model    string     `json:"model"`
	Serial   string     `json:"serial"`
	FSType   string     `json:"fstype"`
	Label    string     `json:"label"`
	PTType   string     `json:"pttype"`
	Children []BlockDev `json:"children"`
}

// lsblkColumns are the columns ParseLsblk expects.
const lsblkColumns = "NAME,PATH,TYPE,TRAN,SIZE,MODEL,SERIAL,FSTYPE,LABEL,PTTYPE"

func ParseLsblk(data []byte) ([]BlockDev, error) {
	var w struct {
		Devs []BlockDev `json:"blockdevices"`
	}
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, fmt.Errorf("lsblk output: %w", err)
	}
	return w.Devs, nil
}

func (d BlockDev) String() string {
	s := fmt.Sprintf("%s (%.0f GB", d.Path, float64(d.Size)/1e9)
	if m := strings.TrimSpace(d.Model); m != "" {
		s += ", " + m
	}
	if sn := strings.TrimSpace(d.Serial); sn != "" {
		s += ", serial " + sn
	}
	return s + ")"
}

// contents describes what is on a drive, for the log; empty when nothing.
func (d BlockDev) contents() string {
	var what []string
	if d.PTType != "" {
		what = append(what, d.PTType+" partition table")
	}
	if d.FSType != "" {
		what = append(what, d.FSType+" filesystem")
	}
	for _, c := range d.Children {
		w := c.Path
		if c.FSType != "" {
			w += " " + c.FSType
		}
		if c.Label != "" {
			w += fmt.Sprintf(" %q", c.Label)
		}
		what = append(what, w)
	}
	return strings.Join(what, ", ")
}

// backingfiles returns the XFS filesystem labelled backingfiles on d, the
// drive itself or one of its partitions.
func (d BlockDev) backingfiles() string {
	if d.Label == Label && d.FSType == "xfs" {
		return d.Path
	}
	for _, c := range d.Children {
		if c.Label == Label && c.FSType == "xfs" {
			return c.Path
		}
	}
	return ""
}

// USBChoice is what ChooseUSB decided.
type USBChoice struct {
	Drive  BlockDev
	Node   string // what to mount
	Format bool   // Node is the empty drive and gets formatted
}

// ChooseUSB picks the drive for /backingfiles from lsblk's tree. rootDisk
// is the disk root is on, which is never a candidate. empty reports
// whether a drive carries no signature at all (blkid's probe), which
// lsblk alone can miss.
func ChooseUSB(devs []BlockDev, rootDisk string, empty func(node string) bool) (USBChoice, error) {
	var drives []BlockDev
	for _, d := range devs {
		if d.Type == "disk" && d.Tran == "usb" && d.Path != rootDisk && d.Size > 0 {
			drives = append(drives, d)
		}
	}
	// An earlier setup wins, even next to other drives.
	for _, d := range drives {
		if node := d.backingfiles(); node != "" {
			return USBChoice{Drive: d, Node: node}, nil
		}
	}
	switch len(drives) {
	case 0:
		return USBChoice{}, errors.New("no USB drive found; connect the drive for the data (not to the port for the car) and start again")
	case 1:
	default:
		var names []string
		for _, d := range drives {
			names = append(names, d.String())
		}
		return USBChoice{}, fmt.Errorf("more than one USB drive: %s; leave only the one for the data connected", strings.Join(names, "; "))
	}
	d := drives[0]
	if what := d.contents(); what != "" || !empty(d.Path) {
		if what == "" {
			what = "a signature blkid recognises"
		}
		return USBChoice{}, fmt.Errorf("USB drive %s holds data (%s); only an empty drive is formatted. "+
			"To erase this one, run: sudo squirreld setup-storage -usb-device %s", d, what, d.Path)
	}
	return USBChoice{Drive: d, Node: d.Path, Format: true}, nil
}

// SetupUSB mounts /backingfiles from a USB drive, formatting it first when
// it is new and empty. device, when set, is a drive the user named
// explicitly: it is used, and erased, whatever is on it.
func SetupUSB(o Options, device string) error {
	if isMountpoint(o.Mountpoint) {
		o.Log("%s is already mounted; nothing to do", o.Mountpoint)
		return nil
	}
	if o.Root == "" {
		out, err := run("findmnt", "-n", "-o", "SOURCE", "/")
		if err != nil {
			return err
		}
		o.Root = strings.TrimSpace(out)
	}
	rootDisk, err := parentDisk(o.Root)
	if err != nil {
		return err
	}
	out, err := run("lsblk", "-J", "-b", "-o", lsblkColumns)
	if err != nil {
		return err
	}
	devs, err := ParseLsblk([]byte(out))
	if err != nil {
		return err
	}

	var c USBChoice
	if device != "" {
		c, err = namedUSB(devs, rootDisk, device)
	} else {
		c, err = ChooseUSB(devs, rootDisk, func(node string) bool { return !hasFS(node) })
	}
	if err != nil {
		return err
	}
	if !c.Format {
		o.Log("found existing %s filesystem %s on USB drive %s", Label, c.Node, c.Drive)
		return mount(o, c.Node)
	}
	plan := fmt.Sprintf("format USB drive %s as XFS for %s", c.Drive, o.Mountpoint)
	if what := c.Drive.contents(); what != "" {
		plan += "; this erases " + what
	}
	if o.Confirm != nil && !o.Confirm(plan+"\n") {
		return errors.New("cancelled")
	}
	o.Log("%s", plan)
	if device != "" {
		// Partitions of the old layout must not linger.
		if _, err := run("wipefs", "-a", c.Node); err != nil {
			return err
		}
		run("blockdev", "--rereadpt", c.Node)
	}
	if _, err := run("mkfs.xfs", "-f", "-m", "reflink=1", "-L", Label, c.Node); err != nil {
		return err
	}
	return mount(o, c.Node)
}

// namedUSB checks a drive the user named: a whole USB disk, not the one
// root is on, and not mounted.
func namedUSB(devs []BlockDev, rootDisk, device string) (USBChoice, error) {
	for _, d := range devs {
		if d.Path != device {
			continue
		}
		switch {
		case d.Type != "disk":
			return USBChoice{}, fmt.Errorf("%s is not a whole drive", device)
		case d.Tran != "usb":
			return USBChoice{}, fmt.Errorf("%s is not a USB drive", device)
		case d.Path == rootDisk:
			return USBChoice{}, fmt.Errorf("%s holds the system", device)
		}
		if out, _ := run("findmnt", "-n", "-o", "TARGET", "-S", device); strings.TrimSpace(out) != "" {
			return USBChoice{}, fmt.Errorf("%s is mounted", device)
		}
		for _, c := range d.Children {
			if out, _ := run("findmnt", "-n", "-o", "TARGET", "-S", c.Path); strings.TrimSpace(out) != "" {
				return USBChoice{}, fmt.Errorf("%s is mounted at %s", c.Path, strings.TrimSpace(out))
			}
		}
		return USBChoice{Drive: d, Node: d.Path, Format: true}, nil
	}
	return USBChoice{}, fmt.Errorf("%s not found", device)
}
