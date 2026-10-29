package mp4

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// seiPayload encodes a SeiMetadata protobuf the way dashcam.proto
// describes it.
func seiPayload(speed float32, lat, lon float64, ap uint64, left bool) []byte {
	var b []byte
	varint := func(field, v uint64) {
		b = binary.AppendUvarint(b, field<<3)
		b = binary.AppendUvarint(b, v)
	}
	f32 := func(field uint64, v float32) {
		b = binary.AppendUvarint(b, field<<3|5)
		b = binary.LittleEndian.AppendUint32(b, math.Float32bits(v))
	}
	f64 := func(field uint64, v float64) {
		b = binary.AppendUvarint(b, field<<3|1)
		b = binary.LittleEndian.AppendUint64(b, math.Float64bits(v))
	}
	varint(1, 1)
	varint(2, 1)
	varint(3, 1234)
	f32(4, speed)
	f32(6, -12.5)
	if left {
		varint(7, 1)
	}
	varint(10, ap)
	f64(11, lat)
	f64(12, lon)
	f64(13, 271.5)
	b = append(b, 0x8a, 0x01, 0x02, 0x00, 0x00) // field 17, length 2: unknown, skipped
	return b
}

// seiNAL wraps a protobuf as Tesla does, with emulation prevention.
func seiNAL(pb []byte) []byte {
	payload := append([]byte{0x42, 0x42, 0x42, 0x69}, pb...)
	raw := append([]byte{seiUserData, byte(len(payload))}, payload...)
	raw = append(raw, 0x80)
	nal := []byte{0x06}
	zeros := 0
	for _, c := range raw {
		if zeros >= 2 && c <= 3 {
			nal = append(nal, 3)
			zeros = 0
		}
		nal = append(nal, c)
		if c == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return nal
}

func TestTelemetry(t *testing.T) {
	var mdat []byte
	addNAL := func(nal []byte) {
		mdat = binary.BigEndian.AppendUint32(mdat, uint32(len(nal)))
		mdat = append(mdat, nal...)
	}
	for i := 0; i < 10; i++ {
		addNAL(seiNAL(seiPayload(float32(i), 52.1+float64(i)/1000, 5.1, 2, i%2 == 0)))
		addNAL([]byte{0x65, 1, 2, 3, 4}) // a frame
	}
	file := append(mkbox("ftyp", []byte("isom")), mkbox("mdat", mdat)...)
	file = append(file, mkbox("moov", nil)...)

	all, err := Telemetry(bytes.NewReader(file), int64(len(file)), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 10 {
		t.Fatalf("%d samples, want 10", len(all))
	}
	s := all[3]
	if s.Speed != 3 || s.Gear != 1 || s.Autopilot != 2 || s.Left || s.Steering != -12.5 ||
		math.Abs(s.Lat-52.103) > 1e-9 || s.Lon != 5.1 || s.Heading != 271.5 {
		t.Errorf("sample 3 = %+v", s)
	}
	if !all[4].Left {
		t.Error("blinker lost")
	}
	few, _ := Telemetry(bytes.NewReader(file), int64(len(file)), 5)
	if len(few) != 5 || few[1].Speed != 2 || few[4].Speed != 8 {
		t.Errorf("downsampled: %+v", few)
	}
	// A clip without telemetry: no samples, no error.
	plain := append(mkbox("ftyp", []byte("isom")), mkbox("mdat", append(binary.BigEndian.AppendUint32(nil, 3), 0x65, 1, 2))...)
	if got, err := Telemetry(bytes.NewReader(plain), int64(len(plain)), 0); err != nil || len(got) != 0 {
		t.Errorf("plain clip: %v %v", got, err)
	}
}
