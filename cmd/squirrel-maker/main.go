// Command squirrel-maker is Squirrel Maker on the command line: it serves
// the maker's page on localhost and opens it in the browser, until Ctrl-C.
// The Mac app (cmd/squirrel-app) is the same maker in its own window.
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

	"github.com/marcusaram/squirrel/internal/makerui"
)

var version = "dev"

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
	if !*noBrowser {
		exec.Command("open", url).Start()
	}
	log.Fatal(http.Serve(ln, srv.Handler()))
}
