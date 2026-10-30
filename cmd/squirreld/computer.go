package main

import (
	"context"
	"errors"
	"sync"

	"github.com/marcusaram/squirrel/internal/exfatgen"
	"github.com/marcusaram/squirrel/internal/gadget"
	"github.com/marcusaram/squirrel/internal/nbd"
	"github.com/marcusaram/squirrel/internal/usbview"
)

// Computer mode: plugged into a computer instead of the car, the Pi is a
// read-only USB drive with the kept clips, every camera of a drive or event
// as one long video. The car's drive is disconnected meanwhile, so the
// computer cannot put its own files on it; a restart is always car mode.

type computerStatus struct {
	State string `json:"state"` // off, building, on
	Done  int    `json:"done,omitempty"`
	Total int    `json:"total,omitempty"`
	Files int    `json:"files,omitempty"`
	Size  int64  `json:"size,omitempty"`
	Error string `json:"error,omitempty"`
}

type computer struct {
	mu     sync.Mutex
	status computerStatus
	dev    *nbd.Device
	longs  *usbview.Longs
}

func (c *computer) get() computerStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.status.State == "" {
		return computerStatus{State: "off", Error: c.status.Error}
	}
	return c.status
}

func (c *computer) set(fn func(*computerStatus)) {
	c.mu.Lock()
	fn(&c.status)
	c.mu.Unlock()
}

// ComputerMode implements web.Backend: it starts building the drive and
// returns; the status shows the progress.
func (d *daemon) ComputerMode(on bool, lang, unit string) error {
	if !on {
		return d.carMode()
	}
	c := &d.computer
	c.mu.Lock()
	if c.status.State == "building" || c.status.State == "on" {
		c.mu.Unlock()
		return nil
	}
	if !d.repairing.CompareAndSwap(false, true) {
		c.mu.Unlock()
		return errors.New("a maintenance task is running; try again when it is done")
	}
	c.status = computerStatus{State: "building"}
	c.mu.Unlock()
	go func() {
		defer d.repairing.Store(false)
		if err := d.buildComputer(lang, unit); err != nil {
			d.log.Error("computer mode", "err", err)
			c.set(func(s *computerStatus) { *s = computerStatus{State: "off", Error: err.Error()} })
			d.space.Hold.Store(false)
		}
	}()
	return nil
}

func (d *daemon) buildComputer(lang, unit string) (err error) {
	c := &d.computer
	// Nothing may disappear while the computer reads.
	d.space.Hold.Store(true)
	longs := &usbview.Longs{}
	defer func() {
		if err != nil {
			longs.Close() // the recordings it opened
		}
	}()
	root, files, err := usbview.Tree(d.index, lang, unit, longs, func(done, total int) {
		c.set(func(s *computerStatus) { s.Done, s.Total = done, total })
	})
	if err != nil {
		return err
	}
	vol, err := exfatgen.Build(root, "Squirrel")
	if err != nil {
		return err
	}
	dev, err := nbd.Serve(vol, vol.Size())
	if err != nil {
		return err
	}
	d.log.Warn("computer mode: the car's drive is disconnected", "files", files, "bytes", vol.Size(), "device", dev.Path)
	d.gadget.Unbind()
	d.gadget.SetLuns([]gadget.Lun{{Name: "videos", Image: dev.Path, ReadOnly: true}})
	if err := d.gadget.Ensure(); err != nil {
		d.gadget.SetLuns(d.carLuns())
		d.gadget.Ensure()
		dev.Close()
		return err
	}
	c.set(func(s *computerStatus) {
		*s = computerStatus{State: "on", Files: files, Size: vol.Size()}
	})
	c.mu.Lock()
	c.dev, c.longs = dev, longs
	c.mu.Unlock()
	return nil
}

// carMode gives the drive back to the car.
func (d *daemon) carMode() error {
	c := &d.computer
	c.mu.Lock()
	if c.status.State == "building" {
		c.mu.Unlock()
		return errors.New("computer mode is still being prepared; wait for it to finish")
	}
	dev, longs := c.dev, c.longs
	c.dev, c.longs = nil, nil
	c.status = computerStatus{State: "off"}
	c.mu.Unlock()
	if dev == nil {
		return nil
	}
	d.gadget.Unbind()
	d.gadget.SetLuns(d.carLuns())
	err := d.gadget.Ensure()
	dev.Close()
	longs.Close()
	d.space.Hold.Store(false)
	d.log.Info("car mode: the car's drive is back", "err", err)
	return err
}

func (d *daemon) carLuns() []gadget.Lun {
	var luns []gadget.Lun
	for _, dr := range d.cfg.Get().Drives {
		luns = append(luns, gadget.Lun{Name: dr.Name, Image: dr.Image})
	}
	return luns
}

func (d *daemon) computerOn() bool { return d.computer.get().State != "off" }

// stopComputer is for shutting down: a restarted daemon starts in car mode.
func (d *daemon) stopComputer(ctx context.Context) {
	if d.computer.get().State == "on" {
		d.carMode()
	}
}

// ComputerOn implements web.Backend.
func (d *daemon) ComputerOn() bool { return d.computerOn() }
