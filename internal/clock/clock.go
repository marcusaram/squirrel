// Package clock keeps the system time roughly right across power cuts.
// A Pi has no battery-backed clock; Raspberry Pi OS remembers the time in
// a file on root, which is read-only here. In the car there is often no
// network to get the time from, so it is kept on /backingfiles instead.
package clock

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

const interval = 15 * time.Minute

func saved(path string) time.Time {
	b, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(n, 0)
}

// Restore moves the clock forward to the saved time if it is behind.
func Restore(path string, log *slog.Logger) {
	t := saved(path)
	if t.IsZero() || !time.Now().Before(t) {
		return
	}
	if err := setTime(t); err != nil {
		log.Warn("restoring the clock", "err", err)
		return
	}
	log.Info("clock restored from the last saved time", "time", t.Format(time.DateTime))
}

// Keep saves the time every 15 minutes.
func Keep(ctx context.Context, path string) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		save(path)
		select {
		case <-ctx.Done():
			save(path)
			return
		case <-t.C:
		}
	}
}

func save(path string) {
	now := time.Now()
	if now.Before(saved(path)) {
		return
	}
	os.WriteFile(path, []byte(strconv.FormatInt(now.Unix(), 10)+"\n"), 0o644)
}
