package maker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

type Disk struct {
	ID       string `json:"id"` // disk4
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Protocol string `json:"protocol"`
}

// AllowDiskImages makes attached disk images count as cards. Only for
// tests: an attached image cannot be told apart from a real disk by much.
var AllowDiskImages = os.Getenv("SQUIRREL_MAKER_ALLOW_DISK_IMAGES") == "1"

var diskIDRe = regexp.MustCompile(`^disk[0-9]+$`)

type diskInfo struct {
	DeviceIdentifier  string
	WholeDisk         bool
	Internal          bool
	RemovableMedia    bool
	Ejectable         bool
	VirtualOrPhysical string
	BusProtocol       string
	TotalSize         int64
	MediaName         string
	WritableMedia     bool
}

// checkDisk returns the disk if it is safe to overwrite: an external,
// physical, writable whole disk the size of an SD card.
func checkDisk(ctx context.Context, id string) (Disk, error) {
	if !diskIDRe.MatchString(id) {
		return Disk{}, fmt.Errorf("invalid disk %q", id)
	}
	var d diskInfo
	if err := plist(ctx, &d, "diskutil", "info", "-plist", id); err != nil {
		return Disk{}, err
	}
	virtualOK := AllowDiskImages && d.VirtualOrPhysical == "Virtual" && d.BusProtocol == "Disk Image"
	// A Mac's own SD slot reports its card as internal; it is still a
	// removable card, unlike the built-in SSD.
	sdSlot := d.BusProtocol == "Secure Digital" && d.RemovableMedia && d.Ejectable
	switch {
	case d.DeviceIdentifier != id || !d.WholeDisk:
		return Disk{}, fmt.Errorf("%s is not a whole disk", id)
	case d.Internal && !sdSlot:
		return Disk{}, fmt.Errorf("%s is an internal disk", id)
	case d.VirtualOrPhysical != "Physical" && !virtualOK:
		return Disk{}, fmt.Errorf("%s is not a physical disk", id)
	case !d.WritableMedia:
		return Disk{}, fmt.Errorf("%s is write-protected (check the lock switch on the card)", id)
	case d.TotalSize < 8<<30 || d.TotalSize > 2<<40:
		return Disk{}, fmt.Errorf("%s is %d GB; expected an SD card of 8 GB to 2 TB", id, d.TotalSize>>30)
	}
	name := d.MediaName
	// The volume name (what Finder shows) is easier to recognise.
	var part struct{ VolumeName string }
	if plist(ctx, &part, "diskutil", "info", "-plist", id+"s1") == nil && part.VolumeName != "" {
		name = part.VolumeName + ", " + name
	}
	return Disk{ID: id, Name: name, Size: d.TotalSize, Protocol: d.BusProtocol}, nil
}

// ListDisks returns the cards that can be written.
func ListDisks(ctx context.Context) ([]Disk, error) {
	// Not just "external": cards in the Mac's own SD slot count as
	// internal. checkDisk decides what may be written.
	args := []string{"list", "-plist", "physical"}
	if AllowDiskImages {
		args = []string{"list", "-plist"}
	}
	var l struct{ WholeDisks []string }
	if err := plist(ctx, &l, "diskutil", args...); err != nil {
		return nil, err
	}
	var out []Disk
	for _, id := range l.WholeDisks {
		if d, err := checkDisk(ctx, id); err == nil {
			out = append(out, d)
		}
	}
	return out, nil
}

// WriteCard writes image to the card. The raw disk is opened by macOS's
// authopen, which asks for permission itself and hands back the open
// disk. Root alone is not enough for the Mac's own SD slot, which macOS
// treats as an internal disk.
func WriteCard(ctx context.Context, image, id string, report Progress) error {
	d, err := checkDisk(ctx, id)
	if err != nil {
		return err
	}
	fi, err := os.Stat(image)
	if err != nil {
		return err
	}
	if fi.Size() > d.Size {
		return fmt.Errorf("the image (%d GB) does not fit on %s", fi.Size()>>30, d.Name)
	}
	report("Preparing the SD card", -1)
	if _, err := command(ctx, "diskutil", "unmountDisk", "/dev/"+id); err != nil {
		return err
	}
	report("Asking for permission", -1)
	dev, err := authOpen("/dev/r" + id)
	if err != nil {
		return err
	}
	err = WriteRaw(image, dev, report)
	dev.Close()
	if err != nil {
		return err
	}
	report("Ejecting the card", -1)
	_, err = command(ctx, "diskutil", "eject", "/dev/"+id)
	return err
}

func appleQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// authOpen has /usr/libexec/authopen open path read-write and pass the
// descriptor back over a socket (its -stdoutpipe mode).
func authOpen(path string) (*os.File, error) {
	pair, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, err
	}
	ours := os.NewFile(uintptr(pair[0]), "authopen")
	defer ours.Close()
	theirs := os.NewFile(uintptr(pair[1]), "authopen-child")
	cmd := exec.Command("/usr/libexec/authopen", "-stdoutpipe", "-o", strconv.Itoa(syscall.O_RDWR), path)
	cmd.Stdout = theirs
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		theirs.Close()
		return nil, err
	}
	theirs.Close()
	buf := make([]byte, 64)
	oob := make([]byte, syscall.CmsgSpace(4))
	_, oobn, _, _, rerr := syscall.Recvmsg(pair[0], buf, oob, 0)
	werr := cmd.Wait()
	if rerr == nil && oobn > 0 {
		if msgs, err := syscall.ParseSocketControlMessage(oob[:oobn]); err == nil && len(msgs) > 0 {
			if fds, err := syscall.ParseUnixRights(&msgs[0]); err == nil && len(fds) > 0 {
				return os.NewFile(uintptr(fds[0]), path), nil
			}
		}
	}
	if werr != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("no access to the card: %s", msg)
		}
		return nil, errors.New("no access to the card (cancelled?)")
	}
	return nil, errors.New("authopen did not return the card")
}
