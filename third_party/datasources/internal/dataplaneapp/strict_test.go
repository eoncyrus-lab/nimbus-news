package dataplaneapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Ricaardo/nimbus-os/datasources/envelope"
	"github.com/Ricaardo/nimbus-os/datasources/market/hub"
	"github.com/Ricaardo/nimbus-os/datasources/yahoo"
)

func TestStrictAndLegacyQuoteGoldenShapes(t *testing.T) {
	producers := coreProducers{
		quote: func(context.Context, string) (*yahoo.Quote, error) {
			return &yahoo.Quote{Symbol: "AAPL", Name: "Apple", Last: 211.25, Currency: "USD", Time: 1752969600}, nil
		},
		history: func(context.Context, string, int) ([]yahoo.Bar, error) { return []yahoo.Bar{}, nil },
	}
	mux := newDatagwMuxWithProducers(producers)

	strict := httptest.NewRecorder()
	mux.ServeHTTP(strict, httptest.NewRequest(http.MethodGet, "/v1/quote?symbols=US%3AAAPL", nil))
	if strict.Code != http.StatusOK {
		t.Fatalf("strict code = %d body=%s", strict.Code, strict.Body.String())
	}
	strictGolden := normalizedGolden(t, strict.Body.Bytes())
	wantStrict := `{"data":[{"asset_type":"stock","change":0,"change_pct":0,"high":0,"last":211.25,"low":0,"name":"Apple","open":0,"prev_close":0,"provider":"yahoo","symbol":"US:AAPL","ts":"2025-07-20T00:00:00Z","volume":0}],"endpoint":"quote","error":null,"params":{"symbols":"US:AAPL"},"provider":"market-hub","raw_hash":"\u003chash\u003e","retrieved_at":"\u003ctime\u003e","status":"ok","version":1}`
	if strictGolden != wantStrict {
		t.Fatalf("strict golden mismatch\n got: %s\nwant: %s", strictGolden, wantStrict)
	}
	if _, err := envelope.DecodeObservationEnvelope[[]strictQuote](strict.Body.Bytes()); err != nil {
		t.Fatalf("strict contract validation: %v", err)
	}

	legacy := httptest.NewRecorder()
	mux.ServeHTTP(legacy, httptest.NewRequest(http.MethodGet, "/quote?symbol=AAPL", nil))
	if legacy.Code != http.StatusOK {
		t.Fatalf("legacy code = %d body=%s", legacy.Code, legacy.Body.String())
	}
	legacyGolden := normalizedGolden(t, legacy.Body.Bytes())
	wantLegacy := `{"data":{"currency":"USD","day_high":0,"day_low":0,"exchange":"","last_price":211.25,"name":"Apple","open":0,"previous_close":0,"symbol":"AAPL","time":1752969600,"volume":0},"endpoint":"chart.quote","params":{"symbol":"AAPL"},"provider":"yahoo","raw_hash":"\u003chash\u003e","retrieved_at":"\u003ctime\u003e"}`
	if legacyGolden != wantLegacy {
		t.Fatalf("legacy golden mismatch\n got: %s\nwant: %s", legacyGolden, wantLegacy)
	}
}

func TestStrictMultiQuoteFullSemanticParityAcrossListeners(t *testing.T) {
	wantRows := []strictQuote{
		{
			Symbol: "US:AAPL", Name: "Apple", Last: 211.25, PrevClose: 200,
			Open: 201, High: 212, Low: 199, Volume: 42, Change: 11.25,
			ChangePct: 5.625, AssetType: "stock", Provider: "yahoo",
			Timestamp: "2025-07-20T00:00:00Z",
		},
		{
			Symbol: "HK:00700", Name: "Tencent", Last: 500, PrevClose: 490,
			Open: 492, High: 501, Low: 488, Volume: 84, Change: 10,
			ChangePct: 10.0 / 490.0 * 100, AssetType: "stock", Provider: "yahoo",
			Timestamp: "2025-07-20T00:00:00Z",
		},
	}
	producers := coreProducers{
		quote: func(_ context.Context, symbol string) (*yahoo.Quote, error) {
			var row strictQuote
			switch symbol {
			case "AAPL":
				row = wantRows[0]
			case "0700.HK":
				row = wantRows[1]
			default:
				t.Fatalf("unexpected Yahoo symbol %q", symbol)
			}
			return &yahoo.Quote{
				Symbol: symbol, Name: row.Name, Last: row.Last, PrevClose: row.PrevClose,
				Open: row.Open, High: row.High, Low: row.Low,
				Volume: row.Volume, Time: 1752969600,
			}, nil
		},
		history: func(context.Context, string, int) ([]yahoo.Bar, error) { return []yahoo.Bar{}, nil },
	}
	dataMux := newDatagwMuxWithProducers(producers)
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("symbols"); got != "US:AAPL,HK:00700" {
			t.Errorf("worker symbols=%q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": wantRows})
	}))
	defer worker.Close()
	t.Setenv("MARKET_FUTU_WORKER", worker.URL)
	facadeMux := buildDataplaneMux(dataMux, hub.New())

	decode := func(handler http.Handler) envelope.ObservationEnvelope[[]strictQuote] {
		t.Helper()
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/quote?symbols=US%3AAAPL%2CHK%3A00700", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("code=%d body=%s", recorder.Code, recorder.Body.String())
		}
		observation, err := envelope.DecodeObservationEnvelope[[]strictQuote](recorder.Body.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		return observation
	}
	dataObservation := decode(dataMux)
	facadeObservation := decode(facadeMux)
	if dataObservation.Provider != facadeObservation.Provider ||
		dataObservation.Endpoint != facadeObservation.Endpoint ||
		dataObservation.Status != facadeObservation.Status ||
		!reflect.DeepEqual(dataObservation.Params, facadeObservation.Params) ||
		!reflect.DeepEqual(dataObservation.Data, facadeObservation.Data) ||
		dataObservation.RawHash != facadeObservation.RawHash ||
		!reflect.DeepEqual(*dataObservation.Data, wantRows) {
		t.Fatalf("strict quote mismatch: data=%+v facade=%+v", dataObservation, facadeObservation)
	}
}

func TestStrictHistoryEmptyAndDependencyErrorAreDistinct(t *testing.T) {
	producers := coreProducers{
		quote: func(context.Context, string) (*yahoo.Quote, error) { return nil, errors.New("quote down") },
		history: func(context.Context, string, int) ([]yahoo.Bar, error) {
			return []yahoo.Bar{}, nil
		},
	}
	mux := newDatagwMuxWithProducers(producers)

	empty := httptest.NewRecorder()
	mux.ServeHTTP(empty, httptest.NewRequest(http.MethodGet, "/v1/history?symbol=EMPTY", nil))
	observation, err := envelope.DecodeObservationEnvelope[[]yahoo.Bar](empty.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if empty.Code != http.StatusOK || observation.Status != envelope.ObservationStatusEmpty || observation.Data == nil || len(*observation.Data) != 0 {
		t.Fatalf("empty result: code=%d observation=%+v", empty.Code, observation)
	}

	failed := httptest.NewRecorder()
	mux.ServeHTTP(failed, httptest.NewRequest(http.MethodGet, "/v1/quote?symbols=US%3AAAPL", nil))
	failure, err := envelope.DecodeObservationEnvelope[any](failed.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if failed.Code != http.StatusBadGateway || failure.Status != envelope.ObservationStatusError || failure.Data != nil || failure.Error == nil || failure.Error.Dependency != "yahoo" {
		t.Fatalf("dependency result: code=%d observation=%+v", failed.Code, failure)
	}
}

func TestStrictNamespaceDoesNotExposeUnmigratedRoutes(t *testing.T) {
	rec := httptest.NewRecorder()
	newDatagwMuxWithProducers(defaultCoreProducers()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/funding", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unmigrated strict route code = %d, want 404", rec.Code)
	}
}

func TestStrictHistoryFallbackUsesExplicitStatuses(t *testing.T) {
	warehouseStatus := http.StatusBadGateway
	warehouseBody := `{"error":"warehouse unavailable"}`
	warehouse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(warehouseStatus)
		_, _ = w.Write([]byte(warehouseBody))
	}))
	defer warehouse.Close()
	futuBody := `{"data":[{"close":1}],"provenance":{"source":"stub"}}`
	futu := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(futuBody))
	}))
	defer futu.Close()
	t.Setenv("WAREHOUSE_URL", warehouse.URL)
	t.Setenv("MARKET_FUTU_WORKER", futu.URL)
	mux := buildDataplaneMux(http.NewServeMux(), hub.New())

	recovered := httptest.NewRecorder()
	mux.ServeHTTP(recovered, httptest.NewRequest(http.MethodGet, "/v1/history?symbol=US:SPY&limit=3", nil))
	observation, err := envelope.DecodeObservationEnvelope[[]map[string]any](recovered.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Code != http.StatusOK || observation.Status != envelope.ObservationStatusDegraded || observation.Error == nil || observation.Error.Dependency != "warehouse" {
		t.Fatalf("recovered result: code=%d observation=%+v", recovered.Code, observation)
	}

	warehouseStatus = http.StatusOK
	warehouseBody = `{"data":[]}`
	legitimateEmpty := httptest.NewRecorder()
	mux.ServeHTTP(legitimateEmpty, httptest.NewRequest(http.MethodGet, "/v1/history?symbol=US:SPY&limit=3", nil))
	emptyFallback, err := envelope.DecodeObservationEnvelope[[]map[string]any](legitimateEmpty.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if emptyFallback.Status != envelope.ObservationStatusOK || emptyFallback.Error != nil {
		t.Fatalf("empty fallback result = %+v", emptyFallback)
	}

	futuBody = `{"data":[],"provenance":{"error":"market dependency unavailable"}}`
	failedFallback := httptest.NewRecorder()
	mux.ServeHTTP(failedFallback, httptest.NewRequest(http.MethodGet, "/v1/history?symbol=US:SPY&limit=3", nil))
	degradedEmpty, err := envelope.DecodeObservationEnvelope[[]map[string]any](failedFallback.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if failedFallback.Code != http.StatusOK || degradedEmpty.Status != envelope.ObservationStatusEmpty || degradedEmpty.Error != nil || degradedEmpty.Data == nil || len(*degradedEmpty.Data) != 0 {
		t.Fatalf("empty primary + failed fallback result: code=%d observation=%+v", failedFallback.Code, degradedEmpty)
	}
}

func TestApplyFallbackFourOutcomeCombinations(t *testing.T) {
	primaryFailure := &envelope.ObservationError{
		Code: "dependency_failure", Message: "warehouse down", Dependency: "warehouse", Retryable: true,
	}
	fallbackFailure := &envelope.ObservationError{
		Code: "dependency_failure", Message: "market down", Dependency: "market-hub", Retryable: true,
	}
	emptyPrimary := observationResult{
		Status: envelope.ObservationStatusEmpty, Provider: "warehouse", Endpoint: "history",
		Data: []any{}, HTTPCode: http.StatusOK,
	}
	errorPrimary := observationResult{
		Status: envelope.ObservationStatusError, Provider: "warehouse", Endpoint: "history",
		Failure: primaryFailure, HTTPCode: http.StatusBadGateway,
	}
	okFallback := observationResult{
		Status: envelope.ObservationStatusOK, Provider: "market-hub", Endpoint: "kline",
		Data: []any{map[string]any{"close": 1}}, HTTPCode: http.StatusOK,
	}
	emptyFallback := observationResult{
		Status: envelope.ObservationStatusEmpty, Provider: "market-hub", Endpoint: "kline",
		Data: []any{}, HTTPCode: http.StatusOK,
	}
	errorFallback := observationResult{
		Status: envelope.ObservationStatusError, Provider: "market-hub", Endpoint: "kline",
		Failure: fallbackFailure, HTTPCode: http.StatusBadGateway,
	}

	tests := []struct {
		name              string
		primary, fallback observationResult
		wantStatus        envelope.ObservationStatus
		wantDependency    string
		wantNonEmptyData  bool
	}{
		{"empty + usable fallback", emptyPrimary, okFallback, envelope.ObservationStatusOK, "", true},
		{"empty + failed fallback", emptyPrimary, errorFallback, envelope.ObservationStatusEmpty, "", false},
		{"failed + usable fallback", errorPrimary, okFallback, envelope.ObservationStatusDegraded, "warehouse", true},
		{"failed + empty fallback", errorPrimary, emptyFallback, envelope.ObservationStatusError, "warehouse", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := applyFallback(test.primary, test.fallback)
			if got.Status != test.wantStatus {
				t.Fatalf("status=%s want=%s", got.Status, test.wantStatus)
			}
			dependency := ""
			if got.Failure != nil {
				dependency = got.Failure.Dependency
			}
			if dependency != test.wantDependency {
				t.Fatalf("dependency=%q want=%q", dependency, test.wantDependency)
			}
			empty := true
			if got.Data != nil {
				encoded, err := json.Marshal(got.Data)
				if err != nil {
					t.Fatal(err)
				}
				empty, err = structurallyEmptyCollection(encoded)
				if err != nil {
					t.Fatal(err)
				}
			}
			if test.wantNonEmptyData == empty {
				t.Fatalf("data=%v wantNonEmpty=%v", got.Data, test.wantNonEmptyData)
			}
		})
	}
}

func TestObservationFromResultRejectsDegradedEmpty(t *testing.T) {
	_, err := observationFromResult(observationResult{
		Status: envelope.ObservationStatusDegraded, Provider: "market-hub", Endpoint: "history",
		Data: []any{}, Failure: &envelope.ObservationError{Code: "partial", Message: "fallback failed"},
	})
	if err == nil || !strings.Contains(err.Error(), "usable data") {
		t.Fatalf("error=%v, want degraded-empty rejection", err)
	}
}

func normalizedGolden(t *testing.T, body []byte) string {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	if _, ok := value["retrieved_at"]; ok {
		value["retrieved_at"] = "<time>"
	}
	if hash, ok := value["raw_hash"].(string); ok {
		if !strings.HasPrefix(hash, "sha256:") || len(hash) != len("sha256:")+64 {
			t.Fatalf("invalid raw hash %q", hash)
		}
		value["raw_hash"] = "<hash>"
	}
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}
