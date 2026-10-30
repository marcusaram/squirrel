package mp4

import (
	"encoding/binary"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var testWords = Words{Language: "nld", Unit: "km/h", Factor: 3.6, Brake: "REM",
	Autopilot: [4]string{"", "Zelfrijdend", "Autosteer", "Cruise control"}}

// Every line fits the sample, whatever the car did, with room for the
// box that fills it up.
func TestSubtitleFormat(t *testing.T) {
	size := testWords.sampleSize()
	for _, s := range []Sample{
		{},
		{Gear: 1, Speed: 22.5, Pedal: 0.35, Steering: -12.4, Left: true, Brake: true, Autopilot: 1},
		{Gear: 2, Speed: 1e6, Pedal: 1e6, Steering: 1e6, Right: true, Autopilot: 3},
		{Gear: 9, Speed: -4, Steering: 0.4, Autopilot: 7},
	} {
		line := testWords.format(s, 100)
		if 2+len(line)+8 > size {
			t.Errorf("%q: %d bytes, more than %d", line, len(line), size-10)
		}
	}
	got := testWords.format(Sample{Gear: 1, Speed: 22.5, Pedal: 0.35, Steering: -12.4, Left: true, Brake: true, Autopilot: 1}, 100)
	for _, want := range []string{"◀   D   81 km/h   ▂ \u200512°\u2005 \u2007   ■■□□□   REM   Zelfrijdend   ▶"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
}

// The text of a part comes from the telemetry in each sample's frame; a
// frame without it gets an empty line padded with a 'free' box.
func TestSubtitleRender(t *testing.T) {
	var data []byte
	var frames []frameRef
	addFrame := func(nals ...[]byte) {
		start := len(data)
		for _, n := range nals {
			data = binary.BigEndian.AppendUint32(data, uint32(len(n)))
			data = append(data, n...)
		}
		frames = append(frames, frameRef{int64(start), uint32(len(data) - start)})
	}
	addFrame(seiNAL(seiPayload(10, 52, 5, 2, true)), []byte{0x65, 1, 2, 3})
	addFrame([]byte{0x41, 1, 2, 3})                                                        // no telemetry
	addFrame([]byte{0x41, 9}, seiNAL(seiPayload(20, 52, 5, 0, false)))                     // after the picture: not looked for
	addFrame([]byte{0x09, 0x10}, seiNAL(seiPayload(30, 52, 5, 0, false)), []byte{0x41, 7}) // after a delimiter
	f, err := os.CreateTemp(t.TempDir(), "frames")
	if err != nil {
		t.Fatal(err)
	}
	f.Write(data)
	defer f.Close()

	size := testWords.sampleSize()
	out := (&subs{frames: frames, words: &testWords, size: size}).render(f)
	if len(out) != 4*size {
		t.Fatalf("%d bytes, want %d", len(out), 4*size)
	}
	text := func(i int) string {
		s := out[i*size : (i+1)*size]
		return string(s[2 : 2+binary.BigEndian.Uint16(s)])
	}
	if got := text(0); !strings.Contains(got, " 36 km/h") || !strings.Contains(got, "◀") || !strings.Contains(got, "Autosteer") {
		t.Errorf("sample 0: %q", got)
	}
	for _, i := range []int{1, 2} {
		s := out[i*size : (i+1)*size]
		if text(i) != "" || string(s[6:10]) != "free" || int(binary.BigEndian.Uint32(s[2:])) != size-2 {
			t.Errorf("sample %d: %q", i, s)
		}
	}
	// After the text its colours, then a box fills the sample up exactly.
	n := 2 + len(text(0))
	if string(out[n+4:n+8]) != "styl" || int(binary.BigEndian.Uint32(out[n:])) != stylSize {
		t.Errorf("sample 0 has no colours: %q", out[:size])
	}
	if n += stylSize; string(out[n+4:n+8]) != "free" || int(binary.BigEndian.Uint32(out[n:])) != size-n {
		t.Errorf("sample 0 is not filled up: %q", out[:size])
	}
	if got := text(3); !strings.Contains(got, "108 km/h") {
		t.Errorf("sample 3: %q", got)
	}
}

// A long video with subtitles keeps its frames, and gets a text track
// ffmpeg reads with two lines a second, also over a missing file.
func TestConcatSubs(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	var files []string
	for i := 0; i < 2; i++ {
		f := filepath.Join(dir, string(rune('a'+i))+".mp4")
		out, err := exec.Command("ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=12", "-t", "5",
			"-c:v", "libx264", "-preset", "veryfast", "-g", "24", "-bf", "0", "-pix_fmt", "yuv420p", f).CombinedOutput()
		if err != nil {
			t.Fatalf("ffmpeg: %v %s", err, out)
		}
		files = append(files, f)
	}
	files = []string{files[0], filepath.Join(dir, "missing.mp4"), files[1]}
	l, err := ConcatSubs(files, nil, testWords)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := Concat(files)
	if err != nil {
		t.Fatal(err)
	}
	if want := plain.Size + int64(20*testWords.sampleSize()) + int64(len(l.Head)-len(plain.Head)); l.Size != want {
		t.Errorf("size %d, want %d", l.Size, want)
	}
	r := l.Open()
	defer r.Close()
	long := filepath.Join(dir, "long.mp4")
	out, _ := os.Create(long)
	n, err := io.Copy(out, r)
	out.Close()
	if err != nil || n != l.Size {
		t.Fatalf("copied %d of %d: %v", n, l.Size, err)
	}
	probe, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_name,nb_frames:stream_tags=language",
		"-of", "default=nw=1", long).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe: %v %s", err, probe)
	}
	for _, want := range []string{"codec_name=h264\nnb_frames=120", "codec_name=mov_text\nnb_frames=20", "TAG:language=nld"} {
		if !strings.Contains(string(probe), want) {
			t.Errorf("ffprobe lacks %q:\n%s", want, probe)
		}
	}
	srt, err := exec.Command("ffmpeg", "-v", "error", "-i", long, "-map", "0:s", "-f", "srt", "-").CombinedOutput()
	if err != nil || !strings.Contains(string(srt), "00:00:09,500 --> 00:00:10,000") {
		t.Errorf("subtitles: %v\n%s", err, srt)
	}
	if out, err := exec.Command("ffmpeg", "-v", "error", "-i", long, "-map", "0:v", "-f", "null", "-").CombinedOutput(); err != nil || len(out) > 0 {
		t.Errorf("decoding: %v %s", err, out)
	}
}

// teslaClip writes a 5-second clip of 60 frames at 12 per second, each
// with telemetry: speed base+i m/s and the accelerator at pedal.
func teslaClip(t *testing.T, path string, base, pedal float32) {
	var mdat []byte
	var sizes []byte
	for i := range 60 {
		var pb []byte
		pb = binary.AppendUvarint(pb, 2<<3)
		pb = binary.AppendUvarint(pb, 1) // drive
		pb = binary.AppendUvarint(pb, 4<<3|5)
		pb = binary.LittleEndian.AppendUint32(pb, math.Float32bits(base+float32(i)))
		pb = binary.AppendUvarint(pb, 5<<3|5)
		pb = binary.LittleEndian.AppendUint32(pb, math.Float32bits(pedal))
		start := len(mdat)
		for _, nal := range [][]byte{seiNAL(pb), {0x65, 1, 2, 3}} {
			mdat = binary.BigEndian.AppendUint32(mdat, uint32(len(nal)))
			mdat = append(mdat, nal...)
		}
		sizes = append(sizes, be32(uint32(len(mdat)-start))...)
	}
	ftyp := mk("ftyp", []byte("isom"), be32(0x200), []byte("isomavc1"))
	data := uint32(len(ftyp) + 8)
	stbl := mk("stbl",
		full("stsd", 0, 0, be32(1), mk("avc1", make([]byte, 78))),
		sttsBox([][2]uint32{{60, 1000}}),
		stscBox([][3]uint32{{1, 60, 1}}),
		full("stsz", 0, 0, be32(0), be32(60), sizes),
		full("stco", 0, 0, be32(1), be32(data)))
	trak := mk("trak",
		full("tkhd", 0, 3, make([]byte, 72), be32(320<<16), be32(240<<16)),
		mk("mdia",
			full("mdhd", 0, 0, be32(0), be32(0), be32(12000), be32(60000), be16(0x55c4), be16(0)),
			full("hdlr", 0, 0, be32(0), []byte("vide"), make([]byte, 12), []byte("v\x00")),
			mk("minf", full("vmhd", 0, 1, make([]byte, 8)), mk("dinf", full("dref", 0, 0, be32(1), full("url ", 0, 1))), stbl)))
	file := append(append(ftyp, mk("mdat", mdat)...), mk("moov", trak)...)
	if err := os.WriteFile(path, file, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Through the Reader, every half second shows the frame at that moment,
// and the accelerator is read as 0 to 100 for the whole drive: the first
// minute's light touch (0.6) is 1%, not 60%.
func TestConcatSubsTelemetry(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.mp4"), filepath.Join(dir, "b.mp4")
	teslaClip(t, a, 0, 0.6)
	teslaClip(t, b, 100, 40)
	l, err := ConcatSubs([]string{a, b}, nil, testWords)
	if err != nil {
		t.Fatal(err)
	}
	r := l.Open()
	defer r.Close()
	all, err := io.ReadAll(r)
	if err != nil || int64(len(all)) != l.Size {
		t.Fatalf("read %d of %d: %v", len(all), l.Size, err)
	}
	// The same bytes in small reads, from a fresh Reader.
	r2 := l.Open()
	defer r2.Close()
	var small []byte
	buf := make([]byte, 7)
	for {
		n, err := r2.Read(buf)
		small = append(small, buf[:n]...)
		if err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if string(small) != string(all) {
		t.Error("small reads differ")
	}
	long := string(all)
	// Sample k of a minute is frame 6k: speed base+6k m/s, in km/h.
	for _, want := range []string{
		"D   0 km/h", "D   22 km/h", "D   194 km/h", // first minute, samples 0, 1, 9
		"D   360 km/h", "D   382 km/h", "D   554 km/h", // second
		"   ■□□□□   ", "   ■■□□□   ", // 0.6 of 100, 40
	} {
		if !strings.Contains(long, want) {
			t.Errorf("long video lacks %q", want)
		}
	}
	if strings.Contains(long, "   ■■■□□   ") { // 60
		t.Error("the light touch reads as 60%")
	}
}

// The arrow of a blinker that stays on blinks: on in one sample,
// transparent in the next; the right one, off, is always transparent.
func TestSubtitleBlinks(t *testing.T) {
	var data []byte
	var frames []frameRef
	for range 3 {
		start := len(data)
		for _, n := range [][]byte{seiNAL(seiPayload(10, 52, 5, 0, true)), {0x65, 1}} {
			data = binary.BigEndian.AppendUint32(data, uint32(len(n)))
			data = append(data, n...)
		}
		frames = append(frames, frameRef{int64(start), uint32(len(data) - start)})
	}
	f, err := os.CreateTemp(t.TempDir(), "frames")
	if err != nil {
		t.Fatal(err)
	}
	f.Write(data)
	defer f.Close()
	size := testWords.sampleSize()
	out := (&subs{frames: frames, words: &testWords, size: size}).render(f)
	for i, want := range []bool{true, false, true} {
		s := out[i*size:]
		n := 2 + int(binary.BigEndian.Uint16(s))
		alpha := func(record int) byte { return s[n+10+record*12+11] }
		if string(s[n+4:n+8]) != "styl" || (alpha(0) == 0xFF) != want || alpha(1) != 0xFF || alpha(2) != 0 {
			t.Errorf("sample %d: colours % x", i, s[n:n+stylSize])
		}
	}
}

// The wheel's blocks grow, left or right, at 2, 15, 30, 60, 90, 135 and
// 180 degrees, and are empty below; the accelerator's squares fill, one
// per 20, with any press.
func TestSteerBlocksAndPedal(t *testing.T) {
	for _, c := range []struct {
		angle float64
		want  string
	}{
		{0, "\u2007 \u20070°\u2007 \u2007"},
		{1.9, "\u2007 \u20072°\u2007 \u2007"},
		{2, "\u2007 \u20072°\u2007 ▂"},
		{-12, "▂ \u200512°\u2005 \u2007"},
		{-15, "▃ \u200515°\u2005 \u2007"},
		{95, "\u2007 \u200595°\u2005 ▆"},
		{-150, "▇ 150° \u2007"},
		{-180, "█ 180° \u2007"},
		{-450, "█ 450° \u2007"},
	} {
		deg := int(math.Round(math.Abs(c.angle)))
		if got := steerBlocks(c.angle, deg); got != c.want {
			t.Errorf("%v°: %q, want %q", c.angle, got, c.want)
		}
	}
	for p, want := range map[float64]string{0: "□□□□□", 0.5: "■□□□□", 50: "■■■□□", 100: "■■■■■", 250: "■■■■■", -3: "□□□□□"} {
		if got := pedalBar(p); got != want {
			t.Errorf("pedal %v: %q, want %q", p, got, want)
		}
	}
}
