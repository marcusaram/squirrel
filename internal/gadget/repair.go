package gadget

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Repair runs fsck on the filesystem inside a drive image. The drive must
// not be connected to the car while this runs.
func Repair(ctx context.Context, image string, offset int64, fsType string) (string, error) {
	out, err := exec.CommandContext(ctx, "losetup", "--find", "--show", "--offset", strconv.FormatInt(offset, 10), image).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("losetup: %v", err)
	}
	dev := strings.TrimSpace(string(out))
	defer exec.Command("losetup", "-d", dev).Run()

	var cmd *exec.Cmd
	switch fsType {
	case "exfat":
		cmd = exec.CommandContext(ctx, "fsck.exfat", "-p", dev)
	case "fat32":
		cmd = exec.CommandContext(ctx, "fsck.vfat", "-a", "-w", dev)
	default:
		return "", fmt.Errorf("unknown filesystem %q", fsType)
	}
	out, err = cmd.CombinedOutput()
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		// 1 means errors were found and corrected.
		err = nil
	}
	return string(out), err
}
