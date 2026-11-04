package alternativeme

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFearGreedIndex(t *testing.T) {
	oldBase := BaseURL
	defer func() { BaseURL = oldBase }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fng/" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("limit") != "2" {
			t.Fatalf("limit = %s", r.URL.Query().Get("limit"))
		}
		w.Write([]byte(`{"name":"Fear and Greed Index","data":[
			{"value":"45","value_classification":"Fear","timestamp":"1718000000"},
			{"value":"60","value_classification":"Greed","timestamp":"1717913600"}
		]}`))
	}))
	defer srv.Close()
	BaseURL = srv.URL

	fg, err := FearGreedIndex(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(fg) != 2 {
		t.Fatalf("len = %d, want 2", len(fg))
	}
	if fg[0].Value != 45 || fg[0].Classification != "Fear" {
		t.Fatalf("fg[0] = %+v", fg[0])
	}
	if fg[0].Timestamp.IsZero() || fg[0].Timestamp.Unix() != 1718000000 {
		t.Fatalf("fg[0].Timestamp = %v", fg[0].Timestamp)
	}
}

func TestFearGreedIndexLenientValueAndTimestamp(t *testing.T) {
	oldBase := BaseURL
	defer func() { BaseURL = oldBase }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"value":"n/a","value_classification":"Unknown","timestamp":"0"}]}`))
	}))
	defer srv.Close()
	BaseURL = srv.URL

	fg, err := FearGreedIndex(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(fg) != 1 || fg[0].Value != 0 || !fg[0].Timestamp.IsZero() {
		t.Fatalf("lenient parse failed: %+v", fg)
	}
}

func TestFearGreedIndexEmpty(t *testing.T) {
	oldBase := BaseURL
	defer func() { BaseURL = oldBase }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()
	BaseURL = srv.URL

	fg, err := FearGreedIndex(context.Background(), 1)
	if err != nil {
		t.Fatalf("empty should not error: %v", err)
	}
	if fg != nil {
		t.Fatalf("empty should return nil, got %v", fg)
	}
}
