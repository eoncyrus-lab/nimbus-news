package event

import (
	"time"
)

// budgetState 是单个自然日的即时推送预算。持久化后重启不会重复消耗。
type budgetState struct {
	Date        string             `json:"date"`
	Used        int                `json:"used"`
	ClassUsed   map[string]int     `json:"class_used,omitempty"`
	ThreadUsed  map[string]int     `json:"thread_used,omitempty"`
	ThreadScore map[string]float64 `json:"thread_score,omitempty"`
}

func newBudgetState(date string) *budgetState {
	return &budgetState{
		Date:        date,
		ClassUsed:   make(map[string]int),
		ThreadUsed:  make(map[string]int),
		ThreadScore: make(map[string]float64),
	}
}

// allowInstant 判断是否还能即时推送，并说明预算状态。
func (e *Engine) allowInstant(story *Story, critical bool) (bool, string) {
	if critical {
		return true, "critical"
	}
	now := e.now().In(e.loc)
	if e.inQuietHours(now) && story.Score < e.cfg.QuietBypassScore {
		return false, "quiet_hours"
	}
	if e.budget.Used >= e.cfg.DailyHardLimit {
		return false, "hard_budget"
	}
	if e.budget.ThreadUsed[story.ThreadID] >= 1 {
		prior := e.budget.ThreadScore[story.ThreadID]
		if story.Score < prior+e.cfg.UpgradeDelta {
			return false, "thread_cap"
		}
	}
	if e.budget.ThreadUsed[story.ThreadID] >= 2 {
		return false, "thread_cap"
	}
	if e.budget.Used >= e.cfg.DailyBudget &&
		e.budget.ClassUsed[story.Class] >= e.cfg.ClassReservation {
		return false, "budget"
	}
	return true, "ok"
}

func (e *Engine) spendInstant(story *Story) {
	e.budget.Used++
	e.budget.ClassUsed[story.Class]++
	e.budget.ThreadUsed[story.ThreadID]++
	if story.Score > e.budget.ThreadScore[story.ThreadID] {
		e.budget.ThreadScore[story.ThreadID] = story.Score
	}
	e.flushBudgetLocked()
}

func (e *Engine) inQuietHours(now time.Time) bool {
	minutes := now.Hour()*60 + now.Minute()
	start, end := e.cfg.QuietStart, e.cfg.QuietEnd
	if start == end {
		return false
	}
	if start < end {
		return minutes >= start && minutes < end
	}
	return minutes >= start || minutes < end
}

// ensureBudgetLocked 在跨日或首次使用时加载/重置预算。
func (e *Engine) ensureBudgetLocked(now time.Time) {
	date := now.In(e.loc).Format("2006-01-02")
	if e.budget != nil && e.budget.Date == date {
		return
	}
	e.budget = newBudgetState(date)
	if e.store == nil {
		return
	}
	key := "budget:" + e.ns + ":" + date
	var loaded budgetState
	if ok, err := GetState(e.store, stateBucket, key, &loaded); err == nil && ok && loaded.Date == date {
		if loaded.ClassUsed == nil {
			loaded.ClassUsed = make(map[string]int)
		}
		if loaded.ThreadUsed == nil {
			loaded.ThreadUsed = make(map[string]int)
		}
		if loaded.ThreadScore == nil {
			loaded.ThreadScore = make(map[string]float64)
		}
		e.budget = &loaded
	}
}

func (e *Engine) flushBudgetLocked() {
	if e.store == nil || e.budget == nil {
		return
	}
	key := "budget:" + e.ns + ":" + e.budget.Date
	_ = PutState(e.store, stateBucket, key, e.budget, e.retention())
}
