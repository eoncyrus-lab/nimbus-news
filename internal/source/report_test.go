package source

import "testing"

func TestExtractJSONObjectSkipsBanner(t *testing.T) {
	out := []byte("login success!\n{\"a\": 1}\nlogout success!\n")
	got := extractJSONObject(out)
	if string(got) != `{"a": 1}` {
		t.Fatalf("got %q", got)
	}
}

func TestExtractJSONObjectArray(t *testing.T) {
	out := []byte("banner\n[1,2,3]\ntrailer")
	got := extractJSONObject(out)
	if string(got) != `[1,2,3]` {
		t.Fatalf("got %q", got)
	}
}

func TestExtractJSONObjectRejectsGarbage(t *testing.T) {
	if got := extractJSONObject([]byte("no json here")); got != nil {
		t.Fatalf("got %q", got)
	}
	if got := extractJSONObject([]byte("{\"broken\": }")); got != nil {
		t.Fatalf("got %q", got)
	}
}

func TestExtractJSONObjectHandlesBracesInBanner(t *testing.T) {
	out := []byte("[WARN] config {x} missing\n{\"ok\": true}\nlogout success!\n")
	got := extractJSONObject(out)
	if string(got) != `{"ok": true}` {
		t.Fatalf("got %q", got)
	}
}

func TestExtractJSONObjectReturnsLastCompleteDocument(t *testing.T) {
	out := []byte("{\"phase\": 1}\nprogress\n{\"result\": [1,2]}\n")
	got := extractJSONObject(out)
	if string(got) != `{"result": [1,2]}` {
		t.Fatalf("got %q", got)
	}
}

func TestExtractJSONObjectIgnoresBracesInStrings(t *testing.T) {
	out := []byte("noise\n{\"text\": \"a } b {\", \"n\": 1}\n")
	got := extractJSONObject(out)
	if string(got) != `{"text": "a } b {", "n": 1}` {
		t.Fatalf("got %q", got)
	}
}

func TestSanitizeJSONNumbers(t *testing.T) {
	in := []byte(`{"a": NaN, "b": -Infinity, "c": Infinity, "s": "NaN"}`)
	got := string(sanitizeJSONNumbers(in))
	want := `{"a": null, "b": null, "c": null, "s": "NaN"}`
	if got != want {
		t.Fatalf("got %s", got)
	}
}
