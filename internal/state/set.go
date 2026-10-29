// Package state keeps small persistent sets of archive paths: clips that
// must not be extracted again, and clips already uploaded to the NAS.
//
// The file is append-only with one path per line and is rewritten when
// entries are dropped. A torn last line after a power cut only leaves a
// key that matches no real path.
package state

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Set struct {
	mu   sync.Mutex
	path string
	m    map[string]struct{}
	f    *os.File
}

func Open(path string) (*Set, error) {
	s := &Set{path: path, m: map[string]struct{}{}}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if f, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "-"):
				delete(s.m, line[1:])
			case line != "":
				s.m[line] = struct{}{}
			}
		}
		f.Close()
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	// Rewrite on open to compact the removal lines.
	if err := s.rewrite(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Set) Has(k string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.m[k]
	return ok
}

func (s *Set) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}

func (s *Set) Add(k string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[k]; ok {
		return nil
	}
	s.m[k] = struct{}{}
	return s.appendLine(k)
}

func (s *Set) Remove(k string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[k]; !ok {
		return nil
	}
	delete(s.m, k)
	return s.appendLine("-" + k)
}

// Retain drops every key for which keep returns false.
func (s *Set) Retain(keep func(string) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for k := range s.m {
		if !keep(k) {
			delete(s.m, k)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.rewrite()
}

func (s *Set) appendLine(line string) error {
	if _, err := s.f.WriteString(line + "\n"); err != nil {
		return err
	}
	return s.f.Sync()
}

func (s *Set) rewrite() error {
	tmp := s.path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for k := range s.m {
		w.WriteString(k + "\n")
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	f.Close()
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	if s.f != nil {
		s.f.Close()
	}
	s.f, err = os.OpenFile(s.path, os.O_WRONLY|os.O_APPEND, 0o644)
	return err
}
