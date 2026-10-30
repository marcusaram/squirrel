// Package usbview builds what computer mode shows: the archive as a
// read-only drive, a folder per drive or event with each camera as one
// long video (see mp4.Concat), plus the event's details and thumbnail.
package usbview

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/marcusaram/squirrel/internal/archive"
	"github.com/marcusaram/squirrel/internal/exfatgen"
	"github.com/marcusaram/squirrel/internal/mp4"
)

// Words are the folder names in the drive's language.
type Words struct {
	Kinds  map[string]string // recent, saved, sentry, track, other
	ReadMe string
	Text   string
	Subs   mp4.Words // the videos' subtitles
}

var languages = map[string]Words{
	"en": {
		Kinds:  map[string]string{"recent": "Drives", "saved": "Saved", "sentry": "Sentry", "track": "Track mode", "other": "Other"},
		ReadMe: "Read me.txt",
		Text:   "Squirrel in computer mode\r\n\r\nEvery camera of every drive and event is one video here, joined from the\r\ncar's one-minute recordings. This drive is read-only; nothing on it can be\r\nchanged or deleted.\r\n\r\nThe videos have subtitles with the car's speed, steering, pedals and\r\nAutopilot, where the car recorded them; turn them on in your video player.\r\n\r\nWhat was kept after this drive was made appears the next time computer\r\nmode is switched on. To give the Pi back to the car, choose \"Back to car\r\nmode\" in its web interface, or restart it.\r\n",
		Subs: mp4.Words{Language: "eng", Unit: "km/h", Factor: 3.6, Brake: "BRAKE",
			Autopilot: [4]string{"", "Self-driving", "Autosteer", "Cruise"}},
	},
	"nl": {
		Kinds:  map[string]string{"recent": "Ritten", "saved": "Opgeslagen", "sentry": "Sentry", "track": "Circuitmodus", "other": "Overig"},
		ReadMe: "Lees mij.txt",
		Text:   "Squirrel in computermodus\r\n\r\nElke camera van elke rit en gebeurtenis is hier één video, samengevoegd uit\r\nde opnames van een minuut van de auto. Deze schijf is alleen-lezen; er kan\r\nniets op worden veranderd of verwijderd.\r\n\r\nDe video's hebben ondertitels met de snelheid, het stuur, de pedalen en\r\nAutopilot van de auto, waar de auto die opnam; zet ze aan in je videospeler.\r\n\r\nWat na het maken van deze schijf is bewaard, verschijnt de volgende keer dat\r\nde computermodus aangaat. Om de Pi terug te geven aan de auto, kies je\r\n\"Terug naar autostand\" in de web-interface, of start je hem opnieuw op.\r\n",
		Subs: mp4.Words{Language: "nld", Unit: "km/h", Factor: 3.6, Brake: "REM",
			Autopilot: [4]string{"", "Zelfrijdend", "Autosteer", "Cruise control"}},
	},
}

// Longs rebuilds a long video when its data is read, and keeps the last
// few: each holds the frame tables of a whole drive.
type Longs struct {
	mu    sync.Mutex
	cache []longEntry
}

type longEntry struct {
	key  string
	long *mp4.Long
	r    *mp4.Reader
}

func (c *Longs) readAt(files []string, starts []float64, words mp4.Words, size int64, p []byte, off int64) (int, error) {
	key := strings.Join(files, "\x00")
	c.mu.Lock()
	defer c.mu.Unlock()
	var e *longEntry
	for i := range c.cache {
		if c.cache[i].key == key {
			e = &c.cache[i]
		}
	}
	if e == nil {
		l, err := mp4.ConcatSubs(files, starts, words)
		if err != nil {
			return 0, err
		}
		if l.Size != size {
			// The recordings changed since the drive was made.
			return 0, fmt.Errorf("usbview: %s changed size", files[0])
		}
		if len(c.cache) >= 6 {
			c.cache[0].r.Close()
			c.cache = c.cache[1:]
		}
		c.cache = append(c.cache, longEntry{key: key, long: l, r: l.Open()})
		e = &c.cache[len(c.cache)-1]
	}
	if _, err := e.r.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	return io.ReadFull(e.r, p)
}

// Close closes the original files the cache holds open.
func (c *Longs) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.cache {
		e.r.Close()
	}
	c.cache = nil
}

// Tree builds the drive's folders from the archive. lang is "en" or "nl",
// unit the subtitles' speed, "kmh" or "mph" (as the web player shows it);
// progress is called after each group.
func Tree(ix *archive.Index, lang, unit string, longs *Longs, progress func(done, total int)) (*exfatgen.Dir, int, error) {
	words, ok := languages[lang]
	if !ok {
		words = languages["en"]
	}
	words.Subs = subtitleWords(words.Subs, unit)
	now := time.Now()
	root := &exfatgen.Dir{Mod: now}
	text := []byte(words.Text)
	root.Files = append(root.Files, &exfatgen.File{Name: words.ReadMe, Size: int64(len(text)), Mod: now,
		ReadAt: func(p []byte, off int64) (int, error) { return copy(p, text[off:]), nil }})
	kinds := map[string]*exfatgen.Dir{}
	used := map[*exfatgen.Dir]map[string]bool{}
	groups := ix.Groups()
	files := 1
	for i, g := range groups {
		if progress != nil {
			progress(i, len(groups))
		}
		start := groupTime(g)
		kind, ok := words.Kinds[g.Kind]
		if !ok {
			kind = words.Kinds["other"]
		}
		parent := kinds[kind]
		if parent == nil {
			parent = &exfatgen.Dir{Name: kind, Mod: now}
			kinds[kind] = parent
			root.Dirs = append(root.Dirs, parent)
			used[parent] = map[string]bool{}
		}
		dir := &exfatgen.Dir{Name: unique(used[parent], folderName(g, start)), Mod: start}
		starts := g.SegmentStarts()
		for _, cam := range g.Cameras {
			var paths []string
			for _, seg := range g.Segments {
				p := ""
				if rel := seg.Cams[cam]; rel != "" {
					p = filepath.Join(ix.Root(), filepath.FromSlash(path.Clean("/"+rel)))
				}
				paths = append(paths, p)
			}
			l, err := mp4.ConcatSubs(paths, starts, words.Subs)
			if err != nil {
				continue
			}
			size := l.Size
			dir.Files = append(dir.Files, &exfatgen.File{Name: cam + ".mp4", Size: size, Mod: start,
				ReadAt: func(p []byte, off int64) (int, error) { return longs.readAt(paths, starts, words.Subs, size, p, off) }})
		}
		extras := g.Others
		if g.Thumb != "" {
			extras = append(extras, g.Thumb)
		}
		if g.Event != nil {
			extras = append(extras, path.Join(g.ID, "event.json"))
		}
		names := map[string]bool{}
		for _, f := range dir.Files {
			names[strings.ToUpper(f.Name)] = true
		}
		for _, rel := range extras {
			if f := diskFile(ix.Root(), rel); f != nil && !names[strings.ToUpper(f.Name)] {
				names[strings.ToUpper(f.Name)] = true
				dir.Files = append(dir.Files, f)
			}
		}
		if len(dir.Files) == 0 {
			continue
		}
		files += len(dir.Files)
		parent.Dirs = append(parent.Dirs, dir)
	}
	if progress != nil {
		progress(len(groups), len(groups))
	}
	return root, files, nil
}

// subtitleWords are the subtitles' words with speed in unit, "kmh" or
// "mph"; anything else is km/h.
func subtitleWords(w mp4.Words, unit string) mp4.Words {
	if unit == "mph" {
		w.Unit, w.Factor = "mph", 2.23694
	}
	return w
}

// groupTime is when a group starts, in the car's local time as the file
// names give it.
func groupTime(g archive.Group) time.Time {
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if t, err := time.Parse(layout, g.Start); err == nil {
			return t
		}
	}
	return time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
}

func folderName(g archive.Group, start time.Time) string {
	name := start.Format("2006-01-02 15.04")
	if start.Year() == 1980 {
		// No time to name it by, such as loose files of RecentClips
		// (group "RecentClips/other").
		name = path.Base(g.ID)
	}
	if g.Event != nil && g.Event.City != "" {
		name += " " + g.Event.City
	}
	return clean(name)
}

// clean makes a name exFAT accepts.
func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune("\"*/:<>?\\|", r) {
			return '-'
		}
		return r
	}, s)
	s = strings.TrimRight(strings.TrimSpace(s), ".")
	if s == "" {
		s = "-"
	}
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200])
	}
	return s
}

func unique(used map[string]bool, name string) string {
	try := name
	for i := 2; used[strings.ToUpper(try)]; i++ {
		try = fmt.Sprintf("%s (%d)", name, i)
	}
	used[strings.ToUpper(try)] = true
	return try
}

// diskFile is an archived file as it is, opened when read.
func diskFile(root, rel string) *exfatgen.File {
	p := filepath.Join(root, filepath.FromSlash(path.Clean("/"+rel)))
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return nil
	}
	return &exfatgen.File{Name: clean(path.Base(rel)), Size: fi.Size(), Mod: fi.ModTime(),
		ReadAt: func(b []byte, off int64) (int, error) {
			f, err := os.Open(p)
			if err != nil {
				return 0, err
			}
			defer f.Close()
			return f.ReadAt(b, off)
		}}
}
