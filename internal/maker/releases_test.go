package maker

import (
	"context"
	"os"
	"testing"
)

// Talks to downloads.raspberrypi.com and GitHub; opt-in with TBX_NET=1.
func TestReleasesOnline(t *testing.T) {
	if os.Getenv("TBX_NET") != "1" {
		t.Skip("set TBX_NET=1")
	}
	for _, b := range Boards {
		rels, err := Releases(context.Background(), b)
		if err != nil {
			t.Errorf("%s: %v", b.ID, err)
		}
		if len(rels) == 0 || !rels[0].Tested || rels[0].URL != b.Image.URL {
			t.Errorf("%s: tested image not first: %+v", b.ID, rels)
		}
		for _, r := range rels {
			t.Logf("%s: %s tested=%v size=%d sha=%.12s", b.ID, r.Name, r.Tested, r.Size, r.SHA256)
		}
	}
}

func TestReleaseByURLOffline(t *testing.T) {
	b, _ := BoardByID("rpi")
	r, err := ReleaseByURL(context.Background(), b, "")
	if err != nil || !r.Tested || r.URL != DefaultImage.URL {
		t.Errorf("default: %+v %v", r, err)
	}
	// Only what the publisher lists: an arbitrary URL is never fetched.
	b.releases = func(context.Context, Image) ([]Image, error) {
		return []Image{{Name: "newer", URL: "https://downloads.raspberrypi.com/x.img.xz", SHA256: "ab"}}, nil
	}
	if r, err := ReleaseByURL(context.Background(), b, "https://downloads.raspberrypi.com/x.img.xz"); err != nil || r.Tested || r.SHA256 != "ab" {
		t.Errorf("newer: %+v %v", r, err)
	}
	if _, err := ReleaseByURL(context.Background(), b, "https://evil.example/x.img.xz"); err == nil {
		t.Error("unlisted URL accepted")
	}
}

// With an older "tested" release, the real listings must yield newer ones
// with checksums. Opt-in with TBX_NET=1.
func TestNewerReleasesOnline(t *testing.T) {
	if os.Getenv("TBX_NET") != "1" {
		t.Skip("set TBX_NET=1")
	}
	old := DefaultImage
	old.URL = "https://downloads.raspberrypi.com/raspios_lite_arm64/images/raspios_lite_arm64-2026-06-19/x.img.xz"
	pi, err := piOSReleases(context.Background(), old)
	if err != nil || len(pi) == 0 {
		t.Fatalf("pi: %v %v", pi, err)
	}
	rock, _ := BoardByID("rock4cplus")
	arm, err := armbianReleases(armbianAsset, "2026-10-17T00:00:00Z")(context.Background(), rock.Image)
	if err != nil || len(arm) == 0 {
		t.Fatalf("armbian: %v %v", arm, err)
	}
	for _, img := range append(pi, arm...) {
		if !sha256Re.MatchString(img.SHA256) || img.Size <= 0 {
			t.Errorf("incomplete: %+v", img)
		}
		t.Logf("%s %s %d %.12s", img.Name, img.URL, img.Size, img.SHA256)
	}
}
