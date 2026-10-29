package maker

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
)

// isArmbian: one Linux partition at 16 MiB and nothing else, as Armbian
// writes it.
func isArmbian(parts []winPart) bool {
	return len(parts) == 1 && parts[0].Type == 0x83 && parts[0].Offset == armbianRootStart
}

// bootDir returns the folder of a Raspberry Pi card's FAT boot partition,
// giving it a drive letter when Windows has not.
func bootDir(ctx context.Context, disk int, parts []winPart) (string, error) {
	if len(parts) == 0 || (parts[0].Type != 0x0C && parts[0].Type != 0x0B && parts[0].Type != 0x0E && parts[0].Type != 0x06) {
		return "", errors.New("this card has no Squirrel")
	}
	letter := parts[0].Letter
	if letter == "" {
		if _, err := powershell(ctx, fmt.Sprintf(`Add-PartitionAccessPath -DiskNumber %d -PartitionNumber %d -AssignDriveLetter`,
			disk, parts[0].Number)); err != nil {
			return "", err
		}
		again, err := partitions(ctx, disk)
		if err != nil || len(again) == 0 || again[0].Letter == "" {
			return "", errors.New("the card's boot partition got no drive letter")
		}
		letter = again[0].Letter
	}
	return letter + `:\`, nil
}

// InspectCard tells whether a card holds Squirrel, for which board and
// which version.
func InspectCard(ctx context.Context, id string) (CardInfo, error) {
	d, err := checkDisk(ctx, id)
	if err != nil {
		return CardInfo{}, err
	}
	n, _ := diskNumber(id)
	parts, err := partitions(ctx, n)
	if err != nil {
		return CardInfo{}, err
	}
	if isArmbian(parts) {
		if !elevated() {
			return CardInfo{Disk: d, Board: "rock4cplus"}, nil
		}
		dev, err := openDisk(n, false)
		if err != nil {
			return CardInfo{}, err
		}
		defer dev.Close()
		r, err := openRoot(dev)
		if err != nil {
			return CardInfo{}, err
		}
		info, err := inspectArmbian(r)
		info.Disk = d
		return info, err
	}
	dir, err := bootDir(ctx, n, parts)
	if err != nil {
		return CardInfo{}, err
	}
	info, err := inspectPiBoot(dir)
	info.Disk = d
	return info, err
}

// UpdateCard puts the Squirrel of this app on a card that already holds
// it. all is the whole payload.
func UpdateCard(ctx context.Context, id string, all fs.FS, report Progress) (CardInfo, error) {
	if !elevated() {
		return CardInfo{}, errNotAdmin
	}
	d, err := checkDisk(ctx, id)
	if err != nil {
		return CardInfo{}, err
	}
	n, _ := diskNumber(id)
	parts, err := partitions(ctx, n)
	if err != nil {
		return CardInfo{}, err
	}
	if isArmbian(parts) {
		dev, err := openDisk(n, true)
		if err != nil {
			return CardInfo{}, err
		}
		defer dev.Close()
		r, err := openRoot(dev)
		if err != nil {
			return CardInfo{}, err
		}
		report("Updating the card", -1)
		if err := updateArmbian(r, all, report); err != nil {
			return CardInfo{}, err
		}
		if err := dev.Sync(); err != nil {
			return CardInfo{}, err
		}
		info, err := inspectArmbian(r)
		info.Disk = d
		return info, err
	}
	dir, err := bootDir(ctx, n, parts)
	if err != nil {
		return CardInfo{}, err
	}
	if _, err := inspectPiBoot(dir); err != nil {
		return CardInfo{}, err
	}
	report("Updating the card", -1)
	if err := updatePiBoot(dir, all); err != nil {
		return CardInfo{}, err
	}
	info, err := inspectPiBoot(dir)
	info.Disk = d
	if err != nil {
		return info, err
	}
	// Written through to the card before it is pulled out.
	_, err = powershell(ctx, fmt.Sprintf(`Write-VolumeCache -DriveLetter %s`, dir[:1]))
	return info, err
}
