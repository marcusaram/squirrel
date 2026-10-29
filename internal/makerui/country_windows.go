package makerui

import (
	"os/exec"
	"regexp"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// systemCountry is the country or region Windows is set to, e.g. NL.
func systemCountry() string {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"[System.Globalization.RegionInfo]::CurrentRegion.TwoLetterISORegionName")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.Output()
	if c := strings.ToUpper(strings.TrimSpace(string(out))); err == nil && regexp.MustCompile(`^[A-Z]{2}$`).MatchString(c) {
		return c
	}
	return ""
}
