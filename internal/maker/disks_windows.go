package maker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

type Disk struct {
	ID       string `json:"id"` // PhysicalDrive3
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Protocol string `json:"protocol"`
}

// AllowDiskImages makes attached virtual disks (VHDs) count as cards.
// Only for tests.
var AllowDiskImages = os.Getenv("SQUIRREL_MAKER_ALLOW_DISK_IMAGES") == "1"

var diskIDRe = regexp.MustCompile(`^PhysicalDrive([0-9]+)$`)

var errNotAdmin = errors.New("writing to a card needs administrator rights; start Squirrel Maker as administrator")

// elevated tells whether the maker runs as administrator, which raw disk
// access needs on Windows.
func elevated() bool { return windows.GetCurrentProcessToken().IsElevated() }

// powershell runs a script without showing a console window.
func powershell(ctx context.Context, script string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-Command", "$ErrorActionPreference = 'Stop'; [Console]::OutputEncoding = [Text.Encoding]::UTF8; "+script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("powershell: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

type winDisk struct {
	Number   int
	Name     string
	Size     int64
	Bus      string
	System   bool
	Boot     bool
	ReadOnly bool
}

func listWinDisks(ctx context.Context) ([]winDisk, error) {
	out, err := powershell(ctx, `ConvertTo-Json -Compress -InputObject @(Get-Disk | ForEach-Object {
		[pscustomobject]@{ Number = [int]$_.Number; Name = [string]$_.FriendlyName; Size = [int64]$_.Size;
			Bus = [string]$_.BusType; System = [bool]$_.IsSystem; Boot = [bool]$_.IsBoot; ReadOnly = [bool]$_.IsReadOnly } })`)
	if err != nil {
		return nil, err
	}
	var disks []winDisk
	if err := json.Unmarshal(bytes.TrimSpace(out), &disks); err != nil {
		return nil, fmt.Errorf("listing disks: %w", err)
	}
	return disks, nil
}

// winPart is a partition as Windows sees it.
type winPart struct {
	Number int
	Type   int // MBR type: 12 for FAT32, 131 for Linux
	Offset int64
	Size   int64
	Letter string
	Label  string
}

func partitions(ctx context.Context, disk int) ([]winPart, error) {
	out, err := powershell(ctx, fmt.Sprintf(`ConvertTo-Json -Compress -InputObject @(Get-Partition -DiskNumber %d -ErrorAction SilentlyContinue | ForEach-Object {
		$v = $_ | Get-Volume -ErrorAction SilentlyContinue
		$l = if ($_.DriveLetter -and [int][char]$_.DriveLetter -ne 0) { [string]$_.DriveLetter } else { '' }
		[pscustomobject]@{ Number = [int]$_.PartitionNumber; Type = [int]$_.MbrType; Offset = [int64]$_.Offset;
			Size = [int64]$_.Size; Letter = $l; Label = [string]$v.FileSystemLabel } })`, disk))
	if err != nil {
		return nil, err
	}
	var parts []winPart
	if err := json.Unmarshal(bytes.TrimSpace(out), &parts); err != nil {
		return nil, fmt.Errorf("listing partitions: %w", err)
	}
	return parts, nil
}

func diskNumber(id string) (int, error) {
	m := diskIDRe.FindStringSubmatch(id)
	if m == nil {
		return 0, fmt.Errorf("invalid disk %q", id)
	}
	return strconv.Atoi(m[1])
}

// checkDisk returns the disk if it is safe to overwrite: a card reader or
// USB disk, not the one Windows runs from, writable, the size of an SD
// card.
func checkDisk(ctx context.Context, id string) (Disk, error) {
	n, err := diskNumber(id)
	if err != nil {
		return Disk{}, err
	}
	disks, err := listWinDisks(ctx)
	if err != nil {
		return Disk{}, err
	}
	for _, d := range disks {
		if d.Number != n {
			continue
		}
		if err := diskOK(d); err != nil {
			return Disk{}, fmt.Errorf("%s %w", id, err)
		}
		return toDisk(ctx, d), nil
	}
	return Disk{}, fmt.Errorf("%s not found", id)
}

// toDisk names a disk after its first labelled partition, if it has one.
func toDisk(ctx context.Context, d winDisk) Disk {
	name := d.Name
	if parts, err := partitions(ctx, d.Number); err == nil {
		for _, p := range parts {
			if p.Label != "" {
				name = p.Label + ", " + name
				break
			}
		}
	}
	return Disk{ID: fmt.Sprintf("PhysicalDrive%d", d.Number), Name: name, Size: d.Size, Protocol: d.Bus}
}

func diskOK(d winDisk) error {
	bus := d.Bus == "USB" || d.Bus == "SD" || d.Bus == "MMC" ||
		AllowDiskImages && (d.Bus == "File Backed Virtual" || d.Bus == "FileBackedVirtual")
	switch {
	case !bus:
		return fmt.Errorf("is not a card or USB disk (%s)", d.Bus)
	case d.System || d.Boot:
		return errors.New("holds Windows")
	case d.ReadOnly:
		return errors.New("is write-protected (check the lock switch on the card)")
	case d.Size < 8<<30 || d.Size > 2<<40:
		return fmt.Errorf("is %d GB; expected an SD card of 8 GB to 2 TB", d.Size>>30)
	}
	return nil
}

// ListDisks returns the cards that can be written.
func ListDisks(ctx context.Context) ([]Disk, error) {
	disks, err := listWinDisks(ctx)
	if err != nil {
		return nil, err
	}
	var out []Disk
	for _, d := range disks {
		if diskOK(d) == nil {
			out = append(out, toDisk(ctx, d))
		}
	}
	return out, nil
}

func openDisk(n int, write bool) (*os.File, error) {
	flag := os.O_RDONLY
	if write {
		flag = os.O_RDWR
	}
	return os.OpenFile(fmt.Sprintf(`\\.\PhysicalDrive%d`, n), flag, 0)
}

// WriteCard writes image to the card. Windows lets go of a disk's volumes
// only when they are gone, so the card is cleared first; the partition
// table is written last (see WriteRaw), so nothing is mounted halfway.
func WriteCard(ctx context.Context, image, id string, report Progress) error {
	if !elevated() {
		return errNotAdmin
	}
	d, err := checkDisk(ctx, id)
	if err != nil {
		return err
	}
	n, _ := diskNumber(id)
	fi, err := os.Stat(image)
	if err != nil {
		return err
	}
	if fi.Size() > d.Size {
		return fmt.Errorf("the image (%d GB) does not fit on %s", fi.Size()>>30, d.Name)
	}
	report("Preparing the SD card", -1)
	if _, err := powershell(ctx, fmt.Sprintf(`$d = Get-Disk -Number %d
		if ($d.PartitionStyle -ne 'RAW') { Clear-Disk -Number %d -RemoveData -RemoveOEM -Confirm:$false }`, n, n)); err != nil {
		return err
	}
	dev, err := openDisk(n, true)
	if err != nil {
		return err
	}
	err = WriteRaw(image, dev, report)
	dev.Close()
	if err != nil {
		return err
	}
	report("Finishing", -1)
	_, err = powershell(ctx, fmt.Sprintf(`Update-Disk -Number %d`, n))
	return err
}
