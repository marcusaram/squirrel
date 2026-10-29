// Package diag keeps an optional status report and the system log, and
// puts both on the car's drive at each start. Plugged into a computer, the
// Pi then shows what happened last time, without taking the SD card out.
package diag

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CamFile is the name of the report on the car's drive.
const CamFile = "squirrel-log.txt"

type Writer struct {
	Path    string
	Report  func() string // the part that changes; time is added here
	Enabled func() bool
	last    string
}

// Run writes the report every 30 seconds when it has changed, so the SD
// card is not written to for nothing.
func (w *Writer) Run(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		w.write()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (w *Writer) write() {
	if w.Enabled != nil && !w.Enabled() {
		w.last = ""
		return
	}
	body := w.Report()
	if body == w.last {
		return
	}
	w.last = body
	text := fmt.Sprintf("Squirrel status, %s\n(the clock is only right once the Pi has had internet)\n\n%s",
		time.Now().Format("2006-01-02 15:04:05"), body)
	// Rewritten in place and synced: the Pi loses power without warning.
	f, err := os.OpenFile(w.Path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return
	}
	f.WriteString(text)
	f.Sync()
	f.Close()
}

// Command output for the report, trimmed.
func Command(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil && s == "" {
		s = err.Error()
	}
	return s
}

const journalDir = "/var/log/journal"

// Journal makes the system log survive restarts, or keeps it in memory
// only (the Raspberry Pi OS default, no SD card writes). Root is
// read-only, so a kept journal lives in store on /backingfiles and is
// mounted over /var/log/journal.
func Journal(keep bool, store string) {
	mounted := Command("findmnt", "-n", "-o", "TARGET", journalDir) == journalDir
	switch {
	case keep && !mounted:
		os.MkdirAll(store, 0o755)
		os.MkdirAll(journalDir, 0o755) // /var/log is in RAM
		if out := Command("mount", "--bind", store, journalDir); out != "" {
			return
		}
		Command("systemd-tmpfiles", "--create", "--prefix", journalDir)
		Command("journalctl", "--flush")
	case !keep:
		Command("journalctl", "--relinquish-var")
		if mounted {
			Command("umount", journalDir)
		}
		os.RemoveAll(store)
	}
}

// ForCam is the text for the car's drive: the last status report and the
// system log of the previous run.
func ForCam(statusPath string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "Squirrel log, written at start %s\n", time.Now().Format("2006-01-02 15:04:05"))
	b.WriteString("This file is replaced at every start while diagnostics are on.\n\n")
	b.WriteString("=== Status at the end of the previous run\n\n")
	if data, err := os.ReadFile(statusPath); err == nil {
		b.Write(data)
	} else {
		b.WriteString("(none)\n")
	}
	b.WriteString("\n=== System log of the previous run\n\n")
	b.WriteString(Command("journalctl", "-b", "-1", "--no-pager", "-n", "400", "-o", "short"))
	b.WriteString("\n")
	return []byte(strings.ReplaceAll(b.String(), "\n", "\r\n"))
}
