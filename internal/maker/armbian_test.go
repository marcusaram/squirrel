package maker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLockRoot(t *testing.T) {
	in := "root:$y$j9T$abc:20000:0:99999:7:::\ndaemon:*:20000:0:99999:7:::\n"
	out, err := lockRoot([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if want := "root:!$y$j9T$abc:20000:0:99999:7:::\ndaemon:*:20000:0:99999:7:::\n"; string(out) != want {
		t.Errorf("got %q", out)
	}
	again, _ := lockRoot(out)
	if string(again) != string(out) {
		t.Errorf("locking twice changed it: %q", again)
	}
	if _, err := lockRoot([]byte("daemon:*:1:::::\n")); err == nil {
		t.Error("no root entry accepted")
	}
}

// The settings must come out of bash exactly as typed, quotes and all.
func TestArmbianSetupInBash(t *testing.T) {
	s := testSettings()
	s.Board = "rock4cplus"
	s.WiFi = []Network{{SSID: `Thuis "5G" it's #1`, Password: `p@ss $HOME 'q' \n`}, {SSID: "Open net"}}
	env, err := s.ArmbianSetup()
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "setup.env")
	os.WriteFile(f, env, 0o600)
	out, err := exec.Command("bash", "-c", `set -u; . "$1"; printf '%s|' "$TUSB_HOSTNAME" "$TUSB_USER" "${#TUSB_WIFI_SSID[@]}" "${TUSB_WIFI_SSID[@]}" "${TUSB_WIFI_PSK[@]}"`, "-", f).Output()
	if err != nil {
		t.Fatal(err)
	}
	want := s.Hostname + "|" + s.User + "|2|" + s.WiFi[0].SSID + "|Open net|" + s.WiFi[0].Password + "||"
	if string(out) != want {
		t.Errorf("bash read\n%q\nwant\n%q", out, want)
	}
	if !strings.Contains(string(env), "TUSB_PASSWORD_HASH='$6$") {
		t.Errorf("no password hash in\n%s", env)
	}
}

// TestCustomizeRealArmbian adapts the real Armbian image with the real
// payload. Opt-in: ARMBIAN_IMG=path/to/uncompressed.img, and `make
// payload` must have run (debugfs, e2fsck and the packages).
func TestCustomizeRealArmbian(t *testing.T) {
	src := os.Getenv("ARMBIAN_IMG")
	if src == "" {
		t.Skip("set ARMBIAN_IMG to the uncompressed Armbian image")
	}
	payload := os.DirFS("../makerui/payload")
	tools := "../makerui/payload/mac"
	img := filepath.Join(t.TempDir(), "a.img")
	if out, err := exec.Command("cp", "-c", src, img).CombinedOutput(); err != nil {
		if out, err := exec.Command("cp", src, img).CombinedOutput(); err != nil {
			t.Fatalf("copy: %v %s", err, out)
		}
		_ = out
	}
	s := testSettings()
	s.Board = "rock4cplus"
	err := CustomizeArmbian(context.Background(), img, s, payload, tools, func(string, float64) {})
	if err != nil {
		t.Fatal(err)
	}
	d := debugfs{bin: filepath.Join(tools, "debugfs"), image: img}
	shadow, _ := d.read(context.Background(), "/etc/shadow")
	if !strings.HasPrefix(string(shadow), "root:!") {
		t.Errorf("root not locked: %.40s", shadow)
	}
	// Doing it again (a second build from the same image) must also work.
	if err := CustomizeArmbian(context.Background(), img, s, payload, tools, func(string, float64) {}); err != nil {
		t.Fatalf("second run: %v", err)
	}
}
