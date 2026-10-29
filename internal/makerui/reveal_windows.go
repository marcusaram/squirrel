package makerui

import (
	"os/exec"
	"syscall"
)

// revealFile shows path selected in Explorer. Explorer reads its own
// command line: Go would quote "/select,C:\a b\c" as a whole when the path
// has a space, which Explorer does not understand, so only the path is
// quoted.
func revealFile(path string) {
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe /select,"` + path + `"`}
	cmd.Run()
}
