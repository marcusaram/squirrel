//go:build !windows

package main

// adminWarning: other systems ask for permission when a card is opened.
func adminWarning() string { return "" }
