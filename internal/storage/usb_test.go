package storage

import (
	"strings"
	"testing"
)

const lsblkPi4 = `{"blockdevices":[
 {"name":"sda","path":"/dev/sda","type":"disk","tran":"usb","size":500107862016,"model":"Portable SSD T7","serial":"S5SXNG0R","fstype":null,"label":null,"pttype":null},
 {"name":"mmcblk0","path":"/dev/mmcblk0","type":"disk","tran":null,"size":31914983424,"model":null,"serial":"0x1234","fstype":null,"label":null,"pttype":"dos",
  "children":[{"name":"mmcblk0p1","path":"/dev/mmcblk0p1","type":"part","tran":null,"size":536870912,"fstype":"vfat","label":"bootfs","pttype":"dos"},
   {"name":"mmcblk0p2","path":"/dev/mmcblk0p2","type":"part","tran":null,"size":4294967296,"fstype":"ext4","label":"rootfs","pttype":"dos"}]}]}`

func usbDevs(t *testing.T, js string) []BlockDev {
	t.Helper()
	d, err := ParseLsblk([]byte(js))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func empty(string) bool { return true }

func TestChooseUSBEmptyDrive(t *testing.T) {
	c, err := ChooseUSB(usbDevs(t, lsblkPi4), "/dev/mmcblk0", empty)
	if err != nil {
		t.Fatal(err)
	}
	if c.Node != "/dev/sda" || !c.Format {
		t.Errorf("choice = %+v", c)
	}
	if s := c.Drive.String(); !strings.Contains(s, "500 GB") || !strings.Contains(s, "S5SXNG0R") {
		t.Errorf("description %q lacks size or serial", s)
	}
}

// Never format a drive with anything on it, however it shows up.
func TestChooseUSBRefusesData(t *testing.T) {
	exfat := strings.Replace(lsblkPi4, `"fstype":null,"label":null,"pttype":null},`, `"fstype":null,"label":null,"pttype":"gpt",
  "children":[{"name":"sda1","path":"/dev/sda1","type":"part","size":500107000000,"fstype":"exfat","label":"T7","pttype":"gpt"}]},`, 1)
	for name, tc := range map[string]struct {
		js    string
		empty func(string) bool
	}{
		"partitions":      {exfat, empty},
		"blkid signature": {lsblkPi4, func(string) bool { return false }},
	} {
		_, err := ChooseUSB(usbDevs(t, tc.js), "/dev/mmcblk0", tc.empty)
		if err == nil || !strings.Contains(err.Error(), "holds data") || !strings.Contains(err.Error(), "-usb-device /dev/sda") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestChooseUSBEarlierSetup(t *testing.T) {
	js := strings.Replace(lsblkPi4, `"fstype":null,"label":null,"pttype":null},`, `"fstype":"xfs","label":"backingfiles","pttype":null},`, 1)
	// A second, empty drive next to it does not matter.
	js = strings.Replace(js, `{"name":"mmcblk0"`, `{"name":"sdb","path":"/dev/sdb","type":"disk","tran":"usb","size":64000000000,"fstype":null,"label":null,"pttype":null},
 {"name":"mmcblk0"`, 1)
	c, err := ChooseUSB(usbDevs(t, js), "/dev/mmcblk0", empty)
	if err != nil {
		t.Fatal(err)
	}
	if c.Node != "/dev/sda" || c.Format {
		t.Errorf("choice = %+v", c)
	}
}

func TestChooseUSBAmbiguousOrMissing(t *testing.T) {
	two := strings.Replace(lsblkPi4, `{"name":"mmcblk0"`, `{"name":"sdb","path":"/dev/sdb","type":"disk","tran":"usb","size":64000000000,"fstype":null,"label":null,"pttype":null},
 {"name":"mmcblk0"`, 1)
	if _, err := ChooseUSB(usbDevs(t, two), "/dev/mmcblk0", empty); err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Errorf("two drives: %v", err)
	}
	// The drive root is on is never a candidate, even over USB.
	if _, err := ChooseUSB(usbDevs(t, lsblkPi4), "/dev/sda", empty); err == nil || !strings.Contains(err.Error(), "no USB drive") {
		t.Errorf("root on the USB drive: %v", err)
	}
}
