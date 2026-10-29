// Package makerui is Squirrel Maker without its window: the page, its
// API and what goes on a card. The command line version (cmd/squirrel-maker)
// serves it on localhost; the Mac app (cmd/squirrel-app) shows it in its
// own window.
package makerui

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/marcusaram/squirrel/internal/config"
	"github.com/marcusaram/squirrel/internal/maker"
)

//go:embed all:payload
var payloadFS embed.FS

//go:embed static
var staticFS embed.FS

// Options are what differs between the command line version and the app.
type Options struct {
	Version string
	// Token, when set, must come with every API request (the command line
	// version: any page in the browser can reach localhost).
	Token string
	// Quit, when set, offers quitting from the page.
	Quit func()
	// Native, when set, asks and tells through the app's own dialogs; a
	// web view has no confirm() or alert().
	Native *Native
}

// Native is the app's own dialogs.
type Native struct {
	Ask  func(message, ok string) bool
	Tell func(message string)
}

// New sets up the maker with its download cache and remembered answers in
// the user's folders (SQUIRREL_MAKER_HOME for development).
func New(opts Options) (*Server, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	conf, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(payloadFS, "payload/files")
	if err != nil {
		return nil, err
	}
	all, err := fs.Sub(payloadFS, "payload")
	if err != nil {
		return nil, err
	}
	// The app was called TeslaUSB Maker before; keep its downloads and
	// remembered answers.
	for _, d := range []string{cache, conf} {
		if _, err := os.Stat(filepath.Join(d, "squirrel-maker")); os.IsNotExist(err) {
			os.Rename(filepath.Join(d, "teslausb-maker"), filepath.Join(d, "squirrel-maker"))
		}
	}
	a := &Server{
		opts:     opts,
		token:    opts.Token,
		cacheDir: filepath.Join(cache, "squirrel-maker"),
		stateDir: filepath.Join(conf, "squirrel-maker"),
		payload:  sub,
		all:      all,
	}
	if dir := os.Getenv("SQUIRREL_MAKER_HOME"); dir != "" {
		a.cacheDir, a.stateDir = filepath.Join(dir, "cache"), filepath.Join(dir, "state")
	}
	return a, nil
}

// Busy reports whether a build, write or update is running; quitting then
// would leave a card or image half done.
func (a *Server) Busy() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.job != nil && !a.job.Done
}

func (a *Server) ask(w http.ResponseWriter, r *http.Request) {
	var in struct{ Message, OK string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": a.opts.Native.Ask(in.Message, in.OK)})
}

func (a *Server) tell(w http.ResponseWriter, r *http.Request) {
	var in struct{ Message string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	a.opts.Native.Tell(in.Message)
	writeJSON(w, map[string]bool{"ok": true})
}

type job struct {
	Kind   string  `json:"kind"` // build or write
	Step   string  `json:"step"`
	Frac   float64 `json:"frac"`
	Done   bool    `json:"done"`
	Error  string  `json:"error,omitempty"`
	Result string  `json:"result,omitempty"`
}

type Server struct {
	opts     Options
	token    string
	cacheDir string
	stateDir string
	payload  fs.FS // the squirrel folder of a Raspberry Pi card
	all      fs.FS // files/, armbian/ and mac/

	mu    sync.Mutex
	job   *job
	image string // last built image
}

// Handler serves the page and its API.
func (a *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("/", http.FileServerFS(static))
	api := http.NewServeMux()
	api.HandleFunc("GET /api/defaults", a.defaults)
	api.HandleFunc("POST /api/build", a.build)
	api.HandleFunc("GET /api/job", a.getJob)
	api.HandleFunc("GET /api/disks", a.disks)
	api.HandleFunc("GET /api/releases", a.releases)
	api.HandleFunc("POST /api/write", a.write)
	api.HandleFunc("GET /api/card", a.card)
	api.HandleFunc("POST /api/updatecard", a.updateCard)
	api.HandleFunc("POST /api/reveal", a.reveal)
	if a.opts.Quit != nil {
		api.HandleFunc("POST /api/quit", a.quit)
	}
	if a.opts.Native != nil {
		api.HandleFunc("POST /api/ask", a.ask)
		api.HandleFunc("POST /api/tell", a.tell)
	}
	// Any web page could send requests to a port on localhost; the token
	// from the URL the command line version printed keeps them out. The app's
	// page is not on the network and has no token.
	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.token != "" && r.Header.Get("X-Token") != a.token {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		api.ServeHTTP(w, r)
	}))
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

var userRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func (a *Server) defaults(w http.ResponseWriter, r *http.Request) {
	s := maker.Settings{Hostname: "squirrel", CamPercent: 40, NAS: config.Default().NAS}
	if u, err := user.Current(); err == nil && userRe.MatchString(strings.ToLower(u.Username)) {
		s.User = strings.ToLower(u.Username)
	} else {
		s.User = "pi"
	}
	if link, err := os.Readlink("/etc/localtime"); err == nil {
		if _, tz, ok := strings.Cut(link, "zoneinfo/"); ok {
			s.Timezone = tz
		}
	}
	// The wifi country follows where you are, which the timezone tells
	// better than the language the Mac is set to.
	if c := countryForTimezone(s.Timezone); c != "" {
		s.Country = c
	} else if c := systemCountry(); c != "" {
		s.Country = c
	}
	if home, err := os.UserHomeDir(); err == nil {
		for _, k := range []string{"id_ed25519.pub", "id_ecdsa.pub", "id_rsa.pub"} {
			if b, err := os.ReadFile(filepath.Join(home, ".ssh", k)); err == nil {
				s.SSHKey = strings.TrimSpace(string(b))
				break
			}
		}
	}
	// Earlier answers, without the passwords.
	if b, err := os.ReadFile(filepath.Join(a.stateDir, "settings.json")); err == nil {
		json.Unmarshal(b, &s)
	}
	s.APPassword = config.RandomPassword(10)
	_, payloadErr := fs.Stat(a.payload, "squirreld")
	writeJSON(w, map[string]any{
		"settings":   s,
		"boards":     maker.Boards,
		"payload_ok": payloadErr == nil,
		"version":    a.opts.Version,
		"native":     a.opts.Native != nil,
		"platform":   runtime.GOOS,
	})
}

func countryForTimezone(tz string) string {
	data, err := os.ReadFile("/usr/share/zoneinfo/zone.tab")
	if err != nil || tz == "" {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Split(line, "\t")
		if len(f) >= 3 && f[2] == tz {
			return f[0]
		}
	}
	return ""
}

// saveSettings remembers the answers for a next card, but never the
// passwords.
func (a *Server) saveSettings(s maker.Settings) {
	s.Password, s.APPassword, s.NAS.Password = "", "", ""
	// A copy of the list: the build that runs next shares the original,
	// and must keep the passwords.
	s.WiFi = slices.Clone(s.WiFi)
	for i := range s.WiFi {
		s.WiFi[i].Password = ""
	}
	os.MkdirAll(a.stateDir, 0o700)
	b, _ := json.MarshalIndent(s, "", "  ")
	os.WriteFile(filepath.Join(a.stateDir, "settings.json"), b, 0o600)
}

func (a *Server) start(kind string) (*job, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.job != nil && !a.job.Done {
		return nil, errors.New("a task is already running")
	}
	a.job = &job{Kind: kind, Step: "Starting", Frac: -1}
	return a.job, nil
}

func (a *Server) update(j *job, fn func(*job)) {
	a.mu.Lock()
	fn(j)
	a.mu.Unlock()
}

func (a *Server) getJob(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.job == nil {
		writeJSON(w, nil)
		return
	}
	writeJSON(w, a.job)
}

func (a *Server) build(w http.ResponseWriter, r *http.Request) {
	var s maker.Settings
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Validate(); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	j, err := a.start("build")
	if err != nil {
		fail(w, http.StatusConflict, err)
		return
	}
	a.saveSettings(s)
	go func() {
		report := func(step string, frac float64) { a.update(j, func(j *job) { j.Step, j.Frac = step, frac }) }
		out, err := a.runBuild(context.Background(), s, report)
		a.update(j, func(j *job) {
			j.Done = true
			if err != nil {
				j.Error = err.Error()
				return
			}
			j.Step, j.Frac, j.Result = "Done", 1, out
			a.image = out
		})
	}()
	writeJSON(w, j)
}

func (a *Server) runBuild(ctx context.Context, s maker.Settings, report maker.Progress) (string, error) {
	board, err := maker.BoardByID(s.Board)
	if err != nil {
		return "", err
	}
	rel, err := maker.ReleaseByURL(ctx, board, s.Image)
	if err != nil {
		return "", err
	}
	xzPath, err := maker.Fetch(ctx, rel.Image, a.cacheDir, report)
	if err != nil {
		return "", err
	}
	out := filepath.Join(a.cacheDir, "squirrel-"+s.Hostname+".img")
	os.Remove(out)
	if err := maker.Decompress(ctx, xzPath, out, report); err != nil {
		return "", err
	}
	if board.Payload != "" {
		var payload fs.FS
		if payload, err = fs.Sub(a.all, board.Payload); err == nil {
			err = maker.Customize(ctx, out, s, payload, report)
		}
	} else {
		err = maker.CustomizeArmbian(ctx, out, s, a.all, report)
	}
	if err != nil {
		os.Remove(out)
		return "", err
	}
	return out, nil
}

// releases lists the OS images for a board: the tested one, then newer
// ones from the publisher. Offline it is just the tested one.
func (a *Server) releases(w http.ResponseWriter, r *http.Request) {
	board, err := maker.BoardByID(r.URL.Query().Get("board"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rels, err := maker.Releases(r.Context(), board)
	out := map[string]any{"releases": rels}
	if err != nil {
		out["error"] = "newer releases could not be listed: " + err.Error()
	}
	writeJSON(w, out)
}

func (a *Server) disks(w http.ResponseWriter, r *http.Request) {
	d, err := maker.ListDisks(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	if d == nil {
		d = []maker.Disk{}
	}
	writeJSON(w, d)
}

func (a *Server) write(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Disk string `json:"disk"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	a.mu.Lock()
	image := a.image
	a.mu.Unlock()
	if image == "" {
		fail(w, http.StatusBadRequest, errors.New("build the image first"))
		return
	}
	j, err := a.start("write")
	if err != nil {
		fail(w, http.StatusConflict, err)
		return
	}
	go func() {
		report := func(step string, frac float64) { a.update(j, func(j *job) { j.Step, j.Frac = step, frac }) }
		start := time.Now()
		err := maker.WriteCard(context.Background(), image, in.Disk, report)
		a.update(j, func(j *job) {
			j.Done = true
			if err != nil {
				j.Error = err.Error()
				return
			}
			j.Step, j.Frac = fmt.Sprintf("Done in %s", time.Since(start).Round(time.Second)), 1
		})
	}()
	writeJSON(w, j)
}

// card tells whether a card holds Squirrel, and which version.
func (a *Server) card(w http.ResponseWriter, r *http.Request) {
	info, err := maker.InspectCard(r.Context(), r.URL.Query().Get("id"))
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, err)
		return
	}
	writeJSON(w, info)
}

// updateCard puts this app's Squirrel on a card that has it; the Pi
// installs it at its next start and keeps its settings and clips.
func (a *Server) updateCard(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Disk string `json:"disk"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	j, err := a.start("update")
	if err != nil {
		fail(w, http.StatusConflict, err)
		return
	}
	go func() {
		report := func(step string, frac float64) { a.update(j, func(j *job) { j.Step, j.Frac = step, frac }) }
		info, err := maker.UpdateCard(context.Background(), in.Disk, a.all, report)
		a.update(j, func(j *job) {
			j.Done = true
			if err != nil {
				j.Error = err.Error()
				return
			}
			v := info.Version
			if v == "" {
				v = a.opts.Version
			}
			j.Step, j.Frac = "Updated to "+v, 1
		})
	}()
	writeJSON(w, j)
}

func (a *Server) quit(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	busy := a.job != nil && !a.job.Done
	a.mu.Unlock()
	if busy {
		fail(w, http.StatusConflict, errors.New("wait until the current task is done"))
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
	go func() {
		time.Sleep(200 * time.Millisecond)
		a.opts.Quit()
	}()
}

func (a *Server) reveal(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	image := a.image
	a.mu.Unlock()
	if image != "" {
		revealFile(image) // Finder or Explorer shows the image selected
	}
	writeJSON(w, map[string]bool{"ok": true})
}
