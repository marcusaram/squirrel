// Package gadget exposes the disk images to the car as a USB mass storage
// device through the Linux configfs gadget API.
//
// The gadget outlives the daemon: Ensure leaves an existing, correctly
// configured gadget alone, so restarting or upgrading squirreld never makes
// the drive disappear from the car.
package gadget

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// The IDs match teslausb's; the name and strings are Squirrel's. A gadget
// left by teslausb or by squirreld under its earlier name is replaced.
const (
	Name      = "squirrel"
	idVendor  = "0x1d6b" // Linux Foundation
	idProduct = "0x0104" // Multifunction Composite Gadget
	lang      = "0x409"
	cfgName   = "c.1"
	function  = "mass_storage.0"
)

type Lun struct {
	Name     string // cam, music, ...
	Image    string
	ReadOnly bool // the host cannot write (computer mode)
}

type Gadget struct {
	ConfigFS string // usually /sys/kernel/config
	SysUDC   string // usually /sys/class/udc
	UDC      string // empty: first available
	MaxPower int    // mA; 0: based on the board
	Luns     []Lun

	mu sync.Mutex
}

func New(luns []Lun, udc string, maxPower int) *Gadget {
	return &Gadget{ConfigFS: "/sys/kernel/config", SysUDC: "/sys/class/udc", UDC: udc, MaxPower: maxPower, Luns: luns}
}

func (g *Gadget) root() string { return filepath.Join(g.ConfigFS, "usb_gadget", Name) }

func write(p, v string) error {
	if err := os.WriteFile(p, []byte(v+"\n"), 0o644); err != nil {
		return fmt.Errorf("gadget: %w", err)
	}
	return nil
}

func read(p string) string {
	b, _ := os.ReadFile(p)
	return strings.TrimSpace(string(b))
}

func (g *Gadget) udc() (string, error) {
	if g.UDC != "" {
		return g.UDC, nil
	}
	entries, err := os.ReadDir(g.SysUDC)
	if err != nil || len(entries) == 0 {
		return "", errors.New("gadget: no USB device controller found (is dwc2/dwc3 in peripheral/OTG mode?)")
	}
	return entries[0].Name(), nil
}

// matches reports whether the existing gadget exposes exactly our images.
func (g *Gadget) matches() bool {
	fn := filepath.Join(g.root(), "functions", function)
	for i, l := range g.Luns {
		lun := filepath.Join(fn, "lun."+strconv.Itoa(i))
		if read(filepath.Join(lun, "file")) != l.Image || (read(filepath.Join(lun, "ro")) == "1") != l.ReadOnly {
			return false
		}
	}
	_, err := os.Stat(filepath.Join(fn, "lun."+strconv.Itoa(len(g.Luns))))
	return os.IsNotExist(err)
}

// Ensure creates and binds the gadget if needed. It is cheap and safe to
// call repeatedly.
func (g *Gadget) Ensure() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.Luns) == 0 {
		return errors.New("gadget: no drives configured")
	}
	for _, l := range g.Luns {
		if _, err := os.Stat(l.Image); err != nil {
			return fmt.Errorf("gadget: image for %s: %w", l.Name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(g.ConfigFS, "usb_gadget")); err != nil {
		if out, err := exec.Command("modprobe", "libcomposite").CombinedOutput(); err != nil {
			return fmt.Errorf("gadget: modprobe libcomposite: %v: %s", err, out)
		}
	}
	// teslausb's gadget, or ours from before Squirrel, holds the drive
	// image open; it goes. Best effort: bind takes the controller from it
	// anyway, and the car must get its drive.
	for _, old := range legacyNames {
		if r := filepath.Join(g.ConfigFS, "usb_gadget", old); r != g.root() {
			if _, err := os.Stat(r); err == nil {
				teardownAt(r)
			}
		}
	}
	if _, err := os.Stat(g.root()); err == nil {
		if g.matches() {
			if read(filepath.Join(g.root(), "UDC")) == "" {
				return g.bind()
			}
			return nil
		}
		if err := g.teardown(); err != nil {
			return err
		}
	}
	if err := g.create(); err != nil {
		return err
	}
	return g.bind()
}

func (g *Gadget) create() error {
	r := g.root()
	c := filepath.Join(r, "configs", cfgName)
	fn := filepath.Join(r, "functions", function)
	for _, d := range []string{filepath.Join(c, "strings", lang), filepath.Join(r, "strings", lang), fn} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("gadget: %w", err)
		}
	}
	serial := "Squirrel"
	if id, err := os.ReadFile("/etc/machine-id"); err == nil {
		sum := sha256.Sum256(id)
		serial = fmt.Sprintf("Squirrel-%x", sum)
	}
	attrs := [][2]string{
		{"idVendor", idVendor},
		{"idProduct", idProduct},
		{"bcdDevice", "0x0100"},
		{"bcdUSB", "0x0200"},
		{"strings/" + lang + "/serialnumber", serial},
		{"strings/" + lang + "/manufacturer", "Squirrel"},
		{"strings/" + lang + "/product", "Squirrel USB Drive"},
		{"configs/" + cfgName + "/strings/" + lang + "/configuration", "Squirrel"},
		{"configs/" + cfgName + "/MaxPower", strconv.Itoa(g.maxPower())},
	}
	for _, a := range attrs {
		if err := write(filepath.Join(r, a[0]), a[1]); err != nil {
			return err
		}
	}
	for i, l := range g.Luns {
		lun := filepath.Join(fn, "lun."+strconv.Itoa(i))
		if err := os.MkdirAll(lun, 0o755); err != nil {
			return fmt.Errorf("gadget: %w", err)
		}
		ro := "0"
		if l.ReadOnly {
			ro = "1"
		}
		// ro can only change while no file is attached.
		if read(filepath.Join(lun, "ro")) != ro {
			write(filepath.Join(lun, "file"), "")
			if err := write(filepath.Join(lun, "ro"), ro); err != nil {
				return err
			}
		}
		if err := write(filepath.Join(lun, "file"), l.Image); err != nil {
			return err
		}
		inquiry := "Squirrel " + strings.ToUpper(l.Name)
		if fi, err := os.Stat(l.Image); err == nil {
			inquiry += fmt.Sprintf(" %dG", fi.Size()>>30)
		}
		if err := write(filepath.Join(lun, "inquiry_string"), inquiry); err != nil {
			return err
		}
	}
	link := filepath.Join(c, function)
	if _, err := os.Lstat(link); os.IsNotExist(err) {
		if err := os.Symlink(fn, link); err != nil {
			return fmt.Errorf("gadget: %w", err)
		}
	}
	return nil
}

// legacyGadgets are kernel modules that claim the controller on their own.
// teslausb loads g_ether at boot so the host sees a responsive device until
// the mass storage gadget exists.
var legacyGadgets = []string{"g_ether", "g_mass_storage", "g_multi"}

func (g *Gadget) bind() error {
	udc, err := g.udc()
	if err != nil {
		return err
	}
	for _, l := range g.Luns {
		if mountedLocally(l.Image) {
			return fmt.Errorf("gadget: %s is mounted on the Pi itself; not giving it to the car until it is unmounted", l.Image)
		}
	}
	for _, m := range legacyGadgets {
		if _, err := os.Stat("/sys/module/" + m); err == nil {
			if out, err := exec.Command("modprobe", "-r", m).CombinedOutput(); err != nil {
				return fmt.Errorf("gadget: unloading %s: %v: %s", m, err, out)
			}
		}
	}
	// Another gadget (an old setup, g_mass_storage, ...) may hold the
	// controller.
	others, _ := filepath.Glob(filepath.Join(g.ConfigFS, "usb_gadget", "*", "UDC"))
	for _, o := range others {
		if filepath.Dir(o) != g.root() && read(o) == udc {
			write(o, "")
		}
	}
	return write(filepath.Join(g.root(), "UDC"), udc)
}

// Unbind disconnects the drives from the car without removing the gadget.
func (g *Gadget) Unbind() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return write(filepath.Join(g.root(), "UDC"), "")
}

// SetLuns changes what the gadget offers; the next Ensure rebuilds it.
func (g *Gadget) SetLuns(luns []Lun) {
	g.mu.Lock()
	g.Luns = luns
	g.mu.Unlock()
}

// Reopen makes the drives open their image files again, for when a file
// was replaced: the kernel keeps the old one open otherwise, even across an
// unbind. Call it while unbound.
func (g *Gadget) Reopen() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	fn := filepath.Join(g.root(), "functions", function)
	for i, l := range g.Luns {
		lun := filepath.Join(fn, "lun."+strconv.Itoa(i))
		// The host may have locked the medium; forced_eject ignores that.
		if err := write(filepath.Join(lun, "forced_eject"), "1"); err != nil {
			if err := write(filepath.Join(lun, "file"), ""); err != nil {
				return err
			}
		}
		if err := write(filepath.Join(lun, "file"), l.Image); err != nil {
			return err
		}
	}
	return nil
}

func (g *Gadget) Bind() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.bind()
}

// legacyNames are gadgets of the same drive under earlier names.
var legacyNames = []string{"teslausb"}

func (g *Gadget) teardown() error { return teardownAt(g.root()) }

func teardownAt(r string) error {
	write(filepath.Join(r, "UDC"), "")
	os.Remove(filepath.Join(r, "configs", cfgName, "strings", lang))
	os.Remove(filepath.Join(r, "configs", cfgName, function))
	luns, _ := filepath.Glob(filepath.Join(r, "functions", function, "lun.*"))
	for _, l := range luns {
		if filepath.Base(l) != "lun.0" {
			os.Remove(l)
		}
	}
	os.Remove(filepath.Join(r, "functions", function))
	os.Remove(filepath.Join(r, "configs", cfgName))
	os.Remove(filepath.Join(r, "strings", lang))
	if err := os.Remove(r); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("gadget: removing old gadget: %w", err)
	}
	return nil
}

func (g *Gadget) maxPower() int {
	if g.MaxPower > 0 {
		return g.MaxPower
	}
	return maxPowerFor(read("/proc/device-tree/model"))
}

// maxPowerFor is the power budget for a board, by its device tree model.
func maxPowerFor(model string) int {
	switch {
	case strings.Contains(model, "Raspberry Pi 5"):
		return 600
	case strings.Contains(model, "Raspberry Pi 4"):
		return 500
	case strings.Contains(model, "Raspberry Pi Zero 2"):
		return 200
	case strings.Contains(model, "Raspberry Pi Zero"):
		return 100
	}
	return 500
}

type Status struct {
	UDC   string `json:"udc"`
	State string `json:"state"` // "configured" when the car uses the drive
	Bound bool   `json:"bound"`
}

func (g *Gadget) Status() Status {
	udc := read(filepath.Join(g.root(), "UDC"))
	s := Status{UDC: udc, Bound: udc != ""}
	if udc != "" {
		s.State = read(filepath.Join(g.SysUDC, udc, "state"))
	}
	return s
}
