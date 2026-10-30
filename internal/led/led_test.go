package led

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPatterns(t *testing.T) {
	sys := t.TempDir()
	if Find(sys) != nil {
		t.Fatal("found an LED in an empty sysfs")
	}
	dir := filepath.Join(sys, "class/leds/ACT")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "trigger"), []byte("none [actpwr] heartbeat timer\n"), 0o644)
	l := Find(sys)
	if l == nil {
		t.Fatal("ACT not found")
	}
	read := func(n string) string { b, _ := os.ReadFile(filepath.Join(dir, n)); return string(b) }

	l.Set(Waiting)
	if read("trigger") != "timer\n" || read("delay_on") != "80\n" || read("delay_off") != "1920\n" {
		t.Errorf("waiting: %q %q %q", read("trigger"), read("delay_on"), read("delay_off"))
	}
	// A kernel without the pattern trigger: the kernel's own heartbeat.
	l.load = func(string) error { return os.ErrNotExist }
	l.Set(Connected)
	if read("trigger") != "heartbeat\n" {
		t.Errorf("connected without pattern: %q", read("trigger"))
	}
	// With it (loaded on demand): a double tick every second.
	l.Set(Waiting)
	l.load = func(string) error {
		return os.WriteFile(filepath.Join(dir, "trigger"), []byte("none [timer] heartbeat pattern\n"), 0o644)
	}
	os.WriteFile(filepath.Join(dir, "max_brightness"), []byte("1\n"), 0o644)
	l.Set(Connected)
	if read("trigger") != "pattern\n" || read("pattern") != "1 70 1 0 0 130 0 0 1 70 1 0 0 1230 0 0\n" {
		t.Errorf("connected at rest: %q %q", read("trigger"), read("pattern"))
	}
	// Busy: quicker, but never more than 60 a minute.
	l.Load(9)
	if read("pattern") != "1 70 1 0 0 130 0 0 1 70 1 0 0 730 0 0\n" {
		t.Errorf("connected under load: %q", read("pattern"))
	}
	for _, c := range []struct {
		load float64
		ms   int
	}{{0, 1500}, {0.5, 1375}, {1, 1250}, {2, 1125}, {100, 1000}} {
		if got := heartbeatPeriod(c.load); got != c.ms {
			t.Errorf("load %g: %d ms, want %d", c.load, got, c.ms)
		}
	}
	// Repeating a pattern must not rewrite the files (it would restart the
	// blink every two seconds).
	os.WriteFile(filepath.Join(dir, "trigger"), []byte("marker\n"), 0o644)
	l.Set(Connected)
	if read("trigger") != "marker\n" {
		t.Error("same pattern written again")
	}
	var none *LED
	none.Set(Problem) // a Pi without LED must not crash
}

// Armbian has the timer trigger as a module: loaded on demand, and the LED
// is left alone when that fails.
func TestTimerModule(t *testing.T) {
	sys := t.TempDir()
	dir := filepath.Join(sys, "class/leds/blue:status")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "trigger"), []byte("none [heartbeat] pattern\n"), 0o644)
	read := func(n string) string { b, _ := os.ReadFile(filepath.Join(dir, n)); return string(b) }
	l := Find(sys)
	var loaded []string
	l.load = func(m string) error { loaded = append(loaded, m); return os.ErrNotExist }
	l.Set(Busy)
	if read("trigger") != "none [heartbeat] pattern\n" || len(loaded) != 1 || loaded[0] != "ledtrig-timer" {
		t.Errorf("busy without the timer module: %q, loaded %q", read("trigger"), loaded)
	}
	l.load = func(m string) error {
		return os.WriteFile(filepath.Join(dir, "trigger"), []byte("none [heartbeat] pattern timer\n"), 0o644)
	}
	l.Set(Problem)
	if read("trigger") != "timer\n" || read("delay_on") != "40\n" {
		t.Errorf("problem with the timer module: %q %q", read("trigger"), read("delay_on"))
	}
}
