package makerui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/marcusaram/squirrel/internal/maker"
)

// Saving the answers drops the passwords, but must not take them out of
// the settings the image is built with.
func TestSaveSettingsKeepsPasswordsForBuild(t *testing.T) {
	a := &Server{stateDir: t.TempDir()}
	s := maker.Settings{WiFi: []maker.Network{{SSID: "Home IoT", Password: "geheim123"}}}
	a.saveSettings(s)
	if s.WiFi[0].Password != "geheim123" {
		t.Fatal("saving the settings erased the wifi password used for the build")
	}
}

// The app's page has no token and asks through the app's own dialogs; the
// browser version needs its token and has no such dialogs.
func TestNativeAndBrowser(t *testing.T) {
	t.Setenv("SQUIRREL_MAKER_HOME", t.TempDir())
	var asked string
	app, err := New(Options{Version: "test", Native: &Native{
		Ask:  func(message, ok string) bool { asked = message + "|" + ok; return true },
		Tell: func(string) {},
	}})
	if err != nil {
		t.Fatal(err)
	}
	h := app.Handler()
	call := func(h http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("X-Token", token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call(h, "GET", "/api/defaults", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"native":true`) {
		t.Errorf("app defaults: %d %s", w.Code, w.Body)
	}
	if w := call(h, "POST", "/api/ask", `{"message":"Erase?","ok":"Erase"}`, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) || asked != "Erase?|Erase" {
		t.Errorf("ask: %d %s %q", w.Code, w.Body, asked)
	}
	if w := call(h, "POST", "/api/quit", "", ""); w.Code == 200 {
		t.Error("the app offers quitting from the page")
	}
	if w := call(h, "GET", "/", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "Squirrel Maker") {
		t.Errorf("page: %d", w.Code)
	}

	browser, _ := New(Options{Version: "test", Token: "secret", Quit: func() {}})
	b := browser.Handler()
	if w := call(b, "GET", "/api/defaults", "", ""); w.Code != http.StatusForbidden {
		t.Errorf("browser without token: %d", w.Code)
	}
	if w := call(b, "GET", "/api/defaults", "", "secret"); w.Code != 200 || !strings.Contains(w.Body.String(), `"native":false`) {
		t.Errorf("browser defaults: %d %s", w.Code, w.Body)
	}
	if w := call(b, "POST", "/api/ask", `{}`, "secret"); w.Code == 200 {
		t.Error("the browser version has native dialogs")
	}
}
