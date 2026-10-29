package storage

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Options are the parts of the environment Setup looks at; the defaults
// are the running system. Tests point them at a loop device.
type Options struct {
	Root       string // root partition device; default: what / is mounted from
	Fstab      string
	Mountpoint string
	RootTarget int64 // bytes
	MinData    int64 // bytes
	Confirm    func(plan string) bool
	Log        func(format string, args ...any)
}

func run(name string, args ...string) (string, error) {
	return runIn(nil, name, args...)
}

func runIn(stdin []byte, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func isMountpoint(dir string) bool {
	return exec.Command("mountpoint", "-q", dir).Run() == nil
}

// hasFS uses blkid's low-level probe, which also finds signatures of
// filesystems that are not in any cache.
func hasFS(node string) bool {
	err := exec.Command("blkid", "-p", node).Run()
	var ee *exec.ExitError
	return !(errors.As(err, &ee) && ee.ExitCode() == 2)
}

func readTable(disk string) (Table, error) {
	out, err := run("sfdisk", "--json", disk)
	if err != nil {
		return Table{}, err
	}
	return ParseSfdisk([]byte(out))
}

func diskSectors(disk string) (int64, error) {
	out, err := run("blockdev", "--getsz", disk)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(out), 10, 64)
}

// Setup makes sure a filesystem labelled backingfiles is mounted at the
// mountpoint, creating the partition when needed.
func Setup(o Options) error {
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
	disk, err := parentDisk(o.Root)
	if err != nil {
		return err
	}
	// Only a partition on the card itself: a USB stick or another disk
	// that happens to carry the label is not ours to take.
	out, _ := run("blkid", "-t", "LABEL="+Label, "-o", "device")
	for _, dev := range strings.Fields(out) {
		if d, err := parentDisk(dev); err == nil && d == disk {
			o.Log("found existing %s partition %s", Label, dev)
			return mount(o, dev)
		}
		o.Log("ignoring %s: labelled %s, but not on %s", dev, Label, disk)
	}
	t, err := readTable(disk)
	if err != nil {
		return err
	}
	size, err := diskSectors(disk)
	if err != nil {
		return err
	}
	plan, err := Decide(t, size, o.Root, o.RootTarget/sectorSize, o.MinData/sectorSize, hasFS)
	if err != nil {
		return err
	}
	if o.Confirm != nil && !o.Confirm(plan.String()) {
		return errors.New("cancelled")
	}

	if b, err := run("sfdisk", "--dump", disk); err == nil {
		os.WriteFile("/root/partition-table-before-squirreld.sfdisk", []byte(b), 0o600)
	}
	if plan.RootSize > 0 {
		n, _ := Number(plan.Root.Node)
		if err := resizePart(disk, n, plan.Root.Start, plan.RootSize, t.ID); err != nil {
			return err
		}
		o.Log("grew partition %s; growing its filesystem", plan.Root.Node)
		if _, err := run("resize2fs", plan.Root.Node); err != nil {
			return err
		}
	}

	node := plan.Data.Node
	if plan.DataNew {
		line := fmt.Sprintf("%d,%d,83\n", plan.Data.Start, plan.Data.Size)
		if _, err := runIn([]byte(line), "sfdisk", "--no-reread", "--no-tell-kernel", "--append", disk); err != nil {
			return err
		}
		if err := keepDiskID(disk, t.ID); err != nil {
			return err
		}
		nt, err := readTable(disk)
		if err != nil {
			return err
		}
		for _, p := range nt.Partitions {
			if p.Start == plan.Data.Start {
				node = p.Node
			}
		}
		if node == "" {
			return errors.New("new partition not found after writing it")
		}
		n, _ := Number(node)
		if _, err := run("partx", "--add", "--nr", strconv.Itoa(n), disk); err != nil {
			return err
		}
	} else {
		n, _ := Number(node)
		if err := resizePart(disk, n, plan.Data.Start, plan.Data.Size, t.ID); err != nil {
			return err
		}
	}
	if err := waitForNode(node); err != nil {
		return err
	}
	if !plan.DataNew && hasFS(node) {
		return fmt.Errorf("%s now shows a filesystem signature; not formatting it", node)
	}
	o.Log("formatting %s as XFS", node)
	// -f: the space comes from unallocated or empty area, which may hold
	// leftovers of earlier use of the card.
	if _, err := run("mkfs.xfs", "-f", "-m", "reflink=1", "-L", Label, node); err != nil {
		return err
	}
	return mount(o, node)
}

// parentDisk returns the disk a partition is on, e.g. /dev/mmcblk0.
func parentDisk(part string) (string, error) {
	out, err := run("lsblk", "-n", "-d", "-o", "PKNAME", part)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("%s is not a partition", part)
	}
	return "/dev/" + strings.TrimSpace(out), nil
}

// resizePart changes a partition's size in the table and tells the kernel,
// which works while the partition is mounted (as growpart does).
func resizePart(disk string, n int, start, size int64, id string) error {
	line := fmt.Sprintf("%d,%d\n", start, size)
	if _, err := runIn([]byte(line), "sfdisk", "--no-reread", "--no-tell-kernel", "-N", strconv.Itoa(n), disk); err != nil {
		return err
	}
	if err := keepDiskID(disk, id); err != nil {
		return err
	}
	_, err := run("partx", "--update", "--nr", strconv.Itoa(n), disk)
	return err
}

// keepDiskID restores the disk identifier if writing the table changed it:
// fstab and cmdline.txt refer to partitions by it.
func keepDiskID(disk, id string) error {
	if id == "" {
		return nil
	}
	out, err := run("sfdisk", "--disk-id", disk)
	if err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(out), id) {
		return nil
	}
	_, err = run("sfdisk", "--disk-id", disk, id)
	return err
}

func waitForNode(node string) error {
	for range 50 {
		if _, err := os.Stat(node); err == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("%s did not appear", node)
}

// mount adds node to fstab by UUID, so another disk with the same label
// can never take its place, and mounts it.
func mount(o Options, node string) error {
	data, err := os.ReadFile(o.Fstab)
	if err != nil {
		return err
	}
	has := false
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && !strings.HasPrefix(f[0], "#") && f[1] == o.Mountpoint {
			has = true
		}
	}
	if !has {
		os.WriteFile(o.Fstab+".before-squirreld", data, 0o644)
		uuid, err := run("blkid", "-s", "UUID", "-o", "value", node)
		if err != nil || strings.TrimSpace(uuid) == "" {
			return fmt.Errorf("no UUID for %s: %v", node, err)
		}
		entry := fmt.Sprintf("UUID=%s %s xfs %s 0 2\n", strings.TrimSpace(uuid), o.Mountpoint, MountOptions)
		if len(data) > 0 && data[len(data)-1] != '\n' {
			entry = "\n" + entry
		}
		if err := os.WriteFile(o.Fstab, append(data, entry...), 0o644); err != nil {
			return err
		}
		o.Log("added %s to %s", o.Mountpoint, o.Fstab)
	}
	if err := os.MkdirAll(o.Mountpoint, 0o755); err != nil {
		return err
	}
	if _, err := run("mount", "--fstab", o.Fstab, o.Mountpoint); err != nil {
		return err
	}
	os.MkdirAll(filepath.Join(o.Mountpoint, "squirreld"), 0o755)
	o.Log("mounted %s", o.Mountpoint)
	return nil
}
