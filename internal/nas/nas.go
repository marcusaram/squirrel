// Package nas uploads archived clips to an SMB share when the NAS is
// reachable (in practice: when the car is parked at home).
//
// It talks SMB itself instead of using a kernel CIFS mount. A CIFS mount
// whose server disappears (the car drives off) can hang processes in
// uninterruptible sleep; here every network operation has a deadline and a
// broken connection is simply dropped and redialed later.
package nas

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cloudsoda/go-smb2"

	"github.com/marcusaram/squirrel/internal/archive"
	"github.com/marcusaram/squirrel/internal/config"
)

const ioTimeout = 60 * time.Second

type Status struct {
	State        string    `json:"state"` // disabled, unreachable, idle, uploading, error
	Pending      int       `json:"pending"`
	PendingBytes int64     `json:"pending_bytes"`
	Current      string    `json:"current,omitempty"`
	CurrentDone  int64     `json:"current_done,omitempty"`
	CurrentSize  int64     `json:"current_size,omitempty"`
	RateBps      float64   `json:"rate_bps,omitempty"`
	Uploaded     int       `json:"uploaded_session"`
	LastSuccess  time.Time `json:"last_success,omitzero"`
	LastError    string    `json:"last_error,omitempty"`
	LastErrorAt  time.Time `json:"last_error_at,omitzero"`
	LastCheck    time.Time `json:"last_check,omitzero"`
}

type Syncer struct {
	Config func() config.Config
	Index  *archive.Index
	Log    *slog.Logger

	trigger chan struct{}
	mu      sync.Mutex
	status  Status
}

func New(cfg func() config.Config, ix *archive.Index, log *slog.Logger) *Syncer {
	return &Syncer{Config: cfg, Index: ix, Log: log, trigger: make(chan struct{}, 1)}
}

func (s *Syncer) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *Syncer) setStatus(fn func(*Status)) {
	s.mu.Lock()
	fn(&s.status)
	s.mu.Unlock()
}

func (s *Syncer) Trigger() {
	select {
	case s.trigger <- struct{}{}:
	default:
	}
}

func (s *Syncer) Run(ctx context.Context) {
	backoff := time.Minute
	for {
		err := s.cycle(ctx)
		wait := time.Minute
		if err != nil && ctx.Err() == nil {
			s.Log.Warn("nas sync", "err", err)
			s.setStatus(func(st *Status) {
				st.State = "error"
				st.LastError = err.Error()
				st.LastErrorAt = time.Now()
			})
			wait = backoff
			backoff = min(backoff*2, 10*time.Minute)
		} else {
			backoff = time.Minute
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		case <-s.trigger:
		}
	}
}

func uploadKinds(f config.Folders) func(string) bool {
	return func(kind string) bool {
		switch kind {
		case "recent":
			return f.Recent
		case "saved":
			return f.Saved
		case "sentry":
			return f.Sentry
		case "track":
			return f.Track
		}
		return false
	}
}

func address(host string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(host, "445")
}

func (s *Syncer) cycle(ctx context.Context) error {
	cfg := s.Config().NAS
	pending := s.Index.Pending(uploadKinds(cfg.Upload))
	var bytes int64
	for _, f := range pending {
		bytes += f.Size
	}
	s.setStatus(func(st *Status) {
		st.Pending, st.PendingBytes = len(pending), bytes
		st.LastCheck = time.Now()
	})
	if !cfg.Enabled || cfg.Host == "" || cfg.Share == "" {
		s.setStatus(func(st *Status) { st.State = "disabled" })
		return nil
	}
	if len(pending) == 0 {
		s.setStatus(func(st *Status) { st.State = "idle" })
		return nil
	}
	share, closeFn, err := Connect(ctx, cfg)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) || errors.Is(err, context.DeadlineExceeded) {
			s.setStatus(func(st *Status) { st.State = "unreachable" })
			return nil
		}
		return err
	}
	defer closeFn()

	s.setStatus(func(st *Status) { st.State = "uploading"; st.Uploaded = 0 })
	made := map[string]bool{}
	for _, f := range pending {
		if ctx.Err() != nil {
			return nil
		}
		if s.Config().NAS != cfg {
			return nil // settings changed; start over with the new ones
		}
		if err := s.upload(share, cfg.Path, f, made); err != nil {
			if os.IsNotExist(err) {
				// Pruned meanwhile.
				s.setStatus(func(st *Status) { st.Pending--; st.PendingBytes -= f.Size })
				continue
			}
			return fmt.Errorf("%s: %w", f.Rel, err)
		}
		if err := s.Index.MarkUploaded(f.Rel); err != nil {
			return err
		}
		s.setStatus(func(st *Status) {
			st.Uploaded++
			st.Pending--
			st.PendingBytes -= f.Size
			st.LastSuccess = time.Now()
		})
	}
	s.setStatus(func(st *Status) { st.State = "idle"; st.Current = ""; st.RateBps = 0 })
	s.Log.Info("nas sync done", "files", len(pending))
	return nil
}

// Connect dials the NAS and mounts the share.
func Connect(ctx context.Context, cfg config.NAS) (*smb2.Share, func(), error) {
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(dctx, "tcp", address(cfg.Host))
	if err != nil {
		return nil, nil, err
	}
	d := &smb2.Dialer{Initiator: &smb2.NTLMInitiator{User: cfg.User, Password: cfg.Password, Domain: cfg.Domain}}
	sess, err := d.DialConn(dctx, &deadlineConn{Conn: conn}, address(cfg.Host))
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("smb login: %w", err)
	}
	share, err := sess.Mount(cfg.Share)
	if err != nil {
		sess.Logoff()
		conn.Close()
		return nil, nil, fmt.Errorf("smb share %q: %w", cfg.Share, err)
	}
	return share, func() {
		share.Umount()
		sess.Logoff()
		conn.Close()
	}, nil
}

// Test checks the settings by connecting and writing a small file.
func Test(ctx context.Context, cfg config.NAS) error {
	share, closeFn, err := Connect(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeFn()
	dir := strings.Trim(cfg.Path, "/")
	if dir != "" {
		if err := share.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	p := path.Join(dir, ".squirreld-test")
	if err := share.WriteFile(p, []byte("ok\n"), 0o644); err != nil {
		return fmt.Errorf("writing: %w", err)
	}
	return share.Remove(p)
}

func (s *Syncer) upload(share *smb2.Share, base string, f archive.File, made map[string]bool) error {
	local, err := os.Open(filepath.Join(s.Index.Root(), filepath.FromSlash(f.Rel)))
	if err != nil {
		return err
	}
	defer local.Close()
	fi, err := local.Stat()
	if err != nil {
		return err
	}
	remote := path.Join(strings.Trim(base, "/"), f.Rel)
	dir := path.Dir(remote)
	if !made[dir] {
		if err := share.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		made[dir] = true
	}
	if rfi, err := share.Stat(remote); err == nil && rfi.Size() == fi.Size() {
		return nil // already there, e.g. from an earlier interrupted run
	}

	partial := remote + ".partial"
	out, err := share.Create(partial)
	if err != nil {
		return err
	}
	pr := &progressReader{r: local, start: time.Now(), report: func(done int64, rate float64) {
		s.setStatus(func(st *Status) {
			st.Current, st.CurrentDone, st.CurrentSize, st.RateBps = f.Rel, done, fi.Size(), rate
		})
	}}
	_, err = io.Copy(out, pr)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		share.Remove(partial)
		return err
	}
	if rfi, err := share.Stat(partial); err != nil || rfi.Size() != fi.Size() {
		share.Remove(partial)
		return fmt.Errorf("size check failed after upload")
	}
	share.Remove(remote)
	return share.Rename(partial, remote)
}

type progressReader struct {
	r      io.Reader
	n      int64
	start  time.Time
	last   time.Time
	report func(done int64, rate float64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.n += int64(n)
	if now := time.Now(); now.Sub(p.last) > 500*time.Millisecond || err == io.EOF {
		p.last = now
		rate := float64(p.n) / max(now.Sub(p.start).Seconds(), 0.001)
		p.report(p.n, rate)
	}
	return n, err
}

// deadlineConn gives every read and write its own deadline, so a vanished
// server turns into an error instead of a hang.
type deadlineConn struct{ net.Conn }

func (c *deadlineConn) Read(b []byte) (int, error) {
	c.Conn.SetReadDeadline(time.Now().Add(ioTimeout))
	return c.Conn.Read(b)
}

func (c *deadlineConn) Write(b []byte) (int, error) {
	c.Conn.SetWriteDeadline(time.Now().Add(ioTimeout))
	return c.Conn.Write(b)
}
