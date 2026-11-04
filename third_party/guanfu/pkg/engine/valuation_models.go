package engine

import (
	"math"
	"time"
)

// ahrCompressedLabel 压缩版 AHR999 标签（阈值经 pow(x,0.75) 映射）
func ahrCompressedLabel(v float64) string {
	switch {
	case v < ctComp045:
		return "极端低估区"
	case v < ctComp08:
		return "低估区"
	case v < ctComp12:
		return "合理"
	case v < ctComp20:
		return "偏高"
	case v < ctComp50:
		return "高估区"
	case v < ctComp200:
		return "泡沫区"
	default:
		return "极端泡沫区"
	}
}

func d3Label(score int, val, mayer, dd float64) string {
	hasV := val > 0 && val < 0.8
	hasM := mayer > 0 && mayer < 1.0
	hasP := dd < -0.20
	switch {
	case hasV && hasM && hasP:
		return "VMP 三项全满（极端底部）"
	case hasV && !hasM && hasP:
		return "V-P 便宜+不跌+恐慌（恐慌底）"
	case !hasV && hasM && hasP:
		return "-MP 偏贵+跌+恐慌（熊市反弹陷阱）"
	case hasV && hasM && !hasP:
		return "VM- 估值便宜+动量弱（熊市中继）"
	case hasV && !hasM && !hasP:
		return "V-- 仅估值便宜（历史强势低估组合）"
	case !hasV && hasM && !hasP:
		return "-M- 仅动量（偏贵+跌，下跌延续风险）"
	case !hasV && !hasM && hasP:
		return "--P 仅恐慌（估值合理+恐慌，假底信号）"
	default:
		return "--- 三项全缺（估值偏高+不跌+无恐慌）"
	}
}

const (
	minerEfficiencyJPerTH = 25.0  // J/TH for modern ASIC
	minerElecCostPerKWh   = 0.05  // $/kWh global industrial avg
	minerBTCMinedPerDay   = 450.0 // 3.125 subsidy * 144 blocks (post-2024 halving)
)

func calcMinerCostFloor(hashRateEHs float64) (float64, bool) {
	if hashRateEHs <= 0 {
		return 0, false
	}
	// Convert EH/s → TH/s: 1 EH = 1e6 TH
	hashRateTHs := hashRateEHs * 1e6
	// Electricity cost per TH per day
	elecPerTHPerDay := minerEfficiencyJPerTH * 24.0 * minerElecCostPerKWh / 3600.0
	// Total daily electricity cost
	dailyElecUSD := hashRateTHs * elecPerTHPerDay
	// Per-BTC production cost
	costPerBTC := dailyElecUSD / minerBTCMinedPerDay
	if !isUsablePositive(costPerBTC) {
		return 0, false
	}
	return costPerBTC, true
}

// calcDifficultyCostRatio maps the latest difficulty adjustment to a rough
// miner pressure signal. It complements the pure electricity cost floor.
func calcDifficultyCostRatio(difficultyChangePct float64) (ratio float64, note string) {
	switch {
	case difficultyChangePct < -7:
		ratio = 0.85
		note = "difficulty -7%+: miner capitulation, historically at/near cost floor"
	case difficultyChangePct < -5:
		ratio = 0.92
		note = "difficulty -5~-7%: approaching miner cost floor"
	case difficultyChangePct < -2:
		ratio = 0.97
		note = "difficulty -2~-5%: marginal miners under pressure"
	case difficultyChangePct < 0:
		ratio = 1.0
		note = "difficulty flat/slight down: miners at breakeven"
	default:
		ratio = 1.05
		note = "difficulty rising: miners profitable, expanding"
	}
	return ratio, note
}

type cyclePeak struct {
	halvingDate  time.Time
	peakDate     time.Time
	peakPrice    float64
	halvingPrice float64
}

var knownCyclePeaks = []cyclePeak{
	{time.Date(2012, 11, 28, 0, 0, 0, 0, time.UTC), time.Date(2013, 12, 4, 0, 0, 0, 0, time.UTC), 1147, 12},
	{time.Date(2016, 7, 9, 0, 0, 0, 0, time.UTC), time.Date(2017, 12, 17, 0, 0, 0, 0, time.UTC), 19783, 650},
	{time.Date(2020, 5, 11, 0, 0, 0, 0, time.UTC), time.Date(2021, 11, 10, 0, 0, 0, 0, time.UTC), 68789, 8700},
}

// calcDiminishingROI estimates the current cycle's implied peak price and
// timing from exponential decay in historical cycle returns.
func calcDiminishingROI(halving200wSMA float64) (estPeakPrice float64, estPeakDays int, roiMultiple float64, ok bool) {
	if len(knownCyclePeaks) < 2 || halving200wSMA <= 0 {
		return 0, 0, 0, false
	}

	roi0 := knownCyclePeaks[0].peakPrice / knownCyclePeaks[0].halvingPrice
	roi1 := knownCyclePeaks[1].peakPrice / knownCyclePeaks[1].halvingPrice
	roi2 := knownCyclePeaks[2].peakPrice / knownCyclePeaks[2].halvingPrice
	rho := math.Sqrt((roi1 / roi0) * (roi2 / roi1))
	estROI := roi2 * rho

	peakDelays := make([]float64, len(knownCyclePeaks))
	for i, cp := range knownCyclePeaks {
		peakDelays[i] = cp.peakDate.Sub(cp.halvingDate).Hours() / 24
	}
	lastDelay := peakDelays[len(peakDelays)-1]
	estDelay := lastDelay + (552-lastDelay)*0.5
	if estDelay < lastDelay {
		estDelay = lastDelay
	}

	estPeakPrice = halving200wSMA * estROI
	if !isUsablePositive(estPeakPrice) {
		return 0, 0, 0, false
	}

	estPeakDays = int(estDelay)
	roiMultiple = estROI
	return estPeakPrice, estPeakDays, roiMultiple, true
}

func isUsablePositive(value float64) bool {
	return value > 0 && isUsableFinite(value)
}

func isUsableFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
