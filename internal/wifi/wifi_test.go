package wifi

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcusaram/squirrel/internal/config"
)

// fakeNM plays NetworkManager: which network is up and who is on the
// hotspot, and records what the manager asks for.
type fakeNM struct {
	conn    string // active connection on wlan0, "" when searching
	clients int
	calls   []string
}

func (f *fakeNM) run(name string, args ...string) (string, error) {
	cmd := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, cmd)
	switch {
	case strings.HasPrefix(cmd, "nmcli -t -f DEVICE,TYPE,STATE,CONNECTION device"):
		if f.conn == "" {
			return "wlan0:wifi:disconnected:\nlo:loopback:connected (externally):lo\n", nil
		}
		return "wlan0:wifi:connected:" + f.conn + "\n", nil
	case strings.HasPrefix(cmd, "iw dev wlan0 link"):
		return "Connected to 12:34:56:78:9a:bc (on wlan0)\n\tSSID: " + f.conn + "\n\tfreq: 2437\n", nil
	case strings.HasPrefix(cmd, "iw dev wlan0 station dump"):
		return strings.Repeat("Station aa:bb:cc:dd:ee:ff (on wlan0)\n\tinactive time: 10 ms\n", f.clients), nil
	case strings.HasPrefix(cmd, "nmcli connection up "+HotspotConn):
		f.conn = HotspotConn
	case strings.HasPrefix(cmd, "nmcli connection down "+HotspotConn):
		f.conn = ""
	}
	return "", nil
}

func TestHotspotLifecycle(t *testing.T) {
	nm := &fakeNM{}
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	cfg := config.WiFi{AP: true, APSSID: "Squirrel", APPassword: "geheim123"}
	m := &Manager{Config: func() config.WiFi { return cfg }, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Run: nm.run, Now: func() time.Time { return now }, LinkUp: func() bool { return nm.conn != "" },
		Writable: func(fn func() error) error { nm.calls = append(nm.calls, "writable"); return fn() }}
	step := func(d time.Duration) Status { now = now.Add(d); m.Step(); return m.Status() }

	// In the car: no known network. Keep searching for 90 s, then hotspot.
	if s := step(0); s.Mode != "searching" {
		t.Fatalf("start: %+v", s)
	}
	if s := step(60 * time.Second); s.Mode != "searching" {
		t.Fatalf("after 60s: %+v", s)
	}
	// Searching without a link asks nmcli nothing until the link comes up.
	if n := len(nm.calls); n != 1 {
		t.Errorf("nmcli asked %d times while searching: %v", n, nm.calls)
	}
	if s := step(40 * time.Second); s.Mode != "hotspot" || s.SSID != "Squirrel" {
		t.Fatalf("after 100s: %+v", s)
	}
	// A phone joins and stays for a long time: the hotspot stays.
	nm.clients = 1
	for range 30 {
		if s := step(time.Minute); s.Mode != "hotspot" || s.Clients != 1 {
			t.Fatalf("with a phone: %+v", s)
		}
	}
	// The phone leaves: after a few quiet minutes, look for known networks.
	nm.clients = 0
	if s := step(3 * time.Minute); s.Mode != "hotspot" {
		t.Fatalf("3 quiet minutes: %+v", s)
	}
	if s := step(2 * time.Minute); s.Mode != "searching" {
		t.Fatalf("5 quiet minutes: %+v", s)
	}
	// Home: NetworkManager joins the home network by itself.
	nm.conn = "Thuis"
	if s := step(10 * time.Second); s.Mode != "client" || s.SSID != "Thuis" {
		t.Fatalf("at home: %+v", s)
	}
	for range 20 {
		if s := step(time.Minute); s.Mode != "client" {
			t.Fatalf("stays on home wifi: %+v", s)
		}
	}
	// Hotspot turned off in the settings: never started.
	nm.conn = ""
	cfg.AP = false
	if s := step(10 * time.Minute); s.Mode == "hotspot" {
		t.Fatalf("hotspot disabled: %+v", s)
	}
	// The hotspot was created once, with the configured name and password.
	adds := 0
	for _, c := range nm.calls {
		if strings.HasPrefix(c, "nmcli connection add") {
			adds++
			if !strings.Contains(c, "ssid Squirrel") || !strings.Contains(c, "wifi-sec.psk geheim123") || !strings.Contains(c, "ipv4.method shared") {
				t.Errorf("hotspot created as: %s", c)
			}
		}
	}
	if adds != 1 {
		t.Errorf("hotspot connection created %d times", adds)
	}
	// NetworkManager stores it on /backingfiles; remounting root would
	// not reach NetworkManager and only cost writes to the card.
	for _, c := range nm.calls {
		if c == "writable" {
			t.Errorf("root made writable for nmcli: %v", nm.calls)
			break
		}
	}
}

func TestFields(t *testing.T) {
	got := fields(`Thuis\: 5G:a1b2:802-11-wireless:yes`)
	if len(got) != 4 || got[0] != "Thuis: 5G" || got[3] != "yes" {
		t.Errorf("fields = %q", got)
	}
}

func TestWirelessIface(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "eth0"), 0o755)
	os.MkdirAll(filepath.Join(dir, "wlP1p1s0", "wireless"), 0o755)
	if got := wirelessIface(dir); got != "wlP1p1s0" {
		t.Errorf("got %q", got)
	}
	if got := wirelessIface(filepath.Join(dir, "none")); got != "wlan0" {
		t.Errorf("fallback: got %q", got)
	}
}

// Connected to a known network, nmcli is asked only every ten minutes
// while the link stays up, and at once when it goes.
func TestClientPollsLittle(t *testing.T) {
	var calls int
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	link := true
	m := &Manager{Config: func() config.WiFi { return config.WiFi{AP: true, APSSID: "Squirrel", APPassword: "geheim123"} },
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now },
		LinkUp: func() bool { return link },
		Run: func(name string, args ...string) (string, error) {
			calls++
			switch {
			case name == "iw":
				return "Connected to 12:34:56:78:9a:bc (on wlan0)\n\tSSID: Home\n", nil
			case link:
				return "wlan0:wifi:connected:Home 1\n", nil
			}
			return "wlan0:wifi:disconnected:\n", nil
		}}
	m.Step()
	// The network's name, not its profile's ("Home 1").
	if s := m.Status(); s.Mode != "client" || s.SSID != "Home" || calls != 2 {
		t.Fatalf("first step: %+v, %d calls", s, calls)
	}
	for i := 0; i < 59; i++ { // 9 min 50 s
		now = now.Add(10 * time.Second)
		m.Step()
	}
	if calls != 2 {
		t.Errorf("asked %d times in ten minutes with the link up", calls-2)
	}
	now = now.Add(10 * time.Second)
	m.Step()
	if calls != 4 {
		t.Errorf("not asked after ten minutes: %d calls", calls)
	}
	link = false
	now = now.Add(10 * time.Second)
	m.Step()
	if m.Status().Mode != "searching" {
		t.Errorf("link gone, status %+v", m.Status())
	}
}
