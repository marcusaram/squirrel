// Package logbuf keeps the most recent log records in memory for the web UI.
package logbuf

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

type Record struct {
	Time  time.Time `json:"time"`
	Level string    `json:"level"`
	Msg   string    `json:"msg"`
}

type Buffer struct {
	mu   sync.Mutex
	recs []Record
	next int
	full bool
}

func New(n int) *Buffer { return &Buffer{recs: make([]Record, n)} }

func (b *Buffer) add(r Record) {
	b.mu.Lock()
	b.recs[b.next] = r
	b.next = (b.next + 1) % len(b.recs)
	if b.next == 0 {
		b.full = true
	}
	b.mu.Unlock()
}

// Records returns the buffered records, oldest first.
func (b *Buffer) Records() []Record {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.full {
		return append([]Record(nil), b.recs[:b.next]...)
	}
	return append(append([]Record(nil), b.recs[b.next:]...), b.recs[:b.next]...)
}

// Handler returns a slog.Handler that records into b.
func (b *Buffer) Handler(level slog.Leveler) slog.Handler {
	return &handler{buf: b, level: level}
}

type handler struct {
	buf    *Buffer
	level  slog.Leveler
	attrs  []slog.Attr
	prefix string
}

func (h *handler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level.Level() }

func (h *handler) Handle(_ context.Context, r slog.Record) error {
	var sb strings.Builder
	sb.WriteString(r.Message)
	write := func(a slog.Attr) bool {
		fmt.Fprintf(&sb, " %s%s=%v", h.prefix, a.Key, a.Value)
		return true
	}
	for _, a := range h.attrs {
		write(a)
	}
	r.Attrs(write)
	h.buf.add(Record{Time: r.Time, Level: r.Level.String(), Msg: sb.String()})
	return nil
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	c.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &c
}

func (h *handler) WithGroup(name string) slog.Handler {
	c := *h
	c.prefix = h.prefix + name + "."
	return &c
}
