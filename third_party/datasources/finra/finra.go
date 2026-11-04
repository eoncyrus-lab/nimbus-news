// Package finra is the independent FINRA data source: the RegSHO daily
// consolidated short-volume files. Fetch-only and stateless; callers cache and
// aggregate across days themselves.
//
// No API key (a descriptive User-Agent is courteous and used by default).
package finra

import (
	"bufio"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Ricaardo/nimbus-os/datasources/internal/httpx"
)

// BaseURL is the FINRA CDN host. It is a var (not const) so tests can point it
// at an httptest server.
var BaseURL = "https://cdn.finra.org"

// UserAgent is sent on requests; FINRA asks for a descriptive UA + contact.
var UserAgent = "nimbus-os data-gateway championdoggg@gmail.com"

// ShortVol is one symbol's short and total consolidated volume for a day.
type ShortVol struct {
	ShortVolume float64
	TotalVolume float64
}

// RegSHODaily fetches and parses one trading day's consolidated short-volume
// file (date as YYYYMMDD), keyed by uppercase symbol. It returns nil (NOT an
// error) when the file isn't available — holiday, not-yet-published, or blocked
// — matching the source's daily cadence so callers can walk back over a window.
func RegSHODaily(ctx context.Context, yyyymmdd string) (map[string]ShortVol, error) {
	url := fmt.Sprintf("%s/equity/regsho/daily/CNMSshvol%s.txt", BaseURL, yyyymmdd)
	body, err := httpx.Default.GetBytes(ctx, url, map[string]string{"User-Agent": UserAgent})
	if err != nil {
		return nil, nil // not published / holiday / blocked
	}
	rows := parseDay(string(body))
	if len(rows) == 0 {
		return nil, nil
	}
	return rows, nil
}

// parseDay parses a CNMSshvol pipe-delimited file into {symbol: ShortVol}.
// Rows whose TotalVolume is non-numeric are skipped (matching consumers that
// dropna on total); an unparseable ShortVolume contributes 0 but the row still
// counts. Duplicate symbols accumulate (groupby-sum).
func parseDay(text string) map[string]ShortVol {
	out := map[string]ShortVol{}
	sc := bufio.NewScanner(strings.NewReader(text))
	if !sc.Scan() {
		return out
	}
	header := strings.Split(strings.TrimSpace(sc.Text()), "|")
	iSym := indexOf(header, "Symbol")
	iShort := indexOf(header, "ShortVolume")
	iTotal := indexOf(header, "TotalVolume")
	if iSym < 0 || iShort < 0 || iTotal < 0 {
		return out
	}
	maxIdx := iSym
	for _, i := range []int{iShort, iTotal} {
		if i > maxIdx {
			maxIdx = i
		}
	}
	for sc.Scan() {
		parts := strings.Split(sc.Text(), "|")
		if len(parts) <= maxIdx {
			continue
		}
		sym := strings.ToUpper(strings.TrimSpace(parts[iSym]))
		if sym == "" {
			continue
		}
		total, err := strconv.ParseFloat(strings.TrimSpace(parts[iTotal]), 64)
		if err != nil {
			continue // total must be numeric
		}
		short, err := strconv.ParseFloat(strings.TrimSpace(parts[iShort]), 64)
		if err != nil {
			short = 0
		}
		v := out[sym]
		v.ShortVolume += short
		v.TotalVolume += total
		out[sym] = v
	}
	return out
}

func indexOf(cols []string, name string) int {
	for i, c := range cols {
		if strings.TrimSpace(c) == name {
			return i
		}
	}
	return -1
}
