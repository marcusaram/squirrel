package makerui

import (
	"os/exec"
	"strings"
)

// systemCountry is the region the Mac is set to, e.g. NL from nl_NL.
func systemCountry() string {
	out, err := exec.Command("defaults", "read", "-g", "AppleLocale").Output()
	if err != nil {
		return ""
	}
	if _, c, ok := strings.Cut(strings.TrimSpace(string(out)), "_"); ok && len(c) >= 2 {
		return strings.ToUpper(c[:2])
	}
	return ""
}
