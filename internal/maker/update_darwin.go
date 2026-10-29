package maker

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
)

// partitions returns the partitions of a disk and what diskutil says they
// hold, e.g. DOS_FAT_32 or Linux.
func partitions(ctx context.Context, id string) ([]string, []string, error) {
	var l struct {
		AllDisksAndPartitions []struct {
			Partitions []struct {
				DeviceIdentifier string
				Content          string
			}
		}
	}
	if err := plist(ctx, &l, "diskutil", "list", "-plist", id); err != nil {
		return nil, nil, err
	}
	var ids, contents []string
	for _, d := range l.AllDisksAndPartitions {
		for _, p := range d.Partitions {
			ids = append(ids, p.DeviceIdentifier)
			contents = append(contents, p.Content)
		}
	}
	return ids, contents, nil
}

// mountBoot mounts a Raspberry Pi card's FAT boot partition (macOS often
// has already) and returns where.
func mountBoot(ctx context.Context, part string) (string, error) {
	var info struct{ MountPoint string }
	if err := plist(ctx, &info, "diskutil", "info", "-plist", part); err == nil && info.MountPoint != "" {
		return info.MountPoint, nil
	}
	if _, err := command(ctx, "diskutil", "mount", part); err != nil {
		return "", err
	}
	if err := plist(ctx, &info, "diskutil", "info", "-plist", part); err != nil {
		return "", err
	}
	if info.MountPoint == "" {
		return "", errors.New("the card's boot partition did not mount")
	}
	return info.MountPoint, nil
}

// isArmbianCard: one Linux partition and nothing else, as Armbian writes
// it. Only reading the card (which needs permission) tells for sure.
func isArmbianCard(contents []string) bool {
	return len(contents) == 1 && contents[0] == "Linux"
}

// InspectCard tells whether a card holds Squirrel, for which board and
// which version. An Armbian card cannot be read without asking for
// permission, so for one it only reports the board.
func InspectCard(ctx context.Context, id string) (CardInfo, error) {
	d, err := checkDisk(ctx, id)
	if err != nil {
		return CardInfo{}, err
	}
	ids, contents, err := partitions(ctx, id)
	if err != nil {
		return CardInfo{}, err
	}
	if isArmbianCard(contents) {
		return CardInfo{Disk: d, Board: "rock4cplus"}, nil
	}
	if len(ids) == 0 || (contents[0] != "DOS_FAT_32" && contents[0] != "Windows_FAT_32" && contents[0] != "DOS_FAT_16") {
		return CardInfo{}, errors.New("this card has no Squirrel")
	}
	mp, err := mountBoot(ctx, ids[0])
	if err != nil {
		return CardInfo{}, err
	}
	info, err := inspectPiBoot(mp)
	info.Disk = d
	return info, err
}

// UpdateCard puts the Squirrel of this app on a card that already holds
// it. all is the whole payload; tools holds debugfs and e2fsck.
func UpdateCard(ctx context.Context, id string, all fs.FS, tools func() (string, error), report Progress) (CardInfo, error) {
	d, err := checkDisk(ctx, id)
	if err != nil {
		return CardInfo{}, err
	}
	ids, contents, err := partitions(ctx, id)
	if err != nil {
		return CardInfo{}, err
	}
	if isArmbianCard(contents) {
		dir, err := tools()
		if err != nil {
			return CardInfo{}, err
		}
		report("Preparing the SD card", -1)
		if _, err := command(ctx, "diskutil", "unmountDisk", "/dev/"+id); err != nil {
			return CardInfo{}, err
		}
		report("Asking for permission", -1)
		dev, err := authOpen("/dev/r" + id)
		if err != nil {
			return CardInfo{}, err
		}
		defer dev.Close()
		dfs := debugfs{bin: filepath.Join(dir, "debugfs"), e2fsck: filepath.Join(dir, "e2fsck"), dev: dev}
		if err := updateArmbian(ctx, dfs, all, report); err != nil {
			return CardInfo{}, err
		}
		info, err := inspectArmbian(ctx, dfs)
		info.Disk = d
		dev.Close()
		report("Ejecting the card", -1)
		command(ctx, "diskutil", "eject", "/dev/"+id)
		return info, err
	}

	if len(ids) == 0 {
		return CardInfo{}, errors.New("this card has no Squirrel")
	}
	mp, err := mountBoot(ctx, ids[0])
	if err != nil {
		return CardInfo{}, err
	}
	if _, err := inspectPiBoot(mp); err != nil {
		return CardInfo{}, err
	}
	report("Updating the card", -1)
	if err := updatePiBoot(mp, all); err != nil {
		return CardInfo{}, err
	}
	info, err := inspectPiBoot(mp)
	info.Disk = d
	if err != nil {
		return info, err
	}
	command(ctx, "dot_clean", "-m", mp)
	command(ctx, "sync")
	report("Ejecting the card", -1)
	_, err = command(ctx, "diskutil", "eject", "/dev/"+id)
	return info, err
}
