// Package sysinfo reads the few system values the dashboard shows, straight
// from /proc and /sys.
package sysinfo

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type System struct {
	Hostname  string   `json:"hostname"`
	Model     string   `json:"model,omitempty"`
	Uptime    float64  `json:"uptime"`
	Load      string   `json:"load,omitempty"`
	TempC     float64  `json:"temp_c,omitempty"`
	MemTotal  uint64   `json:"mem_total,omitempty"`
	MemAvail  uint64   `json:"mem_avail,omitempty"`
	WiFiLevel int      `json:"wifi_dbm,omitempty"`
	Addresses []string `json:"addresses,omitempty"`
}

func readTrim(p string) string {
	b, _ := os.ReadFile(p)
	return strings.TrimSpace(strings.TrimRight(string(b), "\x00"))
}

func Read() System {
	var s System
	s.Hostname, _ = os.Hostname()
	s.Model = readTrim("/proc/device-tree/model")
	if f := strings.Fields(readTrim("/proc/uptime")); len(f) > 0 {
		s.Uptime, _ = strconv.ParseFloat(f[0], 64)
	}
	if f := strings.Fields(readTrim("/proc/loadavg")); len(f) >= 3 {
		s.Load = strings.Join(f[:3], " ")
	}
	if t, err := strconv.ParseFloat(readTrim("/sys/class/thermal/thermal_zone0/temp"), 64); err == nil {
		s.TempC = t / 1000
	}
	if f, err := os.Open("/proc/meminfo"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) < 2 {
				continue
			}
			n, _ := strconv.ParseUint(fields[1], 10, 64)
			switch fields[0] {
			case "MemTotal:":
				s.MemTotal = n * 1024
			case "MemAvailable:":
				s.MemAvail = n * 1024
			}
		}
		f.Close()
	}
	// /proc/net/wireless: "wlan0: 0000   54.  -56.  -256 ..."
	if lines := strings.Split(readTrim("/proc/net/wireless"), "\n"); len(lines) > 2 {
		if f := strings.Fields(lines[2]); len(f) >= 4 {
			lvl, _ := strconv.ParseFloat(strings.TrimSuffix(f[3], "."), 64)
			s.WiFiLevel = int(lvl)
		}
	}
	if ifs, err := net.Interfaces(); err == nil {
		for _, i := range ifs {
			if i.Flags&net.FlagLoopback != 0 || i.Flags&net.FlagUp == 0 {
				continue
			}
			addrs, _ := i.Addrs()
			for _, a := range addrs {
				if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
					s.Addresses = append(s.Addresses, i.Name+" "+ipn.IP.String())
				}
			}
		}
	}
	return s
}

// Activity measures how much the car reads and writes through the mass
// storage gadget, using the I/O counters of the kernel's file-storage
// thread.
type Activity struct {
	mu    sync.Mutex
	pid   string
	lastR uint64
	lastW uint64
	lastT time.Time
	snap  ActivitySnapshot
}

type ActivitySnapshot struct {
	ReadBps   float64   `json:"read_bps"`
	WriteBps  float64   `json:"write_bps"`
	LastWrite time.Time `json:"last_write,omitzero"`
}

func (a *Activity) findPID() string {
	procs, _ := filepath.Glob("/proc/[0-9]*/comm")
	for _, p := range procs {
		if readTrim(p) == "file-storage" {
			return filepath.Base(filepath.Dir(p))
		}
	}
	return ""
}

// Sample updates the rates; call it every few seconds.
func (a *Activity) Sample() ActivitySnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pid == "" || readTrim("/proc/"+a.pid+"/comm") != "file-storage" {
		a.pid = a.findPID()
		a.lastT = time.Time{}
	}
	var r, w uint64
	if a.pid != "" {
		for _, line := range strings.Split(readTrim("/proc/"+a.pid+"/io"), "\n") {
			k, v, ok := strings.Cut(line, ": ")
			if !ok {
				continue
			}
			n, _ := strconv.ParseUint(v, 10, 64)
			switch k {
			case "read_bytes":
				r = n
			case "write_bytes":
				w = n
			}
		}
	}
	now := time.Now()
	if !a.lastT.IsZero() {
		dt := now.Sub(a.lastT).Seconds()
		if dt > 0 && r >= a.lastR && w >= a.lastW {
			a.snap.ReadBps = float64(r-a.lastR) / dt
			a.snap.WriteBps = float64(w-a.lastW) / dt
		}
		if w > a.lastW {
			a.snap.LastWrite = now
		}
	} else {
		a.snap.ReadBps, a.snap.WriteBps = 0, 0
	}
	a.lastR, a.lastW, a.lastT = r, w, now
	return a.snap
}

// LoadAverage is the 1-minute load average, 0 when it cannot be read.
func LoadAverage() float64 {
	f := strings.Fields(readTrim("/proc/loadavg"))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}
