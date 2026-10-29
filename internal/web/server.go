// Package web serves the dashboard, the clip browser and the JSON API.
package web

import (
	"archive/zip"
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/marcusaram/squirrel/internal/archive"
	"github.com/marcusaram/squirrel/internal/config"
	"github.com/marcusaram/squirrel/internal/logbuf"
	"github.com/marcusaram/squirrel/internal/mp4"
	"github.com/marcusaram/squirrel/internal/nas"
	"github.com/marcusaram/squirrel/internal/state"
	"github.com/marcusaram/squirrel/internal/wifi"
)

//go:embed static
var static embed.FS

// Backend is what the server needs from the rest of the daemon.
type Backend interface {
	Status() any
	Scan()
	SyncNAS()
	Repair(ctx context.Context) (string, error)
	ResizeCam(ctx context.Context, percent int) (string, error)
	// ComputerMode switches between the car's drive and the read-only
	// drive with long videos for a computer.
	ComputerMode(on bool, lang string) error
	ComputerOn() bool
}

type Server struct {
	Config  *config.Store
	Index   *archive.Index
	Tomb    *state.Set
	Logs    *logbuf.Buffer
	Log     *slog.Logger
	Backend Backend
	WiFi    *wifi.Manager
	Version string
	// StaticDir serves the UI from disk instead of the embedded copy,
	// for working on it without rebuilding.
	StaticDir string

	cttsMu sync.Mutex
	ctts   map[string]cttsEntry

	telMu sync.Mutex
	tel   map[string]telEntry

	longMu    sync.Mutex
	longs     map[string]longEntry
	longBuild sync.Mutex // one build at a time: requests come in bursts
}

type longEntry struct {
	key  string // the files and their sizes; changes when they do
	long *mp4.Long
	mod  time.Time
	etag string
}

type telEntry struct {
	size    int64
	mtime   time.Time
	samples []mp4.Sample
	dur     float64 // seconds of video the samples cover
}

type cttsEntry struct {
	size    int64
	mtime   time.Time
	offsets []int64
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(static, "static")
	if s.StaticDir != "" {
		sub = os.DirFS(s.StaticDir)
	}
	mux.Handle("GET /", http.FileServerFS(sub))
	mux.HandleFunc("GET /media/{path...}", s.media)
	mux.HandleFunc("GET /api/telemetry", s.telemetry)
	mux.HandleFunc("GET /api/long", s.longIndex)
	mux.HandleFunc("GET /long/{cam}/{id...}", s.longVideo)
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/groups", s.groups)
	mux.HandleFunc("GET /api/group", s.group)
	mux.HandleFunc("DELETE /api/group", s.deleteGroup)
	mux.HandleFunc("GET /api/download", s.download)
	mux.HandleFunc("GET /api/config", s.getConfig)
	mux.HandleFunc("PUT /api/config", s.putConfig)
	mux.HandleFunc("POST /api/nas/test", s.testNAS)
	mux.HandleFunc("POST /api/nas/sync", func(w http.ResponseWriter, r *http.Request) { s.Backend.SyncNAS(); ok(w) })
	mux.HandleFunc("POST /api/scan", func(w http.ResponseWriter, r *http.Request) { s.Backend.Scan(); ok(w) })
	mux.HandleFunc("POST /api/repair", s.repair)
	mux.HandleFunc("POST /api/camsize", s.camSize)
	mux.HandleFunc("POST /api/computer", s.computer)
	mux.HandleFunc("GET /api/logs", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.Logs.Records()) })
	mux.HandleFunc("POST /api/reboot", s.reboot)
	mux.HandleFunc("GET /api/wifi", s.wifiInfo)
	mux.HandleFunc("GET /api/wifi/scan", s.wifiScan)
	mux.HandleFunc("POST /api/wifi/networks", s.wifiAdd)
	mux.HandleFunc("DELETE /api/wifi/networks", s.wifiRemove)
	mux.HandleFunc("PUT /api/wifi/hotspot", s.wifiHotspot)
	// Changes only from the web UI itself: another site open on a phone at
	// home must not be able to add a wifi network or replace the drive.
	return s.captive(s.auth(http.NewCrossOriginProtection().Handler(mux)))
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		web := s.Config.Get().Web
		if web.User != "" {
			u, p, okAuth := r.BasicAuth()
			if !okAuth || subtle.ConstantTimeCompare([]byte(u), []byte(web.User)) != 1 ||
				subtle.ConstantTimeCompare([]byte(p), []byte(web.Password)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="squirrel"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter) { writeJSON(w, map[string]bool{"ok": true}) }

func fail(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.Backend.Status())
}

type groupSummary struct {
	archive.Group
	SegmentCount int `json:"segment_count"`
}

func (s *Server) groups(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	out := []groupSummary{}
	for _, g := range s.Index.Groups() {
		if kind != "" && g.Kind != kind {
			continue
		}
		sum := groupSummary{Group: g, SegmentCount: len(g.Segments)}
		sum.Segments = nil
		sum.Others = nil
		out = append(out, sum)
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d-%s"`, s.Index.Version(), kind))
	writeJSON(w, out)
}

func (s *Server) group(w http.ResponseWriter, r *http.Request) {
	g, found := s.Index.Group(r.URL.Query().Get("id"))
	if !found {
		fail(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	writeJSON(w, g)
}

func (s *Server) deleteGroup(w http.ResponseWriter, r *http.Request) {
	if s.Backend.ComputerOn() {
		fail(w, http.StatusConflict, errors.New("switch back to car mode first; a computer may be reading these clips"))
		return
	}
	id := r.URL.Query().Get("id")
	freed, err := s.Index.DeleteGroup(id, s.Tomb)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	s.Log.Info("deleted from web UI", "group", id, "bytes", freed)
	ok(w)
}

// openArchived opens a file below the archive root, refusing anything that
// would escape it.
func (s *Server) openArchived(rel string) (*os.File, error) {
	root, err := os.OpenRoot(s.Index.Root())
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.Open(path.Clean(rel))
}

func (s *Server) media(w http.ResponseWriter, r *http.Request) {
	rel := r.PathValue("path")
	f, err := s.openArchived(rel)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	var rs io.ReadSeeker = f
	if strings.HasSuffix(strings.ToLower(rel), ".mp4") {
		w.Header().Set("Content-Type", "video/mp4")
		if offs := s.cttsOffsets(rel, f, fi); len(offs) > 0 {
			rs = mp4.NewPatchedReader(f, offs)
		}
	}
	if r.URL.Query().Has("download") {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", path.Base(rel)))
	}
	http.ServeContent(w, r, path.Base(rel), fi.ModTime(), rs)
}

func (s *Server) cttsOffsets(rel string, f *os.File, fi os.FileInfo) []int64 {
	s.cttsMu.Lock()
	defer s.cttsMu.Unlock()
	if s.ctts == nil || len(s.ctts) > 1000 {
		s.ctts = map[string]cttsEntry{}
	}
	if e, found := s.ctts[rel]; found && e.size == fi.Size() && e.mtime.Equal(fi.ModTime()) {
		return e.offsets
	}
	offs, _ := mp4.CTTSOffsets(f, fi.Size())
	s.ctts[rel] = cttsEntry{fi.Size(), fi.ModTime(), offs}
	return offs
}

// telemetry returns what the car recorded about itself in a clip (speed,
// Autopilot, blinkers, position), a few samples per second. Reading it
// means walking the whole file once, so the result is kept.
func (s *Server) telemetry(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("rel")
	f, err := s.openArchived(rel)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	s.telMu.Lock()
	e, found := s.tel[rel]
	s.telMu.Unlock()
	if !found || e.size != fi.Size() || !e.mtime.Equal(fi.ModTime()) {
		samples, err := mp4.Telemetry(f, fi.Size(), 4*60)
		if err != nil {
			fail(w, http.StatusUnprocessableEntity, err)
			return
		}
		dur, _ := mp4.Duration(f, fi.Size())
		e = telEntry{fi.Size(), fi.ModTime(), samples, dur}
		s.telMu.Lock()
		if s.tel == nil || len(s.tel) > 2000 {
			s.tel = map[string]telEntry{}
		}
		s.tel[rel] = e
		s.telMu.Unlock()
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"samples": e.samples, "dur": e.dur})
}

// long returns the continuous video of one camera of a group, building it
// from the minute files when they changed. Only a handful are kept: each
// holds the frame tables of a whole drive.
func (s *Server) long(g archive.Group, cam string) (longEntry, error) {
	var files []string
	var key strings.Builder
	var mod time.Time
	for _, seg := range g.Segments {
		rel := seg.Cams[cam]
		p := ""
		if rel != "" {
			p = filepath.Join(s.Index.Root(), filepath.FromSlash(path.Clean("/"+rel)))
			if fi, err := os.Stat(p); err == nil {
				fmt.Fprintf(&key, "%s:%d;", rel, fi.Size())
				if fi.ModTime().After(mod) {
					mod = fi.ModTime()
				}
			}
		}
		files = append(files, p)
	}
	id := g.ID + "|" + cam
	cached := func() (longEntry, bool) {
		s.longMu.Lock()
		defer s.longMu.Unlock()
		e, ok := s.longs[id]
		return e, ok && e.key == key.String()
	}
	if e, ok := cached(); ok {
		return e, nil
	}
	// Opening a drive asks for the same video several times at once (the
	// index, the player, Safari's ranges); the first builds it.
	s.longBuild.Lock()
	defer s.longBuild.Unlock()
	if e, ok := cached(); ok {
		return e, nil
	}
	l, err := mp4.ConcatAt(files, g.SegmentStarts())
	if err != nil {
		return longEntry{}, err
	}
	// The modification time alone does not change when a minute goes
	// missing, or when a new version builds the video differently; the
	// ETag does, so browsers drop what they cached.
	h := fnv.New64a()
	fmt.Fprintf(h, "%d|%s", mp4.LongFormat, key.String())
	e := longEntry{key.String(), l, mod, fmt.Sprintf(`"%x"`, h.Sum64())}
	s.longMu.Lock()
	if s.longs == nil || len(s.longs) >= 12 {
		s.longs = map[string]longEntry{}
	}
	s.longs[id] = e
	s.longMu.Unlock()
	return e, nil
}

// longIndex tells the player, per camera, where each minute of a group
// sits in that camera's continuous video.
func (s *Server) longIndex(w http.ResponseWriter, r *http.Request) {
	g, found := s.Index.Group(r.URL.Query().Get("id"))
	if !found {
		fail(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	cams := map[string][]mp4.LongSeg{}
	for _, cam := range g.Cameras {
		if e, err := s.long(g, cam); err == nil {
			cams[cam] = e.long.Segs
		} else {
			s.Log.Warn("long video", "group", g.ID, "camera", cam, "err", err)
		}
	}
	writeJSON(w, map[string]any{"cams": cams})
}

// longVideo serves one camera of a group as a single mp4, with range
// requests; the name makes a sensible download.
func (s *Server) longVideo(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(r.PathValue("id"), ".mp4")
	g, found := s.Index.Group(id)
	if !found {
		http.NotFound(w, r)
		return
	}
	e, err := s.long(g, r.PathValue("cam"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rd := e.long.Open()
	defer rd.Close()
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", e.etag)
	if r.URL.Query().Has("download") {
		name := strings.ReplaceAll(g.ID, "/", "_") + "-" + r.PathValue("cam") + ".mp4"
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	}
	http.ServeContent(w, r, "", e.mod, rd)
}

// download streams a whole group as an uncompressed zip; video does not
// compress, and storing keeps the Pi's CPU out of the way.
func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	g, found := s.Index.Group(r.URL.Query().Get("id"))
	if !found {
		http.NotFound(w, r)
		return
	}
	var rels []string
	for _, seg := range g.Segments {
		for _, rel := range seg.Cams {
			rels = append(rels, rel)
		}
	}
	rels = append(rels, g.Others...)
	if g.Thumb != "" {
		rels = append(rels, g.Thumb)
	}
	if g.Event != nil {
		rels = append(rels, path.Join(g.ID, "event.json"))
	}
	name := strings.ReplaceAll(g.ID, "/", "_") + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	zw := zip.NewWriter(w)
	for _, rel := range rels {
		f, err := s.openArchived(rel)
		if err != nil {
			continue
		}
		fi, _ := f.Stat()
		hdr, _ := zip.FileInfoHeader(fi)
		hdr.Name = rel
		hdr.Method = zip.Store
		zf, err := zw.CreateHeader(hdr)
		if err == nil {
			_, err = io.Copy(zf, f)
		}
		f.Close()
		if err != nil {
			return
		}
	}
	zw.Close()
}

const secretMask = "********"

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	c := s.Config.Get()
	if c.NAS.Password != "" {
		c.NAS.Password = secretMask
	}
	if c.Web.Password != "" {
		c.Web.Password = secretMask
	}
	writeJSON(w, c)
}

// putConfig accepts the editable part of the config. Secrets sent back as
// the mask keep their stored value.
func (s *Server) putConfig(w http.ResponseWriter, r *http.Request) {
	var in config.Config
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	err := s.Config.Update(func(c *config.Config) error {
		// Not 60: that was the old default, which Load replaces.
		if in.ScanInterval < 2 || in.ScanInterval > 30 {
			return errors.New("the scan interval must be between 2 and 30 seconds")
		}
		if in.ReserveGB < 1 {
			return errors.New("the reserve must be at least 1 GB")
		}
		c.ScanInterval = in.ScanInterval
		c.ReserveGB = in.ReserveGB
		c.Keep = in.Keep
		if in.NAS.Password == secretMask {
			in.NAS.Password = c.NAS.Password
		}
		c.NAS = in.NAS
		if in.Web.Password == secretMask {
			in.Web.Password = c.Web.Password
		}
		c.Web.User, c.Web.Password = in.Web.User, in.Web.Password
		c.Diagnostics = in.Diagnostics
		return nil
	})
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	s.Log.Info("settings saved")
	ok(w)
}

func (s *Server) testNAS(w http.ResponseWriter, r *http.Request) {
	var in config.NAS
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if in.Password == secretMask {
		in.Password = s.Config.Get().NAS.Password
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := nas.Test(ctx, in); err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	ok(w)
}

func (s *Server) repair(w http.ResponseWriter, r *http.Request) {
	out, err := s.Backend.Repair(r.Context())
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error(), "output": out})
		return
	}
	writeJSON(w, map[string]string{"output": out})
}

func (s *Server) reboot(w http.ResponseWriter, r *http.Request) {
	s.Log.Warn("reboot requested from web UI")
	ok(w)
	go func() {
		time.Sleep(time.Second)
		exec.Command("systemctl", "reboot").Run()
	}()
}

// captive sends phones on the hotspot to the web UI. The hotspot's DNS
// answers every name with the Pi, so a phone's connectivity check (for
// example captive.apple.com) arrives here and opens the setup page.
func (s *Server) captive(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.WiFi != nil && s.WiFi.HotspotActive() {
			host := r.Host
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			name, _ := os.Hostname()
			if net.ParseIP(host) == nil && host != name && host != name+".local" && host != "localhost" {
				http.Redirect(w, r, "http://"+wifi.HotspotIP+"/", http.StatusFound)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) wifiInfo(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config.Get().WiFi
	out := map[string]any{"hotspot": cfg, "status": wifi.Status{}, "networks": []wifi.Network{}}
	if s.WiFi != nil {
		out["status"] = s.WiFi.Status()
		if nets, err := s.WiFi.Saved(); err == nil {
			out["networks"] = nets
		} else {
			out["error"] = err.Error()
		}
	}
	writeJSON(w, out)
}

func (s *Server) wifiScan(w http.ResponseWriter, r *http.Request) {
	if s.WiFi == nil {
		writeJSON(w, []wifi.Seen{})
		return
	}
	seen, err := s.WiFi.Scan()
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, seen)
}

func (s *Server) wifiAdd(w http.ResponseWriter, r *http.Request) {
	var in struct{ SSID, Password string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<12)).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if s.WiFi == nil {
		fail(w, http.StatusServiceUnavailable, errors.New("no wifi"))
		return
	}
	if err := s.WiFi.Add(in.SSID, in.Password); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	s.Log.Info("wifi network added", "ssid", in.SSID)
	ok(w)
}

func (s *Server) wifiRemove(w http.ResponseWriter, r *http.Request) {
	if s.WiFi == nil {
		fail(w, http.StatusServiceUnavailable, errors.New("no wifi"))
		return
	}
	if err := s.WiFi.Remove(r.URL.Query().Get("uuid")); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	ok(w)
}

func (s *Server) wifiHotspot(w http.ResponseWriter, r *http.Request) {
	var in config.WiFi
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<12)).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	in.APSSID = strings.TrimSpace(in.APSSID)
	if in.APSSID == "" || len(in.APSSID) > 32 || len(in.APPassword) < 8 || len(in.APPassword) > 63 {
		fail(w, http.StatusBadRequest, errors.New("the hotspot needs a name (up to 32 characters) and a password of 8 to 63 characters"))
		return
	}
	if err := s.Config.Update(func(c *config.Config) error { c.WiFi = in; return nil }); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	ok(w)
}

func (s *Server) computer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		On   bool   `json:"on"`
		Lang string `json:"lang"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Backend.ComputerMode(in.On, in.Lang); err != nil {
		fail(w, http.StatusConflict, err)
		return
	}
	ok(w)
}

func (s *Server) camSize(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Percent int `json:"percent"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	out, err := s.Backend.ResizeCam(context.WithoutCancel(r.Context()), in.Percent)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error(), "output": out})
		return
	}
	writeJSON(w, map[string]string{"output": out})
}
