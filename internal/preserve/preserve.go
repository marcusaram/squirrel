// Package preserve copies every finished recording out of the car's disk
// image into the archive, before the car rotates it away.
//
// The image is read while the car keeps writing to it. A recording is taken
// as soon as it is a complete MP4 (the car writes the index last), other
// files once they have looked identical in two scans; after cloning, the
// directory entry is read again to make sure it did not change meanwhile.
//
// Speed matters: the car deletes what it recorded while parked as soon as
// it drives off, often less than a minute after the last file closed. A
// recording that vanishes before it was taken is rescued from where it was
// seen being written, if its clusters are still free and it is whole.
package preserve

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/marcusaram/squirrel/internal/archive"
	"github.com/marcusaram/squirrel/internal/clone"
	"github.com/marcusaram/squirrel/internal/config"
	"github.com/marcusaram/squirrel/internal/fatfs"
	"github.com/marcusaram/squirrel/internal/mp4"
	"github.com/marcusaram/squirrel/internal/state"
)

// Folders on the car's drive, relative to the volume root.
var folders = []struct {
	path string
	on   func(config.Folders) bool
}{
	{"TeslaCam/RecentClips", func(f config.Folders) bool { return f.Recent }},
	{"TeslaCam/SavedClips", func(f config.Folders) bool { return f.Saved }},
	{"TeslaCam/SentryClips", func(f config.Folders) bool { return f.Sentry }},
	{"TeslaTrackMode", func(f config.Folders) bool { return f.Track }},
}

type sig struct {
	size    int64
	cluster uint32
	mtime   time.Time
}

type seen struct {
	e  fatfs.Entry
	at time.Time
}

type Status struct {
	Running       bool      `json:"running"`
	LastScan      time.Time `json:"last_scan,omitzero"`
	LastDuration  string    `json:"last_duration,omitempty"`
	FSType        string    `json:"fs_type,omitempty"`
	ClusterSize   int64     `json:"cluster_size,omitempty"`
	Dirty         bool      `json:"dirty"`
	FilesOnDrive  int       `json:"files_on_drive"`
	Waiting       int       `json:"waiting"`
	Extracted     int       `json:"extracted"`
	BytesShared   int64     `json:"bytes_reflinked"`
	BytesCopied   int64     `json:"bytes_copied"`
	LastExtracted string    `json:"last_extracted,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	LastErrorAt   time.Time `json:"last_error_at,omitzero"`
}

type Preserver struct {
	Config func() config.Config
	Index  *archive.Index
	Tomb   *state.Set
	Log    *slog.Logger

	trigger chan struct{}
	prev    map[string]sig
	since   map[string]time.Time // when a file was first seen as it is now
	pending map[string]seen      // recordings not taken yet, as last seen
	warned  map[string]string    // the last extract error logged per file
	now     func() time.Time

	mu     sync.Mutex
	status Status
	paused bool
}

func (p *Preserver) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

// Trigger asks for a scan now.
func (p *Preserver) Trigger() {
	select {
	case p.trigger <- struct{}{}:
	default:
	}
}

// Pause stops scanning while the image is being repaired.
func (p *Preserver) Pause(on bool) {
	p.mu.Lock()
	p.paused = on
	p.mu.Unlock()
}

func New(cfg func() config.Config, ix *archive.Index, tomb *state.Set, log *slog.Logger) *Preserver {
	return &Preserver{
		Config:  cfg,
		Index:   ix,
		Tomb:    tomb,
		Log:     log,
		trigger: make(chan struct{}, 1),
		prev:    map[string]sig{},
		since:   map[string]time.Time{},
		pending: map[string]seen{},
		warned:  map[string]string{},
		now:     time.Now,
	}
}

func (p *Preserver) Run(ctx context.Context) {
	for {
		p.mu.Lock()
		paused := p.paused
		p.mu.Unlock()
		if !paused {
			if err := p.Scan(ctx); err != nil && ctx.Err() == nil {
				p.fail(err)
			}
		}
		interval := time.Duration(p.Config().ScanInterval) * time.Second
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		case <-p.trigger:
		}
	}
}

func (p *Preserver) fail(err error) {
	p.Log.Error("scan", "err", err)
	p.mu.Lock()
	p.status.LastError = err.Error()
	p.status.LastErrorAt = time.Now()
	p.mu.Unlock()
}

// destPath maps a path on the car's drive to the archive. RecentClips get a
// per-day directory so no directory grows without bound.
func destPath(src string) string {
	rel := strings.TrimPrefix(src, "TeslaCam/")
	if dir, name := path.Split(rel); dir == "RecentClips/" && len(name) > 10 && name[4] == '-' && name[7] == '-' {
		return "RecentClips/" + name[:10] + "/" + name
	}
	return rel
}

func (p *Preserver) Scan(ctx context.Context) error {
	cfg := p.Config()
	start := time.Now()
	p.mu.Lock()
	p.status.Running = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.status.Running = false
		p.mu.Unlock()
	}()

	img, err := os.Open(cfg.CamImage())
	if err != nil {
		return err
	}
	defer img.Close()
	st, err := img.Stat()
	if err != nil {
		return err
	}
	v, err := fatfs.Open(img, st.Size())
	if err != nil {
		return fmt.Errorf("reading %s: %w", cfg.CamImage(), err)
	}

	cur := map[string]sig{}
	files, complete := p.list(v, cfg.Keep)
	now := p.now()

	produced := map[string]bool{}
	waiting := 0
	for _, e := range files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s := sig{e.Size, e.FirstCluster, e.ModTime}
		cur[e.Path] = s
		dest := destPath(e.Path)
		produced[dest] = true
		if e.Size == 0 || p.Tomb.Has(dest) || p.Index.Has(dest, e.Size) {
			delete(p.pending, e.Path)
			continue
		}
		if isMP4(e) && e.FirstCluster >= 2 {
			p.pending[e.Path] = seen{e, now}
		}
		if prev, ok := p.prev[e.Path]; !ok || prev != s || !e.Complete {
			p.since[e.Path] = now
			if !e.Complete || !finished(v, e) {
				waiting++
				continue
			}
		}
		if err := p.extract(img, v, e, dest, now.Sub(p.since[e.Path]), unchanged(v, e)); err != nil {
			waiting++
			// Tried again every scan; said once.
			if p.warned[e.Path] != err.Error() {
				p.warned[e.Path] = err.Error()
				p.Log.Warn("extract", "file", e.Path, "err", err)
			}
			continue
		}
		delete(p.pending, e.Path)
		delete(p.warned, e.Path)
	}
	if complete {
		p.rescue(img, v, cur, now)
	}
	p.prev = cur
	for k := range p.since {
		if _, ok := cur[k]; !ok {
			delete(p.since, k)
		}
	}
	for k := range p.warned {
		if _, ok := cur[k]; !ok {
			delete(p.warned, k)
		}
	}
	if complete {
		// Forget tombstones for clips the car has rotated away itself.
		if err := p.Tomb.Retain(func(k string) bool { return produced[k] }); err != nil {
			p.Log.Warn("tombstones", "err", err)
		}
	}

	p.mu.Lock()
	p.status.LastScan = time.Now()
	p.status.LastDuration = time.Since(start).Round(time.Millisecond).String()
	p.status.FSType = v.Type
	p.status.ClusterSize = v.ClusterSize
	p.status.Dirty = v.Dirty
	p.status.FilesOnDrive = len(files)
	p.status.Waiting = waiting
	p.mu.Unlock()
	return nil
}

// list returns the files in the selected TeslaCam folders. complete is false
// when a directory could not be read.
func (p *Preserver) list(v *fatfs.Volume, keep config.Folders) (files []fatfs.Entry, complete bool) {
	complete = true
	for _, f := range folders {
		if !f.on(keep) {
			continue
		}
		dir, err := v.Lookup(f.path)
		if errors.Is(err, fatfs.ErrNotFound) {
			continue
		}
		if err != nil {
			complete = false
			p.Log.Warn("lookup", "dir", f.path, "err", err)
			continue
		}
		v.Walk(dir, func(e fatfs.Entry, err error) error {
			if err != nil {
				complete = false
				p.Log.Warn("read dir", "dir", e.Path, "err", err)
				return nil
			}
			if !e.IsDir {
				files = append(files, e)
			}
			return nil
		})
	}
	return files, complete
}

// Import preserves every finished clip from an image nobody is writing to,
// such as a snapshot left behind by teslausb. Recordings that were still
// in progress when the image was taken fail validation and are skipped.
func (p *Preserver) Import(path string) (imported, skipped int, err error) {
	img, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer img.Close()
	st, err := img.Stat()
	if err != nil {
		return 0, 0, err
	}
	v, err := fatfs.Open(img, st.Size())
	if err != nil {
		return 0, 0, err
	}
	files, _ := p.list(v, p.Config().Keep)
	for _, e := range files {
		dest := destPath(e.Path)
		if e.Size == 0 || !e.Complete || p.Tomb.Has(dest) || p.Index.Has(dest, e.Size) {
			continue
		}
		if err := p.extract(img, v, e, dest, 0, unchanged(v, e)); err != nil {
			skipped++
			p.Log.Debug("import skipped", "file", e.Path, "err", err)
			continue
		}
		imported++
	}
	return imported, skipped, nil
}

var errChanged = errors.New("file changed while extracting")

func isMP4(e fatfs.Entry) bool { return strings.HasSuffix(strings.ToLower(e.Name), ".mp4") }

// finished tells whether a recording is complete, reading it straight from
// the image so that a file still being written costs nothing.
func finished(v *fatfs.Volume, e fatfs.Entry) bool {
	if !isMP4(e) {
		return false
	}
	f, err := v.OpenFile(e)
	return err == nil && mp4.Validate(f, e.Size) == nil
}

// unchanged checks after cloning that the directory entry still describes
// the same data; otherwise the car may have reused the clusters meanwhile.
func unchanged(v *fatfs.Volume, e fatfs.Entry) func() error {
	return func() error {
		again, err := v.Lookup(e.Path)
		if err != nil || again.FirstCluster != e.FirstCluster || again.Size != e.Size || !again.ModTime.Equal(e.ModTime) {
			return errChanged
		}
		return nil
	}
}

// rescue saves recordings that vanished since the last scan before they
// were taken. Their data stays until the car reuses the clusters, and we
// know where it starts from having seen the file being written.
func (p *Preserver) rescue(img *os.File, v *fatfs.Volume, cur map[string]sig, now time.Time) {
	for path, s := range p.pending {
		if _, ok := cur[path]; ok {
			continue
		}
		delete(p.pending, path)
		dest := destPath(path)
		if p.Tomb.Has(dest) || now.Sub(s.at) > time.Hour {
			continue
		}
		e, err := deleted(v, s.e)
		if err == nil && p.Index.Has(dest, e.Size) {
			continue
		}
		if err == nil {
			err = p.extract(img, v, e, dest, 0, func() error {
				if free, err := v.Free(e); err != nil || !free {
					return errChanged
				}
				return nil
			})
		}
		if err != nil {
			p.Log.Warn("recording deleted before it was saved", "file", path, "err", err)
			continue
		}
		p.Log.Info("rescued a deleted recording", "file", dest)
	}
}

// deleted finds a deleted recording from its last sighting: its size from
// the MP4 itself, its clusters all free, its video track whole.
func deleted(v *fatfs.Volume, last fatfs.Entry) (fatfs.Entry, error) {
	reach, err := v.Reach(last)
	if err != nil {
		return fatfs.Entry{}, err
	}
	f, err := v.OpenFile(reach)
	if err != nil {
		return fatfs.Entry{}, err
	}
	e := last
	if e.Size, err = mp4.Length(f, reach.Size); err != nil {
		return fatfs.Entry{}, err
	}
	if free, err := v.Free(e); err != nil {
		return fatfs.Entry{}, err
	} else if !free {
		return fatfs.Entry{}, errors.New("its clusters are in use again")
	}
	if f, err = v.OpenFile(e); err != nil {
		return fatfs.Entry{}, err
	}
	return e, mp4.CheckTrack(f, e.Size)
}

// keepBroken is how long a recording must stay unchanged before it is kept
// although it does not validate.
const keepBroken = 3 * time.Minute

func (p *Preserver) extract(img *os.File, v *fatfs.Volume, e fatfs.Entry, dest string, stable time.Duration, still func() error) error {
	ext, err := v.Extents(e)
	if err != nil {
		return err
	}
	final := filepath.Join(p.Index.Root(), filepath.FromSlash(dest))
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(final), archive.TmpPrefix+filepath.Base(final))
	out, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			out.Close()
			os.Remove(tmp)
		}
	}()
	res, err := clone.Extract(img, out, ext, e.Size)
	if err != nil {
		return err
	}

	if err := still(); err != nil {
		return err
	}
	if isMP4(e) {
		if err := mp4.Validate(out, e.Size); err != nil {
			// A recording the car finished never changes again. If it has
			// stayed the same for minutes and still looks broken, keep it
			// anyway: a damaged clip beats a missing one.
			if stable < keepBroken {
				return err
			}
			p.Log.Warn("keeping clip that does not validate", "file", e.Path, "err", err)
		}
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	ok = true
	p.Index.Add(dest, e.Size)

	p.mu.Lock()
	p.status.Extracted++
	p.status.BytesShared += res.Reflinked
	p.status.BytesCopied += res.Copied
	p.status.LastExtracted = dest
	p.mu.Unlock()
	p.Log.Debug("preserved", "file", dest, "reflinked", res.Reflinked, "copied", res.Copied)
	return nil
}
