package version

import (
	"bytes"
	"strings"
	"testing"
)

func TestGetFallsBackToDevel(t *testing.T) {
	defer reset(Version, Commit, Date)
	Version, Commit, Date = "", "", ""

	v, _, _ := Get()
	if v == "" {
		t.Fatal("Get returned empty version with no ldflags injection")
	}
}

func TestGetUsesLdflags(t *testing.T) {
	defer reset(Version, Commit, Date)
	Version, Commit, Date = "v1.2.3", "abcdef0123456789", "2026-01-01T00:00:00Z"

	v, c, d := Get()
	if v != "v1.2.3" {
		t.Errorf("version = %q, want v1.2.3", v)
	}
	if c != "abcdef0123456789" {
		t.Errorf("commit = %q, want abcdef0123456789", c)
	}
	if d != "2026-01-01T00:00:00Z" {
		t.Errorf("date = %q, want 2026-01-01T00:00:00Z", d)
	}
}

func TestPrintTruncatesCommit(t *testing.T) {
	defer reset(Version, Commit, Date)
	Version, Commit, Date = "v1.0.0", "abcdef0123456789deadbeef", "2026-01-01T00:00:00Z"

	var buf bytes.Buffer
	Print(&buf, "guanfu")
	out := buf.String()

	if !strings.Contains(out, "guanfu v1.0.0") {
		t.Errorf("output missing name+version: %q", out)
	}
	if !strings.Contains(out, "abcdef012345") {
		t.Errorf("output missing 12-char commit: %q", out)
	}
	if strings.Contains(out, "abcdef0123456") {
		t.Errorf("commit not truncated to 12 chars: %q", out)
	}
}

func reset(v, c, d string) {
	Version, Commit, Date = v, c, d
}
