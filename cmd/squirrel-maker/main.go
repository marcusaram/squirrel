// Command squirrel-maker is Squirrel Maker on the command line: it serves
// the maker's page on localhost and opens it in the browser, until Ctrl-C.
// The app (cmd/squirrel-app) is the same maker in its own window. On
// Windows, run it from a terminal started as administrator: writing a
// card needs that.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"

	"github.com/marcusaram/squirrel/internal/makerui"
)

var version = "dev"

// openBrowser shows url in the default browser.
func openBrowser(url string) {
	switch runtime.GOOS {
	case "windows":
		exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		exec.Command("open", url).Start()
	default:
		exec.Command("xdg-open", url).Start()
	}
}

func main() {
	noBrowser := flag.Bool("no-browser", false, "do not open the page in the browser")
	addr := flag.String("listen", "127.0.0.1:0", "address for the local page")
	flag.Parse()

	// Any page in the browser can reach localhost; the token in the URL
	// printed below keeps the others out.
	b := make([]byte, 16)
	rand.Read(b)
	token := hex.EncodeToString(b)
	srv, err := makerui.New(makerui.Options{Version: version, Token: token, Quit: func() { os.Exit(0) }})
	if err != nil {
		log.Fatal(err)
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	url := fmt.Sprintf("http://%s/?t=%s", ln.Addr(), token)
	fmt.Printf("squirrel-maker %s\nOpen %s\nStop with Ctrl-C.\n", version, url)
	if warning := adminWarning(); warning != "" {
		fmt.Println(warning)
	}
	if !*noBrowser {
		openBrowser(url)
	}
	log.Fatal(http.Serve(ln, srv.Handler()))
}
