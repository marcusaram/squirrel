package maker

import (
	"fmt"
	"regexp"
)

// Board is a kind of single-board computer the maker builds a card for.
type Board struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Image Image  `json:"image"`
	// Payload is the app's payload folder for a Raspberry Pi card; empty
	// for Armbian, which has its own.
	Payload string `json:"-"`
	// USBStorage: the data can go on a USB drive, on a port other than
	// the car's.
	USBStorage bool `json:"usb_storage"`

	releases releaseSource // newer releases of the same OS
}

// ArmbianRock4CPlus is the Armbian build for the Radxa ROCK 4C+. There is
// no stable release for this board, only community builds; this one was
// checked by hand (layout, first start, USB overlay), so it is pinned.
var ArmbianRock4CPlus = Image{
	Name:   "Armbian 26.11.0-trunk.108 (Debian trixie, minimal)",
	URL:    "https://github.com/armbian/community/releases/download/26.11.0-trunk.108/Armbian_community_26.11.0-trunk.108_Rockpi-4cplus_trixie_current_6.18.57_minimal.img.xz",
	SHA256: "83e57a5813af3d20180b4c29e25bd3daed9c95325523066537889d72f32187df",
	Size:   378731356,
}

// armbianTestedPublished is when ArmbianRock4CPlus was published on
// GitHub; later releases are offered as untested.
const armbianTestedPublished = "2026-10-23T15:29:09Z"

// armbianAsset is the minimal trixie image for the ROCK 4C+ among a
// release's many assets.
var armbianAsset = regexp.MustCompile(`^Armbian_community_[0-9A-Za-z.-]+_Rockpi-4cplus_trixie_current_[0-9.]+_minimal\.img\.xz$`)

var Boards = []Board{
	{ID: "rpi", Name: "Raspberry Pi (Zero 2 W, 3, 4, 5)", Image: DefaultImage, Payload: "files", USBStorage: true, releases: piOSReleases},
	// Not tested on a real Zero W yet.
	{ID: "rpizerow", Name: "Raspberry Pi Zero W", Image: PiOS32Image, Payload: "files-armv6", releases: piOSReleases},
	{ID: "rock4cplus", Name: "Radxa ROCK 4C+", Image: ArmbianRock4CPlus, USBStorage: true,
		releases: armbianReleases(armbianAsset, armbianTestedPublished)},
}

// BoardByID returns the board with that ID; an empty ID is the Raspberry Pi.
func BoardByID(id string) (Board, error) {
	if id == "" {
		id = "rpi"
	}
	for _, b := range Boards {
		if b.ID == id {
			return b, nil
		}
	}
	return Board{}, fmt.Errorf("unknown board %q", id)
}
