// Package archive indexes the clips preserved on the Pi and groups them the
// way people look for them: Sentry and saved events, and drives assembled
// from consecutive RecentClips minutes.
package archive

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/marcusaram/squirrel/internal/state"
)

// TmpPrefix marks files that are still being extracted.
const TmpPrefix = ".tbx-"

var clipName = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2})-([a-z_]+)\.mp4$`)

const timeLayout = "2006-01-02_15-04-05"

type File struct {
	Rel    string
	Size   int64
	Kind   string // recent, saved, sentry, track
	Time   string // from the file name, e.g. 2026-10-28_18-40-00
	Camera string
}

type EventInfo struct {
	Timestamp string `json:"timestamp,omitempty"`
	City      string `json:"city,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Camera    string `json:"camera,omitempty"`
	Lat       string `json:"lat,omitempty"`
	Lon       string `json:"lon,omitempty"`
}

type Segment struct {
	Time string            `json:"time"`
	Cams map[string]string `json:"cams"`
}

type Group struct {
	ID       string     `json:"id"`
	Kind     string     `json:"kind"`
	Start    string     `json:"start"`
	End      string     `json:"end"`
	Cameras  []string   `json:"cameras"`
	Segments []Segment  `json:"segments,omitempty"`
	Others   []string   `json:"others,omitempty"`
	Files    int        `json:"files"`
	Size     int64      `json:"size"`
	Uploaded int        `json:"uploaded"`
	Thumb    string     `json:"thumb,omitempty"`
	Event    *EventInfo `json:"event,omitempty"`

	rels []string
}

type Index struct {
	root     string
	uploaded *state.Set

	mu      sync.RWMutex
	files   map[string]File
	events  map[string]*EventInfo // by event directory
	version uint64
	groups  []Group
	built   uint64
}

func NewIndex(root string, uploaded *state.Set) *Index {
	return &Index{root: root, uploaded: uploaded, files: map[string]File{}, events: map[string]*EventInfo{}, built: ^uint64(0)}
}

func (ix *Index) Root() string { return ix.root }

// Load walks the archive directory. Leftovers of interrupted extractions
// are removed.
func (ix *Index) Load(log *slog.Logger) error {
	if err := os.MkdirAll(ix.root, 0o755); err != nil {
		return err
	}
	start := time.Now()
	n := 0
	err := filepath.WalkDir(ix.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Warn("archive walk", "path", p, "err", err)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), TmpPrefix) {
			os.Remove(p)
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(ix.root, p)
		ix.Add(filepath.ToSlash(rel), info.Size())
		n++
		return nil
	})
	log.Info("archive indexed", "files", n, "took", time.Since(start).Round(time.Millisecond))
	return err
}

func kindOf(rel string) string {
	switch strings.SplitN(rel, "/", 2)[0] {
	case "RecentClips":
		return "recent"
	case "SavedClips":
		return "saved"
	case "SentryClips":
		return "sentry"
	case "TeslaTrackMode":
		return "track"
	}
	return ""
}

func (ix *Index) Add(rel string, size int64) {
	f := File{Rel: rel, Size: size, Kind: kindOf(rel)}
	if m := clipName.FindStringSubmatch(path.Base(rel)); m != nil {
		f.Time, f.Camera = m[1], m[2]
	}
	var ev *EventInfo
	if path.Base(rel) == "event.json" {
		ev = readEvent(filepath.Join(ix.root, filepath.FromSlash(rel)))
	}
	ix.mu.Lock()
	ix.files[rel] = f
	if ev != nil {
		ix.events[path.Dir(rel)] = ev
	}
	ix.version++
	ix.mu.Unlock()
}

func (ix *Index) Remove(rel string) {
	ix.mu.Lock()
	delete(ix.files, rel)
	if path.Base(rel) == "event.json" {
		delete(ix.events, path.Dir(rel))
	}
	ix.version++
	ix.mu.Unlock()
}

// Has reports whether rel is archived with the given size.
func (ix *Index) Has(rel string, size int64) bool {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	f, ok := ix.files[rel]
	return ok && f.Size == size
}

// Version changes whenever the index does.
func (ix *Index) Version() uint64 {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.version
}

type Stats struct {
	Files    int   `json:"files"`
	Bytes    int64 `json:"bytes"`
	Events   int   `json:"events"`
	Drives   int   `json:"drives"`
	Uploaded int   `json:"uploaded"`
}

func (ix *Index) Stats() Stats {
	var s Stats
	for _, g := range ix.Groups() {
		s.Files += g.Files
		s.Bytes += g.Size
		s.Uploaded += g.Uploaded
		if g.Kind == "recent" {
			s.Drives++
		} else {
			s.Events++
		}
	}
	return s
}

func readEvent(p string) *EventInfo {
	data, err := os.ReadFile(p)
	if err != nil || len(data) > 64<<10 {
		return nil
	}
	var raw map[string]any
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	s := func(k string) string {
		if v, ok := raw[k]; ok && v != nil {
			return fmt.Sprint(v)
		}
		return ""
	}
	return &EventInfo{
		Timestamp: s("timestamp"),
		City:      s("city"),
		Reason:    s("reason"),
		Camera:    s("camera"),
		Lat:       s("est_lat"),
		Lon:       s("est_lon"),
	}
}

// Groups returns all groups, newest first. Segments are included.
func (ix *Index) Groups() []Group {
	ix.mu.RLock()
	if ix.built == ix.version {
		g := ix.groups
		ix.mu.RUnlock()
		return g
	}
	ix.mu.RUnlock()

	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.built != ix.version {
		ix.groups = ix.buildGroups()
		ix.built = ix.version
	}
	return ix.groups
}

// SegmentStarts is when each segment starts, in seconds from the first,
// from the file names; nil when a time cannot be read.
func (g Group) SegmentStarts() []float64 {
	var out []float64
	var first time.Time
	for i, s := range g.Segments {
		t, err := time.Parse("2006-01-02T15:04:05", s.Time)
		if err != nil {
			return nil
		}
		if i == 0 {
			first = t
		}
		out = append(out, t.Sub(first).Seconds())
	}
	return out
}

func (ix *Index) Group(id string) (Group, bool) {
	for _, g := range ix.Groups() {
		if g.ID == id {
			return g, true
		}
	}
	return Group{}, false
}

func isoTime(t string) string {
	if len(t) != len(timeLayout) {
		return t
	}
	return t[:10] + "T" + strings.ReplaceAll(t[11:], "-", ":")
}

func (ix *Index) buildGroups() []Group {
	byID := map[string]*Group{}
	segs := map[string]map[string]*Segment{}
	var recent []File
	var recentThumb *File

	get := func(id, kind string) *Group {
		g, ok := byID[id]
		if !ok {
			g = &Group{ID: id, Kind: kind}
			byID[id] = g
			segs[id] = map[string]*Segment{}
		}
		return g
	}
	add := func(g *Group, f File) {
		g.Files++
		g.Size += f.Size
		g.rels = append(g.rels, f.Rel)
		if ix.uploaded != nil && ix.uploaded.Has(f.Rel) {
			g.Uploaded++
		}
		switch {
		case f.Camera != "":
			s, ok := segs[g.ID][f.Time]
			if !ok {
				s = &Segment{Time: isoTime(f.Time), Cams: map[string]string{}}
				segs[g.ID][f.Time] = s
			}
			s.Cams[f.Camera] = f.Rel
		case path.Base(f.Rel) == "thumb.png":
			g.Thumb = f.Rel
		case path.Base(f.Rel) != "event.json":
			g.Others = append(g.Others, f.Rel)
		}
	}

	for _, f := range ix.files {
		parts := strings.Split(f.Rel, "/")
		switch f.Kind {
		case "recent":
			if f.Camera != "" {
				recent = append(recent, f)
				continue
			}
			// The car's thumbnail of what it recorded last.
			if f.Rel == "RecentClips/thumb.png" {
				recentThumb = &f
				continue
			}
			add(get("RecentClips/other", "recent"), f)
		case "saved", "sentry":
			id := parts[0] + "/" + f.Time
			if len(parts) >= 3 {
				id = parts[0] + "/" + parts[1]
			}
			g := get(id, f.Kind)
			if len(parts) >= 3 {
				g.Event = ix.events[parts[0]+"/"+parts[1]]
			}
			add(g, f)
		case "track":
			day := "other"
			if name := path.Base(f.Rel); len(name) >= 10 && name[4] == '-' && name[7] == '-' {
				day = name[:10]
			}
			add(get("TeslaTrackMode/"+day, "track"), f)
		default:
			add(get("other", "other"), f)
		}
	}

	// Consecutive RecentClips minutes form one drive. Allow a gap of a
	// couple of minutes so one missing segment does not split a drive.
	slices.SortFunc(recent, func(a, b File) int { return cmp.Compare(a.Time, b.Time) })
	var cur *Group
	var last time.Time
	for _, f := range recent {
		t, err := time.Parse(timeLayout, f.Time)
		if err != nil {
			continue
		}
		if cur == nil || t.Sub(last) > 3*time.Minute {
			cur = get("RecentClips/"+f.Time, "recent")
		}
		last = t
		add(cur, f)
	}
	if recentThumb != nil {
		// It belongs to the newest drive; on its own it would be a drive of
		// no time and no date.
		if cur != nil {
			add(cur, *recentThumb)
		} else {
			add(get("RecentClips/other", "recent"), *recentThumb)
		}
	}

	out := make([]Group, 0, len(byID))
	for id, g := range byID {
		cams := map[string]bool{}
		for _, s := range segs[id] {
			g.Segments = append(g.Segments, *s)
			for c := range s.Cams {
				cams[c] = true
			}
		}
		slices.SortFunc(g.Segments, func(a, b Segment) int { return cmp.Compare(a.Time, b.Time) })
		for c := range cams {
			g.Cameras = append(g.Cameras, c)
		}
		slices.SortFunc(g.Cameras, func(a, b string) int { return cmp.Compare(cameraOrder(a), cameraOrder(b)) })
		slices.Sort(g.Others)
		if n := len(g.Segments); n > 0 {
			g.Start = g.Segments[0].Time
			if t, err := time.Parse("2006-01-02T15:04:05", g.Segments[n-1].Time); err == nil {
				g.End = t.Add(time.Minute).Format("2006-01-02T15:04:05")
			}
		} else if i := strings.LastIndex(id, "/"); i >= 0 {
			g.Start = isoTime(id[i+1:])
		}
		if g.Event != nil && g.Event.Timestamp != "" && g.Start == "" {
			g.Start = g.Event.Timestamp
		}
		out = append(out, *g)
	}
	slices.SortFunc(out, func(a, b Group) int {
		if c := cmp.Compare(b.Start, a.Start); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out
}

var camOrder = []string{"front", "back", "left_repeater", "right_repeater", "left_pillar", "right_pillar"}

func cameraOrder(c string) int {
	if i := slices.Index(camOrder, c); i >= 0 {
		return i
	}
	return len(camOrder)
}

// DeleteGroup removes a group's files from disk and records them in tomb so
// they are not extracted from the car's drive again.
func (ix *Index) DeleteGroup(id string, tomb *state.Set) (int64, error) {
	g, ok := ix.Group(id)
	if !ok {
		return 0, fmt.Errorf("group %q not found", id)
	}
	var freed int64
	dirs := map[string]bool{}
	for _, rel := range g.rels {
		if tomb != nil {
			if err := tomb.Add(rel); err != nil {
				return freed, err
			}
		}
		p := filepath.Join(ix.root, filepath.FromSlash(rel))
		if info, err := os.Stat(p); err == nil {
			freed += info.Size()
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return freed, err
		}
		if ix.uploaded != nil {
			ix.uploaded.Remove(rel)
		}
		ix.Remove(rel)
		dirs[filepath.Dir(p)] = true
	}
	for d := range dirs {
		// Remove now-empty directories up to the archive root.
		for d != ix.root && strings.HasPrefix(d, ix.root) {
			if os.Remove(d) != nil {
				break
			}
			d = filepath.Dir(d)
		}
	}
	return freed, nil
}

// Reclaimable returns how much space deleting the group would free now.
func (ix *Index) Reclaimable(g Group) int64 {
	var n int64
	for _, rel := range g.rels {
		if b, err := unsharedBytes(filepath.Join(ix.root, filepath.FromSlash(rel))); err == nil {
			n += b
		}
	}
	return n
}

var kindPriority = map[string]int{"saved": 0, "sentry": 1, "track": 2, "recent": 3}

// Pending returns archived files of the selected kinds that are not on the
// NAS yet: events first, oldest first.
func (ix *Index) Pending(want func(kind string) bool) []File {
	ix.mu.RLock()
	var out []File
	for _, f := range ix.files {
		if want(f.Kind) && (ix.uploaded == nil || !ix.uploaded.Has(f.Rel)) {
			out = append(out, f)
		}
	}
	ix.mu.RUnlock()
	slices.SortFunc(out, func(a, b File) int {
		if c := cmp.Compare(kindPriority[a.Kind], kindPriority[b.Kind]); c != 0 {
			return c
		}
		return cmp.Compare(a.Rel, b.Rel)
	})
	return out
}

func (ix *Index) MarkUploaded(rel string) error {
	if ix.uploaded == nil {
		return nil
	}
	err := ix.uploaded.Add(rel)
	ix.mu.Lock()
	ix.version++
	ix.mu.Unlock()
	return err
}

// PruneOrder lists groups in the order they should be deleted when space
// runs out: groups already on the NAS first, then RecentClips drives,
// track mode, Sentry and finally saved clips; oldest first within each.
func (ix *Index) PruneOrder() []Group {
	groups := slices.Clone(ix.Groups())
	rank := func(g Group) int {
		if g.Files > 0 && g.Uploaded == g.Files {
			return -1
		}
		return 3 - kindPriority[g.Kind]
	}
	slices.SortStableFunc(groups, func(a, b Group) int {
		if c := cmp.Compare(rank(a), rank(b)); c != 0 {
			return c
		}
		return cmp.Compare(a.Start, b.Start)
	})
	return groups
}
