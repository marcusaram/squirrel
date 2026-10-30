package mp4

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// Subtitles put what the car was doing (gear, speed, steering, pedals,
// blinkers, Autopilot) in a text track of a long video, so any video
// player shows it. The text is read from the recordings' telemetry only
// when a player reads it: every text sample has the same size, so the
// header, and the video's size, are known without reading a frame.

// Words are the subtitles' words, in the drive's language.
type Words struct {
	Language  string    // ISO 639-2/T, such as "nld"
	Unit      string    // speed, such as "km/h"
	Factor    float64   // from m/s to Unit
	Brake     string    // shown while braking
	Autopilot [4]string // by Sample.Autopilot; [0] is unused
}

// subStep is how long one text sample lasts, in seconds.
const subStep = 0.5

// subs is a text part: one text sample per subStep of the recording at
// path, each from the frame shown at its start, size bytes each.
type subs struct {
	path   string
	frames []frameRef
	words  *Words
	size   int
	font   byte // the text's size, as in the sample description
	drive  *pedalScale
}

// pedalScale decides, once per long video, how its accelerator reads: 0
// to 100, or 0 to 1 in older data (see app.js). Per minute it would go
// wrong in a minute with only a light touch, under 1 of 100.
type pedalScale struct {
	once  sync.Once
	scale float64
	parts []*subs
}

// get looks through the drive's minutes, in order, until a press over 1
// shows it is 0 to 100. Only a minute with presses of at most 1 asks, so
// in most drives the first minute with a real press settles it. It looks
// every two seconds, enough for a press: a drive without one is read
// through, while the computer waits for the drive.
func (d *pedalScale) get() float64 {
	d.once.Do(func() {
		d.scale = 100
		for _, p := range d.parts {
			f, err := os.Open(p.path)
			if err != nil {
				continue
			}
			samples, ok := p.read(f, 4)
			f.Close()
			for i, s := range samples {
				if ok[i] && s.Pedal > 1 {
					d.scale = 1
					return
				}
			}
		}
	})
	return d.scale
}

type frameRef struct {
	off  int64
	size uint32
}

// subPlan is a recording's text track: n samples of step, the last one
// last long (in the video's timescale).
type subPlan struct {
	n          int
	step, last uint32
	frames     []frameRef
}

// plan spreads text samples over the track's (held) duration, picking the
// frame that is shown at each sample's start in the long video.
func (t *track) plan() *subPlan {
	step := uint32(subStep * float64(t.timescale))
	if step == 0 || t.duration == 0 {
		return nil
	}
	n := int((t.duration + uint64(step) - 1) / uint64(step))
	p := &subPlan{n: n, step: step, last: uint32(t.duration - uint64(n-1)*uint64(step))}
	// When each frame starts in the long video.
	starts := make([]uint64, 0, len(t.offs))
	var at uint64
	for _, e := range t.evenTimes() {
		for range e[0] {
			starts = append(starts, at)
			at += uint64(e[1])
		}
	}
	frame := 0
	for k := range n {
		want := uint64(k) * uint64(step)
		for frame+1 < len(starts) && frame+1 < len(t.offs) && starts[frame+1] <= want {
			frame++
		}
		p.frames = append(p.frames, frameRef{int64(t.offs[frame]), t.sizes[frame]})
	}
	return p
}

// sampleSize is the size of every text sample in bytes: the length
// prefix, the longest line the words can make, and room for the 'free'
// box that fills up a shorter one.
func (w *Words) sampleSize() int {
	ap := 0
	for i, x := range w.Autopilot {
		if len(x) > len(w.Autopilot[ap]) {
			ap = i
		}
	}
	n := 0
	// The padding makes the angle longest with two digits; try each length.
	for _, v := range []float64{0, 10, 1e6} {
		s := Sample{Speed: v, Pedal: v, Steering: v, Left: true, Right: true, Brake: true, Autopilot: ap}
		n = max(n, len(w.format(s, 1)))
	}
	return 2 + n + stylSize + 8
}

// stylSize is the size of the box that colours a line: three style
// records, for the left arrow, the fields and the right arrow.
const stylSize = 8 + 2 + 3*12

// styl colours a line of n characters: white, with an arrow whose
// blinker is off fully transparent. It keeps its place, so the line
// keeps its width; a space would not do, as QuickTime drops spaces at the
// end of a line.
func styl(n int, font byte, left, right bool) []byte {
	color := func(on bool) []byte {
		if on {
			return []byte{0xFF, 0xFF, 0xFF, 0xFF}
		}
		return []byte{0xFF, 0xFF, 0xFF, 0}
	}
	record := func(from, to int, c []byte) []byte {
		return append(append(be16(uint16(from)), append(be16(uint16(to)), be16(1)...)...), append([]byte{0, font}, c...)...)
	}
	return mk("styl", be16(3), record(0, 1, color(left)), record(1, n-1, color(true)), record(n-1, n, color(right)))
}

// fontSize is the text's size for a video h high.
func fontSize(h uint32) uint32 { return max(h/24, 12) }

// figure is a space as wide as a digit, to pad a number so it keeps its
// width. Inside the line it stays; QuickTime only drops spaces at the
// ends.
const figure = "\u2007"

// blocks grow from nothing (a figure space, about as wide) to full.
var blocks = []string{figure, "▂", "▃", "▄", "▅", "▆", "▇", "█"}

// halfFigure is about half a digit wide (U+2005, a quarter em).
const halfFigure = "\u2005"

// centre pads s with room for missing digits, half on either side, so it
// stays in the middle as the number grows.
func centre(s string, missing int) string {
	side := strings.Repeat(figure, missing/2)
	if missing%2 == 1 {
		side += halfFigure
	}
	return side + s + side
}

// steerSteps are the steering wheel angles, in degrees, at which a block
// grows a step: straight on stays empty, any correction shows a line, and
// half a turn of the wheel, as in an ordinary corner, fills it.
var steerSteps = [7]float64{2, 15, 30, 60, 90, 135, 180}

// steerBlocks are the wheel's angle between two blocks, the left one
// growing as it turns left and the right one as it turns right.
func steerBlocks(angle float64, deg int) string {
	n := 0
	for _, step := range steerSteps {
		if math.Abs(angle) >= step {
			n++
		}
	}
	left, right := blocks[0], blocks[0]
	// Positive to the right, as the web player has it.
	if angle < 0 {
		left = blocks[n]
	} else {
		right = blocks[n]
	}
	return left + " " + centre(strconv.Itoa(deg)+"°", 3-len(strconv.Itoa(deg))) + " " + right
}

// pedalBar is the accelerator, 0 to 100, as five squares that fill, one
// per 20; any press shows.
func pedalBar(p float64) string {
	n := int(math.Ceil(max(0, min(100, p)) / 20))
	return strings.Repeat("■", n) + strings.Repeat("□", 5-n)
}

// format is one line of subtitles. Players draw it in a font of their
// own, so it is not laid out in columns: the fields with a few spaces in
// between, the blinkers' arrows at either end, always (styl shows or
// hides them). The steering wheel and the accelerator are blocks and
// squares that keep their width. scale turns the accelerator into a percentage.
func (w *Words) format(s Sample, scale float64) string {
	clamp := func(v float64) int { return int(math.Round(max(0, min(999, v)))) }
	gear := "?"
	if s.Gear >= 0 && s.Gear < 4 {
		gear = string("PDRN"[s.Gear])
	}
	fields := []string{gear, fmt.Sprintf("%d %s", clamp(s.Speed*w.Factor), w.Unit),
		steerBlocks(s.Steering, clamp(math.Abs(s.Steering))), pedalBar(s.Pedal * scale)}
	if s.Brake {
		fields = append(fields, w.Brake)
	}
	if s.Autopilot > 0 && s.Autopilot < len(w.Autopilot) {
		fields = append(fields, w.Autopilot[s.Autopilot])
	}
	return "◀   " + strings.Join(fields, "   ") + "   ▶"
}

// read takes the telemetry of every stride'th sample from its frame in f.
func (s *subs) read(f *os.File, stride int) ([]Sample, []bool) {
	samples := make([]Sample, len(s.frames))
	ok := make([]bool, len(s.frames))
	buf := make([]byte, 2048)
	for i := 0; i < len(s.frames); i += stride {
		fr := s.frames[i]
		b := buf[:min(int(fr.size), len(buf))]
		if n, _ := f.ReadAt(b, fr.off); n == len(b) {
			samples[i], ok[i] = frameTelemetry(b)
		}
	}
	return samples, ok
}

// render makes the text samples of a part, reading each one's frame from
// f. Every sample is the line and its colours, or nothing for a frame
// without telemetry, and a 'free' box that fills it up to size, which
// players skip.
func (s *subs) render(f *os.File) []byte {
	samples, ok := s.read(f, 1)
	top := 0.0
	for i := range samples {
		if ok[i] {
			top = max(top, samples[i].Pedal)
		}
	}
	scale := 100.0
	switch {
	case top > 1:
		scale = 1
	case top > 0 && s.drive != nil:
		scale = s.drive.get()
	}
	out := make([]byte, 0, s.size*len(s.frames))
	for i := range s.frames {
		start := len(out)
		if ok[i] {
			text := cut(s.words.format(samples[i], scale), s.size-10-stylSize)
			// The car records the blinker's lever, not its lamp: the arrow
			// blinks, half a second on and half a second off.
			on := i%2 == 0
			out = binary.BigEndian.AppendUint16(out, uint16(len(text)))
			out = append(out, text...)
			out = append(out, styl(utf8.RuneCountInString(text), s.font, on && samples[i].Left, on && samples[i].Right)...)
		} else {
			out = append(out, 0, 0)
		}
		rest := s.size - (len(out) - start)
		out = binary.BigEndian.AppendUint32(out, uint32(rest))
		out = append(out, "free"...)
		out = append(out, make([]byte, rest-8)...)
	}
	return out
}

// cut shortens text to at most n bytes, at a whole character. The longest
// line fits; this only guards the sample size.
func cut(text string, n int) string {
	for len(text) > max(n, 0) {
		_, l := utf8.DecodeLastRuneInString(text)
		text = text[:len(text)-l]
	}
	return text
}

// frameTelemetry finds the telemetry SEI among the first NAL units of a
// frame, before its picture.
func frameTelemetry(b []byte) (Sample, bool) {
	for p := 0; p+6 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[p:]))
		if n < 2 || p+4+n > len(b) {
			return Sample{}, false
		}
		switch b[p+4] & 0x1F {
		case nalSEI:
			if b[p+5] == seiUserData {
				if s, ok := decodeSEI(b[p+4 : p+4+n]); ok {
					return s, true
				}
			}
		case 1, 5: // a slice: the picture itself
			return Sample{}, false
		}
		p += 4 + n
	}
	return Sample{}, false
}

// textTrak is the subtitle track: the plans' samples in the chunks at
// offs, timed in the video's timescale ts and drawn at the bottom of a
// w×h video.
func textTrak(plans []*subPlan, offs []uint64, size int, lang string, ts uint32, dur uint64, w, h uint32, dinf []byte) []byte {
	var stts [][2]uint32
	var stsc [][3]uint32
	total := uint32(0)
	for i, p := range plans {
		stts = addStts(stts, uint32(p.n-1), p.step)
		stts = addStts(stts, 1, p.last)
		if k := len(stsc); k == 0 || stsc[k-1][1] != uint32(p.n) {
			stsc = append(stsc, [3]uint32{uint32(i + 1), uint32(p.n), 1})
		}
		total += uint32(p.n)
	}
	stszBox := full("stsz", 0, 0, be32(uint32(size)), be32(total))

	font := fontSize(h)
	boxH := min(font*2, h) // h may be 0 in a tkhd that leaves it out
	// 3GPP timed text (TS 26.245): the track covers the picture and its
	// text box is the bottom of it; centred, white on a translucent black,
	// in a monospaced font so the fields stay put.
	ftab := mk("ftab", be16(1), be16(1), []byte{5}, []byte("Menlo"))
	tx3g := mk("tx3g", make([]byte, 6), be16(1),
		be32(0), []byte{1, 0xFF}, []byte{0, 0, 0, 0x99},
		be16(uint16(h-boxH)), be16(0), be16(uint16(h)), be16(uint16(w)),
		be16(0), be16(0), be16(1), []byte{0, byte(min(font, 255))}, []byte{0xFF, 0xFF, 0xFF, 0xFF},
		ftab)
	stsd := full("stsd", 0, 0, be32(1), tx3g)
	stbl := mk("stbl", stsd, sttsBox(stts), stscBox(stsc), stszBox, co64Box(offs))
	minf := mk("minf", full("nmhd", 0, 0), dinf, stbl)
	hdlr := full("hdlr", 0, 0, be32(0), []byte("sbtl"), make([]byte, 12), []byte("Squirrel\x00"))
	// With a language players can turn the subtitles on by themselves,
	// for one their user reads; QuickTime lists "und" as unknown.
	code := uint16(0x55c4) // und
	if len(lang) == 3 {
		code = 0
		for _, c := range []byte(lang) {
			code = code<<5 | uint16(c-0x60)&0x1F
		}
	}
	mdhd := full("mdhd", 1, 0, be64(0), be64(0), be32(ts), be64(dur), be16(code), be16(0))
	mdia := mk("mdia", mdhd, hdlr, minf)
	// Alternate group 1 and no offset in the matrix, as ffmpeg writes
	// subtitles: VLC draws nothing when the track is moved down instead.
	tkhd := full("tkhd", 1, 3, be64(0), be64(0), be32(2), be32(0), be64(dur), make([]byte, 8),
		be16(0), be16(1), be16(0), be16(0), identity, be32(w<<16), be32(h<<16))
	return mk("trak", tkhd, mdia)
}
