package main

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/marcusaram/squirrel/internal/led"
)

// dataDir is the filesystem everything lives on: a partition of the card
// or a USB drive, mounted at /backingfiles.
const dataDir = "/backingfiles"

// mounted reports whether dir is a mountpoint: on another device than its
// parent.
func mounted(dir string) bool {
	var a, b syscall.Stat_t
	if syscall.Stat(dir, &a) != nil || syscall.Stat(filepath.Dir(dir), &b) != nil {
		return false
	}
	return a.Dev != b.Dev
}

// waitForData holds the start until /backingfiles is mounted. Without it,
// /backingfiles is an empty directory on the root filesystem: no drive for
// the car, no config, and nothing may be written there. A USB drive can
// also appear late, or be unplugged. It returns false when ctx ends first.
func waitForData(ctx context.Context, cfgPath string, ld *led.LED, log *slog.Logger) bool {
	if !strings.HasPrefix(cfgPath, dataDir+"/") || mounted(dataDir) {
		return true
	}
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		// Not set up at all (a development machine): nothing to wait for.
		return true
	}
	log.Error(dataDir + " is not mounted; waiting for it (is the USB drive connected?)")
	ld.Set(led.Problem)
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
		}
		// systemd gives up on a drive that is not there at boot; one that
		// is plugged in later is mounted here, by its fstab entry.
		exec.Command("mount", dataDir).Run()
		if mounted(dataDir) {
			log.Info(dataDir + " is mounted")
			// Its bind for NetworkManager's networks failed at boot too.
			if nm := "/etc/NetworkManager/system-connections"; !mounted(nm) && exec.Command("mount", nm).Run() == nil {
				exec.Command("nmcli", "connection", "reload").Run()
			}
			return true
		}
	}
}
