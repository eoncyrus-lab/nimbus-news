package client

import (
	"testing"
)

func TestDateOfExtractsDatePrefix(t *testing.T) {
	cases := map[string]string{
		"2026-06-26 20:01:38.875": "2026-06-26",
		"2026-06-26T20:01:38Z":    "2026-06-26",
		"2026-06-26":              "2026-06-26",
		"":                        "",
	}
	for in, want := range cases {
		if got := dateOf(in); got != want {
			t.Fatalf("dateOf(%q) = %q, want %q", in, got, want)
		}
	}
}
