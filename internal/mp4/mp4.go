// Package mp4 does the little MP4 box parsing squirreld needs: checking that
// a recording is finished, and finding the 'ctts' boxes that keep Chromium
// based browsers from playing Tesla recordings.
package mp4

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

type box struct {
	typ        string
	off, size  int64
	headerSize int64
}

var ErrInvalid = errors.New("mp4: invalid structure")

func readBoxes(r io.ReaderAt, start, end int64) ([]box, error) {
	var out []box
	var hdr [16]byte
	for off := start; off < end; {
		if end-off < 8 {
			return out, fmt.Errorf("%w: %d trailing bytes", ErrInvalid, end-off)
		}
		if _, err := r.ReadAt(hdr[:8], off); err != nil {
			return out, err
		}
		b := box{typ: string(hdr[4:8]), off: off, headerSize: 8}
		switch size := binary.BigEndian.Uint32(hdr[:4]); size {
		case 0:
			b.size = end - off
		case 1:
			if _, err := r.ReadAt(hdr[8:16], off+8); err != nil {
				return out, err
			}
			b.size = int64(binary.BigEndian.Uint64(hdr[8:16]))
			b.headerSize = 16
		default:
			b.size = int64(size)
		}
		if b.size < b.headerSize || off+b.size > end {
			return out, fmt.Errorf("%w: box %q at %d has size %d", ErrInvalid, b.typ, off, b.size)
		}
		out = append(out, b)
		off += b.size
		if len(out) > 10000 {
			return out, fmt.Errorf("%w: too many boxes", ErrInvalid)
		}
	}
	return out, nil
}

// Validate checks that a file is a complete MP4: the top level boxes cover
// the file exactly, it starts with 'ftyp' and contains 'moov' and 'mdat'.
// A recording that is still being written fails this check.
func Validate(r io.ReaderAt, size int64) error {
	boxes, err := readBoxes(r, 0, size)
	if err != nil {
		return err
	}
	if len(boxes) == 0 || boxes[0].typ != "ftyp" {
		return fmt.Errorf("%w: no ftyp", ErrInvalid)
	}
	var moov, mdat bool
	for _, b := range boxes {
		moov = moov || b.typ == "moov"
		mdat = mdat || b.typ == "mdat"
	}
	if !moov || !mdat {
		return fmt.Errorf("%w: missing moov or mdat", ErrInvalid)
	}
	return nil
}

// Length reads the top level boxes from the start of r, which may go on
// past the file, and returns where the file ends: after its 'moov' and
// 'mdat'. It finds the size of a recording whose directory entry is gone.
func Length(r io.ReaderAt, limit int64) (int64, error) {
	var off int64
	var moov, mdat bool
	for i := 0; i < 16 && off < limit; i++ {
		var h [16]byte
		if _, err := r.ReadAt(h[:8], off); err != nil {
			return 0, err
		}
		size, typ := int64(binary.BigEndian.Uint32(h[:])), string(h[4:8])
		if size == 1 {
			if _, err := r.ReadAt(h[8:16], off+8); err != nil {
				return 0, err
			}
			size = int64(binary.BigEndian.Uint64(h[8:]))
		}
		if size < 8 || off+size > limit || (i == 0) != (typ == "ftyp") {
			return 0, fmt.Errorf("%w: box %q at %d", ErrInvalid, typ, off)
		}
		off += size
		moov = moov || typ == "moov"
		mdat = mdat || typ == "mdat"
		if moov && mdat {
			return off, nil
		}
	}
	return 0, fmt.Errorf("%w: no moov and mdat", ErrInvalid)
}

// CheckTrack checks that a recording's video track is whole: its sample
// tables add up and every chunk lies in the data.
func CheckTrack(r io.ReaderAt, size int64) error {
	_, err := parseTrack(r, size)
	return err
}

// CTTSOffsets returns the file offsets of the type field of every 'ctts'
// box in moov/trak/mdia/minf/stbl.
func CTTSOffsets(r io.ReaderAt, size int64) ([]int64, error) {
	var out []int64
	var walk func(start, end int64, path []string) error
	walk = func(start, end int64, path []string) error {
		boxes, err := readBoxes(r, start, end)
		if err != nil {
			return err
		}
		want := path[0]
		for _, b := range boxes {
			if b.typ != want {
				continue
			}
			if len(path) == 1 {
				out = append(out, b.off+4)
				continue
			}
			if err := walk(b.off+b.headerSize, b.off+b.size, path[1:]); err != nil {
				return err
			}
		}
		return nil
	}
	err := walk(0, size, []string{"moov", "trak", "mdia", "minf", "stbl", "ctts"})
	return out, err
}

// PatchedReader serves a file with every 'ctts' box renamed to 'free',
// which players skip.
type PatchedReader struct {
	io.ReadSeeker
	offsets []int64
	pos     int64
}

func NewPatchedReader(rs io.ReadSeeker, offsets []int64) *PatchedReader {
	return &PatchedReader{ReadSeeker: rs, offsets: offsets}
}

func (p *PatchedReader) Seek(offset int64, whence int) (int64, error) {
	n, err := p.ReadSeeker.Seek(offset, whence)
	p.pos = n
	return n, err
}

func (p *PatchedReader) Read(b []byte) (int, error) {
	n, err := p.ReadSeeker.Read(b)
	for _, off := range p.offsets {
		for i := int64(0); i < 4; i++ {
			if at := off + i - p.pos; at >= 0 && at < int64(n) {
				b[at] = "free"[i]
			}
		}
	}
	p.pos += int64(n)
	return n, err
}
