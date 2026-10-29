package maker

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// WriteRaw puts the whole image on the disk, the partition table last,
// also when the image does not end on a sector boundary.
func TestWriteRaw(t *testing.T) {
	dir := t.TempDir()
	data := make([]byte, 9<<20+123)
	rand.New(rand.NewSource(1)).Read(data)
	img := filepath.Join(dir, "card.img")
	os.WriteFile(img, data, 0o644)
	dev, err := os.OpenFile(filepath.Join(dir, "disk"), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()
	var steps []string
	var last float64
	err = WriteRaw(img, dev, func(s string, f float64) {
		if len(steps) == 0 || steps[len(steps)-1] != s {
			steps = append(steps, s)
		}
		last = f
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dev.Name())
	if len(got) < len(data) || !bytes.Equal(got[:len(data)], data) {
		t.Fatalf("disk holds %d bytes, not the image", len(got))
	}
	if len(got)%512 != 0 || got[len(data)] != 0 {
		t.Errorf("the last sector is not padded with zeros (%d bytes)", len(got))
	}
	if len(steps) != 2 || steps[0] != "Writing the SD card" || steps[1] != "Verifying" || last != 1 {
		t.Errorf("progress: %v, last %v", steps, last)
	}
}
