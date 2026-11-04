package finra

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegSHODaily(t *testing.T) {
	oldBase := BaseURL
	defer func() { BaseURL = oldBase }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/equity/regsho/daily/CNMSshvol20260625.txt" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("User-Agent") != UserAgent {
			t.Fatalf("missing UA")
		}
		// header + rows + a footer line with a non-numeric total (skipped) +
		// a duplicate AAPL row (accumulated).
		w.Write([]byte(
			"Date|Symbol|ShortVolume|ShortExemptVolume|TotalVolume|Market\n" +
				"20260625|AAPL|100|0|400|Q\n" +
				"20260625|nvda|50|0|200|Q\n" +
				"20260625|AAPL|20|0|100|B\n" +
				"20260625|BADV|x|0|x|Q\n"))
	}))
	defer srv.Close()
	BaseURL = srv.URL

	rows, err := RegSHODaily(context.Background(), "20260625")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("len = %d, want 2 (AAPL,NVDA; BADV skipped): %+v", len(rows), rows)
	}
	// AAPL accumulated across two rows
	if rows["AAPL"].ShortVolume != 120 || rows["AAPL"].TotalVolume != 500 {
		t.Fatalf("AAPL = %+v, want short=120 total=500", rows["AAPL"])
	}
	// symbol uppercased
	if rows["NVDA"].ShortVolume != 50 || rows["NVDA"].TotalVolume != 200 {
		t.Fatalf("NVDA = %+v", rows["NVDA"])
	}
}

func TestRegSHODailyNotPublished(t *testing.T) {
	oldBase := BaseURL
	defer func() { BaseURL = oldBase }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r) // 404 — holiday / not yet published
	}))
	defer srv.Close()
	BaseURL = srv.URL

	rows, err := RegSHODaily(context.Background(), "20260101")
	if err != nil {
		t.Fatalf("not-published should not error: %v", err)
	}
	if rows != nil {
		t.Fatalf("not-published should return nil, got %v", rows)
	}
}

func TestParseDayShortVolumeFallsBackToZero(t *testing.T) {
	text := "Date|Symbol|ShortVolume|TotalVolume\n20260625|XYZ|notnum|300\n"
	rows := parseDay(text)
	if rows["XYZ"].ShortVolume != 0 || rows["XYZ"].TotalVolume != 300 {
		t.Fatalf("XYZ = %+v, want short=0 total=300", rows["XYZ"])
	}
}
