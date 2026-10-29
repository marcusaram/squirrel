// Package config holds squirreld's settings, stored as JSON next to the
// data so it stays writable on a read-only root filesystem.
package config

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const DefaultPath = "/backingfiles/squirreld/config.json"

// Before Squirrel, squirreld was teslausbd and kept its settings and state
// in /backingfiles/teslausbd.
const (
	legacyDir      = "/backingfiles/teslausbd"
	defaultDataDir = "/backingfiles/squirreld"
)

// migrate moves the state of the earlier name to the new directory as long
// as the new one has no settings yet; it may already exist (the first
// start creates it). Same filesystem, so every move is a rename.
func migrate(path, legacy string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(legacy, filepath.Base(path))); err != nil {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(legacy)
	if err != nil {
		return err
	}
	for _, e := range entries {
		dst := filepath.Join(dir, e.Name())
		if _, err := os.Stat(dst); err == nil {
			continue // something new of the same name; the old one stays
		}
		if err := os.Rename(filepath.Join(legacy, e.Name()), dst); err != nil {
			return err
		}
	}
	os.Remove(legacy) // only when empty
	return nil
}

type Drive struct {
	Name  string `json:"name"`  // cam, music, lightshow, boombox
	Image string `json:"image"` // backing file exposed to the car
}

// Folders selects TeslaCam folders.
type Folders struct {
	Recent bool `json:"recent"`
	Saved  bool `json:"saved"`
	Sentry bool `json:"sentry"`
	Track  bool `json:"track"`
}

type Gadget struct {
	UDC        string `json:"udc,omitempty"`          // empty: first one found
	MaxPowerMA int    `json:"max_power_ma,omitempty"` // 0: based on board model
}

type NAS struct {
	Enabled  bool    `json:"enabled"`
	Host     string  `json:"host"` // host or host:port
	Share    string  `json:"share"`
	Path     string  `json:"path"` // directory inside the share
	User     string  `json:"user"`
	Password string  `json:"password"`
	Domain   string  `json:"domain"`
	Upload   Folders `json:"upload"`
}

type Web struct {
	Listen   string `json:"listen"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
}

// WiFi controls the fallback hotspot. Known networks themselves are kept
// by NetworkManager.
type WiFi struct {
	// AP starts a hotspot when no known network is in reach, so a phone can
	// reach the web UI (in the car, or to set up wifi).
	AP         bool   `json:"ap"`
	APSSID     string `json:"ap_ssid"`
	APPassword string `json:"ap_password"`
}

type Config struct {
	Drives       []Drive `json:"drives"`
	ArchiveDir   string  `json:"archive_dir"`
	StateDir     string  `json:"state_dir"`
	ScanInterval int     `json:"scan_interval_sec"`
	Keep         Folders `json:"keep"`
	ReserveGB    int     `json:"reserve_gb"`
	Gadget       Gadget  `json:"gadget"`
	NAS          NAS     `json:"nas"`
	Web          Web     `json:"web"`
	WiFi         WiFi    `json:"wifi"`
	// Diagnostics keeps a status report and the system log across restarts
	// and puts them on the car's drive at each start. Off by default: it
	// costs SD card writes.
	Diagnostics bool `json:"diagnostics"`
}

func Default() Config {
	return Config{
		Drives:       []Drive{{Name: "cam", Image: "/backingfiles/cam_disk.bin"}},
		ArchiveDir:   "/backingfiles/archive",
		StateDir:     "/backingfiles/squirreld",
		ScanInterval: 5,
		Keep:         Folders{Recent: true, Saved: true, Sentry: true, Track: true},
		ReserveGB:    10,
		NAS: NAS{
			Path:   "TeslaCam",
			Upload: Folders{Saved: true, Sentry: true, Track: true},
		},
		Web:  Web{Listen: ":80"},
		WiFi: WiFi{AP: true, APSSID: "Squirrel"},
	}
}

// RandomPassword returns n characters that are easy to type on a phone.
func RandomPassword(n int) string {
	const chars = "abcdefghjkmnpqrstuvwxyz23456789"
	b := make([]byte, n)
	rand.Read(b)
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}

// CamImage returns the image the car records to.
func (c Config) CamImage() string {
	for _, d := range c.Drives {
		if d.Name == "cam" {
			return d.Image
		}
	}
	return ""
}

// Store gives concurrent access to the current config and persists changes.
type Store struct {
	mu   sync.RWMutex
	path string
	cfg  Config
	subs []func(old, new Config)
}

// Load reads path. A missing file yields the defaults, completed with what
// an earlier teslausb install left behind: its extra drive images and its
// NAS settings. That first config is saved right away.
func Load(path string) (*Store, error) {
	if path == DefaultPath {
		if err := migrate(path, legacyDir); err != nil {
			return nil, fmt.Errorf("moving %s to %s: %w", legacyDir, filepath.Dir(path), err)
		}
	}
	s := &Store{path: path, cfg: Default()}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		for _, name := range []string{"music", "lightshow", "boombox"} {
			img := "/backingfiles/" + name + "_disk.bin"
			if _, err := os.Stat(img); err == nil {
				s.cfg.Drives = append(s.cfg.Drives, Drive{Name: name, Image: img})
			}
		}
		applyTeslausb(&s.cfg, readTeslausb(TeslausbSettings))
		s.cfg.WiFi.APPassword = RandomPassword(10)
		save(path, s.cfg) // best effort; the defaults work without it
		return s, nil
	case err != nil:
		return nil, err
	}
	if err := json.Unmarshal(data, &s.cfg); err != nil {
		return nil, err
	}
	if s.cfg.StateDir == legacyDir {
		s.cfg.StateDir = defaultDataDir
		save(path, s.cfg)
	}
	if s.cfg.WiFi.APPassword == "" {
		if s.cfg.WiFi.APSSID == "" {
			s.cfg.WiFi = Default().WiFi
		}
		s.cfg.WiFi.APPassword = RandomPassword(10)
		save(path, s.cfg)
	}
	// 60 was the default until the car turned out to delete what it
	// recorded while parked within a minute of driving off. The settings
	// page no longer allows more than 30, so a 60 is always that default.
	if s.cfg.ScanInterval == 60 {
		s.cfg.ScanInterval = Default().ScanInterval
		save(path, s.cfg)
	}
	if s.cfg.ScanInterval < 2 {
		s.cfg.ScanInterval = 2
	}
	return s, nil
}

func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// OnChange registers fn to run after every successful Update.
func (s *Store) OnChange(fn func(old, new Config)) {
	s.mu.Lock()
	s.subs = append(s.subs, fn)
	s.mu.Unlock()
}

// Update applies fn to a copy of the config and saves it.
func (s *Store) Update(fn func(*Config) error) error {
	s.mu.Lock()
	old := s.cfg
	cfg := old
	cfg.Drives = append([]Drive(nil), old.Drives...)
	if err := fn(&cfg); err != nil {
		s.mu.Unlock()
		return err
	}
	if err := save(s.path, cfg); err != nil {
		s.mu.Unlock()
		return err
	}
	s.cfg = cfg
	subs := s.subs
	s.mu.Unlock()
	for _, f := range subs {
		f(old, cfg)
	}
	return nil
}

func save(path string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	f, err := os.Open(tmp)
	if err == nil {
		f.Sync()
		f.Close()
	}
	return os.Rename(tmp, path)
}
