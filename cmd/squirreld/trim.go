package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// trimEvery is how often freed space on /backingfiles is passed on to the
// card (TRIM), so its controller has erased blocks ready. Once a week,
// instead of at every delete (the discard mount option), which slows each
// delete down.
const trimEvery = 7 * 24 * time.Hour

// trim runs fstrim on the data filesystem once a week, at the lowest I/O
// priority and only while the car is not using its drive. A systemd timer
// could not keep its last run on the read-only root; this keeps it in the
// state directory.
func (d *daemon) trim(ctx context.Context) {
	stamp := filepath.Join(d.cfg.Get().StateDir, "trimmed")
	// Not right after a start: the car wants its drive first.
	wait := 10 * time.Minute
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = time.Hour
		if fi, err := os.Stat(stamp); err == nil && time.Since(fi.ModTime()) < trimEvery {
			continue
		}
		if d.gadget.Status().State == "configured" || d.computerOn() {
			continue
		}
		out, err := exec.CommandContext(ctx, "ionice", "-c", "3", "fstrim", "-v", dataDir).CombinedOutput()
		msg := strings.TrimSpace(string(out))
		if err != nil {
			// A card or reader without TRIM: no use trying every hour.
			d.log.Warn("trim", "err", err, "output", msg)
		} else {
			d.log.Info("trim", "result", msg)
		}
		os.WriteFile(stamp, nil, 0o644)
		now := time.Now()
		os.Chtimes(stamp, now, now)
	}
}
