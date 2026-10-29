package maker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcusaram/squirrel/internal/config"
)

func testSettings() Settings {
	return Settings{
		Hostname: "squirrel", User: "driver", Password: "geheim123",
		SSHKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample driver@mac", Country: "NL",
		Timezone: "Europe/Amsterdam", CamPercent: 40,
		WiFi: []Network{{SSID: `Thuis "5G": #1`, Password: `p@ss: 'quoted' # no comment`}, {SSID: "Phone hotspot", Password: "hotspot123"}, {SSID: "Home IoT"}},
		NAS:  config.NAS{Enabled: true, Host: "nas.local", Share: "video", Path: "TeslaCam", User: "tesla", Password: "nas-pw"},
	}
}

func TestFiles(t *testing.T) {
	files, err := testSettings().Files()
	if err != nil {
		t.Fatal(err)
	}
	ud := string(files["user-data"])
	if !strings.HasPrefix(ud, "#cloud-config\n") || !strings.Contains(ud, `passwd: "$6$`) || strings.Contains(ud, "geheim123") {
		t.Errorf("user-data:\n%s", ud)
	}
	nc := string(files["network-config"])
	// The tricky SSID and password must arrive as quoted scalars.
	if !strings.Contains(nc, `"Thuis \"5G\": #1":`) || !strings.Contains(nc, `password: "p@ss: 'quoted' # no comment"`) || !strings.Contains(nc, `"Home IoT": {}`) {
		t.Errorf("network-config:\n%s", nc)
	}
	var cfg config.Config
	if err := json.Unmarshal(files["squirrel/config.json"], &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.NAS.Enabled || cfg.NAS.Password != "nas-pw" || !cfg.WiFi.AP || len(cfg.WiFi.APPassword) != 10 {
		t.Errorf("config = %+v", cfg)
	}
	if string(files["squirrel/cam-size"]) != "40%\n" {
		t.Errorf("cam-size = %q", files["squirrel/cam-size"])
	}
}

func TestValidate(t *testing.T) {
	for name, mod := range map[string]func(*Settings){
		"no login":     func(s *Settings) { s.Password, s.SSHKey = "", "" },
		"bad hostname": func(s *Settings) { s.Hostname = "Tesla USB" },
		"short wifi":   func(s *Settings) { s.WiFi[0].Password = "short" },
		"root user":    func(s *Settings) { s.User = "root" },
		"key newline":  func(s *Settings) { s.SSHKey = "ssh-ed25519 AAA\nrunsomething" },
	} {
		s := testSettings()
		mod(&s)
		if s.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestEditCmdline(t *testing.T) {
	in := "console=serial0,115200 console=tty1 root=PARTUUID=27b94958-02 rootfstype=ext4 fsck.repair=yes rootwait resize\n"
	out, err := EditCmdline(in, "NL")
	if err != nil {
		t.Fatal(err)
	}
	if out != "console=serial0,115200 console=tty1 root=PARTUUID=27b94958-02 rootfstype=ext4 fsck.repair=yes rootwait cfg80211.ieee80211_regdom=NL\n" {
		t.Errorf("got %q", out)
	}
	if _, err := EditCmdline(out, "NL"); err == nil {
		t.Error("accepted a cmdline without resize (already edited, or another release)")
	}
	cfg := EditConfig("arm_64bit=1\n[all]\n", false)
	if strings.Contains(cfg, "sd_overclock") {
		t.Error("overclocked without asking")
	}
	fast := EditConfig("arm_64bit=1\n[all]\n", true)
	if !strings.HasSuffix(fast, "dtparam=sd_overclock=100\n") || EditConfig(fast, true) != fast {
		t.Errorf("overclock: %q", fast)
	}
	if !strings.Contains(cfg, "dtoverlay=dwc2\n") || !strings.HasSuffix(cfg, "\ngpu_mem=16\n") || EditConfig(cfg, false) != cfg {
		t.Errorf("config.txt:\n%s", cfg)
	}
	// A memory split someone set themselves stays; a comment is not one.
	for _, own := range []string{"gpu_mem=128\n", "[pi0]\ngpu_mem_512=64\n"} {
		if got := EditConfig(own, false); strings.Contains(got, "gpu_mem=16") {
			t.Errorf("own gpu_mem overridden:\n%s", got)
		}
	}
	if got := EditConfig("#gpu_mem=128\n", false); !strings.Contains(got, "\ngpu_mem=16\n") {
		t.Errorf("a comment kept gpu_mem out:\n%s", got)
	}
}

// TestWriteFiles writes the boot files to $OUT for validation with the
// cloud-init on a real Raspberry Pi OS root (see `make test-image`).
func TestWriteFiles(t *testing.T) {
	out := os.Getenv("OUT")
	if out == "" {
		t.Skip("set OUT to write the files")
	}
	files, err := testSettings().Files()
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		p := filepath.Join(out, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStorageOnUSB(t *testing.T) {
	s := testSettings()
	if f, _ := s.Files(); f["squirrel/storage"] != nil {
		t.Error("storage file without asking for USB")
	}
	s.Storage = "usb"
	f, err := s.Files()
	if err != nil {
		t.Fatal(err)
	}
	if string(f["squirrel/storage"]) != "usb\n" {
		t.Errorf("storage = %q", f["squirrel/storage"])
	}
	// The Zero W's only USB port is for the car.
	s.Board = "rpizerow"
	if err := s.Validate(); err == nil {
		t.Error("USB storage accepted on a Zero W")
	}
}
