package config

import (
	"bufio"
	"os"
	"strings"
)

// TeslausbSettings is where teslausb keeps its setup variables.
const TeslausbSettings = "/root/teslausb_setup_variables.conf"

// readTeslausb parses the `export KEY=value` lines of a teslausb settings
// file. The file is a shell script; it is parsed, never executed.
func readTeslausb(path string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	vars := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(k, "#") || strings.ContainsAny(k, " \t") {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '\'' || v[0] == '"') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		} else if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		vars[k] = v
	}
	return vars
}

// applyTeslausb copies teslausb's CIFS archive settings. teslausb wrote
// clips to the root of SHARE_NAME in the same SavedClips/SentryClips/
// RecentClips layout squirreld uses, so uploads continue in the same place.
func applyTeslausb(cfg *Config, vars map[string]string) bool {
	if vars["ARCHIVE_SYSTEM"] != "cifs" || vars["ARCHIVE_SERVER"] == "" {
		return false
	}
	share, sub, _ := strings.Cut(strings.Trim(vars["SHARE_NAME"], "/"), "/")
	flag := func(k string, def bool) bool {
		switch vars[k] {
		case "true":
			return true
		case "false":
			return false
		}
		return def
	}
	cfg.NAS = NAS{
		Enabled:  true,
		Host:     vars["ARCHIVE_SERVER"],
		Share:    share,
		Path:     sub,
		User:     vars["SHARE_USER"],
		Password: vars["SHARE_PASSWORD"],
		Domain:   vars["SHARE_DOMAIN"],
		Upload: Folders{
			Recent: flag("ARCHIVE_RECENTCLIPS", false),
			Saved:  flag("ARCHIVE_SAVEDCLIPS", true),
			Sentry: flag("ARCHIVE_SENTRYCLIPS", true),
			Track:  flag("ARCHIVE_TRACKMODECLIPS", true),
		},
	}
	return true
}
