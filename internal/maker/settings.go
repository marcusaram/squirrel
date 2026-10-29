// Package maker turns the official Raspberry Pi OS Lite image into a card
// that sets itself up as a Squirrel drive on its first start, and writes
// it to an SD card.
package maker

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/marcusaram/squirrel/internal/config"
)

type Network struct {
	SSID     string `json:"ssid"`
	Password string `json:"password,omitempty"`
}

// Settings are what the user fills in.
type Settings struct {
	Board      string     `json:"board"` // see Boards; empty is a Raspberry Pi
	Hostname   string     `json:"hostname"`
	User       string     `json:"user"`
	Password   string     `json:"password,omitempty"`
	SSHKey     string     `json:"ssh_key"`
	Country    string     `json:"country"`
	Timezone   string     `json:"timezone"`
	WiFi       []Network  `json:"wifi"`
	APPassword string     `json:"ap_password,omitempty"`
	NAS        config.NAS `json:"nas"`
	CamPercent int        `json:"cam_percent"`
	// Storage "usb" puts the data on an external USB drive, which is
	// formatted; empty keeps it on the card.
	Storage string `json:"storage,omitempty"`
	// Image is the URL of the OS release to build from; empty is the
	// board's tested one. See Releases.
	Image string `json:"image,omitempty"`
	// SDOverclock runs the SD card at up to 100 MHz instead of 50 on the
	// boards whose card sits on the sdhost controller (Zero W, Zero 2 W, 3):
	// at 50 MHz the card manages about 20 MB/s, less than the car sends
	// over USB. Beyond the SD specification, so not every card copes; off
	// unless chosen. Pi 4 and 5 drive the card differently, where it has
	// no effect, and the ROCK has no such setting.
	SDOverclock bool `json:"sd_overclock,omitempty"`
}

var (
	hostnameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	userRe     = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	countryRe  = regexp.MustCompile(`^[A-Z]{2}$`)
	tzRe       = regexp.MustCompile(`^[A-Za-z_]+(/[A-Za-z0-9_+-]+)*$`)
)

func (s *Settings) Validate() error {
	s.Hostname = strings.ToLower(strings.TrimSpace(s.Hostname))
	s.User = strings.TrimSpace(s.User)
	s.Country = strings.ToUpper(strings.TrimSpace(s.Country))
	s.SSHKey = strings.TrimSpace(s.SSHKey)
	board, err := BoardByID(s.Board)
	if err != nil {
		return err
	}
	switch {
	case s.Storage != "" && s.Storage != "usb":
		return fmt.Errorf("unknown storage %q", s.Storage)
	case s.SDOverclock && board.Payload == "":
		return errors.New("SD card overclocking is for a Raspberry Pi")
	case s.Storage == "usb" && !board.USBStorage:
		return errors.New("this board has no USB port for a drive next to the one for the car")
	}
	switch {
	case !hostnameRe.MatchString(s.Hostname):
		return errors.New("the name may only contain lowercase letters, digits and hyphens")
	case !userRe.MatchString(s.User) || s.User == "root":
		return errors.New("the user name may only contain lowercase letters, digits, _ and -")
	case s.Password == "" && s.SSHKey == "":
		return errors.New("enter a password or an SSH key, or you cannot log in to the Pi")
	case s.Password != "" && len(s.Password) < 8:
		return errors.New("the password must be at least 8 characters")
	case s.SSHKey != "" && (strings.ContainsAny(s.SSHKey, "\n\r") || !strings.HasPrefix(s.SSHKey, "ssh-") && !strings.HasPrefix(s.SSHKey, "ecdsa-")):
		return errors.New("the SSH key must be one line starting with ssh- or ecdsa-")
	case !countryRe.MatchString(s.Country):
		return errors.New("the country must be a two-letter code, for example NL")
	case s.Timezone != "" && !tzRe.MatchString(s.Timezone):
		return errors.New("invalid time zone")
	case s.APPassword != "" && (len(s.APPassword) < 8 || len(s.APPassword) > 63):
		return errors.New("the hotspot password must be 8 to 63 characters")
	case s.CamPercent < 10 || s.CamPercent > 70:
		return errors.New("the space for the car must be between 10 and 70%")
	}
	for _, n := range s.WiFi {
		if n.SSID == "" || len(n.SSID) > 32 {
			return errors.New("a wifi name must be 1 to 32 characters")
		}
		if n.Password != "" && (len(n.Password) < 8 || len(n.Password) > 63) {
			return fmt.Errorf("the password for %q must be 8 to 63 characters", n.SSID)
		}
	}
	return nil
}

// q quotes a string for YAML. A JSON string is a valid YAML double-quoted
// scalar, and escapes everything that could break the document.
func q(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Files returns what goes on the boot partition, by path.
func (s Settings) Files() (map[string][]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	files := map[string][]byte{}

	var u strings.Builder
	u.WriteString("#cloud-config\n# Written by squirrel-maker.\n")
	fmt.Fprintf(&u, "hostname: %s\nmanage_etc_hosts: true\n", q(s.Hostname))
	if s.Timezone != "" {
		fmt.Fprintf(&u, "timezone: %s\n", q(s.Timezone))
	}
	u.WriteString("users:\n")
	fmt.Fprintf(&u, "  - name: %s\n", q(s.User))
	u.WriteString("    gecos: Squirrel\n")
	u.WriteString("    groups: [adm, dialout, cdrom, audio, users, sudo, video, games, plugdev, input, gpio, spi, i2c, netdev, render, lpadmin]\n")
	u.WriteString("    shell: /bin/bash\n")
	u.WriteString("    sudo: \"ALL=(ALL) NOPASSWD:ALL\"\n")
	if s.Password != "" {
		u.WriteString("    lock_passwd: false\n")
		fmt.Fprintf(&u, "    passwd: %s\n", q(HashPassword(s.Password)))
	} else {
		u.WriteString("    lock_passwd: true\n")
	}
	if s.SSHKey != "" {
		fmt.Fprintf(&u, "    ssh_authorized_keys:\n      - %s\n", q(s.SSHKey))
	}
	fmt.Fprintf(&u, "ssh_pwauth: %t\n", s.Password != "")
	u.WriteString(`runcmd:
  - [rfkill, unblock, wifi]
  - [install, -m, "0644", /boot/firmware/squirrel/squirrel-firstboot.service, /etc/systemd/system/]
  - [systemctl, daemon-reload]
  - [systemctl, enable, --now, --no-block, squirrel-firstboot.service]
`)
	files["user-data"] = []byte(u.String())

	var n strings.Builder
	n.WriteString("# Written by squirrel-maker.\nnetwork:\n  version: 2\n")
	if len(s.WiFi) > 0 {
		n.WriteString("  wifis:\n    wlan0:\n      dhcp4: true\n      optional: true\n")
		fmt.Fprintf(&n, "      regulatory-domain: %s\n      access-points:\n", q(s.Country))
		for _, w := range s.WiFi {
			// An open network still needs a mapping: a bare key is null,
			// which netplan rejects, and then cloud-init applies nothing.
			if w.Password == "" {
				fmt.Fprintf(&n, "        %s: {}\n", q(w.SSID))
				continue
			}
			fmt.Fprintf(&n, "        %s:\n          password: %s\n", q(w.SSID), q(w.Password))
		}
	}
	files["network-config"] = []byte(n.String())

	id := make([]byte, 6)
	rand.Read(id)
	files["meta-data"] = []byte("# Written by squirrel-maker.\ndsmode: local\ninstance_id: squirrel-" + hex.EncodeToString(id) + "\n")

	// sshswitch.service turns SSH on when this file exists.
	files["ssh"] = []byte{}

	cfg := config.Default()
	cfg.NAS = s.NAS
	if cfg.NAS.Path == "" {
		cfg.NAS.Path = "TeslaCam"
	}
	cfg.WiFi.APPassword = s.APPassword
	if cfg.WiFi.APPassword == "" {
		cfg.WiFi.APPassword = config.RandomPassword(10)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	files["squirrel/config.json"] = append(data, '\n')
	files["squirrel/cam-size"] = []byte(strconv.Itoa(s.CamPercent) + "%\n")
	if s.Storage == "usb" {
		files["squirrel/storage"] = []byte("usb\n")
	}
	return files, nil
}

// EditCmdline removes the resize flag, so Raspberry Pi OS leaves the rest
// of the card free for the data partition, and sets the wifi country.
func EditCmdline(cmdline, country string) (string, error) {
	fields := strings.Fields(cmdline)
	var out []string
	found := false
	for _, f := range fields {
		switch {
		case f == "resize":
			found = true
		case strings.HasPrefix(f, "init=") && strings.Contains(f, "firstboot"):
			return "", errors.New("this image grows root with the old firstboot script; it is not supported")
		case strings.HasPrefix(f, "cfg80211.ieee80211_regdom="):
		default:
			out = append(out, f)
		}
	}
	if !found {
		return "", errors.New("cmdline.txt has no resize flag; this image is not the expected Raspberry Pi OS release")
	}
	out = append(out, "cfg80211.ieee80211_regdom="+country)
	return strings.Join(out, " ") + "\n", nil
}

// setsGPUMem tells whether config.txt sets the video chip's memory itself
// (gpu_mem, or gpu_mem_256 and the like for one memory size); a comment
// does not count.
func setsGPUMem(configTxt string) bool {
	for _, line := range strings.Split(configTxt, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "gpu_mem") {
			return true
		}
	}
	return false
}

// EditConfig turns on the USB device controller driver for all boards,
// gives the video chip the least memory (Squirrel has no screen or camera;
// on a 512 MB Zero this leaves Linux some 48 MB more), and with overclock
// the SD card's faster clock (see Settings.SDOverclock).
func EditConfig(configTxt string, overclock bool) string {
	has := func(want string) bool {
		for _, line := range strings.Split(configTxt, "\n") {
			if strings.TrimSpace(line) == want {
				return true
			}
		}
		return false
	}
	var add string
	if !has("dtoverlay=dwc2") {
		add += "# squirrel: USB device mode, so the Pi can act as the car's drive\ndtoverlay=dwc2\n"
	}
	if !setsGPUMem(configTxt) {
		add += "# squirrel: no screen or camera, so the video chip needs the least memory\ngpu_mem=16\n"
	}
	if overclock && !has("dtparam=sd_overclock=100") {
		add += "# squirrel: the SD card at up to 100 MHz instead of 50 (chosen in Squirrel Maker)\ndtparam=sd_overclock=100\n"
	}
	if add == "" {
		return configTxt
	}
	if !strings.HasSuffix(configTxt, "\n") {
		configTxt += "\n"
	}
	return configTxt + "\n[all]\n" + add
}
