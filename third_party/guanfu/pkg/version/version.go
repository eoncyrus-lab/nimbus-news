// Package version exposes build metadata.
//
// At release build time GoReleaser injects values via -ldflags
// (-X github.com/Ricaardo/nimbus-os/guanfu/pkg/version.Version=... etc.).
// At plain `go build`/`go install` time those vars stay empty and we
// fall back to runtime/debug.ReadBuildInfo() so the binary still
// reports a useful version (module pseudo-version + vcs.revision).
package version

import (
	"fmt"
	"io"
	"runtime/debug"
)

var (
	Version = ""
	Commit  = ""
	Date    = ""
)

// Get returns (version, commit, date) using ldflags first, build info as fallback.
func Get() (string, string, string) {
	v, c, d := Version, Commit, Date
	if v == "" || c == "" || d == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			if v == "" && info.Main.Version != "" {
				v = info.Main.Version
			}
			for _, s := range info.Settings {
				switch s.Key {
				case "vcs.revision":
					if c == "" {
						c = s.Value
					}
				case "vcs.time":
					if d == "" {
						d = s.Value
					}
				}
			}
		}
	}
	if v == "" {
		v = "(devel)"
	}
	return v, c, d
}

// Print writes one-line version + optional commit/date to w.
func Print(w io.Writer, name string) {
	v, c, d := Get()
	fmt.Fprintf(w, "%s %s\n", name, v)
	if c != "" {
		if len(c) > 12 {
			c = c[:12]
		}
		fmt.Fprintf(w, "  commit: %s\n", c)
	}
	if d != "" {
		fmt.Fprintf(w, "  built:  %s\n", d)
	}
}
