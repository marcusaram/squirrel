package usbview

import "testing"

func TestSubtitleWords(t *testing.T) {
	nl := languages["nl"].Subs
	if w := subtitleWords(nl, "mph"); w.Unit != "mph" || w.Factor < 2.236 || w.Factor > 2.237 || w.Brake != "REM" {
		t.Errorf("mph: %+v", w)
	}
	for _, unit := range []string{"kmh", "", "furlongs"} {
		if w := subtitleWords(nl, unit); w.Unit != "km/h" || w.Factor != 3.6 {
			t.Errorf("%q: %+v", unit, w)
		}
	}
	if languages["nl"].Subs.Unit != "km/h" {
		t.Error("the table itself changed")
	}
}
