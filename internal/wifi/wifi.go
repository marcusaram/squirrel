// Package wifi manages wifi through NetworkManager: the networks the Pi
// joins, and a fallback hotspot for when none of them is in reach. With the
// hotspot a phone can reach the web UI in the car, or to set up wifi.
package wifi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/marcusaram/squirrel/internal/config"
	"github.com/marcusaram/squirrel/internal/sysrw"
)

const (
	HotspotConn = "squirrel-hotspot"
	// legacyHotspot is the hotspot from before Squirrel; it is removed.
	legacyHotspot = "teslausb-hotspot"
	HotspotIP     = "10.42.0.1"

	// No known network for this long: start the hotspot.
	searchTime = 90 * time.Second
	// Hotspot without anyone on it for this long: drop it and look for
	// known networks again (arriving home, for the NAS upload).
	idleHotspot = 4 * time.Minute
)

type Status struct {
	Available bool   `json:"available"`
	Mode      string `json:"mode"` // client, hotspot, searching, off
	SSID      string `json:"ssid,omitempty"`
	Clients   int    `json:"clients,omitempty"`
	Error     string `json:"error,omitempty"`
}

type Network struct {
	UUID   string `json:"uuid"`
	SSID   string `json:"ssid"`
	Active bool   `json:"active"`
}

type Seen struct {
	SSID   string `json:"ssid"`
	Signal int    `json:"signal"`
	Secure bool   `json:"secure"`
}

type Manager struct {
	Config func() config.WiFi
	Log    *slog.Logger

	// Replaceable for tests.
	Run      func(name string, args ...string) (string, error)
	Now      func() time.Time
	Writable func(fn func() error) error // makes root writable around fn
	LinkUp   func() bool                 // the kernel's view; nil reads sysfs

	// NetworkManager keeps its connections in a directory on
	// /backingfiles (bind-mounted by fstab): it runs in its own mount
	// namespace, so remounting root writable would not reach it.

	mu        sync.Mutex
	status    Status
	applied   config.WiFi
	since     time.Time // start of the current search, or hotspot uptime
	lastBusy  time.Time // last time someone was on the hotspot
	lastFull  time.Time // last time nmcli was asked
	lastLink  bool      // the link then
	lastError string
}

// iface is the wifi interface: wlan0 on a Raspberry Pi, but the name is
// up to the kernel and udev on other boards. Looked up each time, as the
// driver may only appear after squirreld started.
func iface() string { return wirelessIface("/sys/class/net") }

func wirelessIface(sysNet string) string {
	ents, _ := os.ReadDir(sysNet)
	for _, e := range ents {
		if _, err := os.Stat(filepath.Join(sysNet, e.Name(), "wireless")); err == nil {
			return e.Name()
		}
	}
	return "wlan0"
}

func New(cfg func() config.WiFi, log *slog.Logger) *Manager {
	return &Manager{Config: cfg, Log: log, Run: run, Now: time.Now,
		Writable: func(fn func() error) error { return sysrw.Writable("/", fn) }}
}

func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s: %v: %s", name, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// fields splits a line of `nmcli -t` output, where ':' separates fields
// and '\' escapes ':' and '\' inside values.
func fields(line string) []string {
	var out []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '\\' && i+1 < len(line):
			i++
			cur.WriteByte(line[i])
		case c == ':':
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	return append(out, cur.String())
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimRight(l, "\r"); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// HotspotActive reports whether phones are being served by the hotspot;
// the web server then answers captive portal checks.
func (m *Manager) HotspotActive() bool {
	return m.Status().Mode == "hotspot"
}

// state reads what the wifi interface is doing.
func (m *Manager) state() (Status, error) {
	out, err := m.Run("nmcli", "-t", "-f", "DEVICE,TYPE,STATE,CONNECTION", "device")
	if err != nil {
		return Status{}, err
	}
	st := Status{Mode: "off"}
	for _, l := range lines(out) {
		f := fields(l)
		if len(f) < 4 || f[0] != iface() {
			continue
		}
		st.Available = true
		switch {
		case f[3] == HotspotConn:
			st.Mode = "hotspot"
		case strings.HasPrefix(f[2], "connected"):
			st.Mode = "client"
			st.SSID = f[3]
		case f[2] == "unavailable":
			st.Mode = "off"
		default:
			st.Mode = "searching"
		}
	}
	if !st.Available {
		return st, nil
	}
	// The signal strength is not asked for: the status page shows it from
	// /proc/net/wireless, for free and only while someone looks.
	switch st.Mode {
	case "client":
		// The connection's name is not always the network's (a second
		// profile for it is called "Home 1"); the link knows.
		if out, err := m.Run("iw", "dev", iface(), "link"); err == nil {
			for _, l := range lines(out) {
				if ssid, ok := strings.CutPrefix(strings.TrimSpace(l), "SSID: "); ok {
					st.SSID = ssid
				}
			}
		}
	case "hotspot":
		st.SSID = m.Config().APSSID
		if out, err := m.Run("iw", "dev", iface(), "station", "dump"); err == nil {
			st.Clients = strings.Count(out, "Station ")
		}
	}
	return st, nil
}

// fullEvery is how often nmcli is asked while the link stays as it was:
// connected, only to notice a move to another known network; searching,
// in case the kernel's view and NetworkManager's ever disagree.
const fullEvery = 10 * time.Minute

// linkUp tells whether the wifi interface has a link, from sysfs.
func (m *Manager) linkUp() bool {
	if m.LinkUp != nil {
		return m.LinkUp()
	}
	b, err := os.ReadFile(filepath.Join("/sys/class/net", iface(), "operstate"))
	return err == nil && strings.TrimSpace(string(b)) == "up"
}

// Step looks at the current state once and starts or stops the hotspot.
func (m *Manager) Step() {
	now := m.Now()
	// nmcli costs a Pi Zero W a fifth of a second of its one core, and the
	// kernel tells for free whether the link is up. Connected, or searching
	// with no link, nothing can have changed until the link does; nmcli is
	// asked then, and otherwise rarely. The hotspot keeps its checks: it
	// counts the phones on it.
	link := m.linkUp()
	m.mu.Lock()
	prev := m.status
	same := link == m.lastLink && now.Sub(m.lastFull) < fullEvery
	m.mu.Unlock()
	if same && prev.Mode == "client" && link {
		return
	}
	var st Status
	var err error
	if same && prev.Mode == "searching" && !link {
		st = prev // still searching; the search time runs on below
	} else {
		m.mu.Lock()
		m.lastFull, m.lastLink = now, link
		m.mu.Unlock()
		st, err = m.state()
	}
	cfg := m.Config()
	m.mu.Lock()
	if m.since.IsZero() {
		m.since = now
	}
	prevMode := m.status.Mode
	m.mu.Unlock()
	if err != nil {
		m.setStatus(Status{Error: err.Error()})
		return
	}
	if !st.Available {
		m.setStatus(st)
		return
	}
	if st.Mode != prevMode {
		m.mu.Lock()
		m.since = now
		m.mu.Unlock()
	}
	if prevMode == "" && st.Mode == "hotspot" {
		// Still running from before a restart of the daemon; leave the
		// phones on it alone.
		m.applied = cfg
	}
	if st.Mode == "hotspot" && cfg != m.applied {
		// Settings changed while it runs: rebuild it.
		m.down()
		st.Mode = "searching"
	}

	switch st.Mode {
	case "hotspot":
		m.mu.Lock()
		if st.Clients > 0 {
			m.lastBusy = now
		}
		quiet := now.Sub(maxTime(m.since, m.lastBusy))
		m.mu.Unlock()
		if !cfg.AP || quiet > idleHotspot {
			m.Log.Info("stopping hotspot to look for known networks", "idle", quiet.Round(time.Second))
			m.down()
			st.Mode = "searching"
			m.mu.Lock()
			m.since = now
			m.mu.Unlock()
		}
	case "searching", "off":
		m.mu.Lock()
		waited := now.Sub(m.since)
		m.mu.Unlock()
		if cfg.AP && cfg.APPassword != "" && waited > searchTime {
			if err := m.up(cfg); err != nil {
				st.Error = err.Error()
				m.Log.Error("starting hotspot", "err", err)
			} else {
				m.Log.Info("no known wifi in reach; hotspot started", "ssid", cfg.APSSID)
				st.Mode, st.SSID = "hotspot", cfg.APSSID
			}
			m.mu.Lock()
			m.since = now
			m.mu.Unlock()
		}
	}
	m.setStatus(st)
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func (m *Manager) setStatus(s Status) {
	m.mu.Lock()
	m.status = s
	m.mu.Unlock()
}

func (m *Manager) up(cfg config.WiFi) error {
	if cfg != m.applied {
		m.Run("nmcli", "connection", "delete", HotspotConn)
		_, err := m.Run("nmcli", "connection", "add", "type", "wifi", "ifname", iface(),
			"con-name", HotspotConn, "autoconnect", "no", "ssid", cfg.APSSID,
			"802-11-wireless.mode", "ap", "802-11-wireless.band", "bg",
			"ipv4.method", "shared", "ipv4.addresses", HotspotIP+"/24",
			"wifi-sec.key-mgmt", "wpa-psk", "wifi-sec.psk", cfg.APPassword)
		if err != nil {
			return err
		}
		m.applied = cfg
	}
	_, err := m.Run("nmcli", "connection", "up", HotspotConn)
	return err
}

func (m *Manager) down() {
	m.Run("nmcli", "connection", "down", HotspotConn)
}

// captiveDNS makes the hotspot's DNS answer every name with the Pi, so a
// phone that joins it opens the web UI by itself.
const captiveDNS = "/etc/NetworkManager/dnsmasq-shared.d/squirrel-captive.conf"

// Loop runs Step every ten seconds.
func (m *Manager) Loop(ctx context.Context) {
	if _, err := exec.LookPath("nmcli"); err != nil {
		m.setStatus(Status{Error: "NetworkManager (nmcli) not found"})
		return
	}
	m.Run("rfkill", "unblock", "wifi")
	m.Run("nmcli", "connection", "delete", legacyHotspot)
	if _, err := os.Stat(captiveDNS); errors.Is(err, os.ErrNotExist) {
		m.Writable(func() error {
			os.MkdirAll("/etc/NetworkManager/dnsmasq-shared.d", 0o755)
			return os.WriteFile(captiveDNS, []byte("# squirreld: answer every name with the Pi on its hotspot\naddress=/#/"+HotspotIP+"\n"), 0o644)
		})
	}
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		m.Step()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Saved lists the wifi networks NetworkManager joins, without the hotspot.
func (m *Manager) Saved() ([]Network, error) {
	out, err := m.Run("nmcli", "-t", "-f", "NAME,UUID,TYPE,ACTIVE", "connection", "show")
	if err != nil {
		return nil, err
	}
	nets := []Network{}
	for _, l := range lines(out) {
		f := fields(l)
		if len(f) < 4 || f[2] != "802-11-wireless" || f[0] == HotspotConn || f[0] == legacyHotspot {
			continue
		}
		ssid := f[0]
		if s, err := m.Run("nmcli", "-g", "802-11-wireless.ssid", "connection", "show", f[1]); err == nil && strings.TrimSpace(s) != "" {
			ssid = strings.TrimSpace(s)
		}
		nets = append(nets, Network{UUID: f[1], SSID: ssid, Active: f[3] == "yes"})
	}
	return nets, nil
}

// Scan lists the networks in reach, strongest first per name.
func (m *Manager) Scan() ([]Seen, error) {
	out, err := m.Run("nmcli", "-t", "-f", "SSID,SIGNAL,SECURITY", "device", "wifi", "list", "--rescan", "yes")
	if err != nil {
		return nil, err
	}
	best := map[string]Seen{}
	var order []string
	for _, l := range lines(out) {
		f := fields(l)
		if len(f) < 3 || f[0] == "" {
			continue
		}
		sig, _ := strconv.Atoi(f[1])
		s := Seen{SSID: f[0], Signal: sig, Secure: f[2] != "" && f[2] != "--"}
		if old, ok := best[s.SSID]; !ok || sig > old.Signal {
			if !ok {
				order = append(order, s.SSID)
			}
			best[s.SSID] = s
		}
	}
	res := make([]Seen, 0, len(order))
	for _, k := range order {
		res = append(res, best[k])
	}
	return res, nil
}

// Add stores a network (or replaces the one with the same name) so
// NetworkManager joins it when in reach.
func (m *Manager) Add(ssid, password string) error {
	if ssid == "" || len(ssid) > 32 {
		return errors.New("the network name must be 1 to 32 characters")
	}
	if password != "" && (len(password) < 8 || len(password) > 63) {
		return errors.New("the password must be 8 to 63 characters")
	}
	if nets, err := m.Saved(); err == nil {
		for _, n := range nets {
			if n.SSID == ssid {
				m.Run("nmcli", "connection", "delete", n.UUID)
			}
		}
	}
	args := []string{"connection", "add", "type", "wifi", "ifname", iface(), "con-name", ssid, "ssid", ssid, "connection.autoconnect", "yes"}
	if password != "" {
		args = append(args, "wifi-sec.key-mgmt", "wpa-psk", "wifi-sec.psk", password)
	}
	_, err := m.Run("nmcli", args...)
	return err
}

func (m *Manager) Remove(uuid string) error {
	nets, err := m.Saved()
	if err != nil {
		return err
	}
	for _, n := range nets {
		if n.UUID == uuid {
			_, err := m.Run("nmcli", "connection", "delete", uuid)
			return err
		}
	}
	return errors.New("network not found")
}
