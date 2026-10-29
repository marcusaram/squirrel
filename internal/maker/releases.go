package maker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Release is an OS image the maker can build from. The tested one is the
// board's pinned Image; newer releases of the same OS are offered too,
// marked as untested: they will usually work, but a change in the OS can
// break the first start (as Bookworm to Trixie did).
type Release struct {
	Image
	Tested bool `json:"tested"`
}

// releaseSource lists the releases of a board's OS that are newer than
// the tested one, newest first, with their checksums from the publisher.
type releaseSource func(ctx context.Context, tested Image) ([]Image, error)

// maxNewer is how many newer releases are offered.
const maxNewer = 3

// Releases returns the tested image first, then newer ones. Without a
// network, or when the publisher's listing changed, it returns just the
// tested image and the error.
func Releases(ctx context.Context, b Board) ([]Release, error) {
	out := []Release{{Image: b.Image, Tested: true}}
	if b.releases == nil {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	newer, err := b.releases(ctx, b.Image)
	for i, img := range newer {
		if i == maxNewer {
			break
		}
		out = append(out, Release{Image: img})
	}
	return out, err
}

// ReleaseByURL finds the release with that URL among what Releases offers
// now, so a build only ever uses an image from the publisher's own listing,
// with the publisher's checksum.
func ReleaseByURL(ctx context.Context, b Board, url string) (Release, error) {
	if url == "" || url == b.Image.URL {
		return Release{Image: b.Image, Tested: true}, nil
	}
	rels, err := Releases(ctx, b)
	for _, r := range rels {
		if r.URL == url {
			return r, nil
		}
	}
	if err != nil {
		return Release{}, fmt.Errorf("the chosen image cannot be checked now (%v); choose the tested one", err)
	}
	return Release{}, errors.New("the chosen image is no longer offered; choose another")
}

func httpGet(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

var piOSDir = regexp.MustCompile(`href="(raspios_lite_(arm64|armhf)-(\d{4}-\d{2}-\d{2}))/"`)

// piOSReleases reads downloads.raspberrypi.com's directory listing for the
// architecture of the tested image. Each release folder holds the
// .img.xz and a .sha256 next to it.
func piOSReleases(ctx context.Context, tested Image) ([]Image, error) {
	m := regexp.MustCompile(`/raspios_lite_(arm64|armhf)/images/raspios_lite_[a-z0-9]+-(\d{4}-\d{2}-\d{2})/`).FindStringSubmatch(tested.URL)
	if m == nil {
		return nil, errors.New("tested image is not a Raspberry Pi OS Lite release")
	}
	arch, testedDate := m[1], m[2]
	base := "https://downloads.raspberrypi.com/raspios_lite_" + arch + "/images/"
	page, err := httpGet(ctx, base, 1<<20)
	if err != nil {
		return nil, err
	}
	var dates []string
	dirs := map[string]string{}
	for _, d := range piOSDir.FindAllStringSubmatch(string(page), -1) {
		if d[2] == arch && d[3] > testedDate {
			dates = append(dates, d[3])
			dirs[d[3]] = d[1]
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))
	bits := map[string]string{"arm64": "64-bit", "armhf": "32-bit"}[arch]
	var out []Image
	for _, date := range dates {
		if len(out) == maxNewer {
			break
		}
		img, err := piOSRelease(ctx, base+dirs[date]+"/")
		if err != nil {
			return out, err
		}
		img.Name = fmt.Sprintf("Raspberry Pi OS Lite (%s) %s", bits, date)
		out = append(out, img)
	}
	return out, nil
}

var imgXZ = regexp.MustCompile(`href="([A-Za-z0-9._-]+\.img\.xz)"`)

func piOSRelease(ctx context.Context, dir string) (Image, error) {
	page, err := httpGet(ctx, dir, 1<<20)
	if err != nil {
		return Image{}, err
	}
	m := imgXZ.FindSubmatch(page)
	if m == nil {
		return Image{}, fmt.Errorf("no image in %s", dir)
	}
	url := dir + string(m[1])
	sum, err := httpGet(ctx, url+".sha256", 4096)
	if err != nil {
		return Image{}, err
	}
	img := Image{URL: url, SHA256: firstField(sum)}
	if !sha256Re.MatchString(img.SHA256) {
		return Image{}, fmt.Errorf("no checksum for %s", url)
	}
	req, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	if err != nil {
		return Image{}, err
	}
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
		img.Size = resp.ContentLength // only for the progress bar
	}
	return img, nil
}

var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

func firstField(b []byte) string {
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	sc.Split(bufio.ScanWords)
	if sc.Scan() {
		return strings.ToLower(sc.Text())
	}
	return ""
}

// armbianReleases reads the GitHub releases of armbian/community. Every
// asset carries its SHA-256 there. Pre-releases are left out.
func armbianReleases(asset *regexp.Regexp, testedPublished string) releaseSource {
	return func(ctx context.Context, tested Image) ([]Image, error) {
		data, err := httpGet(ctx, "https://api.github.com/repos/armbian/community/releases?per_page=30", 8<<20)
		if err != nil {
			return nil, err
		}
		var rels []struct {
			Tag        string `json:"tag_name"`
			Prerelease bool   `json:"prerelease"`
			Draft      bool   `json:"draft"`
			Published  string `json:"published_at"`
			Assets     []struct {
				Name   string `json:"name"`
				Size   int64  `json:"size"`
				Digest string `json:"digest"`
				URL    string `json:"browser_download_url"`
			} `json:"assets"`
		}
		if err := json.Unmarshal(data, &rels); err != nil {
			return nil, fmt.Errorf("armbian releases: %w", err)
		}
		sort.Slice(rels, func(i, j int) bool { return rels[i].Published > rels[j].Published })
		var out []Image
		for _, r := range rels {
			if r.Prerelease || r.Draft || r.Published <= testedPublished {
				continue
			}
			for _, a := range r.Assets {
				sum := strings.TrimPrefix(a.Digest, "sha256:")
				if !asset.MatchString(a.Name) || !sha256Re.MatchString(sum) ||
					!strings.HasPrefix(a.URL, "https://github.com/armbian/community/releases/download/") {
					continue
				}
				out = append(out, Image{
					Name:   fmt.Sprintf("Armbian %s (Debian trixie, minimal)", r.Tag),
					URL:    a.URL,
					SHA256: sum,
					Size:   a.Size,
				})
			}
		}
		return out, nil
	}
}
