package nasdaq

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetchEndpoints(t *testing.T) {
	fixtures := map[string][]byte{
		"/api/calendar/earnings":  fixture(t, "earnings.json"),
		"/api/calendar/dividends": fixture(t, "dividends.json"),
		"/api/calendar/splits":    fixture(t, "splits.json"),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("date") != "2026-07-30" {
			t.Errorf("date = %q", r.URL.Query().Get("date"))
		}
		if !strings.Contains(r.Header.Get("User-Agent"), "Mozilla/5.0") {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		if r.Header.Get("Referer") != "https://www.nasdaq.com/" {
			t.Errorf("Referer = %q", r.Header.Get("Referer"))
		}
		body, ok := fixtures[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer server.Close()

	fetchedAt := time.Date(2026, 7, 30, 12, 0, 0, 0, time.FixedZone("test", 8*60*60))
	client := NewClient(server.Client(), server.URL, WithClock(func() time.Time { return fetchedAt }))

	earnings, err := client.FetchEarnings(context.Background(), "2026-07-30")
	if err != nil {
		t.Fatalf("FetchEarnings: %v", err)
	}
	if len(earnings) != 2 {
		t.Fatalf("earnings length = %d", len(earnings))
	}
	if got := earnings[0]; got.Symbol != "US:AAPL" || got.Session != SessionAfterMarket ||
		got.EPS == nil || *got.EPS != 1.40 || !got.Estimated || got.Provenance == "" {
		t.Errorf("earnings[0] = %+v", got)
	}
	if earnings[1].EPS != nil || earnings[1].Session != SessionUnknown {
		t.Errorf("earnings null fields = %+v", earnings[1])
	}
	if earnings[0].FetchedAt.Location() != time.UTC ||
		!earnings[0].FetchedAt.Equal(time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC)) {
		t.Errorf("FetchedAt = %v", earnings[0].FetchedAt)
	}

	dividends, err := client.FetchDividends(context.Background(), "2026-07-30")
	if err != nil {
		t.Fatalf("FetchDividends: %v", err)
	}
	if got := dividends[0].Amount; got == nil || *got != 1234.56 {
		t.Errorf("dividend amount = %v", got)
	}
	if dividends[1].Amount != nil {
		t.Errorf("N/A dividend amount = %v", dividends[1].Amount)
	}

	splits, err := client.FetchSplits(context.Background(), "2026-07-30")
	if err != nil {
		t.Fatalf("FetchSplits: %v", err)
	}
	if got := splits[0].SplitRatio; got == nil || got.Numerator != 3 || got.Denominator != 2 {
		t.Errorf("split ratio = %+v", got)
	}
	if splits[1].SplitRatio != nil {
		t.Errorf("null split ratio = %+v", splits[1].SplitRatio)
	}

	for _, event := range append(append(earnings, dividends...), splits...) {
		if event.EventDate.Location().String() != "America/New_York" {
			t.Errorf("%s location = %s", event.Symbol, event.EventDate.Location())
		}
		if got := event.EventDate.UTC(); !got.Equal(time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC)) {
			t.Errorf("%s UTC event date = %v", event.Symbol, got)
		}
		if len(event.Raw) == 0 || !strings.Contains(event.SourceURL, "?date=2026-07-30") {
			t.Errorf("%s provenance fields missing: %+v", event.Symbol, event)
		}
	}
}

func TestFetchCalendarReturnsPartialResultsAndJoinedErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/calendar/earnings":
			_, _ = w.Write(fixture(t, "earnings.json"))
		case "/api/calendar/dividends":
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		case "/api/calendar/splits":
			_, _ = w.Write(fixture(t, "splits.json"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	events, err := NewClient(server.Client(), server.URL).FetchCalendar(context.Background(), "2026-07-30")
	if len(events) != 4 {
		t.Fatalf("partial event length = %d", len(events))
	}
	if err == nil || !strings.Contains(err.Error(), "nasdaq dividends") ||
		!strings.Contains(err.Error(), "http 503") {
		t.Fatalf("joined error = %v", err)
	}
	if events[0].Type != EventEarnings || events[2].Type != EventSplit {
		t.Errorf("event order = %v, %v", events[0].Type, events[2].Type)
	}
}

func TestEndpointReturnsGoodRowsAlongsideRowErrors(t *testing.T) {
	body := `{"data":{"rows":[
		{"symbol":"GOOD","name":"Good Corp.","epsForecast":"($0.25)","time":"BMO"},
		{"symbol":null,"name":"Broken Corp."}
	]}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	events, err := NewClient(server.Client(), server.URL).FetchEarnings(context.Background(), "2026-01-15")
	if len(events) != 1 {
		t.Fatalf("event length = %d", len(events))
	}
	if events[0].EPS == nil || *events[0].EPS != -0.25 || events[0].Session != SessionBeforeMarket {
		t.Errorf("event = %+v", events[0])
	}
	if err == nil || !strings.Contains(err.Error(), "missing symbol") {
		t.Fatalf("row error = %v", err)
	}
	if got := events[0].EventDate.UTC(); !got.Equal(time.Date(2026, 1, 15, 5, 0, 0, 0, time.UTC)) {
		t.Errorf("winter UTC event date = %v", got)
	}
}

func TestResponseLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"data":{"rows":[]}}`+strings.Repeat(" ", 128))
	}))
	defer server.Close()

	_, err := NewClient(server.Client(), server.URL, WithMaxResponseBytes(32)).
		FetchEarnings(context.Background(), "2026-07-30")
	if err == nil || !strings.Contains(err.Error(), "response exceeds 32 bytes") {
		t.Fatalf("limit error = %v", err)
	}
}

func TestCallerDeadlineIsHonored(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte(`{"data":{"rows":[]}}`))
	}))
	defer server.Close()
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := NewClient(server.Client(), server.URL).FetchSplits(ctx, "2026-07-30")
	<-started
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error = %v", err)
	}
}

func TestInvalidDateDoesNotMakeRequest(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()

	_, err := NewClient(server.Client(), server.URL).FetchEarnings(context.Background(), "07/30/2026")
	if err == nil || requests != 0 {
		t.Fatalf("err = %v, requests = %d", err, requests)
	}
}

func TestFreshCacheHitAvoidsNetworkAndDeepClonesEvents(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(fixture(t, "earnings.json"))
	}))
	defer server.Close()

	client := NewClient(server.Client(), server.URL)
	first, err := client.FetchEarnings(context.Background(), "2026-07-30")
	if err != nil {
		t.Fatal(err)
	}
	*first[0].EPS = 999
	first[0].Raw[0] = 'x'

	second, err := client.FetchEarnings(context.Background(), "2026-07-30")
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests=%d, want 1", requests.Load())
	}
	if second[0].DataStatus != EndpointCacheHit || second[0].EPS == nil || *second[0].EPS != 1.40 ||
		len(second[0].Raw) == 0 || second[0].Raw[0] != '{' {
		t.Fatalf("cached event was mutated: %+v raw=%q", second[0], second[0].Raw)
	}
	*second[0].EPS = 888
	third, err := client.FetchEarnings(context.Background(), "2026-07-30")
	if err != nil {
		t.Fatal(err)
	}
	if *third[0].EPS != 1.40 {
		t.Fatalf("cache-hit mutation leaked: %+v", third[0])
	}
}

func TestSameDateFallbackAfterHTTPAndSchemaDrift(t *testing.T) {
	var mode atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch mode.Load() {
		case 0:
			_, _ = w.Write(fixture(t, "earnings.json"))
		case 1:
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		default:
			_, _ = w.Write([]byte(`{"data":{"items":[]}}`))
		}
	}))
	defer server.Close()

	client := NewClient(server.Client(), server.URL, WithCacheTTL(0))
	if _, err := client.FetchEarnings(context.Background(), "2026-07-30"); err != nil {
		t.Fatal(err)
	}
	mode.Store(1)
	httpFallback, err := client.FetchEarnings(context.Background(), "2026-07-30")
	if len(httpFallback) != 2 || err == nil || httpFallback[0].DataStatus != EndpointCachedFallback ||
		httpFallback[0].CachedAt == nil || !strings.Contains(err.Error(), "http 503") ||
		!strings.Contains(err.Error(), "using cached fallback") {
		t.Fatalf("HTTP fallback events=%+v err=%v", httpFallback, err)
	}
	var drift *SchemaDriftError
	if errors.As(err, &drift) {
		t.Fatalf("HTTP error unexpectedly classified as drift: %v", err)
	}

	mode.Store(2)
	driftFallback, err := client.FetchEarnings(context.Background(), "2026-07-30")
	if len(driftFallback) != 2 || !errors.As(err, &drift) ||
		drift.Endpoint != "earnings" || drift.Path != "data.rows" {
		t.Fatalf("schema fallback events=%+v drift=%+v err=%v", driftFallback, drift, err)
	}
}

func TestCacheFallbackNeverCrossesRequestedDate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("date") == "2026-07-30" {
			_, _ = w.Write(fixture(t, "earnings.json"))
			return
		}
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := NewClient(server.Client(), server.URL, WithCacheTTL(0))
	if _, err := client.FetchEarnings(context.Background(), "2026-07-30"); err != nil {
		t.Fatal(err)
	}
	events, err := client.FetchEarnings(context.Background(), "2026-07-31")
	if len(events) != 0 || err == nil || strings.Contains(err.Error(), "cached fallback") {
		t.Fatalf("cross-date fallback events=%+v err=%v", events, err)
	}
}

func TestSchemaDriftErrorsAsAndCalendarKeepsOtherEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/calendar/earnings":
			_, _ = w.Write([]byte(`{"data":{"rows":[{"symbol":{"new":"shape"}}]}}`))
		case "/api/calendar/dividends":
			_, _ = w.Write(fixture(t, "dividends.json"))
		case "/api/calendar/splits":
			_, _ = w.Write(fixture(t, "splits.json"))
		}
	}))
	defer server.Close()

	events, err := NewClient(server.Client(), server.URL).FetchCalendar(context.Background(), "2026-07-30")
	var drift *SchemaDriftError
	if !errors.As(err, &drift) {
		t.Fatalf("error is not SchemaDriftError: %v", err)
	}
	if drift.Endpoint != "earnings" || drift.Path != "data.rows[0].symbol" {
		t.Fatalf("drift=%+v", drift)
	}
	if len(events) != 4 || events[0].Type != EventDividend || events[2].Type != EventSplit {
		t.Fatalf("partial events=%+v", events)
	}
}

func TestCacheExpiryRefreshesEndpoint(t *testing.T) {
	var requests atomic.Int32
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(fixture(t, "splits.json"))
	}))
	defer server.Close()

	client := NewClient(server.Client(), server.URL,
		WithCacheTTL(time.Minute),
		WithClock(func() time.Time { return now }),
	)
	if _, err := client.FetchSplits(context.Background(), "2026-07-30"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(59 * time.Second)
	if _, err := client.FetchSplits(context.Background(), "2026-07-30"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err := client.FetchSplits(context.Background(), "2026-07-30"); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests=%d, want 2", requests.Load())
	}
}

func TestConcurrentCacheReadsReturnIndependentEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fixture(t, "dividends.json"))
	}))
	defer server.Close()

	client := NewClient(server.Client(), server.URL)
	if _, err := client.FetchDividends(context.Background(), "2026-07-30"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(value float64) {
			defer wg.Done()
			events, err := client.FetchDividends(context.Background(), "2026-07-30")
			if err != nil {
				t.Errorf("FetchDividends: %v", err)
				return
			}
			*events[0].Amount = value
			events[0].Raw[0] = 'x'
		}(float64(i))
	}
	wg.Wait()
	events, err := client.FetchDividends(context.Background(), "2026-07-30")
	if err != nil {
		t.Fatal(err)
	}
	if *events[0].Amount != 1234.56 || events[0].Raw[0] != '{' {
		t.Fatalf("concurrent mutation leaked: %+v raw=%q", events[0], events[0].Raw)
	}
}

func ExampleClient_FetchCalendar() {
	client := NewClient(&http.Client{Timeout: 10 * time.Second}, "")
	events, err := client.FetchCalendar(context.Background(), "2026-07-30")
	_, _ = events, err
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// TestRealWorldResponseShapesV2 覆盖 2026-08 实测的 Nasdaq API 新响应结构:
//   - dividends: rows 嵌套在 data.calendar 下,字段为 dividend_Ex_Date/dividend_Rate
//   - splits: executionDate 为单数字月日 "8/21/2026"
func TestRealWorldResponseShapesV2(t *testing.T) {
	fixtures := map[string][]byte{
		"/api/calendar/dividends": fixture(t, "dividends_v2.json"),
		"/api/calendar/splits":    fixture(t, "splits_v2.json"),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := fixtures[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer server.Close()

	fetchedAt := time.Date(2026, 8, 2, 12, 0, 0, 0, time.FixedZone("test", 8*60*60))
	client := NewClient(server.Client(), server.URL, WithClock(func() time.Time { return fetchedAt }))

	dividends, err := client.FetchDividends(context.Background(), "2026-08-03")
	if err != nil {
		t.Fatalf("FetchDividends: %v", err)
	}
	if len(dividends) == 0 {
		t.Fatal("no dividend rows parsed")
	}
	first := dividends[0]
	if first.Symbol != "US:ASRV" {
		t.Errorf("dividend symbol = %q", first.Symbol)
	}
	if want := time.Date(2026, 8, 3, 0, 0, 0, 0, first.EventDate.Location()); !first.EventDate.Equal(want) {
		t.Errorf("dividend ex-date = %v, want %v", first.EventDate, want)
	}
	if first.Amount == nil || *first.Amount != 0.03 {
		t.Errorf("dividend amount = %v, want 0.03", first.Amount)
	}

	splits, err := client.FetchSplits(context.Background(), "2026-08-03")
	if err != nil {
		t.Fatalf("FetchSplits: %v", err)
	}
	if len(splits) == 0 {
		t.Fatal("no split rows parsed")
	}
	sFirst := splits[0]
	if sFirst.Symbol != "US:SFBS" {
		t.Errorf("split symbol = %q", sFirst.Symbol)
	}
	if want := time.Date(2026, 8, 21, 0, 0, 0, 0, sFirst.EventDate.Location()); !sFirst.EventDate.Equal(want) {
		t.Errorf("split date = %v, want 2026-08-21", sFirst.EventDate)
	}
	if sFirst.SplitRatio == nil || sFirst.SplitRatio.Numerator != 2 || sFirst.SplitRatio.Denominator != 1 {
		t.Errorf("split ratio = %+v, want 2:1", sFirst.SplitRatio)
	}
}
