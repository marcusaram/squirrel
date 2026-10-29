package diag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteOnlyWhenChanged(t *testing.T) {
	p := filepath.Join(t.TempDir(), "status.txt")
	report := "Wifi: mode=client"
	on := true
	w := &Writer{Path: p, Report: func() string { return report }, Enabled: func() bool { return on }}
	w.write()
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "Wifi: mode=client") {
		t.Fatalf("status.txt = %q", b)
	}
	os.WriteFile(p, []byte("marker"), 0o644)
	w.write()
	if b, _ := os.ReadFile(p); string(b) != "marker" {
		t.Error("rewritten although nothing changed")
	}
	report = "Wifi: mode=hotspot"
	w.write()
	if b, _ := os.ReadFile(p); !strings.Contains(string(b), "hotspot") {
		t.Error("change not written")
	}
	// Switched off: nothing is written at all.
	on = false
	report = "Wifi: mode=searching"
	w.write()
	if b, _ := os.ReadFile(p); strings.Contains(string(b), "searching") {
		t.Error("written while diagnostics are off")
	}
}
