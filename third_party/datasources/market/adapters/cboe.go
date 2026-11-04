package adapters

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/cboe"

	types "github.com/Ricaardo/nimbus-os/datasources/market/types"
)

// CBOEAdapter wraps the existing datasources/cboe package as a QuoteAdapter
// for index symbols (^VIX, ^GSPC, ^IXIC, etc.). CBOE provides delayed (15min)
// quotes for ~15 indices keyless — no API key required.
//
// It does NOT implement KLineAdapter (CBOE doesn't publish historical bars).
// K-line requests fall through to Yahoo at pri 10.
type CBOEAdapter struct {
	BaseAdapter
	enabled bool
}

// NewCBOEAdapter creates the adapter. CBOE_ENABLED=0 disables it.
func NewCBOEAdapter() *CBOEAdapter {
	return &CBOEAdapter{
		BaseAdapter: NewBaseAdapter("cboe", 5),
		enabled:     os.Getenv("CBOE_ENABLED") != "0",
	}
}

func (a *CBOEAdapter) SupportedTypes() []string {
	return []string{AssetTypeIndex}
}

// CanHandle returns true for ^-prefixed index symbols that CBOE issues quotes for.
// Symbols not in the known map still pass through — GetQuote will try and
// upstream will return a CBOE error, which falls through to the next adapter.
func (a *CBOEAdapter) CanHandle(symbol string) bool {
	if !a.enabled {
		return false
	}
	return strings.HasPrefix(strings.ToUpper(symbol), "^")
}

func (a *CBOEAdapter) GetQuote(ctx context.Context, symbol string) (*types.MarketQuote, error) {
	cboeSym := toCBOESymbol(symbol)
	q, err := cboe.GetQuote(ctx, cboeSym)
	if err != nil {
		return nil, err
	}
	change := q.PriceChange
	changePct := q.PriceChangePct
	if changePct == 0 && q.Close != 0 {
		changePct = (q.CurrentPrice - q.Close) / q.Close * 100
	}
	return &types.MarketQuote{
		Symbol:        symbol,
		Name:          symbol,
		Price:         q.CurrentPrice,
		Change:        change,
		ChangePercent: changePct,
		Open:          q.Open,
		High:          q.High,
		Low:           q.Low,
		PrevClose:     q.PrevDayClose,
		Volume:        int64(q.Volume),
		UpdateTime:    timeFromCBOE(q.LastTradeTime),
		Currency:      "USD",
		Exchange:      "CBOE",
		AssetType:     AssetTypeIndex,
	}, nil
}

// toCBOESymbol maps ^-prefixed news-internal index symbols to CBOE underscore
// symbols (e.g. ^VIX → _VIX, ^GSPC → _SPX, ^IXIC → _NDX).
func toCBOESymbol(symbol string) string {
	upper := strings.ToUpper(symbol)
	if known, ok := cboeSymbols[upper]; ok {
		return known
	}
	return "_" + strings.TrimPrefix(upper, "^")
}

// cboeSymbols maps common index symbols to their CBOE tickers.
// See https://www.cboe.com/delayed_quotes/ for the full list.
var cboeSymbols = map[string]string{
	"^VIX":  "_VIX",
	"^GSPC": "_SPX",
	"^IXIC": "_NDX",
	"^DJI":  "_DJI",
	"^RUT":  "_RUT",
}

// timeFromCBOE parses CBOE's "2024-01-15T10:30:00" last-trade-time string.
func timeFromCBOE(s string) time.Time {
	if s == "" {
		return time.Now()
	}
	t, err := time.Parse("2006-01-02T15:04:05", s)
	if err != nil {
		return time.Now()
	}
	return t
}
