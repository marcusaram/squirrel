//go:build !darwin && !windows

package makerui

// systemCountry: on other systems the time zone tells the country.
func systemCountry() string { return "" }
