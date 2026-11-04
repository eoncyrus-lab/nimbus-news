// Lazy portfolio allocation engine.
//
// Supported portfolios:
//   - 60/40: SPY 60% + TLT 40%
//   - All-Weather (Dalio): SPY 30% + TLT 40% + GLD 7.5% + Gold 7.5% + Commodity 7.5% + BIL 7.5%
//   - Permanent: SPY 25% + TLT 25% + Gold 25% + SHY 25%
//   - Buffett 90/10: SPY 90% + SHY 10%
//   - Global Market: VTI 60% + BND 40%
//
// Design:
//   - Never output rebalancing trade instructions
//   - Output drift + valuation zone + accumulation/caution hints
//   - Uses live prices from PriceStore

package allocate

import (
	"math"
	"sort"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

// Portfolio defines a target allocation.
type Portfolio struct {
	Name   string             `json:"name"`
	Assets map[string]float64 `json:"assets"` // asset → target weight (0-1)
}

// Status holds current allocation status for a portfolio.
type Status struct {
	Portfolio       string        `json:"portfolio"`
	Date            string        `json:"date"`
	Assets          []AssetStatus `json:"assets"`
	DriftMax        float64       `json:"max_drift_pct"` // largest absolute drift
	RebalanceNeeded bool          `json:"rebalance_needed"`
	OverallZone     string        `json:"overall_zone"`
}

// AssetStatus holds current state for a single portfolio asset.
type AssetStatus struct {
	Asset        string  `json:"asset"`
	TargetPct    float64 `json:"target_pct"`
	CurrentPrice float64 `json:"current_price"`
	Zone         string  `json:"zone"`
	DriftPct     float64 `json:"drift_pct"` // 0 when actual holdings are unavailable
	Hint         string  `json:"hint"`
}

// Predefined portfolios
var (
	Portfolio6040 = Portfolio{
		Name:   "60/40",
		Assets: map[string]float64{"spy": 0.60, "tlt": 0.40},
	}
	PortfolioAllWeather = Portfolio{
		Name: "全天候 (Dalio)",
		Assets: map[string]float64{
			"spy": 0.30, "tlt": 0.40, "gld": 0.075,
			"gold": 0.075, "wti": 0.075, "bil": 0.075,
		},
	}
	PortfolioPermanent = Portfolio{
		Name: "永久组合",
		Assets: map[string]float64{
			"spy": 0.25, "tlt": 0.25, "gold": 0.25, "shy": 0.25,
		},
	}
	PortfolioBuffett = Portfolio{
		Name:   "巴菲特 90/10",
		Assets: map[string]float64{"spy": 0.90, "shy": 0.10},
	}
	PortfolioGlobal = Portfolio{
		Name:   "全球市场",
		Assets: map[string]float64{"vti": 0.60, "bnd": 0.40},
	}

	AllPortfolios = []Portfolio{
		Portfolio6040, PortfolioAllWeather, PortfolioPermanent, PortfolioBuffett, PortfolioGlobal,
	}
)

// RebalanceThreshold is the absolute drift that triggers a rebalance warning.
const RebalanceThreshold = 5.0 // 5%

func Analyze(pf Portfolio) (*Status, error) {
	return analyzeWithStore(pf, &store.PriceStore{})
}

// analyzeWithStore computes the current status of a model portfolio against
// PriceStore data. PriceStore contains market prices, not user holdings or
// lots, so drift remains unknown here rather than being fabricated from target
// weights.
func analyzeWithStore(pf Portfolio, s *store.PriceStore) (*Status, error) {
	if s == nil {
		s = &store.PriceStore{}
	}
	status := &Status{
		Portfolio: pf.Name,
		Date:      time.Now().UTC().Format("2006-01-02"),
		Assets:    make([]AssetStatus, 0, len(pf.Assets)),
	}

	type pricedAsset struct {
		asset  string
		target float64
		price  float64
		zone   string
	}
	var priced []pricedAsset

	for asset, target := range pf.Assets {
		latest, ok := s.Latest(asset)
		price := 0.0
		zone := "无数据"
		if ok {
			price = latest.Close
			zone = assetValuationZone(asset, price)
		}
		priced = append(priced, pricedAsset{asset, target, price, zone})
	}
	sort.Slice(priced, func(i, j int) bool { return priced[i].asset < priced[j].asset })

	maxDrift := 0.0
	for _, pa := range priced {
		hint := ""
		switch {
		case pa.zone == "无数据":
			hint = "数据缺失"
		case pa.zone == "低估区":
			hint = "积累区"
		case pa.zone == "高估区":
			hint = "谨慎"
		default:
			hint = "中性"
		}

		status.Assets = append(status.Assets, AssetStatus{
			Asset:        pa.asset,
			TargetPct:    pa.target * 100,
			CurrentPrice: pa.price,
			Zone:         pa.zone,
			DriftPct:     0,
			Hint:         hint,
		})
	}

	status.DriftMax = maxDrift
	status.RebalanceNeeded = maxDrift > RebalanceThreshold

	// Overall zone
	accumCount := 0
	cautionCount := 0
	for _, a := range status.Assets {
		if a.Hint == "积累区" {
			accumCount++
		} else if a.Hint == "谨慎" {
			cautionCount++
		}
	}
	switch {
	case accumCount > cautionCount && accumCount >= 2:
		status.OverallZone = "偏积累"
	case cautionCount > accumCount && cautionCount >= 2:
		status.OverallZone = "偏防御"
	default:
		status.OverallZone = "中性"
	}

	return status, nil
}

func assetValuationZone(asset string, price float64) string {
	switch asset {
	case "spy", "qqq", "vti":
		// Simplified valuation: PE-based would be better, use price level as proxy
		return "中性" // Phase 3 PE/PB integration would improve this
	case "tlt", "shy", "bnd":
		if price > 0 {
			return "中性" // bond valuation requires yield analysis
		}
	case "gold", "gld":
		return "中性" // gold valuation in engine/asset_gold.go
	}
	return "中性"
}

// ─── Multi-asset consensus / divergence ──────────────────

// ConsensusResult shows whether multiple assets are sending aligned signals.
type ConsensusResult struct {
	Date       string        `json:"date"`
	Direction  string        `json:"direction"`  // "risk_on", "risk_off", "mixed"
	Confidence float64       `json:"confidence"` // 0-1
	Signals    []AssetSignal `json:"signals"`
	Summary    string        `json:"summary"`
}

// AssetSignal is a single asset's directional signal.
type AssetSignal struct {
	Asset    string  `json:"asset"`
	Price    float64 `json:"price"`
	Momentum float64 `json:"momentum_30d_pct"`
	Signal   string  `json:"signal"` // "bullish", "bearish", "neutral"
}

// ConsensusScan scans all tracked assets for directional consensus.
func ConsensusScan() (*ConsensusResult, error) {
	s := &store.PriceStore{}
	assets := []string{"btc", "qqq", "spy", "gold", "tlt", "uup"}
	overview, _ := MultiAssetOverview()

	cs := &ConsensusResult{
		Date: time.Now().UTC().Format("2006-01-02"),
	}

	bullCount := 0
	bearCount := 0
	for _, asset := range assets {
		history, err := s.LoadHistory(asset)
		if err != nil || len(history) < 30 {
			continue
		}

		price := history[0]
		// 30d momentum
		mom := (history[0] - history[minInt2(29, len(history)-1)]) / history[minInt2(29, len(history)-1)] * 100

		signal := "neutral"
		switch {
		case mom > 5:
			signal = "bullish"
			bullCount++
		case mom < -5:
			signal = "bearish"
			bearCount++
		default:
			// Check vs 50d SMA
			if len(history) >= 50 {
				sum := 0.0
				for i := 0; i < 50; i++ {
					sum += history[i]
				}
				sma := sum / 50
				if price > sma*1.03 {
					signal = "bullish"
					bullCount++
				} else if price < sma*0.97 {
					signal = "bearish"
					bearCount++
				}
			}
		}

		cs.Signals = append(cs.Signals, AssetSignal{
			Asset:    asset,
			Price:    price,
			Momentum: math.Round(mom*10) / 10,
			Signal:   signal,
		})
	}

	// Determine consensus
	total := bullCount + bearCount
	if total >= 3 {
		cs.Confidence = float64(total) / float64(len(cs.Signals))
		if bullCount > bearCount*2 {
			cs.Direction = "risk_on"
			cs.Summary = "多资产共振看涨：风险偏好上升，权益/黄金/ BTC 同步走强"
		} else if bearCount > bullCount*2 {
			cs.Direction = "risk_off"
			cs.Summary = "多资产共振看跌：风险偏好下降，多市场同步走弱"
		} else {
			cs.Direction = "mixed"
			cs.Summary = "信号分歧：不同资产方向不一致，需等待确认"
		}
	} else {
		cs.Direction = "mixed"
		cs.Confidence = 0.3
		cs.Summary = "信号不足或方向分散，市场缺乏明确共识"
	}

	_ = overview
	return cs, nil
}

func minInt2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// MultiAssetOverview provides a quick scan of all tracked assets.
func MultiAssetOverview() (*Overview, error) {
	s := &store.PriceStore{}
	assets, _ := s.ListAssets()

	ov := &Overview{Date: time.Now().UTC().Format("2006-01-02")}
	for _, asset := range assets {
		latest, ok := s.Latest(asset)
		if !ok {
			continue
		}
		count, _ := s.Count(asset)
		daysSince := s.DaysSinceLastUpdate(asset)

		ov.Items = append(ov.Items, OverviewItem{
			Asset:       asset,
			Price:       latest.Close,
			AsOf:        latest.Date,
			HistoryDays: count,
			StaleDays:   daysSince,
		})
	}
	return ov, nil
}

// Overview is a multi-asset snapshot.
type Overview struct {
	Date  string         `json:"date"`
	Items []OverviewItem `json:"items"`
}

// OverviewItem is a single asset in the overview.
type OverviewItem struct {
	Asset       string  `json:"asset"`
	Price       float64 `json:"price"`
	AsOf        string  `json:"as_of"`
	HistoryDays int     `json:"history_days"`
	StaleDays   int     `json:"stale_days,omitempty"`
}

// ─── Historical risk metrics ────────────────────────────

// RiskMetrics holds portfolio risk characteristics.
type RiskMetrics struct {
	AnnualVolPct   float64 `json:"annual_vol_pct"`
	MaxDrawdownPct float64 `json:"max_drawdown_pct"`
	SharpeApprox   float64 `json:"sharpe_approx"`
}
