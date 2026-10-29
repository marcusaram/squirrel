package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The state of teslausbd, as squirreld was called, moves to the new place.
func TestMigrate(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "teslausbd")
	os.MkdirAll(filepath.Join(legacy, "tombstones"), 0o755)
	os.WriteFile(filepath.Join(legacy, "config.json"), []byte(`{"state_dir":"x"}`), 0o600)
	path := filepath.Join(dir, "squirreld", "config.json")
	if err := migrate(path, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "squirreld", "tombstones")); err != nil {
		t.Error("state not moved")
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Error("old directory still there")
	}
	// The first start may have made the new directory already.
	dir2 := t.TempDir()
	legacy2 := filepath.Join(dir2, "teslausbd")
	os.MkdirAll(legacy2, 0o755)
	os.WriteFile(filepath.Join(legacy2, "config.json"), []byte(`{}`), 0o600)
	os.MkdirAll(filepath.Join(dir2, "squirreld"), 0o755)
	if err := migrate(filepath.Join(dir2, "squirreld", "config.json"), legacy2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir2, "squirreld", "config.json")); err != nil {
		t.Error("settings not moved into the existing directory")
	}

	// Once moved, a new old directory is left alone.
	os.MkdirAll(legacy, 0o755)
	if err := migrate(path, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Error("touched the old directory after the move")
	}
}

// The old default scan interval of a minute was too slow for recordings the
// car deletes when you drive off; a saved 60 becomes the new default, a
// chosen value stays.
func TestScanIntervalMigrates(t *testing.T) {
	for _, c := range []struct{ saved, want int }{{60, 5}, {20, 20}, {1, 2}} {
		path := filepath.Join(t.TempDir(), "config.json")
		os.WriteFile(path, []byte(fmt.Sprintf(`{"scan_interval_sec":%d,"wifi":{"ap_ssid":"x","ap_password":"y"}}`, c.saved)), 0o600)
		s, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Get().ScanInterval; got != c.want {
			t.Errorf("saved %d: %d, want %d", c.saved, got, c.want)
		}
	}
}
