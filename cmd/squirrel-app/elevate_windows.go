package main

import (
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// elevate starts the app again as administrator, through Windows's own
// prompt, when it runs without: writing a card needs it. Declined, the
// app carries on and says so when a card is to be written.
func elevate() {
	if windows.GetCurrentProcessToken().IsElevated() {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exe)
	args, _ := syscall.UTF16PtrFromString(strings.Join(os.Args[1:], " "))
	if err := windows.ShellExecute(0, verb, file, args, nil, windows.SW_NORMAL); err == nil {
		os.Exit(0)
	}
}
