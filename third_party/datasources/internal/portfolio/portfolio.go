// Package portfolio builds deterministic, versioned portfolio snapshots.
// Decimal inputs stay as strings and are evaluated with big.Rat; no binary
// floating-point values enter the aggregation path.
package portfolio

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"
)

const Version = 1

const (
	maxDecimalDigits = 128
	maxDecimalScale  = 18
)

var (
	canonicalSymbolPattern = regexp.MustCompile(`^(US:[A-Z0-9][A-Z0-9.-]*|HK:[0-9]{5}|CN:[0-9]{6}|CRYPTO:[A-Z0-9]+-[A-Z0-9]+)$`)
	currencyPattern        = regexp.MustCompile(`^[A-Z]{3}$`)
)

// Holding is one valued position in a snapshot. Quantity and UnitPrice are
// caller inputs; MarketValue and BaseValue are deterministically populated by
// BuildSnapshot.
type Holding struct {
	Version     int       `json:"version"`
	Symbol      string    `json:"symbol"`
	Quantity    string    `json:"quantity"`
	UnitPrice   string    `json:"unit_price"`
	Currency    string    `json:"currency"`
	MarketValue string    `json:"market_value"`
	BaseValue   string    `json:"base_value"`
	AsOf        time.Time `json:"as_of"`
	Provenance  []string  `json:"provenance"`
	Stale       bool      `json:"stale"`
}

// FXRate converts one unit of FromCurrency into Rate units of ToCurrency.
type FXRate struct {
	Version      int       `json:"version"`
	FromCurrency string    `json:"from_currency"`
	ToCurrency   string    `json:"to_currency"`
	Rate         string    `json:"rate"`
	AsOf         time.Time `json:"as_of"`
	Provenance   []string  `json:"provenance"`
	Stale        bool      `json:"stale"`
}

// Snapshot is sorted by symbol and currency and has a stable content-derived
// ID. Provenance is a sorted union of holding and consumed FX provenance.
type Snapshot struct {
	Version      int       `json:"version"`
	ID           string    `json:"id"`
	BaseCurrency string    `json:"base_currency"`
	AsOf         time.Time `json:"as_of"`
	Holdings     []Holding `json:"holdings"`
	TotalValue   string    `json:"total_value"`
	Provenance   []string  `json:"provenance"`
	Stale        bool      `json:"stale"`
}

// BuildSnapshot validates and values holdings in baseCurrency using direct FX
// rates. It is pure: it performs no I/O and does not mutate its inputs.
func BuildSnapshot(baseCurrency string, asOf time.Time, holdings []Holding, rates []FXRate) (Snapshot, error) {
	if err := validateCurrency("base currency", baseCurrency); err != nil {
		return Snapshot{}, err
	}
	if err := validateUTC("snapshot as_of", asOf); err != nil {
		return Snapshot{}, err
	}

	type rateKey struct{ from, to string }
	indexedRates := make(map[rateKey]FXRate, len(rates))
	for _, rate := range rates {
		if err := ValidateFXRate(rate); err != nil {
			return Snapshot{}, err
		}
		key := rateKey{rate.FromCurrency, rate.ToCurrency}
		if existing, ok := indexedRates[key]; ok {
			if existing.Rate != rate.Rate || !existing.AsOf.Equal(rate.AsOf) || existing.Stale != rate.Stale || !sameStrings(existing.Provenance, rate.Provenance) {
				return Snapshot{}, fmt.Errorf("portfolio: duplicate FX rate %s/%s conflicts", key.from, key.to)
			}
			continue
		}
		indexedRates[key] = rate
	}

	valued := make([]Holding, len(holdings))
	total := new(big.Rat)
	provenance := make(map[string]struct{})
	identities := make(map[string]struct{}, len(holdings))
	stale := false
	for index, input := range holdings {
		if err := ValidateHolding(input); err != nil {
			return Snapshot{}, err
		}
		identity := input.Symbol + "\x00" + input.Currency
		if _, exists := identities[identity]; exists {
			return Snapshot{}, fmt.Errorf("portfolio: duplicate holding identity %s/%s", input.Symbol, input.Currency)
		}
		identities[identity] = struct{}{}
		quantity, _ := parseDecimal(input.Quantity)
		price, _ := parseDecimal(input.UnitPrice)
		marketValue := new(big.Rat).Mul(quantity, price)
		baseValue := new(big.Rat).Set(marketValue)
		itemProvenance := append([]string(nil), input.Provenance...)
		itemStale := input.Stale
		if input.Currency != baseCurrency {
			rate, ok := indexedRates[rateKey{input.Currency, baseCurrency}]
			if !ok {
				return Snapshot{}, fmt.Errorf("portfolio: missing FX rate %s/%s for %s", input.Currency, baseCurrency, input.Symbol)
			}
			parsedRate, _ := parseDecimal(rate.Rate)
			baseValue.Mul(baseValue, parsedRate)
			itemProvenance = append(itemProvenance, rate.Provenance...)
			itemStale = itemStale || rate.Stale
		}
		item := input
		item.Provenance = normalizedStrings(itemProvenance)
		item.MarketValue = formatDecimal(marketValue)
		item.BaseValue = formatDecimal(baseValue)
		item.Stale = itemStale
		valued[index] = item
		total.Add(total, baseValue)
		stale = stale || itemStale
		for _, source := range item.Provenance {
			provenance[source] = struct{}{}
		}
	}
	sort.Slice(valued, func(i, j int) bool {
		if valued[i].Symbol != valued[j].Symbol {
			return valued[i].Symbol < valued[j].Symbol
		}
		return valued[i].Currency < valued[j].Currency
	})

	snapshot := Snapshot{
		Version:      Version,
		BaseCurrency: baseCurrency,
		AsOf:         asOf,
		Holdings:     valued,
		TotalValue:   formatDecimal(total),
		Provenance:   sortedSet(provenance),
		Stale:        stale,
	}
	identity, err := json.Marshal(struct {
		Version      int       `json:"version"`
		BaseCurrency string    `json:"base_currency"`
		AsOf         time.Time `json:"as_of"`
		Holdings     []Holding `json:"holdings"`
	}{snapshot.Version, snapshot.BaseCurrency, snapshot.AsOf, snapshot.Holdings})
	if err != nil {
		return Snapshot{}, fmt.Errorf("portfolio: encode snapshot identity: %w", err)
	}
	sum := sha256.Sum256(identity)
	snapshot.ID = "portfolio:sha256:" + hex.EncodeToString(sum[:])
	return snapshot, nil
}

func ValidateHolding(holding Holding) error {
	if holding.Version != Version {
		return fmt.Errorf("portfolio: holding version must be %d", Version)
	}
	if !canonicalSymbolPattern.MatchString(holding.Symbol) {
		return fmt.Errorf("portfolio: holding symbol %q is not canonical", holding.Symbol)
	}
	if err := validateCurrency("holding currency", holding.Currency); err != nil {
		return err
	}
	if err := validateUTC("holding as_of", holding.AsOf); err != nil {
		return err
	}
	quantity, err := parseDecimal(holding.Quantity)
	if err != nil {
		return fmt.Errorf("portfolio: holding quantity: %w", err)
	}
	if quantity.Sign() < 0 {
		return fmt.Errorf("portfolio: holding quantity must not be negative")
	}
	price, err := parseDecimal(holding.UnitPrice)
	if err != nil {
		return fmt.Errorf("portfolio: holding unit price: %w", err)
	}
	if price.Sign() < 0 {
		return fmt.Errorf("portfolio: holding unit price must not be negative")
	}
	if err := validateProvenance(holding.Provenance); err != nil {
		return err
	}
	return nil
}

func ValidateFXRate(rate FXRate) error {
	if rate.Version != Version {
		return fmt.Errorf("portfolio: FX rate version must be %d", Version)
	}
	if err := validateCurrency("FX from currency", rate.FromCurrency); err != nil {
		return err
	}
	if err := validateCurrency("FX to currency", rate.ToCurrency); err != nil {
		return err
	}
	if rate.FromCurrency == rate.ToCurrency {
		return fmt.Errorf("portfolio: FX currencies must differ")
	}
	value, err := parseDecimal(rate.Rate)
	if err != nil {
		return fmt.Errorf("portfolio: FX rate: %w", err)
	}
	if value.Sign() <= 0 {
		return fmt.Errorf("portfolio: FX rate must be positive")
	}
	if err := validateUTC("FX as_of", rate.AsOf); err != nil {
		return err
	}
	return validateProvenance(rate.Provenance)
}

func parseDecimal(value string) (*big.Rat, error) {
	if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "eE/") {
		return nil, fmt.Errorf("decimal %q must use plain finite notation", value)
	}
	if strings.HasPrefix(value, "+") {
		return nil, fmt.Errorf("decimal %q must not have a plus sign", value)
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" || (len(parts) == 2 && parts[1] == "") {
		return nil, fmt.Errorf("decimal %q is invalid", value)
	}
	integer := strings.TrimPrefix(parts[0], "-")
	if integer == "" || !digitsOnly(integer) || (len(parts) == 2 && !digitsOnly(parts[1])) {
		return nil, fmt.Errorf("decimal %q is invalid", value)
	}
	scale := 0
	if len(parts) == 2 {
		scale = len(parts[1])
	}
	if len(integer)+scale > maxDecimalDigits {
		return nil, fmt.Errorf("decimal exceeds %d digits", maxDecimalDigits)
	}
	if scale > maxDecimalScale {
		return nil, fmt.Errorf("decimal scale exceeds %d digits", maxDecimalScale)
	}
	parsed, ok := new(big.Rat).SetString(value)
	if !ok {
		return nil, fmt.Errorf("decimal %q is invalid", value)
	}
	return parsed, nil
}

func formatDecimal(value *big.Rat) string {
	denominator := new(big.Int).Set(value.Denom())
	two := big.NewInt(2)
	five := big.NewInt(5)
	scale := 0
	for new(big.Int).Mod(denominator, two).Sign() == 0 {
		denominator.Div(denominator, two)
		scale++
	}
	fives := 0
	for new(big.Int).Mod(denominator, five).Sign() == 0 {
		denominator.Div(denominator, five)
		fives++
	}
	if fives > scale {
		scale = fives
	}
	text := value.FloatString(scale)
	if strings.Contains(text, ".") {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	if text == "-0" {
		return "0"
	}
	return text
}

func validateCurrency(field, value string) error {
	if !currencyPattern.MatchString(value) {
		return fmt.Errorf("portfolio: %s %q must be a canonical ISO-4217 code", field, value)
	}
	return nil
}

func validateUTC(field string, value time.Time) error {
	if value.IsZero() || value.Location() != time.UTC {
		return fmt.Errorf("portfolio: %s must be a non-zero UTC timestamp", field)
	}
	return nil
}

func validateProvenance(values []string) error {
	if len(values) == 0 {
		return fmt.Errorf("portfolio: provenance is required")
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("portfolio: provenance entries must not be blank")
		}
	}
	return nil
}

func normalizedStrings(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return sortedSet(set)
}

func sortedSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func sameStrings(left, right []string) bool {
	left = normalizedStrings(left)
	right = normalizedStrings(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func digitsOnly(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
