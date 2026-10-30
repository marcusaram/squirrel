// Package led shows the state of the Pi on its activity LED, the only
// thing you can see of it in the car.
package led

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type Pattern string

const (
	Waiting   Pattern = "waiting"   // short flash every 2 s: ready, no car
	Connected Pattern = "connected" // heartbeat, 40 to 60 a minute: the car uses the drive
	Busy      Pattern = "busy"      // fast blink: uploading or repairing
	Problem   Pattern = "problem"   // flicker: something needs attention
)

type LED struct {
	dir string
	// load loads a kernel module (a trigger); replaced in tests.
	load func(module string) error

	mu      sync.Mutex
	current Pattern
	period  int     // ms between heartbeats; 0: not using the pattern
	avg     float64 // the latest load average
	max     int     // the LED's full brightness
}

// Find returns the activity LED, or nil when there is none. Newer
// Raspberry Pi kernels call it ACT, older ones led0; on the ROCK 4C+ it is
// the blue status LED.
func Find(sysfs string) *LED {
	for _, name := range []string{"ACT", "led0", "blue:status"} {
		dir := filepath.Join(sysfs, "class/leds", name)
		if _, err := os.Stat(filepath.Join(dir, "trigger")); err == nil {
			return &LED{dir: dir, load: func(m string) error { return exec.Command("modprobe", m).Run() }}
		}
	}
	return nil
}

func (l *LED) write(name, value string) {
	os.WriteFile(filepath.Join(l.dir, name), []byte(value+"\n"), 0o644)
}

func (l *LED) timer(on, off string) {
	if !l.trigger("timer") {
		return
	}
	l.write("trigger", "timer")
	l.write("delay_on", on)
	l.write("delay_off", off)
}

// Set changes the pattern; setting the current one again does nothing.
func (l *LED) Set(p Pattern) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if p == l.current {
		return
	}
	l.current = p
	l.period = 0
	switch p {
	case Waiting:
		l.timer("80", "1920")
	case Connected:
		if !l.heartbeat() {
			l.write("trigger", "heartbeat")
		}
	case Busy:
		l.timer("120", "120")
	case Problem:
		l.timer("40", "40")
	}
}

// heartbeatPattern is a double tick (on 70 ms, off 130, on 70) and a rest
// that makes up the period. In the pattern trigger's brightness/time
// pairs, a step of 0 ms makes a jump instead of a fade.
const heartbeatPattern = "%[1]d 70 %[1]d 0 0 130 0 0 %[1]d 70 %[1]d 0 0 %[2]d 0 0"

// heartbeatPeriod is the time between two heartbeats for a load average:
// 1.5 s at rest (40 a minute), quicker as the Pi gets busier, but never
// below a second (60 a minute), so it stays well apart from the fast
// blink. The kernel's own heartbeat goes past 100 a minute. In steps of
// 125 ms, so a small change in load does not restart the beat.
func heartbeatPeriod(load float64) int {
	if load < 0 {
		load = 0
	}
	p := 1000 + 500/(1+load)
	return int(p/125+0.5) * 125
}

// Load sets how busy the system is (the 1-minute load average); the
// heartbeat follows it.
func (l *LED) Load(avg float64) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.current == Connected && l.period != 0 && heartbeatPeriod(avg) != l.period {
		l.period = heartbeatPeriod(avg)
		l.writeHeartbeat()
	}
	l.avg = avg
}

// trigger tells whether the kernel has a trigger, loading its module when
// needed: Armbian has timer and pattern as modules.
func (l *LED) trigger(name string) bool {
	if strings.Contains(l.read("trigger"), name) {
		return true
	}
	return l.load != nil && l.load("ledtrig-"+name) == nil && strings.Contains(l.read("trigger"), name)
}

// heartbeat sets the heartbeat through the kernel's pattern trigger;
// false when the kernel has none.
func (l *LED) heartbeat() bool {
	if !l.trigger("pattern") {
		return false
	}
	max := 255
	if n, err := strconv.Atoi(strings.TrimSpace(l.read("max_brightness"))); err == nil && n > 0 {
		max = n
	}
	l.max = max
	l.period = heartbeatPeriod(l.avg)
	l.write("trigger", "pattern")
	l.writeHeartbeat()
	return true
}

func (l *LED) writeHeartbeat() {
	l.write("pattern", fmt.Sprintf(heartbeatPattern, l.max, l.period-270))
}

func (l *LED) read(name string) string {
	b, _ := os.ReadFile(filepath.Join(l.dir, name))
	return string(b)
}

// Trigger returns the active trigger, for tests.
func (l *LED) Trigger() string {
	b, _ := os.ReadFile(filepath.Join(l.dir, "trigger"))
	return strings.TrimSpace(string(b))
}
