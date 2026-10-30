package mp4

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
)

// A Long is one continuous video made of consecutive recordings of one
// camera, without copying: a new header (ftyp, moov, mdat header) describes
// every frame of every minute, and the frame data is read from the original
// files. Players see a single file with no seams between the minutes.
//
// Tesla's 'ctts' boxes are left out, as NewPatchedReader does for single
// clips: they make Chromium based browsers stall.
// LongFormat changes whenever the bytes of a long video change for the
// same recordings (how the header is built); caches and ETags include it.
const LongFormat = 2

type Long struct {
	Head  []byte
	Parts []Part
	Size  int64
	// Segs has one entry per input file: where it starts in the long video
	// and how long it is, in seconds. OK is false for a file that could
	// not be read; it is left out of the video.
	Segs []LongSeg
}

// Part is a range of an original file, in the order it follows Head.
type Part struct {
	Path     string
	Off, Len int64
	subs     *subs // a text part, made from Path's frames when read
}

type LongSeg struct {
	Start float64 `json:"start"`
	Dur   float64 `json:"dur"`
	OK    bool    `json:"ok"`
}

// track is what Concat needs of a recording's video track.
type track struct {
	timescale  uint32
	duration   uint64
	tkhd       []byte // raw boxes, taken over from the first file
	hdlr, vmhd []byte
	dinf       []byte
	entries    [][]byte // sample descriptions (avc1 with avcC)
	stts       [][2]uint32
	stss       []uint32 // nil: every sample is a sync sample
	stsc       [][3]uint32
	sizes      []uint32
	chunks     []uint64
	offs       []uint64 // where each sample is, from stsc and chunks
	descs      []uint32 // each sample's description, 1-based
	dataStart  int64    // the mdat payload the chunks are in
	dataEnd    int64
}

const maxTable = 64 << 20 // bytes of one sample table box

func readBox(r io.ReaderAt, b box) ([]byte, error) {
	n := b.size - b.headerSize
	if n > maxTable {
		return nil, fmt.Errorf("%w: %q box of %d bytes", ErrInvalid, b.typ, n)
	}
	buf := make([]byte, n)
	_, err := r.ReadAt(buf, b.off+b.headerSize)
	return buf, err
}

func readRaw(r io.ReaderAt, b box) ([]byte, error) {
	if b.size > maxTable {
		return nil, fmt.Errorf("%w: %q box of %d bytes", ErrInvalid, b.typ, b.size)
	}
	buf := make([]byte, b.size)
	_, err := r.ReadAt(buf, b.off)
	return buf, err
}

func child(r io.ReaderAt, parent box, typ string) (box, error) {
	kids, err := readBoxes(r, parent.off+parent.headerSize, parent.off+parent.size)
	if err != nil {
		return box{}, err
	}
	for _, k := range kids {
		if k.typ == typ {
			return k, nil
		}
	}
	return box{}, fmt.Errorf("%w: no %q in %q", ErrInvalid, typ, parent.typ)
}

func path(r io.ReaderAt, b box, typs ...string) (box, error) {
	var err error
	for _, t := range typs {
		if b, err = child(r, b, t); err != nil {
			return box{}, err
		}
	}
	return b, nil
}

// u32s reads a full box's table of n-field entries after its count.
func table(p []byte, fields, skip int) ([]uint32, error) {
	if len(p) < 8+skip {
		return nil, ErrInvalid
	}
	n := int(binary.BigEndian.Uint32(p[4+skip:]))
	p = p[8+skip:]
	if n < 0 || len(p) < n*fields*4 {
		return nil, fmt.Errorf("%w: table of %d entries in %d bytes", ErrInvalid, n, len(p))
	}
	out := make([]uint32, n*fields)
	for i := range out {
		out[i] = binary.BigEndian.Uint32(p[i*4:])
	}
	return out, nil
}

func parseTrack(r io.ReaderAt, size int64) (*track, error) {
	top, err := readBoxes(r, 0, size)
	if err != nil {
		return nil, err
	}
	var moov box
	var mdats []box
	for _, b := range top {
		switch b.typ {
		case "moov":
			moov = b
		case "mdat":
			mdats = append(mdats, b)
		}
	}
	if moov.typ == "" || len(mdats) == 0 {
		return nil, fmt.Errorf("%w: missing moov or mdat", ErrInvalid)
	}
	traks, err := readBoxes(r, moov.off+moov.headerSize, moov.off+moov.size)
	if err != nil {
		return nil, err
	}
	for _, trak := range traks {
		if trak.typ != "trak" {
			continue
		}
		hdlr, err := path(r, trak, "mdia", "hdlr")
		if err != nil {
			return nil, err
		}
		hp, err := readBox(r, hdlr)
		if err != nil {
			return nil, err
		}
		if len(hp) < 12 || string(hp[8:12]) != "vide" {
			continue
		}
		return parseVideo(r, trak, mdats)
	}
	return nil, fmt.Errorf("%w: no video track", ErrInvalid)
}

func parseVideo(r io.ReaderAt, trak box, mdats []box) (*track, error) {
	t := &track{}
	var err error
	raw := func(dst *[]byte, typs ...string) {
		if err != nil {
			return
		}
		var b box
		if b, err = path(r, trak, typs...); err == nil {
			*dst, err = readRaw(r, b)
		}
	}
	raw(&t.tkhd, "tkhd")
	raw(&t.hdlr, "mdia", "hdlr")
	raw(&t.vmhd, "mdia", "minf", "vmhd")
	raw(&t.dinf, "mdia", "minf", "dinf")
	if err != nil {
		return nil, err
	}
	mdhd, err := path(r, trak, "mdia", "mdhd")
	if err != nil {
		return nil, err
	}
	p, err := readBox(r, mdhd)
	if err != nil {
		return nil, err
	}
	switch {
	case len(p) >= 32 && p[0] == 1:
		t.timescale, t.duration = binary.BigEndian.Uint32(p[20:]), binary.BigEndian.Uint64(p[24:])
	case len(p) >= 20:
		t.timescale, t.duration = binary.BigEndian.Uint32(p[12:]), uint64(binary.BigEndian.Uint32(p[16:]))
	default:
		return nil, fmt.Errorf("%w: mdhd", ErrInvalid)
	}
	if t.timescale == 0 {
		return nil, fmt.Errorf("%w: timescale 0", ErrInvalid)
	}

	stbl, err := path(r, trak, "mdia", "minf", "stbl")
	if err != nil {
		return nil, err
	}
	boxes, err := readBoxes(r, stbl.off+stbl.headerSize, stbl.off+stbl.size)
	if err != nil {
		return nil, err
	}
	for _, b := range boxes {
		switch b.typ {
		case "stsd":
			entries, err := readBoxes(r, b.off+b.headerSize+8, b.off+b.size)
			if err != nil {
				return nil, err
			}
			for _, e := range entries {
				data, err := readRaw(r, e)
				if err != nil {
					return nil, err
				}
				t.entries = append(t.entries, withoutBtrt(data))
			}
		case "stts", "stss", "stsc", "stsz", "stco", "co64":
			p, err := readBox(r, b)
			if err != nil {
				return nil, err
			}
			if err := t.table(b.typ, p); err != nil {
				return nil, err
			}
		}
	}
	if len(t.entries) == 0 || len(t.sizes) == 0 || len(t.chunks) == 0 || len(t.stsc) == 0 {
		return nil, fmt.Errorf("%w: incomplete sample table", ErrInvalid)
	}
	var n uint64
	for _, e := range t.stts {
		n += uint64(e[0])
	}
	if n != uint64(len(t.sizes)) {
		return nil, fmt.Errorf("%w: %d samples timed, %d sized", ErrInvalid, n, len(t.sizes))
	}
	if err := t.place(); err != nil {
		return nil, err
	}
	return t, t.locateData(mdats)
}

// place works out every sample's offset and description from the chunk
// tables.
func (t *track) place() error {
	t.offs = make([]uint64, 0, len(t.sizes))
	t.descs = make([]uint32, 0, len(t.sizes))
	for e, run := range t.stsc {
		last := uint32(len(t.chunks))
		if e+1 < len(t.stsc) {
			last = t.stsc[e+1][0] - 1
		}
		if run[0] < 1 || last > uint32(len(t.chunks)) {
			return fmt.Errorf("%w: stsc", ErrInvalid)
		}
		for c := run[0]; c <= last; c++ {
			off := t.chunks[c-1]
			for range run[1] {
				i := len(t.offs)
				if i >= len(t.sizes) {
					return fmt.Errorf("%w: more samples in chunks than sized", ErrInvalid)
				}
				t.offs = append(t.offs, off)
				t.descs = append(t.descs, run[2])
				off += uint64(t.sizes[i])
			}
		}
	}
	if len(t.offs) != len(t.sizes) {
		return fmt.Errorf("%w: %d samples in chunks, %d sized", ErrInvalid, len(t.offs), len(t.sizes))
	}
	return nil
}

// withoutBtrt drops the bitrate box from a visual sample description. It
// differs per minute while the decoder settings do not, and one shared
// description saves Safari a decoder change at every minute.
func withoutBtrt(e []byte) []byte {
	const children = 8 + 78 // box header and VisualSampleEntry fields
	if len(e) < children {
		return e
	}
	out := append([]byte(nil), e[:children]...)
	for p := children; p < len(e); {
		if p+8 > len(e) {
			return e
		}
		n := int(binary.BigEndian.Uint32(e[p:]))
		if n < 8 || p+n > len(e) {
			return e
		}
		if string(e[p+4:p+8]) != "btrt" {
			out = append(out, e[p:p+n]...)
		}
		p += n
	}
	binary.BigEndian.PutUint32(out, uint32(len(out)))
	return out
}

func (t *track) table(typ string, p []byte) error {
	switch typ {
	case "stts":
		v, err := table(p, 2, 0)
		if err != nil {
			return err
		}
		for i := 0; i < len(v); i += 2 {
			t.stts = append(t.stts, [2]uint32{v[i], v[i+1]})
		}
	case "stss":
		v, err := table(p, 1, 0)
		if err != nil {
			return err
		}
		t.stss = v
	case "stsc":
		v, err := table(p, 3, 0)
		if err != nil {
			return err
		}
		for i := 0; i < len(v); i += 3 {
			t.stsc = append(t.stsc, [3]uint32{v[i], v[i+1], v[i+2]})
		}
	case "stsz":
		if len(p) < 12 {
			return ErrInvalid
		}
		fixed := binary.BigEndian.Uint32(p[4:])
		if fixed == 0 {
			v, err := table(p, 1, 4)
			if err != nil {
				return err
			}
			t.sizes = v
			return nil
		}
		n := binary.BigEndian.Uint32(p[8:])
		if n > maxTable/4 {
			return ErrInvalid
		}
		t.sizes = make([]uint32, n)
		for i := range t.sizes {
			t.sizes[i] = fixed
		}
	case "stco":
		v, err := table(p, 1, 0)
		if err != nil {
			return err
		}
		for _, o := range v {
			t.chunks = append(t.chunks, uint64(o))
		}
	case "co64":
		v, err := table(p, 2, 0)
		if err != nil {
			return err
		}
		for i := 0; i < len(v); i += 2 {
			t.chunks = append(t.chunks, uint64(v[i])<<32|uint64(v[i+1]))
		}
	}
	return nil
}

// locateData finds the mdat payload that holds every chunk.
func (t *track) locateData(mdats []box) error {
	lo, hi := t.chunks[0], t.chunks[0]
	for _, c := range t.chunks {
		lo, hi = min(lo, c), max(hi, c)
	}
	for _, m := range mdats {
		start, end := m.off+m.headerSize, m.off+m.size
		if int64(lo) >= start && int64(hi) < end {
			t.dataStart, t.dataEnd = start, end
			return nil
		}
	}
	return fmt.Errorf("%w: chunks outside one mdat", ErrInvalid)
}

// Concat builds the long video of files, in order. Files that cannot be
// read are left out and marked in Segs; it fails only when none can.
func Concat(files []string) (*Long, error) { return ConcatAt(files, nil) }

// maxHold is the longest a minute's last frame is held to reach the next
// minute's start: the gaps Tesla leaves between its files are around a
// second. A longer gap (a minute missing) is left out instead.
const maxHold = 3.0

// ConcatAt is Concat with each file's start on the clock, in seconds from
// the first (from the file names; nil when not known). Tesla does not
// record for half a second to a second and a half between two minute
// files, and ends each with a frame of almost no duration. Holding that
// last frame until the next file starts keeps the long video in step with
// the clock, and every camera with the others.
func ConcatAt(files []string, starts []float64) (*Long, error) {
	return concatLong(files, starts, nil)
}

// ConcatSubs is ConcatAt with subtitles of what the car was doing, in
// words.
func ConcatSubs(files []string, starts []float64, words Words) (*Long, error) {
	return concatLong(files, starts, &words)
}

func concatLong(files []string, starts []float64, words *Words) (*Long, error) {
	var tracks []*track
	l := &Long{Segs: make([]LongSeg, len(files))}
	var ts uint32
	for _, f := range files {
		t, err := parseFile(f)
		if err == nil && ts != 0 && t.timescale != ts {
			err = fmt.Errorf("%w: timescale %d, earlier files %d", ErrInvalid, t.timescale, ts)
		}
		if err != nil {
			tracks = append(tracks, nil)
			continue
		}
		ts = t.timescale
		tracks = append(tracks, t)
	}
	for i, t := range tracks {
		if t == nil {
			continue
		}
		hold := 0.0
		if j := nextTrack(tracks, i); j >= 0 && len(starts) == len(files) {
			hold = starts[j] - starts[i]
		}
		t.holdLast(hold)
	}
	var at float64
	var plans []*subPlan
	size, lang := 0, ""
	var drive *pedalScale
	if words != nil {
		size, lang = words.sampleSize(), words.Language
		drive = &pedalScale{}
	}
	for i, t := range tracks {
		if t == nil {
			continue
		}
		dur := float64(t.duration) / float64(t.timescale)
		l.Segs[i] = LongSeg{Start: at, Dur: dur, OK: true}
		at += dur
		if words != nil {
			p := t.plan()
			if p == nil {
				p = &subPlan{}
			}
			plans = append(plans, p)
			// Each minute's text goes just before its frames.
			sub := &subs{path: files[i], frames: p.frames, words: words, size: size, drive: drive}
			drive.parts = append(drive.parts, sub)
			l.Parts = append(l.Parts, Part{Path: files[i], Len: int64(p.n * size), subs: sub})
		}
		l.Parts = append(l.Parts, Part{Path: files[i], Off: t.dataStart, Len: t.dataEnd - t.dataStart})
	}
	var first *track
	for _, t := range tracks {
		if t != nil {
			first = t
			break
		}
	}
	if first == nil {
		return nil, errors.New("mp4: none of the recordings can be read")
	}
	if drive != nil {
		// Width and height are the last 8 bytes of every tkhd version.
		font := byte(min(fontSize(binary.BigEndian.Uint32(first.tkhd[len(first.tkhd)-4:])>>16), 255))
		for _, p := range drive.parts {
			p.font = font
		}
	}
	head := func(base uint64) []byte { return longHead(tracks, first, base, plans, size, lang) }
	h := head(0)
	h = head(uint64(len(h)))
	l.Head = h
	l.Size = int64(len(h))
	for _, p := range l.Parts {
		l.Size += p.Len
	}
	return l, nil
}

func nextTrack(tracks []*track, i int) int {
	for j := i + 1; j < len(tracks); j++ {
		if tracks[j] != nil {
			return j
		}
	}
	return -1
}

// holdLast gives the last frame at least a usual frame's duration, and
// more, up to maxHold, so the file lasts until the next one starts span
// seconds after its own start (0: not known).
func (t *track) holdLast(span float64) {
	n := len(t.stts)
	if n == 0 {
		return
	}
	// The usual frame duration: the delta most samples have.
	usual, most := uint32(0), uint32(0)
	for _, e := range t.stts {
		if e[0] > most {
			usual, most = e[1], e[0]
		}
	}
	var total uint64
	for _, e := range t.stts {
		total += uint64(e[0]) * uint64(e[1])
	}
	last := t.stts[n-1][1]
	newLast := max(last, usual)
	if span > 0 {
		want := uint64(span * float64(t.timescale))
		rest := total - uint64(last)
		if want > rest+uint64(newLast) && want <= rest+uint64(newLast)+uint64(maxHold*float64(t.timescale)) {
			newLast = uint32(want - rest)
		}
	}
	if newLast == last {
		return
	}
	if t.stts[n-1][0] > 1 {
		t.stts[n-1][0]--
		t.stts = append(t.stts, [2]uint32{1, newLast})
	} else {
		t.stts[n-1][1] = newLast
	}
	t.duration = total - uint64(last) + uint64(newLast)
}

// evenTimes spreads a file's frames evenly over its duration, keeping the
// last frame's (held) duration. Tesla records at a slightly varying rate,
// which takes a table entry per frame: 600 KB of a 40-minute camera's
// header, which a browser must load before it can play. Even times move a
// frame by a few milliseconds at most and keep every file's length, so the
// minutes still fall on the clock.
func (t *track) evenTimes() [][2]uint32 {
	n := uint64(len(t.sizes))
	if n < 3 || len(t.stts) == 0 {
		return t.stts
	}
	last := uint64(t.stts[len(t.stts)-1][1])
	var total uint64
	for _, e := range t.stts {
		total += uint64(e[0]) * uint64(e[1])
	}
	rest := total - last
	u, r := rest/(n-1), rest%(n-1)
	return [][2]uint32{{uint32(r), uint32(u + 1)}, {uint32(n - 1 - r), uint32(u)}, {1, uint32(last)}}
}

// Duration is how long a recording's video lasts, in seconds.
func Duration(r io.ReaderAt, size int64) (float64, error) {
	t, err := parseTrack(r, size)
	if err != nil {
		return 0, err
	}
	return float64(t.duration) / float64(t.timescale), nil
}

func parseFile(name string) (*track, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return parseTrack(f, fi.Size())
}

func be16(v uint16) []byte { return binary.BigEndian.AppendUint16(nil, v) }
func be32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }
func be64(v uint64) []byte { return binary.BigEndian.AppendUint64(nil, v) }

func mk(typ string, parts ...[]byte) []byte {
	n := 8
	for _, p := range parts {
		n += len(p)
	}
	out := make([]byte, 0, n)
	out = binary.BigEndian.AppendUint32(out, uint32(n))
	out = append(out, typ...)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func full(typ string, version byte, flags uint32, parts ...[]byte) []byte {
	return mk(typ, append([][]byte{be32(uint32(version)<<24 | flags)}, parts...)...)
}

var identity = []byte{0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x40, 0, 0, 0}

// longHead is ftyp, moov and the header of the mdat holding the parts.
// base is where the mdat payload starts, i.e. the length of the result,
// which does not depend on base. plans, when given, are the text samples
// (of size bytes each, in language lang) that come before each track's
// frames.
func longHead(tracks []*track, first *track, base uint64, plans []*subPlan, size int, lang string) []byte {
	var (
		entries [][]byte
		stts    [][2]uint32
		stss    []uint32
		anySync bool
		stsc    [][3]uint32
		sizes   []uint32
		chunks  []uint64
		dur     uint64
		data    uint64
		texts   []*subPlan
		textOff []uint64
	)
	for _, t := range tracks {
		if t != nil && t.stss != nil {
			anySync = true
		}
	}
	for _, t := range tracks {
		if t == nil {
			continue
		}
		if len(plans) > 0 {
			p := plans[0]
			plans = plans[1:]
			if p.n > 0 {
				texts = append(texts, p)
				textOff = append(textOff, base+data)
				data += uint64(p.n * size)
			}
		}
		// Sample descriptions: shared where identical.
		desc := make([]uint32, len(t.entries))
		for i, e := range t.entries {
			desc[i] = 0
			for j, have := range entries {
				if string(have) == string(e) {
					desc[i] = uint32(j + 1)
				}
			}
			if desc[i] == 0 {
				entries = append(entries, e)
				desc[i] = uint32(len(entries))
			}
		}
		samples := uint32(len(sizes))
		for _, e := range t.evenTimes() {
			stts = addStts(stts, e[0], e[1])
		}
		switch {
		case t.stss != nil:
			for _, s := range t.stss {
				stss = append(stss, s+samples)
			}
		case anySync:
			for i := range t.sizes {
				stss = append(stss, samples+uint32(i)+1)
			}
		}
		// New chunks of half a second or so, from one sync sample to the
		// next, instead of Tesla's single chunk per minute: Safari plans
		// its loading per chunk, and with 40 MB ones it ran dry on a Pi
		// and stopped playing.
		sync, count, lastDesc := 0, uint32(0), uint32(0)
		flush := func(d uint32) {
			if count == 0 {
				return
			}
			if n := len(stsc); n == 0 || stsc[n-1][1] != count || stsc[n-1][2] != d {
				stsc = append(stsc, [3]uint32{uint32(len(chunks)), count, d})
			}
			count = 0
		}
		for i, off := range t.offs {
			d := t.descs[i]
			if d >= 1 && int(d) <= len(desc) {
				d = desc[d-1]
			}
			isSync := t.stss == nil
			for sync < len(t.stss) && t.stss[sync] < uint32(i+1) {
				sync++
			}
			if sync < len(t.stss) && t.stss[sync] == uint32(i+1) {
				isSync = true
			}
			if i == 0 || isSync || count >= 64 || off != t.offs[i-1]+uint64(t.sizes[i-1]) || d != lastDesc {
				flush(lastDesc)
				chunks = append(chunks, base+data+uint64(int64(off)-t.dataStart))
			}
			lastDesc = d
			count++
		}
		flush(lastDesc)
		sizes = append(sizes, t.sizes...)
		data += uint64(t.dataEnd - t.dataStart)
		dur += t.duration
	}

	var b []byte
	stsd := full("stsd", 0, 0, append(be32(uint32(len(entries))), concat(entries)...))
	var stssBox []byte
	if anySync {
		b = nil
		for _, s := range stss {
			b = append(b, be32(s)...)
		}
		stssBox = full("stss", 0, 0, be32(uint32(len(stss))), b)
	}
	b = nil
	for _, s := range sizes {
		b = append(b, be32(s)...)
	}
	stszBox := full("stsz", 0, 0, be32(0), be32(uint32(len(sizes))), b)

	ts := first.timescale
	stbl := mk("stbl", stsd, sttsBox(stts), stssBox, stscBox(stsc), stszBox, co64Box(chunks))
	minf := mk("minf", first.vmhd, first.dinf, stbl)
	mdhd := full("mdhd", 1, 0, be64(0), be64(0), be32(ts), be64(dur), be16(0x55c4), be16(0))
	mdia := mk("mdia", mdhd, first.hdlr, minf)
	// Width and height are the last 8 bytes of every tkhd version.
	wh := first.tkhd[len(first.tkhd)-8:]
	tkhd := full("tkhd", 1, 3, be64(0), be64(0), be32(1), be32(0), be64(dur), make([]byte, 8),
		be16(0), be16(0), be16(0), be16(0), identity, wh)
	trak := mk("trak", tkhd, mdia)
	next := uint32(2)
	var text []byte
	if len(texts) > 0 {
		text = textTrak(texts, textOff, size, lang, ts, dur, binary.BigEndian.Uint32(wh)>>16, binary.BigEndian.Uint32(wh[4:])>>16, first.dinf)
		next = 3
	}
	mvhd := full("mvhd", 1, 0, be64(0), be64(0), be32(ts), be64(dur), be32(0x00010000), be16(0x0100),
		make([]byte, 10), identity, make([]byte, 24), be32(next))
	moov := mk("moov", mvhd, trak, text)
	ftyp := mk("ftyp", []byte("isom"), be32(0x200), []byte("isomiso2avc1mp41"))
	mdat := append(append(be32(1), "mdat"...), be64(16+data)...)
	return append(append(ftyp, moov...), mdat...)
}

// addStts adds n samples of delta d to a time table, joining them to the
// last entry when it has the same delta.
func addStts(stts [][2]uint32, n, d uint32) [][2]uint32 {
	if n == 0 {
		return stts
	}
	if k := len(stts); k > 0 && stts[k-1][1] == d {
		stts[k-1][0] += n
		return stts
	}
	return append(stts, [2]uint32{n, d})
}

func sttsBox(stts [][2]uint32) []byte {
	var b []byte
	for _, e := range stts {
		b = append(append(b, be32(e[0])...), be32(e[1])...)
	}
	return full("stts", 0, 0, be32(uint32(len(stts))), b)
}

func stscBox(stsc [][3]uint32) []byte {
	var b []byte
	for _, e := range stsc {
		b = append(append(append(b, be32(e[0])...), be32(e[1])...), be32(e[2])...)
	}
	return full("stsc", 0, 0, be32(uint32(len(stsc))), b)
}

func co64Box(chunks []uint64) []byte {
	var b []byte
	for _, c := range chunks {
		b = append(b, be64(c)...)
	}
	return full("co64", 0, 0, be32(uint32(len(chunks))), b)
}

func concat(bs [][]byte) []byte {
	var out []byte
	for _, b := range bs {
		out = append(out, b...)
	}
	return out
}

// Reader serves a Long as one file. Close it to close the originals.
type Reader struct {
	l     *Long
	pos   int64
	files map[string]*os.File
	ends  []int64 // end of each part in the long file
	// The text of the part last read from, made when first read.
	text     []byte
	textPart int
}

func (l *Long) Open() *Reader {
	r := &Reader{l: l, files: map[string]*os.File{}, textPart: -1}
	at := int64(len(l.Head))
	for _, p := range l.Parts {
		at += p.Len
		r.ends = append(r.ends, at)
	}
	return r
}

func (r *Reader) Seek(off int64, whence int) (int64, error) {
	switch whence {
	case io.SeekCurrent:
		off += r.pos
	case io.SeekEnd:
		off += r.l.Size
	}
	if off < 0 {
		return 0, errors.New("mp4: negative position")
	}
	r.pos = off
	return off, nil
}

func (r *Reader) Read(b []byte) (int, error) {
	if r.pos >= r.l.Size {
		return 0, io.EOF
	}
	if h := int64(len(r.l.Head)); r.pos < h {
		n := copy(b, r.l.Head[r.pos:])
		r.pos += int64(n)
		return n, nil
	}
	i := sort.Search(len(r.ends), func(i int) bool { return r.ends[i] > r.pos })
	p := r.l.Parts[i]
	within := p.Len - (r.ends[i] - r.pos)
	f, ok := r.files[p.Path]
	if !ok {
		var err error
		if f, err = os.Open(p.Path); err != nil {
			return 0, err
		}
		r.files[p.Path] = f
	}
	if rem := r.ends[i] - r.pos; int64(len(b)) > rem {
		b = b[:rem]
	}
	if p.subs != nil {
		if r.textPart != i {
			r.text, r.textPart = p.subs.render(f), i
		}
		n := copy(b, r.text[within:])
		r.pos += int64(n)
		return n, nil
	}
	n, err := f.ReadAt(b, p.Off+within)
	r.pos += int64(n)
	if err == io.EOF && n == len(b) {
		err = nil
	}
	if err == io.EOF {
		err = io.ErrUnexpectedEOF // the original got shorter
	}
	return n, err
}

func (r *Reader) Close() error {
	for _, f := range r.files {
		f.Close()
	}
	return nil
}
