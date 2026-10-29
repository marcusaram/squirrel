// Command squirreld turns a Raspberry Pi (or another board with a USB
// device controller) into the car's dashcam drive, preserves every clip the
// car writes, uploads events to a NAS and serves a web UI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/marcusaram/squirrel/internal/archive"
	"github.com/marcusaram/squirrel/internal/clock"
	"github.com/marcusaram/squirrel/internal/config"
	"github.com/marcusaram/squirrel/internal/diag"
	"github.com/marcusaram/squirrel/internal/driveimage"
	"github.com/marcusaram/squirrel/internal/fatfs"
	"github.com/marcusaram/squirrel/internal/gadget"
	"github.com/marcusaram/squirrel/internal/led"
	"github.com/marcusaram/squirrel/internal/logbuf"
	"github.com/marcusaram/squirrel/internal/nas"
	"github.com/marcusaram/squirrel/internal/preserve"
	"github.com/marcusaram/squirrel/internal/state"
	"github.com/marcusaram/squirrel/internal/sysinfo"
	"github.com/marcusaram/squirrel/internal/web"
	"github.com/marcusaram/squirrel/internal/wifi"
)

var version = "dev"

func usage() {
	fmt.Fprintf(os.Stderr, `squirreld %s

Usage:
  squirreld [run] [-config FILE]       start the daemon
  squirreld inspect IMAGE              list the clips in a drive image (read-only)
  squirreld import [-config FILE] IMAGE...
                                       preserve the clips in old images or teslausb
                                       snapshots (stop the daemon first)
  squirreld create-image -size 64G|40%% [-fs exfat|fat32] [-label CAM] IMAGE
  squirreld setup-storage [-yes] [-root-size 8G]
                                       create and mount the XFS partition for /backingfiles
  squirreld setup-storage -usb [-yes] | -usb-device /dev/sdX
                                       the same on an external USB drive
  squirreld put-file IMAGE NAME FILE   copy a file into the root of a drive image
  squirreld version
`, version)
}

func main() {
	args := os.Args[1:]
	cmd := "run"
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "run":
		err = run(args)
	case "inspect":
		err = inspect(args)
	case "import":
		err = importImages(args)
	case "create-image":
		err = createImage(args)
	case "setup-storage":
		err = setupStorage(args)
	case "put-file":
		err = putFile(args)
	case "version":
		fmt.Println(version)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "squirreld:", err)
		os.Exit(1)
	}
}

type daemon struct {
	cfg     *config.Store
	gadget  *gadget.Gadget
	index   *archive.Index
	pres    *preserve.Preserver
	space   *archive.SpaceManager
	nas     *nas.Syncer
	wifi    *wifi.Manager
	led     *led.LED
	log     *slog.Logger
	started time.Time

	repairing  atomic.Bool
	mu         sync.Mutex
	gadgetErr  string
	activity   sysinfo.ActivitySnapshot
	activityMu sync.Mutex
	act        sysinfo.Activity
	computer   computer
}

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultPath, "config file")
	debug := fs.Bool("debug", false, "verbose logging")
	webDir := fs.String("web-dir", "", "serve the web UI from this directory (development)")
	fs.Parse(args)

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	logs := logbuf.New(500)
	log := slog.New(slog.NewMultiHandler(
		slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}),
		logs.Handler(slog.LevelInfo),
	))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ld := led.Find("/sys")
	if !waitForData(ctx, *cfgPath, ld, log) {
		return nil
	}
	store, err := config.Load(*cfgPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	cfg := store.Get()
	d := &daemon{cfg: store, log: log, started: time.Now(), led: ld}

	// The car is waiting for its drive: expose it before anything else.
	var luns []gadget.Lun
	for _, dr := range cfg.Drives {
		luns = append(luns, gadget.Lun{Name: dr.Name, Image: dr.Image})
	}
	d.gadget = gadget.New(luns, cfg.Gadget.UDC, cfg.Gadget.MaxPowerMA)
	// The car only records to a drive with a TeslaCam folder. Add it
	// before the car gets the drive; never while it is connected. The same
	// moment is the only safe one to put the diagnostics report on it.
	statusPath := filepath.Join(cfg.StateDir, "status.txt")
	journalStore := filepath.Join(cfg.StateDir, "journal")
	clockPath := filepath.Join(cfg.StateDir, "clock")
	clock.Restore(clockPath, log)
	diag.Journal(cfg.Diagnostics, journalStore)
	if cam := cfg.CamImage(); !d.gadget.Status().Bound && cfg.Diagnostics {
		if err := driveimage.WriteFile(cam, diag.CamFile, diag.ForCam(statusPath)); err != nil {
			log.Error("writing diagnostics to the drive", "err", err)
		}
	}
	if cam := cfg.CamImage(); !d.gadget.Status().Bound {
		if ok, err := driveimage.HasTeslaCam(cam); err == nil && !ok {
			if err := driveimage.EnsureTeslaCam(cam); err != nil {
				log.Error("adding TeslaCam folder", "err", err)
			} else {
				log.Info("added the TeslaCam folder to the drive")
			}
		}
	}
	d.ensureGadget()
	log.Info("starting", "version", version, "uptime", sysinfo.Read().Uptime, "gadget", d.gadget.Status().State)

	tomb, err := state.Open(cfg.StateDir + "/tombstones")
	if err != nil {
		return err
	}
	uploaded, err := state.Open(cfg.StateDir + "/uploaded")
	if err != nil {
		return err
	}
	d.index = archive.NewIndex(cfg.ArchiveDir, uploaded)
	d.pres = preserve.New(store.Get, d.index, tomb, log.With("part", "preserve"))
	d.space = &archive.SpaceManager{
		Index:       d.index,
		Tomb:        tomb,
		Reserve:     func() uint64 { return uint64(store.Get().ReserveGB) << 30 },
		SnapshotDir: filepath.Join(filepath.Dir(cfg.CamImage()), "snapshots"),
		Log:         log.With("part", "space"),
	}
	d.nas = nas.New(store.Get, d.index, log.With("part", "nas"))
	d.wifi = wifi.New(func() config.WiFi { return store.Get().WiFi }, log.With("part", "wifi"))

	srv := &web.Server{Config: store, Index: d.index, Tomb: tomb, Logs: logs, Log: log.With("part", "web"), Backend: d, WiFi: d.wifi, Version: version, StaticDir: *webDir}
	httpSrv := &http.Server{Addr: cfg.Web.Listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("web server", "err", err)
		}
	}()

	if err := d.index.Load(log); err != nil {
		log.Error("loading archive", "err", err)
	}
	d.warnAboutSpace()

	var wg sync.WaitGroup
	report := &diag.Writer{Path: statusPath, Report: func() string { return d.diagReport(logs) },
		Enabled: func() bool { return store.Get().Diagnostics }}
	store.OnChange(func(old, new config.Config) {
		if old.Diagnostics != new.Diagnostics {
			diag.Journal(new.Diagnostics, journalStore)
		}
	})
	for _, fn := range []func(context.Context){d.pres.Run, d.space.Run, d.nas.Run, d.wifi.Loop, d.watch, report.Run, d.trim,
		func(ctx context.Context) { clock.Keep(ctx, clockPath) }} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn(ctx)
		}()
	}
	<-ctx.Done()
	d.stopComputer(context.Background())
	log.Info("shutting down; the drive stays connected to the car")
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpSrv.Shutdown(sctx)
	wg.Wait()
	return nil
}

func (d *daemon) ensureGadget() {
	err := d.gadget.Ensure()
	d.mu.Lock()
	defer d.mu.Unlock()
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if msg != d.gadgetErr {
		if err != nil {
			d.log.Error("usb gadget", "err", err)
		} else {
			d.log.Info("usb gadget ready")
		}
	}
	d.gadgetErr = msg
}

// watch keeps the gadget bound and samples the car's I/O rate.
func (d *daemon) watch(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for n := 0; ; n++ {
		if n%5 == 0 && !d.repairing.Load() {
			d.ensureGadget()
		}
		a := d.act.Sample()
		d.activityMu.Lock()
		d.activity = a
		d.activityMu.Unlock()
		d.led.Set(d.ledPattern())
		d.led.Load(sysinfo.LoadAverage())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ledPattern sums up the state for the activity LED.
func (d *daemon) ledPattern() led.Pattern {
	d.mu.Lock()
	gadgetErr := d.gadgetErr
	d.mu.Unlock()
	switch {
	case gadgetErr != "":
		return led.Problem
	case d.repairing.Load() || d.nas.Status().State == "uploading":
		return led.Busy
	case d.gadget.Status().State == "configured":
		return led.Connected
	}
	return led.Waiting
}

// diagReport is what goes into status.txt on the boot partition.
func (d *daemon) diagReport(logs *logbuf.Buffer) string {
	var b strings.Builder
	g := d.gadget.Status()
	d.mu.Lock()
	gerr := d.gadgetErr
	d.mu.Unlock()
	fmt.Fprintf(&b, "USB drive: bound=%v state=%q %s\n", g.Bound, g.State, gerr)
	w := d.wifi.Status()
	fmt.Fprintf(&b, "Wifi: mode=%s ssid=%q clients=%d %s\n", w.Mode, w.SSID, w.Clients, w.Error)
	if sys := sysinfo.Read(); len(sys.Addresses) > 0 {
		fmt.Fprintf(&b, "Addresses: %s\n", strings.Join(sys.Addresses, ", "))
	}
	fmt.Fprintf(&b, "\nnmcli device:\n%s\n", diag.Command("nmcli", "-f", "DEVICE,TYPE,STATE,CONNECTION", "device"))
	fmt.Fprintf(&b, "\nnmcli connections:\n%s\n", diag.Command("nmcli", "-f", "NAME,TYPE,AUTOCONNECT,ACTIVE", "connection", "show"))
	fmt.Fprintf(&b, "\nwifi in reach:\n%s\n", diag.Command("nmcli", "-f", "SSID,CHAN,SIGNAL,SECURITY", "device", "wifi", "list", "--rescan", "no"))
	fmt.Fprintf(&b, "\nrfkill:\n%s\n", diag.Command("rfkill", "list"))
	// Why a known network was not joined (wrong password, not found, ...).
	fmt.Fprintf(&b, "\nNetworkManager log:\n%s\n", diag.Command("journalctl", "-u", "NetworkManager", "-n", "40", "--no-pager", "-o", "short"))
	b.WriteString("\nrecent warnings and errors:\n")
	for _, r := range logs.Records() {
		if r.Level != "INFO" && r.Level != "DEBUG" {
			fmt.Fprintf(&b, "%s %s %s\n", r.Time.Format("15:04:05"), r.Level, r.Msg)
		}
	}
	return b.String()
}

func (d *daemon) warnAboutSpace() {
	fi, err := os.Stat(d.cfg.Get().CamImage())
	if err != nil {
		return
	}
	disk, err := sysinfo.DiskUsage(d.cfg.Get().ArchiveDir)
	if err != nil {
		return
	}
	if uint64(fi.Size())+uint64(d.cfg.Get().ReserveGB)<<30 > disk.Total {
		d.log.Warn("cam image plus reserve is larger than the disk; the car's writes may fail when it fills up",
			"image", fi.Size(), "disk", disk.Total)
	}
}

type imageInfo struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Allocated int64  `json:"allocated"`
}

// Status implements web.Backend.
func (d *daemon) Status() any {
	cfg := d.cfg.Get()
	disk, _ := sysinfo.DiskUsage(cfg.ArchiveDir)
	var img imageInfo
	if fi, err := os.Stat(cfg.CamImage()); err == nil {
		img = imageInfo{Path: cfg.CamImage(), Size: fi.Size(), Allocated: allocated(fi)}
	}
	d.mu.Lock()
	gerr := d.gadgetErr
	d.mu.Unlock()
	d.activityMu.Lock()
	act := d.activity
	d.activityMu.Unlock()

	pres := d.pres.Status()
	space := d.space.Status()
	var warnings []string
	if gerr != "" {
		warnings = append(warnings, "USB: "+gerr)
	}
	if pres.LastError != "" && time.Since(pres.LastErrorAt) < 10*time.Minute {
		warnings = append(warnings, "Keeping clips: "+pres.LastError)
	}
	// No warning for exFAT's "not closed cleanly" flag: whoever has the
	// drive sets it, and the car never clears it, as it only ever loses
	// power. Real trouble reading the drive shows as a scan error above.
	if pres.BytesCopied > 0 && pres.BytesShared == 0 {
		warnings = append(warnings, "Clips are copied instead of reflinked; use XFS for /backingfiles to save space and card wear.")
	}
	if space.Warning != "" {
		warnings = append(warnings, "Storage: "+space.Warning)
	}
	return map[string]any{
		"version":   version,
		"time":      time.Now(),
		"started":   d.started,
		"repairing": d.repairing.Load(),
		"gadget":    d.gadget.Status(),
		"activity":  act,
		"disk":      disk,
		"image":     img,
		"preserve":  pres,
		"space":     space,
		"archive":   d.index.Stats(),
		"nas":       d.nas.Status(),
		"wifi":      d.wifi.Status(),
		"system":    sysinfo.Read(),
		"warnings":  warnings,
		"computer":  d.computer.get(),
	}
}

func (d *daemon) Scan()    { d.pres.Trigger() }
func (d *daemon) SyncNAS() { d.nas.Trigger() }

// Repair disconnects the drive from the car, runs fsck on the cam image and
// reconnects it.
func (d *daemon) Repair(ctx context.Context) (string, error) {
	if d.computerOn() {
		return "", errors.New("switch back to car mode first")
	}
	if !d.repairing.CompareAndSwap(false, true) {
		return "", errors.New("a repair is already running")
	}
	defer d.repairing.Store(false)
	d.pres.Pause(true)
	defer d.pres.Trigger()
	defer d.pres.Pause(false)

	image := d.cfg.Get().CamImage()
	f, err := os.Open(image)
	if err != nil {
		return "", err
	}
	fi, _ := f.Stat()
	v, err := fatfs.Open(f, fi.Size())
	f.Close()
	if err != nil {
		return "", err
	}
	d.log.Warn("repair: disconnecting drive from the car")
	if err := d.gadget.Unbind(); err != nil {
		return "", fmt.Errorf("disconnecting failed, repair cancelled: %w", err)
	}
	defer func() {
		if err := d.gadget.Bind(); err != nil {
			d.log.Error("repair: reconnecting", "err", err)
		}
		d.log.Info("repair: drive reconnected")
	}()
	time.Sleep(2 * time.Second)
	// Not the request's context: killing fsck halfway because the browser
	// went away would do more harm than the repair fixes.
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
	defer cancel()
	out, err := gadget.Repair(rctx, image, v.Offset, v.Type)
	d.log.Info("repair finished", "err", err)
	return out, err
}

// ResizeCam replaces the car's drive with a new, empty one of pct percent
// of the disk; exFAT cannot be resized in place. Everything on the old
// drive is preserved into the archive first.
func (d *daemon) ResizeCam(ctx context.Context, pct int) (string, error) {
	if pct < 10 || pct > 70 {
		return "", errors.New("choose between 10 and 70%")
	}
	if d.computerOn() {
		return "", errors.New("switch back to car mode first")
	}
	if !d.repairing.CompareAndSwap(false, true) {
		return "", errors.New("a maintenance task is already running")
	}
	defer d.repairing.Store(false)
	d.pres.Pause(true)
	defer d.pres.Trigger()
	defer d.pres.Pause(false)

	cam := d.cfg.Get().CamImage()
	size, err := driveimage.PercentOf(filepath.Dir(cam), pct)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	d.log.Warn("resize: disconnecting drive from the car", "percent", pct)
	if err := d.gadget.Unbind(); err != nil {
		return "", fmt.Errorf("disconnecting failed: %w", err)
	}
	defer func() {
		if err := d.gadget.Bind(); err != nil {
			d.log.Error("resize: reconnecting", "err", err)
		}
	}()
	time.Sleep(2 * time.Second)

	// Nothing writes to the drive now, so every finished clip can be
	// taken without waiting for it to look stable.
	n, skipped, err := d.pres.Import(cam)
	if err != nil {
		return out.String(), fmt.Errorf("keeping the clips failed; the drive was not changed: %w", err)
	}
	fmt.Fprintf(&out, "%d clips kept (%d incomplete recordings skipped)\n", n, skipped)

	tmp := cam + ".new"
	os.Remove(tmp)
	if err := driveimage.Create(tmp, size, "exfat", "CAM", &out); err != nil {
		return out.String(), fmt.Errorf("making the new drive failed; the old one stays: %w", err)
	}
	if err := os.Rename(tmp, cam); err != nil {
		os.Remove(tmp)
		return out.String(), err
	}
	// The drive still has the old file open; give it the new one.
	if err := d.gadget.Reopen(); err != nil {
		d.log.Error("resize: opening the new drive", "err", err)
		return out.String(), fmt.Errorf("the new drive is ready, but the car still sees the old one; restart the Pi: %w", err)
	}
	d.log.Info("resize: new drive for the car", "bytes", size)
	fmt.Fprintf(&out, "new USB drive of %d GB\n", size>>30)
	return out.String(), nil
}
