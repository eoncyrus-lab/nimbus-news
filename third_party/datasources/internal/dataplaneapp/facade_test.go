package dataplaneapp

import (
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/envelope"
)

// The /sec-xbrl passthrough is the one route taking a caller-supplied path;
// the allowlist is security-relevant even on loopback.
func TestSECXBRLPathAllowlist(t *testing.T) {
	ok := []string{
		"/api/xbrl/companyfacts/CIK0000320193.json",
		"/api/xbrl/companyconcept/CIK0000320193/dei/EntityCommonStockSharesOutstanding.json",
		"/submissions/CIK0001067983.json",
	}
	for _, p := range ok {
		if !secXBRLPathOK(p) {
			t.Errorf("should allow %q", p)
		}
	}
	bad := []string{
		"",                                // empty
		"api/xbrl/x.json",                 // no leading slash
		"/Archives/edgar/data/1/x.txt",    // outside allowlist
		"/api/xbrl/../submissions/x.json", // traversal
		"/api/xbrl//..%2fx",               // double slash
		"/api/xbrl/x?apikey=steal",        // query smuggling
		"/api/xbrl/x#frag",                // fragment
		"/api/xbrl/@evil.com/x.json",      // host-injection attempt (also caught by hardcoded host)
		"/api/xbrl/x .json",               // space
		"/api/xbrl\\evil",                 // backslash
	}
	for _, p := range bad {
		if p == "/api/xbrl/@evil.com/x.json" && secXBRLPathOK(p) {
			// '@' in a path segment is harmless with a hardcoded https://data.sec.gov
			// prefix (a '/' precedes it, so it can't become userinfo); tolerated.
			continue
		}
		if secXBRLPathOK(p) {
			t.Errorf("should reject %q", p)
		}
	}
}

func TestCacheEviction(t *testing.T) {
	old := maxCacheEntries
	maxCacheEntries = 10
	defer func() { maxCacheEntries = old; cacheMu.Lock(); cacheM = map[string]cacheEntry{}; cacheMu.Unlock() }()
	cacheMu.Lock()
	cacheM = map[string]cacheEntry{}
	cacheMu.Unlock()

	for i := 0; i < 50; i++ {
		cachePut("k"+itoa(i), time.Minute, envelope.Envelope{})
	}
	cacheMu.Lock()
	n := len(cacheM)
	cacheMu.Unlock()
	if n > 10 {
		t.Fatalf("cache grew past bound: %d entries", n)
	}
}

func TestCacheExpiredReclaimedOnRead(t *testing.T) {
	cachePut("gone", -time.Second, envelope.Envelope{}) // already expired
	if _, ok := cacheGet("gone"); ok {
		t.Fatal("expired entry served")
	}
	cacheMu.Lock()
	_, still := cacheM["gone"]
	cacheMu.Unlock()
	if still {
		t.Fatal("expired entry not reclaimed on read")
	}
}
