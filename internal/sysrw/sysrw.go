// Package sysrw makes the read-only root filesystem writable for the few
// moments squirreld changes system settings (a wifi network, the hotspot).
//
// Root is mounted read-only so a power cut in the car cannot damage it;
// everything that changes all the time lives on /backingfiles or in RAM.
package sysrw

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

var mu sync.Mutex

// ReadOnly reports whether the filesystem at mountpoint is mounted
// read-only, according to mounts (normally /proc/self/mounts).
func ReadOnly(mounts, mountpoint string) bool {
	f, err := os.Open(mounts)
	if err != nil {
		return false
	}
	defer f.Close()
	ro := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 || fields[1] != mountpoint {
			continue
		}
		// The last entry for a mountpoint is the one in effect.
		ro = false
		for _, o := range strings.Split(fields[3], ",") {
			if o == "ro" {
				ro = true
			}
		}
	}
	return ro
}

// Writable runs fn with mountpoint writable, and puts it back to
// read-only afterwards if it was read-only before.
func Writable(mountpoint string, fn func() error) error {
	mu.Lock()
	defer mu.Unlock()
	if !ReadOnly("/proc/self/mounts", mountpoint) {
		return fn()
	}
	if out, err := exec.Command("mount", "-o", "remount,rw", mountpoint).CombinedOutput(); err != nil {
		return fmt.Errorf("making %s writable: %v: %s", mountpoint, err, out)
	}
	ferr := fn()
	exec.Command("sync").Run()
	if out, err := exec.Command("mount", "-o", "remount,ro", mountpoint).CombinedOutput(); err != nil && ferr == nil {
		ferr = fmt.Errorf("making %s read-only again: %v: %s", mountpoint, err, out)
	}
	return ferr
}
