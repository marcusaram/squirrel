package archive

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marcusaram/squirrel/internal/state"
	"github.com/marcusaram/squirrel/internal/sysinfo"
)

// ImportedMarker is written next to a teslausb snapshot once its clips are
// in the archive, which makes the snapshot safe to delete.
const ImportedMarker = ".squirreld-imported"

// legacyImportedMarker is the same marker from before squirreld was
// renamed (teslausbd).
const legacyImportedMarker = ".teslausbd-imported"

// SpaceManager keeps free space on the backing filesystem above a reserve.
//
// This matters for more than the archive: the car's image shares blocks
// with archived clips, so every block the car overwrites needs a fresh
// block. If the filesystem fills up, the car's writes fail.
//
// Archived clips often share all their blocks with the car's image or with
// old teslausb snapshots. Deleting such a clip frees nothing, so the
// manager only deletes what actually gives space back.
type SpaceManager struct {
	Index       *Index
	Tomb        *state.Set
	Reserve     func() uint64
	SnapshotDir string // teslausb's snapshots, deleted first once imported
	Log         *slog.Logger

	mu     sync.Mutex
	status SpaceStatus
	// After a pass that found nothing to delete, the next one waits a
	// while: each reads the block map of every kept clip.
	idleUntil time.Time

	// Hold stops pruning, while computer mode shows the kept clips.
	Hold atomic.Bool
}

const noSpaceWarning = "little free space, and the kept clips still share their space with the USB drive or old snapshots; deleting them would not free anything"

type SpaceStatus struct {
	Reserve     uint64    `json:"reserve"`
	LastPrune   time.Time `json:"last_prune,omitzero"`
	PrunedTotal int       `json:"pruned_groups"`
	Warning     string    `json:"warning,omitempty"`
}

func (m *SpaceManager) Status() SpaceStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

func (m *SpaceManager) Run(ctx context.Context) {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		m.Check()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Check frees space when it is below the reserve, aiming for the reserve
// plus some headroom. Space freed by XFS can show up with a delay, so each
// call does one bounded step and the next tick looks again.
func (m *SpaceManager) Check() {
	if m.Hold.Load() {
		return
	}
	reserve := m.Reserve()
	d, err := sysinfo.DiskUsage(m.Index.Root())
	if err != nil {
		m.Log.Error("statfs", "err", err)
		return
	}
	if d.Free < reserve && time.Now().Before(m.idleUntil) {
		return // the warning from the last pass stays
	}
	m.setStatus(func(s *SpaceStatus) { s.Reserve = reserve; s.Warning = "" })
	if d.Free >= reserve {
		return
	}
	if m.pruneSnapshot() {
		return
	}

	need := int64(reserve + reserve/5 - d.Free)
	var freed int64
	deleted, examined := 0, 0
	for _, g := range m.Index.PruneOrder() {
		if freed >= need || examined >= 2000 {
			break
		}
		examined++
		r := m.Index.Reclaimable(g)
		if r < 1<<20 {
			continue // its blocks are still in use elsewhere
		}
		if _, err := m.Index.DeleteGroup(g.ID, m.Tomb); err != nil {
			m.Log.Error("prune", "group", g.ID, "err", err)
			continue
		}
		deleted++
		freed += r
		m.Log.Info("pruned for space", "group", g.ID, "freed", r, "uploaded", g.Uploaded == g.Files)
	}
	if deleted > 0 {
		m.setStatus(func(s *SpaceStatus) { s.LastPrune = time.Now(); s.PrunedTotal += deleted })
		return
	}
	m.idleUntil = time.Now().Add(5 * time.Minute)
	m.Log.Warn("low on space and nothing left whose deletion frees space", "free", d.Free, "reserve", reserve)
	m.setStatus(func(s *SpaceStatus) { s.Warning = noSpaceWarning })
}

func (m *SpaceManager) setStatus(fn func(*SpaceStatus)) {
	m.mu.Lock()
	fn(&m.status)
	m.mu.Unlock()
}

// pruneSnapshot deletes the oldest teslausb snapshot whose clips were
// imported. Snapshots that were never imported are left alone.
func (m *SpaceManager) pruneSnapshot() bool {
	if m.SnapshotDir == "" {
		return false
	}
	marks, _ := filepath.Glob(filepath.Join(m.SnapshotDir, "snap-*", ImportedMarker))
	old, _ := filepath.Glob(filepath.Join(m.SnapshotDir, "snap-*", legacyImportedMarker))
	marks = append(marks, old...)
	if len(marks) == 0 {
		return false
	}
	sort.Strings(marks)
	dir := filepath.Dir(marks[0])
	if err := os.RemoveAll(dir); err != nil {
		m.Log.Error("removing imported teslausb snapshot", "dir", dir, "err", err)
		return false
	}
	m.Log.Info("removed imported teslausb snapshot for space", "dir", dir)
	return true
}
