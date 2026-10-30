//go:build !windows

package makerui

import "os/exec"

// revealFile shows path selected in the Finder.
func revealFile(path string) {
	exec.Command("open", "-R", path).Run()
}
