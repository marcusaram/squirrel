package mp4

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

func mkbox(typ string, children ...[]byte) []byte {
	var body []byte
	for _, c := range children {
		body = append(body, c...)
	}
	b := make([]byte, 8, 8+len(body))
	binary.BigEndian.PutUint32(b, uint32(8+len(body)))
	copy(b[4:], typ)
	return append(b, body...)
}

func sample() []byte {
	stbl := mkbox("stbl", mkbox("stsd", make([]byte, 16)), mkbox("ctts", make([]byte, 24)))
	moov := mkbox("moov", mkbox("mvhd", make([]byte, 100)),
		mkbox("trak", mkbox("mdia", mkbox("minf", stbl))))
	var f []byte
	f = append(f, mkbox("ftyp", []byte("isom0000"))...)
	f = append(f, mkbox("mdat", make([]byte, 1000))...)
	f = append(f, moov...)
	return f
}

func TestValidate(t *testing.T) {
	f := sample()
	if err := Validate(bytes.NewReader(f), int64(len(f))); err != nil {
		t.Fatalf("complete file: %v", err)
	}
	// A recording cut off in the middle of mdat.
	if err := Validate(bytes.NewReader(f[:500]), 500); err == nil {
		t.Error("truncated file validated")
	}
	// Recording that still has the zero sized mdat placeholder, no moov.
	partial := append(mkbox("ftyp", []byte("isom0000")), 0, 0, 0, 0, 'm', 'd', 'a', 't', 1, 2, 3)
	if err := Validate(bytes.NewReader(partial), int64(len(partial))); err == nil {
		t.Error("partial file validated")
	}
}

func TestCTTSPatch(t *testing.T) {
	f := sample()
	offs, err := CTTSOffsets(bytes.NewReader(f), int64(len(f)))
	if err != nil {
		t.Fatal(err)
	}
	if len(offs) != 1 || string(f[offs[0]:offs[0]+4]) != "ctts" {
		t.Fatalf("offsets = %v", offs)
	}
	want := bytes.Clone(f)
	copy(want[offs[0]:], "free")

	// Read in odd sized chunks after seeking, so the box name straddles
	// read boundaries.
	for _, chunk := range []int{1, 3, 7, 64, 4096} {
		pr := NewPatchedReader(bytes.NewReader(f), offs)
		if _, err := pr.Seek(10, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		var got []byte
		buf := make([]byte, chunk)
		for {
			n, err := pr.Read(buf)
			got = append(got, buf[:n]...)
			if err == io.EOF {
				break
			}
		}
		if !bytes.Equal(got, want[10:]) {
			t.Errorf("chunk %d: patched output differs", chunk)
		}
	}
}
