package main

import "golang.org/x/sys/windows"

// adminWarning says when writing a card will not work: on Windows that
// needs administrator rights.
func adminWarning() string {
	if windows.GetCurrentProcessToken().IsElevated() {
		return ""
	}
	return "Writing a card needs administrator rights: run squirrel-maker from a terminal opened as administrator."
}
