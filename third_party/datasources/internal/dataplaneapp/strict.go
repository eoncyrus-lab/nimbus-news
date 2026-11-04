package dataplaneapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/envelope"
	"github.com/Ricaardo/nimbus-os/datasources/market"
	"github.com/Ricaardo/nimbus-os/datasources/market/hub"
	"github.com/Ricaardo/nimbus-os/datasources/yahoo"
)

// observationResult is the internal producer contract shared by strict and
// legacy adapters. Status is assigned by the producer; adapters never infer a
// dependency failure from whether Data happens to be empty.
type observationResult struct {
	Status   envelope.ObservationStatus
	Provider string
	Endpoint string
	Params   map[string]any
	Data     any
	Failure  *envelope.ObservationError
	HTTPCode int
}

type coreProducers struct {
	quote   func(context.Context, string) (*yahoo.Quote, error)
	history func(context.Context, string, int) ([]yahoo.Bar, error)
}

func defaultCoreProducers() coreProducers {
	svc := market.NewMarketService()
	return coreProducers{
		quote: func(ctx context.Context, symbol string) (*yahoo.Quote, error) {
			q, err := svc.GetMarketQuote(ctx, symbol)
			if err != nil {
				return nil, err
			}
			return &yahoo.Quote{
				Symbol: q.Symbol, Name: q.Name, Last: q.Price,
				PrevClose: q.PrevClose, Open: q.Open, High: q.High,
				Low: q.Low, Volume: q.Volume, Currency: q.Currency,
				Exchange: q.Exchange, Time: q.UpdateTime.Unix(),
			}, nil
		},
		history: func(ctx context.Context, symbol string, limit int) ([]yahoo.Bar, error) {
			klines, err := svc.GetKLines(ctx, symbol, "1d", limit)
			if err != nil {
				return nil, err
			}
			bars := make([]yahoo.Bar, len(klines))
			for i, k := range klines {
				bars[i] = yahoo.Bar{
					Date: k.Time.Format("2006-01-02"), Open: k.Open, High: k.High,
					Low: k.Low, Close: k.Close, Volume: k.Volume,
				}
			}
			return bars, nil
		},
	}
}

func produceQuote(ctx context.Context, producers coreProducers, symbol string) observationResult {
	params := map[string]any{"symbol": symbol}
	if strings.TrimSpace(symbol) == "" {
		return failedResult("yahoo", "chart.quote", params, http.StatusBadRequest,
			"invalid_request", "symbol required", "", false)
	}
	quote, err := producers.quote(ctx, symbol)
	if err != nil {
		return dependencyFailure("yahoo", "chart.quote", params, "yahoo", err)
	}
	if quote == nil {
		return failedResult("yahoo", "chart.quote", params, http.StatusBadGateway,
			"dependency_invalid_response", "yahoo returned no quote", "yahoo", true)
	}
	return observationResult{
		Status: envelope.ObservationStatusOK, Provider: "yahoo", Endpoint: "chart.quote",
		Params: params, Data: quote, HTTPCode: http.StatusOK,
	}
}

// strictQuote is the shared /v1/quote row on both listeners. The data listener
// adapts Yahoo into this facade shape; the facade listener decodes its market
// hub rows into the same type, so strict callers always receive a list.
type strictQuote struct {
	Symbol    string  `json:"symbol"`
	Name      string  `json:"name"`
	Last      float64 `json:"last"`
	PrevClose float64 `json:"prev_close"`
	Open      float64 `json:"open"`
	High      float64 `json:"high"`
	Low       float64 `json:"low"`
	Volume    int64   `json:"volume"`
	Change    float64 `json:"change"`
	ChangePct float64 `json:"change_pct"`
	AssetType string  `json:"asset_type"`
	Provider  string  `json:"provider"`
	Timestamp string  `json:"ts"`
}

func produceStrictQuotes(ctx context.Context, producers coreProducers, rawSymbols string) observationResult {
	symbols := splitSymbols(rawSymbols)
	params := map[string]any{"symbols": strings.Join(symbols, ",")}
	if len(symbols) == 0 {
		return failedResult("market-hub", "quote", params, http.StatusBadRequest,
			"invalid_request", "symbols required", "", false)
	}
	rows := make([]strictQuote, 0, len(symbols))
	issues := make([]string, 0)
	for _, canonical := range symbols {
		quote, err := producers.quote(ctx, hub.ToHubSymbol(canonical))
		if err != nil {
			issues = append(issues, canonical+": "+err.Error())
			continue
		}
		if quote == nil {
			issues = append(issues, canonical+": yahoo returned no quote")
			continue
		}
		change := 0.0
		changePct := 0.0
		if quote.PrevClose != 0 {
			change = quote.Last - quote.PrevClose
			changePct = change / quote.PrevClose * 100
		}
		rows = append(rows, strictQuote{
			Symbol: canonical, Name: quote.Name, Last: quote.Last, PrevClose: quote.PrevClose,
			Open: quote.Open, High: quote.High, Low: quote.Low, Volume: quote.Volume,
			Change: change, ChangePct: changePct, AssetType: "stock", Provider: "yahoo",
			Timestamp: time.Unix(quote.Time, 0).UTC().Format(time.RFC3339),
		})
	}
	if len(issues) != 0 {
		failure := &envelope.ObservationError{
			Code: "dependency_partial_failure", Message: strings.Join(issues, "; "),
			Dependency: "yahoo", Retryable: true,
		}
		if len(rows) == 0 {
			return observationResult{
				Status: envelope.ObservationStatusError, Provider: "market-hub", Endpoint: "quote",
				Params: params, Failure: failure, HTTPCode: http.StatusBadGateway,
			}
		}
		return observationResult{
			Status: envelope.ObservationStatusDegraded, Provider: "market-hub", Endpoint: "quote",
			Params: params, Data: rows, Failure: failure, HTTPCode: http.StatusOK,
		}
	}
	return observationResult{
		Status: envelope.ObservationStatusOK, Provider: "market-hub", Endpoint: "quote",
		Params: params, Data: rows, HTTPCode: http.StatusOK,
	}
}

func splitSymbols(raw string) []string {
	parts := strings.Split(raw, ",")
	symbols := make([]string, 0, len(parts))
	for _, part := range parts {
		if symbol := strings.TrimSpace(part); symbol != "" {
			symbols = append(symbols, symbol)
		}
	}
	return symbols
}

func produceHistory(ctx context.Context, producers coreProducers, symbol string, limit int) observationResult {
	params := map[string]any{"symbol": symbol, "limit": limit}
	if strings.TrimSpace(symbol) == "" {
		return failedResult("yahoo", "chart.history", params, http.StatusBadRequest,
			"invalid_request", "symbol required", "", false)
	}
	bars, err := producers.history(ctx, symbol, limit)
	if err != nil {
		return dependencyFailure("yahoo", "chart.history", params, "yahoo", err)
	}
	if bars == nil {
		bars = []yahoo.Bar{}
	}
	status := envelope.ObservationStatusOK
	if len(bars) == 0 {
		status = envelope.ObservationStatusEmpty
	}
	return observationResult{
		Status: status, Provider: "yahoo", Endpoint: "chart.history",
		Params: params, Data: bars, HTTPCode: http.StatusOK,
	}
}

func dependencyFailure(provider, endpoint string, params map[string]any, dependency string, err error) observationResult {
	return failedResult(provider, endpoint, params, http.StatusBadGateway,
		"dependency_failure", err.Error(), dependency, true)
}

func failedResult(provider, endpoint string, params map[string]any, httpCode int, code, message, dependency string, retryable bool) observationResult {
	return observationResult{
		Status: envelope.ObservationStatusError, Provider: provider, Endpoint: endpoint,
		Params: params, HTTPCode: httpCode,
		Failure: &envelope.ObservationError{
			Code: code, Message: message, Dependency: dependency, Retryable: retryable,
		},
	}
}

func handleLegacyQuote(producers coreProducers, w http.ResponseWriter, r *http.Request) {
	c, cancel := ctx(r)
	defer cancel()
	writeLegacyResult(w, produceQuote(c, producers, r.URL.Query().Get("symbol")))
}

func handleLegacyHistory(producers coreProducers, w http.ResponseWriter, r *http.Request) {
	symbol := r.URL.Query().Get("symbol")
	limit := atoiDefault(r.URL.Query().Get("limit"), 120)
	c, cancel := ctx(r)
	defer cancel()
	writeLegacyResult(w, produceHistory(c, producers, symbol, limit))
}

func handleStrictQuote(producers coreProducers, w http.ResponseWriter, r *http.Request) {
	c, cancel := ctx(r)
	defer cancel()
	writeStrictResult(w, produceStrictQuotes(c, producers, r.URL.Query().Get("symbols")))
}

func handleStrictHistory(producers coreProducers, w http.ResponseWriter, r *http.Request) {
	symbol := r.URL.Query().Get("symbol")
	limit := atoiDefault(r.URL.Query().Get("limit"), 120)
	c, cancel := ctx(r)
	defer cancel()
	writeStrictResult(w, produceHistory(c, producers, symbol, limit))
}

func writeLegacyResult(w http.ResponseWriter, result observationResult) {
	if result.Status == envelope.ObservationStatusError {
		writeErr(w, result.HTTPCode, result.Failure.Message)
		return
	}
	writeEnv(w, envelope.Wrap(result.Provider, result.Endpoint, result.Params, result.Data))
}

func writeStrictResult(w http.ResponseWriter, result observationResult) {
	observation, err := observationFromResult(result)
	if err != nil {
		result = failedResult(result.Provider, result.Endpoint, result.Params, http.StatusInternalServerError,
			"internal_encoding_error", "failed to encode observation", "", false)
		observation, _ = observationFromResult(result)
	}
	payload, err := json.Marshal(observation)
	if err != nil {
		// The fallback observation contains strings/maps only. Keep the HTTP
		// status non-success even if an unexpected custom marshaler failed.
		writeErr(w, http.StatusInternalServerError, "failed to encode observation")
		return
	}
	code := http.StatusOK
	if result.Status == envelope.ObservationStatusError {
		code = result.HTTPCode
		if code < 400 {
			code = http.StatusInternalServerError
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write(append(payload, '\n'))
}

func observationFromResult(result observationResult) (any, error) {
	switch result.Status {
	case envelope.ObservationStatusOK:
		return envelope.NewSuccess(result.Provider, result.Endpoint, result.Params, result.Data)
	case envelope.ObservationStatusEmpty:
		return envelope.NewEmpty(result.Provider, result.Endpoint, result.Params, result.Data)
	case envelope.ObservationStatusDegraded:
		if result.Failure == nil {
			return nil, fmt.Errorf("degraded result requires failure")
		}
		encoded, err := json.Marshal(result.Data)
		if err != nil {
			return nil, err
		}
		empty, err := structurallyEmptyCollection(encoded)
		if err != nil {
			return nil, err
		}
		if empty {
			return nil, fmt.Errorf("degraded result requires usable data")
		}
		return envelope.NewDegraded(result.Provider, result.Endpoint, result.Params, result.Data, *result.Failure)
	case envelope.ObservationStatusError:
		if result.Failure == nil {
			return nil, fmt.Errorf("failed result requires failure")
		}
		return envelope.NewFailure[any](result.Provider, result.Endpoint, result.Params, *result.Failure)
	default:
		return nil, fmt.Errorf("unsupported result status %q", result.Status)
	}
}

// decodeFacadeResult adapts one known facade response to an explicit status.
// Scalars such as 0, false, and "" are usable values; only structurally empty
// collections are empty. A producer-reported error is never reclassified as a
// successful empty collection.
func decodeFacadeResult(code int, body []byte, provider, endpoint, dependency string, params map[string]any) observationResult {
	if code < 200 || code >= 300 {
		message := facadeErrorMessage(body)
		if message == "" {
			message = fmt.Sprintf("%s returned HTTP %d", dependency, code)
		}
		mappedCode := http.StatusBadGateway
		errorCode := "dependency_failure"
		retryable := true
		if code >= 400 && code < 500 {
			mappedCode = code
			errorCode = "invalid_request"
			retryable = false
		}
		return failedResult(provider, endpoint, params, mappedCode, errorCode, message, dependency, retryable)
	}

	var wire struct {
		Data       json.RawMessage   `json:"data"`
		Error      json.RawMessage   `json:"error"`
		Errors     map[string]string `json:"errors"`
		Provenance struct {
			Error string `json:"error"`
		} `json:"provenance"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return failedResult(provider, endpoint, params, http.StatusBadGateway,
			"dependency_invalid_response", "dependency returned invalid JSON", dependency, true)
	}
	if len(wire.Data) == 0 || bytes.Equal(bytes.TrimSpace(wire.Data), []byte("null")) {
		return failedResult(provider, endpoint, params, http.StatusBadGateway,
			"dependency_invalid_response", "dependency response has no usable data field", dependency, true)
	}

	issue := facadeWireIssue(wire.Error, wire.Errors, wire.Provenance.Error)
	empty, err := structurallyEmptyCollection(wire.Data)
	if err != nil {
		return failedResult(provider, endpoint, params, http.StatusBadGateway,
			"dependency_invalid_response", "dependency data field is invalid JSON", dependency, true)
	}
	data := append(json.RawMessage(nil), wire.Data...)
	if issue != "" {
		if empty {
			return failedResult(provider, endpoint, params, http.StatusBadGateway,
				"dependency_failure", issue, dependency, true)
		}
		return observationResult{
			Status: envelope.ObservationStatusDegraded, Provider: provider, Endpoint: endpoint,
			Params: params, Data: data, HTTPCode: http.StatusOK,
			Failure: &envelope.ObservationError{
				Code: "dependency_partial_failure", Message: issue, Dependency: dependency, Retryable: true,
			},
		}
	}
	status := envelope.ObservationStatusOK
	if empty {
		status = envelope.ObservationStatusEmpty
	}
	return observationResult{
		Status: status, Provider: provider, Endpoint: endpoint,
		Params: params, Data: data, HTTPCode: http.StatusOK,
	}
}

func normalizeStrictQuoteResult(result observationResult) observationResult {
	if result.Status == envelope.ObservationStatusError {
		return result
	}
	raw, ok := result.Data.(json.RawMessage)
	if !ok {
		return failedResult(result.Provider, result.Endpoint, result.Params, http.StatusInternalServerError,
			"internal_contract_error", "strict quote adapter received unexpected data type", "", false)
	}
	var rows []strictQuote
	if err := json.Unmarshal(raw, &rows); err != nil || rows == nil {
		return failedResult(result.Provider, result.Endpoint, result.Params, http.StatusBadGateway,
			"dependency_invalid_response", "quote data must be an array", "market-hub", true)
	}
	result.Data = rows
	return result
}

func facadeErrorMessage(body []byte) string {
	var wire struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(body, &wire) != nil || wire.Error == nil {
		return ""
	}
	switch value := wire.Error.(type) {
	case string:
		return value
	default:
		payload, err := json.Marshal(value)
		if err != nil {
			return ""
		}
		return string(payload)
	}
}

func facadeWireIssue(rawError json.RawMessage, errorsByKey map[string]string, provenanceError string) string {
	parts := make([]string, 0, len(errorsByKey)+2)
	if len(rawError) != 0 && !bytes.Equal(bytes.TrimSpace(rawError), []byte("null")) {
		if message := facadeErrorMessage([]byte(`{"error":` + string(rawError) + `}`)); message != "" {
			parts = append(parts, message)
		}
	}
	keys := make([]string, 0, len(errorsByKey))
	for key := range errorsByKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, key+": "+errorsByKey[key])
	}
	if provenanceError != "" {
		parts = append(parts, provenanceError)
	}
	return strings.Join(parts, "; ")
}

func structurallyEmptyCollection(raw json.RawMessage) (bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return false, fmt.Errorf("empty JSON")
	}
	switch trimmed[0] {
	case '[':
		var values []json.RawMessage
		if err := json.Unmarshal(trimmed, &values); err != nil {
			return false, err
		}
		return len(values) == 0, nil
	case '{':
		var values map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &values); err != nil {
			return false, err
		}
		return len(values) == 0, nil
	default:
		var value any
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return false, err
		}
		return false, nil
	}
}

type routeFallbackPolicy struct {
	onEmpty bool
	onError bool
}

var strictHistoryFallbackPolicy = routeFallbackPolicy{onEmpty: true, onError: true}

func (p routeFallbackPolicy) allows(status envelope.ObservationStatus) bool {
	switch status {
	case envelope.ObservationStatusEmpty:
		return p.onEmpty
	case envelope.ObservationStatusError:
		return p.onError
	default:
		return false
	}
}

// applyFallback combines two already-classified producer outcomes. A recovered
// primary failure remains visible as degraded; a legitimate primary empty may
// be replaced by fallback data without inventing an error.
func applyFallback(primary, fallback observationResult) observationResult {
	switch primary.Status {
	case envelope.ObservationStatusEmpty:
		switch fallback.Status {
		case envelope.ObservationStatusOK, envelope.ObservationStatusDegraded:
			return fallback
		case envelope.ObservationStatusEmpty, envelope.ObservationStatusError:
			return primary
		}
	case envelope.ObservationStatusError:
		switch fallback.Status {
		case envelope.ObservationStatusOK:
			fallback.Status = envelope.ObservationStatusDegraded
			fallback.Failure = primary.Failure
			fallback.HTTPCode = http.StatusOK
			return fallback
		case envelope.ObservationStatusDegraded:
			return fallback
		case envelope.ObservationStatusEmpty, envelope.ObservationStatusError:
			return primary
		}
	}
	return primary
}
