package httpx

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Guards the high-blast-radius invariant: the request body must be re-sent in
// full on a retry. A reader consumed on attempt 1 would send an empty body on
// attempt 2 — this test fails loudly if that regresses.
func TestPostJSONRetriesWithFullBody(t *testing.T) {
	client := New(2 * time.Second)

	var attempts int
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if attempts == 1 {
			w.WriteHeader(http.StatusInternalServerError) // force one retry
			w.Write([]byte(`{"err":"boom"}`))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	var out struct {
		OK bool `json:"ok"`
	}
	reqBody := []byte(`{"type":"us-btc-spot"}`)
	if err := client.PostJSON(context.Background(), srv.URL, nil, reqBody, &out); err != nil {
		t.Fatalf("PostJSON: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2 (one 500 retry)", attempts)
	}
	for i, b := range bodies {
		if b != string(reqBody) {
			t.Fatalf("attempt %d body = %q, want %q (body not re-sent in full)", i+1, b, reqBody)
		}
	}
	if !out.OK {
		t.Fatalf("response not decoded: %+v", out)
	}
}

func TestPostJSONContentTypeAndHeaderOverride(t *testing.T) {
	client := New(2 * time.Second)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Fatalf("Content-Type = %q, want application/json", ct)
		}
		if k := r.Header.Get("x-soso-api-key"); k != "k1" {
			t.Fatalf("api key header = %q", k)
		}
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	var out map[string]any
	err := client.PostJSON(context.Background(), srv.URL,
		map[string]string{"x-soso-api-key": "k1"}, []byte(`{}`), &out)
	if err != nil {
		t.Fatal(err)
	}
}

// TestTransportErrorRedactsSecret guards that a transport-level failure (here a
// connection-refused to a closed port) does not leak a `token`/`api_key` query
// value into the returned error — Go's *url.Error otherwise embeds the full URL.
func TestTransportErrorRedactsSecret(t *testing.T) {
	// Bind then immediately close a listener to get a definitely-dead port.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := srv.URL
	srv.Close()

	c := &Client{hc: &http.Client{Timeout: time.Second}, retries: 2, ua: DefaultUserAgent}
	_, err := c.GetBytes(context.Background(), deadURL+"/x?token=SUPERSECRET&api_key=ALSOSECRET&foo=ok", nil)
	if err == nil {
		t.Fatal("expected a transport error")
	}
	msg := err.Error()
	for _, leak := range []string{"SUPERSECRET", "ALSOSECRET"} {
		if strings.Contains(msg, leak) {
			t.Fatalf("error leaked secret %q: %s", leak, msg)
		}
	}
	if !strings.Contains(msg, "REDACTED") {
		t.Fatalf("expected REDACTED marker in error: %s", msg)
	}
	if !strings.Contains(msg, "foo=ok") {
		t.Fatalf("non-secret param should survive: %s", msg)
	}
}
