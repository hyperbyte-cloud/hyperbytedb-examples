package main

import "testing"

func TestEscapeTag_spacesAndCommas(t *testing.T) {
	if got := escapeTag("Neon Breach"); got != `Neon\ Breach` {
		t.Fatalf("escapeTag: got %q want %q", got, `Neon\ Breach`)
	}
}
