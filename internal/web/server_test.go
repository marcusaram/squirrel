package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcusaram/squirrel/internal/config"
)

// Another site open in the same browser must not be able to change
// anything; the web UI itself must.
func TestCrossOrigin(t *testing.T) {
	store, err := config.Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Config: store, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h := s.Handler()
	for _, tc := range []struct {
		site string
		want int
	}{{"cross-site", http.StatusForbidden}, {"same-origin", http.StatusServiceUnavailable}} {
		r := httptest.NewRequest("POST", "http://squirrel.local/api/wifi/networks", strings.NewReader(`{"ssid":"x","password":"12345678"}`))
		r.Header.Set("Content-Type", "text/plain")
		r.Header.Set("Sec-Fetch-Site", tc.site)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("%s: status %d, want %d", tc.site, w.Code, tc.want)
		}
	}
}
