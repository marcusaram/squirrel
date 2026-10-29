package mp4

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

// Since firmware 2025.44.25, on HW3 and later, Tesla puts what the car was
// doing into every video frame: an H.264 SEI message (user data
// unregistered) holding a protobuf, described in Tesla's dashcam.proto
// (github.com/teslamotors/dashcam). Parked cars may leave it out.

// Sample is one frame's telemetry, in the units of dashcam.proto.
type Sample struct {
	Gear      int     `json:"g"`  // 0 park, 1 drive, 2 reverse, 3 neutral
	Speed     float64 `json:"v"`  // m/s
	Pedal     float64 `json:"p"`  // accelerator position
	Steering  float64 `json:"s"`  // steering wheel angle, degrees
	Left      bool    `json:"bl"` // blinkers
	Right     bool    `json:"br"`
	Brake     bool    `json:"b"`
	Autopilot int     `json:"a"` // 0 none, 1 self driving, 2 autosteer, 3 TACC
	Lat       float64 `json:"la"`
	Lon       float64 `json:"lo"`
	Heading   float64 `json:"h"` // degrees
}

const (
	nalSEI             = 6
	seiUserData        = 5
	maxSEI             = 4096 // bytes; Tesla's messages are around a hundred
	maxTelemetryFrames = 1 << 20
)

// Telemetry returns the telemetry of a Tesla recording, at most max samples
// spread evenly over the clip (every frame when it has fewer). A file
// without telemetry returns no samples and no error.
func Telemetry(r io.ReaderAt, size int64, max int) ([]Sample, error) {
	boxes, err := readBoxes(r, 0, size)
	if err != nil {
		return nil, err
	}
	var all []Sample
	for _, b := range boxes {
		if b.typ != "mdat" {
			continue
		}
		if all, err = mdatTelemetry(r, b.off+b.headerSize, b.off+b.size, all); err != nil {
			return nil, err
		}
	}
	if max <= 0 || len(all) <= max {
		return all, nil
	}
	out := make([]Sample, max)
	for i := range out {
		out[i] = all[i*len(all)/max]
	}
	return out, nil
}

// mdatTelemetry walks the length-prefixed NAL units of an mdat box (Tesla
// writes one H.264 track) and decodes the SEI messages among them.
func mdatTelemetry(r io.ReaderAt, off, end int64, out []Sample) ([]Sample, error) {
	var hdr [6]byte
	buf := make([]byte, maxSEI)
	for off+6 <= end {
		if _, err := r.ReadAt(hdr[:], off); err != nil {
			return out, err
		}
		n := int64(binary.BigEndian.Uint32(hdr[:4]))
		if n < 2 || off+4+n > end {
			return out, fmt.Errorf("%w: NAL unit of %d bytes at %d", ErrInvalid, n, off)
		}
		if hdr[4]&0x1F == nalSEI && hdr[5] == seiUserData && n <= maxSEI {
			nal := buf[:n]
			if _, err := r.ReadAt(nal, off+4); err != nil {
				return out, err
			}
			if s, ok := decodeSEI(nal); ok {
				out = append(out, s)
				if len(out) > maxTelemetryFrames {
					return out, errors.New("mp4: too many telemetry frames")
				}
			}
		}
		off += 4 + n
	}
	return out, nil
}

// decodeSEI takes a SEI NAL unit: header byte, payload type 5, payload
// size, then Tesla's marker (0x42 bytes and 0x69) and the protobuf, and an
// RBSP trailing byte.
func decodeSEI(nal []byte) (Sample, bool) {
	rbsp := unescape(nal[1:])
	i := 1 // past the payload type
	size := 0
	for i < len(rbsp) && rbsp[i] == 0xFF {
		size += 255
		i++
	}
	if i >= len(rbsp) {
		return Sample{}, false
	}
	size += int(rbsp[i])
	i++
	end := min(i+size, len(rbsp))
	for i < end && rbsp[i] == 0x42 {
		i++
	}
	if i >= end || rbsp[i] != 0x69 {
		return Sample{}, false
	}
	return decodeSample(rbsp[i+1 : end])
}

// unescape removes H.264 emulation prevention bytes (00 00 03).
func unescape(b []byte) []byte {
	out := make([]byte, 0, len(b))
	zeros := 0
	for _, c := range b {
		if zeros >= 2 && c == 3 {
			zeros = 0
			continue
		}
		out = append(out, c)
		if c == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return out
}

// decodeSample reads the SeiMetadata protobuf. Unknown fields are skipped,
// so a later version with more fields still decodes.
func decodeSample(b []byte) (Sample, bool) {
	var s Sample
	seen := false
	for len(b) > 0 {
		key, n := binary.Uvarint(b)
		if n <= 0 {
			return s, false
		}
		b = b[n:]
		field, wire := key>>3, key&7
		var v uint64
		switch wire {
		case 0:
			v, n = binary.Uvarint(b)
			if n <= 0 {
				return s, false
			}
			b = b[n:]
		case 1:
			if len(b) < 8 {
				return s, false
			}
			v, b = binary.LittleEndian.Uint64(b), b[8:]
		case 5:
			if len(b) < 4 {
				return s, false
			}
			v, b = uint64(binary.LittleEndian.Uint32(b)), b[4:]
		case 2:
			l, n := binary.Uvarint(b)
			if n <= 0 || uint64(len(b)-n) < l {
				return s, false
			}
			b = b[n+int(l):]
			continue
		default:
			return s, false
		}
		f32 := float64(math.Float32frombits(uint32(v)))
		f64 := math.Float64frombits(v)
		switch field {
		case 2:
			s.Gear = int(v)
		case 4:
			s.Speed = f32
		case 5:
			s.Pedal = f32
		case 6:
			s.Steering = f32
		case 7:
			s.Left = v != 0
		case 8:
			s.Right = v != 0
		case 9:
			s.Brake = v != 0
		case 10:
			s.Autopilot = int(v)
		case 11:
			s.Lat = f64
		case 12:
			s.Lon = f64
		case 13:
			s.Heading = f64
		}
		seen = true
	}
	for _, f := range []*float64{&s.Speed, &s.Pedal, &s.Steering, &s.Lat, &s.Lon, &s.Heading} {
		if math.IsNaN(*f) || math.IsInf(*f, 0) {
			*f = 0 // JSON has no NaN
		}
	}
	return s, seen
}
