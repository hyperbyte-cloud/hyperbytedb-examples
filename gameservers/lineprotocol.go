package main

import "strings"

// escapeTag escapes a string for use as an InfluxDB line protocol tag value.
func escapeTag(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, " ", `\ `)
	s = strings.ReplaceAll(s, ",", `\,`)
	s = strings.ReplaceAll(s, "=", `\=`)
	return s
}
