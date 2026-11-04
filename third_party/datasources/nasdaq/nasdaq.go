// Package nasdaq fetches and normalizes Nasdaq's public US event calendars.
//
// It is fetch-only and maintains an in-memory last-good response cache. Callers
// own request deadlines through the supplied context and HTTP client.
package nasdaq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultBaseURL          = "https://api.nasdaq.com"
	DefaultMaxResponseBytes = int64(4 << 20)
	DefaultCacheTTL         = 30 * time.Minute
)

type EventType string

const (
	EventEarnings EventType = "earnings"
	EventDividend EventType = "dividend"
	EventSplit    EventType = "split"
)

type Session string

const (
	SessionBeforeMarket Session = "before_market"
	SessionAfterMarket  Session = "after_market"
	SessionUnknown      Session = "unknown"
)

// SplitRatio is Nasdaq's displayed ratio, expressed as numerator:denominator.
type SplitRatio struct {
	Numerator   float64 `json:"numerator"`
	Denominator float64 `json:"denominator"`
}

// Event is the stable, normalized form shared by all three calendars. EPS is
// Nasdaq's earnings forecast, Amount is a dividend amount, and SplitRatio is
// populated only for split events. Raw retains the source row for auditability.
type Event struct {
	Type       EventType       `json:"type"`
	Symbol     string          `json:"symbol"`
	Company    string          `json:"company"`
	EventDate  time.Time       `json:"event_date"`
	Session    Session         `json:"session"`
	EPS        *float64        `json:"eps,omitempty"`
	Amount     *float64        `json:"amount,omitempty"`
	SplitRatio *SplitRatio     `json:"split_ratio,omitempty"`
	Raw        json.RawMessage `json:"raw,omitempty"`
	Source     string          `json:"source"`
	SourceURL  string          `json:"source_url"`
	FetchedAt  time.Time       `json:"fetched_at"`
	Estimated  bool            `json:"estimated"`
	Provenance string          `json:"provenance,omitempty"`
	DataStatus EndpointOutcome `json:"data_status,omitempty"`
	CachedAt   *time.Time      `json:"cached_at,omitempty"`
}

// SchemaDriftError reports an incompatible Nasdaq response shape. Endpoint and
// Path identify the low-cardinality source location; Detail describes the
// expected shape. Wrapped decode errors remain available through errors.As.
type SchemaDriftError struct {
	Endpoint string
	Path     string
	Detail   string
	Err      error
}

func (e *SchemaDriftError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("schema drift at %s %s: %s: %v", e.Endpoint, e.Path, e.Detail, e.Err)
	}
	return fmt.Sprintf("schema drift at %s %s: %s", e.Endpoint, e.Path, e.Detail)
}

func (e *SchemaDriftError) Unwrap() error { return e.Err }

type EndpointOutcome string

const (
	EndpointNetworkSuccess EndpointOutcome = "network_success"
	EndpointCacheHit       EndpointOutcome = "cache_hit"
	EndpointCachedFallback EndpointOutcome = "cached_fallback"
	EndpointFailure        EndpointOutcome = "failure"
)

type EndpointFailureKind string

const (
	EndpointFailureNone        EndpointFailureKind = ""
	EndpointFailureRequest     EndpointFailureKind = "request"
	EndpointFailureSchemaDrift EndpointFailureKind = "schema_drift"
)

// EndpointStatus contains only bounded labels, suitable for counters.
type EndpointStatus struct {
	Endpoint string
	Outcome  EndpointOutcome
	Failure  EndpointFailureKind
}

type cacheEntry struct {
	events   []Event
	storedAt time.Time
}

// HTTPDoer is implemented by *http.Client. Its timeout and transport settings
// are used unchanged.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Client struct {
	httpClient       HTTPDoer
	baseURL          string
	maxResponseBytes int64
	now              func() time.Time
	location         *time.Location
	cacheTTL         time.Duration
	observer         func(EndpointStatus)
	cacheMu          sync.Mutex
	cache            map[string]cacheEntry
}

type Option func(*Client)

// WithMaxResponseBytes changes the maximum accepted body size per endpoint.
func WithMaxResponseBytes(n int64) Option {
	return func(c *Client) {
		if n > 0 {
			c.maxResponseBytes = n
		}
	}
}

// WithClock is primarily useful when consumers need deterministic fetched-at
// values in tests.
func WithClock(now func() time.Time) Option {
	return func(c *Client) {
		if now != nil {
			c.now = now
		}
	}
}

// WithCacheTTL changes how long a successful endpoint response is served
// without a network request. A zero duration disables fresh cache hits while
// retaining last-good entries for failure fallback.
func WithCacheTTL(ttl time.Duration) Option {
	return func(c *Client) {
		if ttl >= 0 {
			c.cacheTTL = ttl
		}
	}
}

// WithEndpointObserver installs a low-cardinality endpoint outcome callback.
// The callback may be invoked concurrently by FetchCalendar.
func WithEndpointObserver(observer func(EndpointStatus)) Option {
	return func(c *Client) {
		c.observer = observer
	}
}

// NewClient constructs a calendar client. A nil HTTP client uses
// http.DefaultClient; baseURL may be empty to use Nasdaq's production API.
func NewClient(httpClient HTTPDoer, baseURL string, options ...Option) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		// Go installations include tzdata on supported Nimbus hosts. Keeping a
		// fixed fallback is safer than creating dates in the machine timezone.
		location = time.FixedZone("America/New_York", -5*60*60)
	}
	client := &Client{
		httpClient:       httpClient,
		baseURL:          strings.TrimRight(baseURL, "/"),
		maxResponseBytes: DefaultMaxResponseBytes,
		now:              time.Now,
		location:         location,
		cacheTTL:         DefaultCacheTTL,
		cache:            make(map[string]cacheEntry),
	}
	for _, option := range options {
		option(client)
	}
	return client
}

// FetchCalendar fetches all calendars independently. Successful endpoint and
// row results are returned even when other requests or rows fail; the returned
// error joins all failures.
func (c *Client) FetchCalendar(ctx context.Context, date string) ([]Event, error) {
	type result struct {
		index  int
		events []Event
		err    error
	}
	fetchers := []struct {
		name string
		fn   func(context.Context, string) ([]Event, error)
	}{
		{name: "earnings", fn: c.FetchEarnings},
		{name: "dividends", fn: c.FetchDividends},
		{name: "splits", fn: c.FetchSplits},
	}

	results := make(chan result, len(fetchers))
	for i, fetcher := range fetchers {
		go func(index int, name string, fetch func(context.Context, string) ([]Event, error)) {
			events, err := fetch(ctx, date)
			if err != nil {
				err = fmt.Errorf("nasdaq %s: %w", name, err)
			}
			results <- result{index: index, events: events, err: err}
		}(i, fetcher.name, fetcher.fn)
	}

	ordered := make([][]Event, len(fetchers))
	orderedErrors := make([]error, len(fetchers))
	for range fetchers {
		result := <-results
		ordered[result.index] = result.events
		orderedErrors[result.index] = result.err
	}
	var events []Event
	var errs []error
	for _, endpointEvents := range ordered {
		events = append(events, endpointEvents...)
	}
	for _, err := range orderedErrors {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return events, errors.Join(errs...)
}

func (c *Client) FetchEarnings(ctx context.Context, date string) ([]Event, error) {
	return c.fetch(ctx, EventEarnings, "earnings", date)
}

func (c *Client) FetchDividends(ctx context.Context, date string) ([]Event, error) {
	return c.fetch(ctx, EventDividend, "dividends", date)
}

func (c *Client) FetchSplits(ctx context.Context, date string) ([]Event, error) {
	return c.fetch(ctx, EventSplit, "splits", date)
}

func (c *Client) fetch(ctx context.Context, eventType EventType, endpoint, date string) ([]Event, error) {
	requestDate, err := time.ParseInLocation("2006-01-02", date, c.location)
	if err != nil {
		return nil, fmt.Errorf("invalid date %q (want YYYY-MM-DD): %w", date, err)
	}
	cacheKey := endpoint + "\x00" + requestDate.Format("2006-01-02")
	if events, ok := c.freshCache(cacheKey); ok {
		c.observe(EndpointStatus{Endpoint: endpoint, Outcome: EndpointCacheHit})
		return events, nil
	}
	sourceURL := c.baseURL + "/api/calendar/" + endpoint + "?date=" + url.QueryEscape(date)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return c.fallback(cacheKey, endpoint, err)
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Referer", "https://www.nasdaq.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return c.fallback(cacheKey, endpoint, err)
	}
	defer resp.Body.Close()
	body, err := readBounded(resp.Body, c.maxResponseBytes)
	if err != nil {
		return c.fallback(cacheKey, endpoint, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return c.fallback(cacheKey, endpoint, fmt.Errorf("http %d: %s", resp.StatusCode, truncate(body, 200)))
	}
	rows, err := extractRows(endpoint, body)
	if err != nil {
		return c.fallback(cacheKey, endpoint, err)
	}

	fetchedAt := c.now().UTC()
	events := make([]Event, 0, len(rows))
	var rowErrors []error
	for i, raw := range rows {
		event, err := c.normalize(eventType, endpoint, i, requestDate, sourceURL, fetchedAt, raw)
		if err != nil {
			rowErrors = append(rowErrors, fmt.Errorf("row %d: %w", i, err))
			continue
		}
		events = append(events, event)
	}
	if rowErr := errors.Join(rowErrors...); rowErr != nil {
		var drift *SchemaDriftError
		if errors.As(rowErr, &drift) && c.hasCache(cacheKey) {
			return c.fallback(cacheKey, endpoint, rowErr)
		}
		c.observe(EndpointStatus{Endpoint: endpoint, Outcome: EndpointFailure, Failure: failureKind(rowErr)})
		return events, rowErr
	}
	c.storeCache(cacheKey, events, fetchedAt)
	c.observe(EndpointStatus{Endpoint: endpoint, Outcome: EndpointNetworkSuccess})
	return cloneEvents(events), nil
}

func (c *Client) normalize(eventType EventType, endpoint string, rowIndex int, requestDate time.Time, sourceURL string, fetchedAt time.Time, raw json.RawMessage) (Event, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return Event{}, &SchemaDriftError{
			Endpoint: endpoint,
			Path:     fmt.Sprintf("data.rows[%d]", rowIndex),
			Detail:   "expected object row",
			Err:      err,
		}
	}
	rawSymbol, ok := lookup(fields, "symbol", "ticker")
	if !ok || string(rawSymbol) == "null" {
		return Event{}, &SchemaDriftError{
			Endpoint: endpoint,
			Path:     fmt.Sprintf("data.rows[%d].symbol", rowIndex),
			Detail:   "missing symbol: required string or number field",
		}
	}
	symbol, ok := requiredString(rawSymbol)
	if !ok || cleanText(symbol) == "" {
		return Event{}, &SchemaDriftError{
			Endpoint: endpoint,
			Path:     fmt.Sprintf("data.rows[%d].symbol", rowIndex),
			Detail:   "expected non-empty string or number",
		}
	}
	symbol = cleanText(symbol)
	if symbol == "" {
		return Event{}, errors.New("missing symbol")
	}
	if !strings.HasPrefix(strings.ToUpper(symbol), "US:") {
		symbol = "US:" + strings.ToUpper(strings.TrimPrefix(symbol, "$"))
	} else {
		symbol = strings.ToUpper(symbol)
	}
	event := Event{
		Type:      eventType,
		Symbol:    symbol,
		Company:   cleanText(stringField(fields, "companyName", "name", "company")),
		EventDate: requestDate,
		Session:   SessionUnknown,
		Raw:       append(json.RawMessage(nil), raw...),
		Source:    "nasdaq",
		SourceURL: sourceURL,
		FetchedAt: fetchedAt,
	}

	switch eventType {
	case EventEarnings:
		event.Session = parseSession(stringField(fields, "time", "session", "when"))
		event.EPS = parseNumberField(fields, "epsForecast", "eps", "consensusEPS")
		event.Estimated = true
		event.Provenance = "Nasdaq earnings calendar dates are algorithmic estimates and are not company-confirmed."
	case EventDividend:
		event.Amount = parseNumberField(fields, "amount", "dividendAmount", "cashAmount", "dividend_Rate")
		if value := stringField(fields, "exOrEffDate", "exDate", "eventDate", "date", "dividend_Ex_Date"); cleanText(value) != "" {
			parsed, err := c.parseEventDate(value)
			if err != nil {
				return Event{}, fmt.Errorf("dividend date: %w", err)
			}
			event.EventDate = parsed
		}
	case EventSplit:
		if value := stringField(fields, "executionDate", "exOrEffDate", "eventDate", "date"); cleanText(value) != "" {
			parsed, err := c.parseEventDate(value)
			if err != nil {
				return Event{}, fmt.Errorf("split date: %w", err)
			}
			event.EventDate = parsed
		}
		event.SplitRatio = parseSplitRatio(stringField(fields, "ratio", "splitRatio"))
	}
	return event, nil
}

func (c *Client) parseEventDate(value string) (time.Time, error) {
	value = cleanText(value)
	// "1/2/2006" 同时兼容单/双数字月日("8/21/2026" 与 "08/21/2026"),
	// 故置于 "01/02/2006" 之前。
	for _, layout := range []string{"1/2/2006", "2006-01-02", "Jan 2, 2006", "January 2, 2006"} {
		if parsed, err := time.ParseInLocation(layout, value, c.location); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported value %q", value)
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return body, nil
}

func extractRows(endpoint string, body []byte) ([]json.RawMessage, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, &SchemaDriftError{
			Endpoint: endpoint,
			Path:     "$",
			Detail:   "expected JSON object envelope",
			Err:      err,
		}
	}
	container := root
	if data, ok := lookup(root, "data"); ok {
		if string(data) == "null" {
			return nil, nil
		}
		if err := json.Unmarshal(data, &container); err != nil {
			return nil, &SchemaDriftError{
				Endpoint: endpoint,
				Path:     "data",
				Detail:   "expected object",
				Err:      err,
			}
		}
	}
	rawRows, ok := lookup(container, "rows")
	if !ok {
		// Nasdaq dividends 端点(2026-08 实测):rows 嵌套在 data.calendar 下。
		if calendar, found := lookup(container, "calendar"); found {
			var cal map[string]json.RawMessage
			if json.Unmarshal(calendar, &cal) == nil {
				rawRows, ok = lookup(cal, "rows")
			}
		}
	}
	if !ok {
		return nil, &SchemaDriftError{
			Endpoint: endpoint,
			Path:     "data.rows",
			Detail:   "required array field missing",
		}
	}
	if string(rawRows) == "null" {
		return nil, nil
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(rawRows, &rows); err != nil {
		return nil, &SchemaDriftError{
			Endpoint: endpoint,
			Path:     "data.rows",
			Detail:   "expected array",
			Err:      err,
		}
	}
	return rows, nil
}

func requiredString(raw json.RawMessage) (string, bool) {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value, true
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String(), true
	}
	return "", false
}

func (c *Client) freshCache(key string) ([]Event, bool) {
	now := c.now()
	c.cacheMu.Lock()
	entry, ok := c.cache[key]
	c.cacheMu.Unlock()
	if !ok || c.cacheTTL <= 0 || now.Sub(entry.storedAt) >= c.cacheTTL {
		return nil, false
	}
	events := cloneEvents(entry.events)
	for i := range events {
		events[i].DataStatus = EndpointCacheHit
	}
	return events, true
}

func (c *Client) storeCache(key string, events []Event, storedAt time.Time) {
	c.cacheMu.Lock()
	c.cache[key] = cacheEntry{events: cloneEvents(events), storedAt: storedAt}
	c.cacheMu.Unlock()
}

func (c *Client) hasCache(key string) bool {
	c.cacheMu.Lock()
	_, ok := c.cache[key]
	c.cacheMu.Unlock()
	return ok
}

func (c *Client) fallback(key, endpoint string, cause error) ([]Event, error) {
	c.cacheMu.Lock()
	entry, ok := c.cache[key]
	c.cacheMu.Unlock()
	if !ok {
		c.observe(EndpointStatus{Endpoint: endpoint, Outcome: EndpointFailure, Failure: failureKind(cause)})
		return nil, cause
	}
	events := cloneEvents(entry.events)
	cachedAt := entry.storedAt.UTC()
	for i := range events {
		events[i].DataStatus = EndpointCachedFallback
		events[i].CachedAt = timePointer(cachedAt)
	}
	c.observe(EndpointStatus{
		Endpoint: endpoint,
		Outcome:  EndpointCachedFallback,
		Failure:  failureKind(cause),
	})
	fallbackErr := fmt.Errorf("nasdaq %s using cached fallback from %s", endpoint, cachedAt.Format(time.RFC3339))
	return events, errors.Join(cause, fallbackErr)
}

func failureKind(err error) EndpointFailureKind {
	var drift *SchemaDriftError
	if errors.As(err, &drift) {
		return EndpointFailureSchemaDrift
	}
	return EndpointFailureRequest
}

func (c *Client) observe(status EndpointStatus) {
	if c.observer != nil {
		c.observer(status)
	}
}

func cloneEvents(events []Event) []Event {
	if events == nil {
		return nil
	}
	cloned := make([]Event, len(events))
	for i, event := range events {
		cloned[i] = event
		cloned[i].Raw = append(json.RawMessage(nil), event.Raw...)
		if event.EPS != nil {
			value := *event.EPS
			cloned[i].EPS = &value
		}
		if event.Amount != nil {
			value := *event.Amount
			cloned[i].Amount = &value
		}
		if event.SplitRatio != nil {
			value := *event.SplitRatio
			cloned[i].SplitRatio = &value
		}
		if event.CachedAt != nil {
			value := *event.CachedAt
			cloned[i].CachedAt = &value
		}
	}
	return cloned
}

func timePointer(value time.Time) *time.Time { return &value }

func lookup(fields map[string]json.RawMessage, names ...string) (json.RawMessage, bool) {
	for _, name := range names {
		for key, value := range fields {
			if strings.EqualFold(key, name) {
				return value, true
			}
		}
	}
	return nil, false
}

func stringField(fields map[string]json.RawMessage, names ...string) string {
	raw, ok := lookup(fields, names...)
	if !ok || string(raw) == "null" {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String()
	}
	return ""
}

func parseNumberField(fields map[string]json.RawMessage, names ...string) *float64 {
	raw, ok := lookup(fields, names...)
	if !ok || string(raw) == "null" {
		return nil
	}
	var number float64
	if json.Unmarshal(raw, &number) == nil {
		return &number
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	value = cleanText(value)
	switch strings.ToUpper(value) {
	case "", "N/A", "NA", "--", "NULL", "NONE":
		return nil
	}
	negative := strings.HasPrefix(value, "(") && strings.HasSuffix(value, ")")
	value = strings.Trim(value, "()")
	value = strings.ReplaceAll(value, ",", "")
	value = strings.TrimSpace(strings.TrimLeft(value, "$€£¥"))
	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return nil
	}
	if negative {
		number = -number
	}
	return &number
}

func parseSplitRatio(value string) *SplitRatio {
	value = cleanText(value)
	if value == "" || strings.EqualFold(value, "N/A") || value == "--" {
		return nil
	}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ':' || r == '/' || unicodeDash(r)
	})
	if len(parts) != 2 {
		return nil
	}
	numerator, err1 := strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(parts[0], ",", "")), 64)
	denominator, err2 := strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(parts[1], ",", "")), 64)
	if err1 != nil || err2 != nil || numerator <= 0 || denominator <= 0 {
		return nil
	}
	return &SplitRatio{Numerator: numerator, Denominator: denominator}
}

func unicodeDash(r rune) bool {
	return r == '-' || r == '–' || r == '—'
}

func parseSession(value string) Session {
	value = strings.ToLower(strings.NewReplacer("_", " ", "-", " ").Replace(cleanText(value)))
	switch {
	case strings.Contains(value, "pre market"), strings.Contains(value, "before market"), strings.Contains(value, "bmo"):
		return SessionBeforeMarket
	case strings.Contains(value, "after market"), strings.Contains(value, "after hours"),
		strings.Contains(value, "post market"), strings.Contains(value, "amc"):
		return SessionAfterMarket
	default:
		return SessionUnknown
	}
}

func cleanText(value string) string {
	return strings.TrimSpace(strings.ReplaceAll(value, "\u00a0", " "))
}

func truncate(body []byte, limit int) string {
	if len(body) > limit {
		return string(body[:limit])
	}
	return string(body)
}
