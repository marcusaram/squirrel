//go:build darwin || windows

// Command squirrel-app is Squirrel Maker as an app for macOS and Windows:
// its own window, icon and menu, built with Wails. The page and everything
// behind it are the same as the browser version's (internal/makerui). On
// Windows it asks for administrator rights when it starts: writing a card
// needs them.
package main

import (
	_ "embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/marcusaram/squirrel/internal/makerui"
)

var version = "dev"

//go:embed icon.png
var icon []byte

func main() {
	elevate()
	var app *application.App
	srv, err := makerui.New(makerui.Options{
		Version: version,
		Native: &makerui.Native{
			Ask:  func(message, ok string) bool { return ask(app, message, ok) },
			Tell: func(message string) { tell(app, message) },
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	app = application.New(application.Options{
		Name:        "Squirrel Maker",
		Description: "Makes and updates SD cards for Squirrel, the dashcam keeper for Tesla.",
		Icon:        icon,
		Assets:      application.AssetOptions{Handler: srv.Handler(), DisableLogging: true},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		// Quitting halfway through writing a card leaves it unusable.
		ShouldQuit: func() bool {
			return !srv.Busy() || ask(app, "Squirrel Maker is still working on an image or a card. Quit anyway?", "Quit")
		},
	})
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "Squirrel Maker",
		Width:     880,
		Height:    860,
		MinWidth:  560,
		MinHeight: 560,
		URL:       "/",
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// ask shows a question with the given button and Cancel, and waits for
// the answer.
func ask(app *application.App, message, ok string) bool {
	answer := make(chan bool, 1)
	d := app.Dialog.Question().SetTitle("Squirrel Maker").SetMessage(message)
	d.AddButton(ok).OnClick(func() { answer <- true })
	cancel := d.AddButton("Cancel").OnClick(func() { answer <- false })
	d.SetCancelButton(cancel).SetDefaultButton(cancel)
	if w := app.Window.Current(); w != nil {
		d.AttachToWindow(w)
	}
	d.Show()
	return <-answer
}

func tell(app *application.App, message string) {
	done := make(chan struct{}, 1)
	d := app.Dialog.Info().SetTitle("Squirrel Maker").SetMessage(message)
	d.AddButton("OK").OnClick(func() { done <- struct{}{} })
	d.Show()
	<-done
}
