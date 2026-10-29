package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTeslausbSettings(t *testing.T) {
	p := filepath.Join(t.TempDir(), "teslausb_setup_variables.conf")
	os.WriteFile(p, []byte(`#!/bin/bash
export CAM_SIZE=40G
export ARCHIVE_SYSTEM=cifs
export ARCHIVE_SERVER=nas.local
export SHARE_NAME='video/TeslaCam'
export SHARE_USER=tesla
export SHARE_PASSWORD='pa$$ word#1'
# export SHARE_DOMAIN=domain
export ARCHIVE_RECENTCLIPS=true # keep drives too
export ARCHIVE_SENTRYCLIPS=false
`), 0o600)
	cfg := Default()
	if !applyTeslausb(&cfg, readTeslausb(p)) {
		t.Fatal("cifs settings not applied")
	}
	n := cfg.NAS
	if !n.Enabled || n.Host != "nas.local" || n.Share != "video" || n.Path != "TeslaCam" ||
		n.User != "tesla" || n.Password != "pa$$ word#1" || n.Domain != "" {
		t.Errorf("nas = %+v", n)
	}
	if want := (Folders{Recent: true, Saved: true, Sentry: false, Track: true}); n.Upload != want {
		t.Errorf("upload = %+v, want %+v", n.Upload, want)
	}

	os.WriteFile(p, []byte("export ARCHIVE_SYSTEM=rclone\n"), 0o600)
	cfg = Default()
	if applyTeslausb(&cfg, readTeslausb(p)) || cfg.NAS.Enabled {
		t.Error("rclone settings must not enable SMB upload")
	}
}
