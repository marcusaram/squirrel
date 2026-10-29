package mp4

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Makes three short H.264 clips with ffmpeg, joins them and lets ffmpeg
// decode the result. Skipped without ffmpeg.
func TestConcat(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	var files []string
	for i, hue := range []string{"0", "120", "240"} {
		f := filepath.Join(dir, string(rune('a'+i))+".mp4")
		out, err := exec.Command("ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=12", "-t", "5",
			"-vf", "hue=h="+hue, "-c:v", "libx264", "-preset", "veryfast", "-g", "24", "-bf", "0", "-pix_fmt", "yuv420p", f).CombinedOutput()
		if err != nil {
			t.Fatalf("ffmpeg: %v %s", err, out)
		}
		files = append(files, f)
	}
	broken := filepath.Join(dir, "broken.mp4")
	os.WriteFile(broken, []byte("not a video"), 0o644)
	files = append(files[:2], broken, files[2])

	l, err := Concat(files)
	if err != nil {
		t.Fatal(err)
	}
	if l.Segs[2].OK || !l.Segs[3].OK || l.Segs[3].Start < 9.9 || l.Segs[3].Start > 10.1 {
		t.Errorf("segments: %+v", l.Segs)
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
	probe, err := exec.Command("ffprobe", "-v", "error", "-count_frames", "-select_streams", "v:0",
		"-show_entries", "stream=nb_read_frames:format=duration", "-of", "default=nw=1", long).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe: %v %s", err, probe)
	}
	if !strings.Contains(string(probe), "nb_read_frames=180") || !strings.Contains(string(probe), "duration=15.0") {
		t.Errorf("ffprobe:\n%s", probe)
	}
	if out, err := exec.Command("ffmpeg", "-v", "error", "-i", long, "-f", "null", "-").CombinedOutput(); err != nil || len(out) > 0 {
		t.Errorf("decoding: %v %s", err, out)
	}
	// Every frame exactly as in the originals.
	packets := func(f string) string {
		out, err := exec.Command("ffmpeg", "-v", "error", "-i", f, "-c", "copy", "-f", "framemd5", "-").Output()
		if err != nil {
			t.Fatalf("framemd5 %s: %v", f, err)
		}
		var sums []string
		for _, line := range strings.Split(string(out), "\n") {
			if fields := strings.Split(line, ","); !strings.HasPrefix(line, "#") && len(fields) == 6 {
				sums = append(sums, strings.TrimSpace(fields[4])+strings.TrimSpace(fields[5]))
			}
		}
		return strings.Join(sums, "\n")
	}
	if want := packets(files[0]) + "\n" + packets(files[1]) + "\n" + packets(files[3]); packets(long) != want {
		t.Error("frames differ from the originals")
	}
	// One shared description, and a chunk per GOP of 24 frames rather
	// than one per file.
	f, _ := os.Open(long)
	defer f.Close()
	tr, err := parseTrack(f, l.Size)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.entries) != 1 || len(tr.chunks) != 9 {
		t.Errorf("%d descriptions, %d chunks", len(tr.entries), len(tr.chunks))
	}
	// Seeking into the middle of the third clip.
	r.Seek(l.Size-1000, io.SeekStart)
	tail, err := io.ReadAll(r)
	if err != nil || len(tail) != 1000 {
		t.Errorf("tail: %d %v", len(tail), err)
	}
}

// Tesla leaves a gap of about a second between its minute files; the last
// frame is held so each file starts on the clock. A gap longer than
// maxHold (a minute missing) is not filled.
func TestConcatAtHoldsToTheClock(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	var files []string
	for i := 0; i < 3; i++ {
		f := filepath.Join(dir, string(rune('a'+i))+".mp4")
		out, err := exec.Command("ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=12", "-t", "5",
			"-c:v", "libx264", "-preset", "veryfast", "-g", "24", "-bf", "0", "-pix_fmt", "yuv420p", f).CombinedOutput()
		if err != nil {
			t.Fatalf("ffmpeg: %v %s", err, out)
		}
		files = append(files, f)
	}
	l, err := ConcatAt(files, []float64{0, 5.8, 20})
	if err != nil {
		t.Fatal(err)
	}
	near := func(a, b float64) bool { return a-b < 0.01 && b-a < 0.01 }
	if !near(l.Segs[1].Start, 5.8) {
		t.Errorf("second file starts at %.3f, want 5.8 (held)", l.Segs[1].Start)
	}
	if !near(l.Segs[2].Start, 10.8) {
		t.Errorf("third file starts at %.3f, want 10.8 (gap too long to hold)", l.Segs[2].Start)
	}
	r := l.Open()
	defer r.Close()
	long := filepath.Join(dir, "long.mp4")
	out, _ := os.Create(long)
	io.Copy(out, r)
	out.Close()
	probe, err := exec.Command("ffprobe", "-v", "error", "-count_frames", "-select_streams", "v:0",
		"-show_entries", "stream=nb_read_frames:format=duration", "-of", "default=nw=1", long).CombinedOutput()
	var dur float64
	if i := strings.Index(string(probe), "duration="); i >= 0 {
		fmt.Sscanf(string(probe)[i+len("duration="):], "%g", &dur)
	}
	if err != nil || !strings.Contains(string(probe), "nb_read_frames=180") || !near(dur, 15.8) {
		t.Errorf("ffprobe: %v\n%s", err, probe)
	}
	if out, err := exec.Command("ffmpeg", "-v", "error", "-i", long, "-f", "null", "-").CombinedOutput(); err != nil || len(out) > 0 {
		t.Errorf("decoding: %v %s", err, out)
	}
}

// Even times keep a file's length and its last frame, in three entries
// however irregular the frames were.
func TestEvenTimes(t *testing.T) {
	tr := &track{sizes: make([]uint32, 2134), stts: [][2]uint32{{700, 280}, {500, 279}, {600, 282}, {333, 271}, {1, 6800}}}
	var want uint64
	for _, e := range tr.stts {
		want += uint64(e[0]) * uint64(e[1])
	}
	even := tr.evenTimes()
	var got, n uint64
	for _, e := range even {
		got += uint64(e[0]) * uint64(e[1])
		n += uint64(e[0])
	}
	if got != want || n != 2134 || len(even) != 3 || even[2] != [2]uint32{1, 6800} {
		t.Errorf("even = %v: %d samples, total %d, want %d", even, n, got, want)
	}
}
