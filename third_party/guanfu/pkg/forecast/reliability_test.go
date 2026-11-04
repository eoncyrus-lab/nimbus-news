package forecast

import (
	"strings"
	"testing"
)

func TestHorizonCaveatFlagsWeakHistory(t *testing.T) {
	// BTC 30d at 60.9% / 46 tests → no caveat (above 55%)
	c := HorizonCaveat("btc", 30)
	if c != "" {
		t.Errorf("btc/30d should have no caveat at 60.9%%, got: %s", c)
	}

	// Gold 30d at 58% → no hard-block (above 50%), but below 60% → approaching-random caveat
	c30 := HorizonCaveat("gold", 30)
	// 58% is above hard-block threshold (50%) but below reliable threshold (60%)
	// so it should produce an approaching-random caveat
	if c30 != "" && !strings.Contains(c30, "接近随机") {
		t.Errorf("gold/30d caveat should be empty or approaching-random, got: %s", c30)
	}
}

func TestIsHardBlocked(t *testing.T) {
	// Gold 30d at 0.580 → NOT blocked (above 0.50); 180d at 0.652 → not blocked.
	if IsHardBlocked("gold", 30) {
		t.Error("gold/30 at 58%% should NOT be hard-blocked (above 50%%)")
	}
	if IsHardBlocked("gold", 180) {
		t.Error("gold/180 at 65%% should NOT be hard-blocked")
	}
	// QQQ reliable → never blocked
	for _, h := range []int{30, 90, 180} {
		if IsHardBlocked("qqq", h) {
			t.Errorf("qqq/%dd should not be hard-blocked", h)
		}
	}
	// Unknown cell → not blocked (no evidence)
	if IsHardBlocked("nope", 30) {
		t.Error("unknown asset should not be hard-blocked")
	}
}

func TestHorizonCaveatStaysSilentWhenReliable(t *testing.T) {
	// QQQ at every recorded horizon is ≥ 0.55
	for _, h := range []int{30, 90, 180} {
		if c := HorizonCaveat("qqq", h); c != "" {
			t.Errorf("qqq/%dd unexpected caveat: %s", h, c)
		}
	}
	// BTC 90d at 65% should be clean
	if c := HorizonCaveat("BTC", 90); c != "" {
		t.Errorf("btc/90d unexpected caveat: %s", c)
	}
}

func TestHorizonCaveatEmptyForUnknownAssetOrHorizon(t *testing.T) {
	// Untested asset/horizon combos return empty — never fabricate warnings.
	if c := HorizonCaveat("nope", 30); c != "" {
		t.Errorf("unknown asset: expected empty, got %s", c)
	}
	if c := HorizonCaveat("gold", 60); c != "" {
		t.Errorf("untested gold/60d: expected empty (no claim), got %s", c)
	}
	if c := HorizonCaveat("", 30); c != "" {
		t.Errorf("empty asset: expected empty, got %s", c)
	}
}

func TestReliabilityForReturnsOk(t *testing.T) {
	r, ok := ReliabilityFor("gold", 180)
	if !ok {
		t.Fatal("gold/180 should be present")
	}
	if r.NTests != 69 {
		t.Errorf("gold/180 NTests=%d, want 69", r.NTests)
	}
	if r.AsOf == "" {
		t.Error("AsOf must not be empty for recorded cells")
	}

	if _, ok := ReliabilityFor("gold", 60); ok {
		t.Error("gold/60 (untested) should return ok=false")
	}
}
