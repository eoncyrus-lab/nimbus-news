// Package event 实现 Story → Thread → Decision 事件模型。
//
// 三个层次：
//   - Story：高精度身份，只用于“是否重复推送”。
//   - Thread：低精度主题，只用于“同一主体每日最多一条即时”的排版/预算约束。
//   - Decision：可解释的车道判定（instant/defer/archive），带命中的规则与预算状态。
package event

import "time"

// Mode 事件引擎运行模式。
const (
	ModeOff    = "off"
	ModeShadow = "shadow" // 只写 Story/Thread/Decision，不改变推送
	ModeActive = "active" // 事件决策接管 typed routing 的车道
)

const (
	LaneInstant = "instant"
	LaneDefer   = "defer"
	LaneArchive = "archive"
	LaneSilent  = "silent"
)

// Class 新闻类别，基础分见 rules.go。
const (
	ClassCentralBank   = "central_bank"
	ClassDataSurprise  = "data_surprise"
	ClassGeopolitics   = "geopolitics"
	ClassSystemicCo    = "systemic_company"
	ClassMarketRegime  = "market_regime"
	ClassAnalysis      = "analysis"
	ClassDomesticPolit = "domestic_politics"
	ClassUnclassified  = "unclassified"
)

// Story 一条事件的身份与生命周期。
type Story struct {
	ID             string    `json:"story_id"`
	CanonicalURL   string    `json:"canonical_url,omitempty"`
	TitleCore      string    `json:"title_core"`
	Title          string    `json:"title"`
	Entities       []string  `json:"entities,omitempty"`
	Class          string    `json:"class"`
	BaseScore      float64   `json:"base_score"`
	Score          float64   `json:"score"`
	FirstSeen      time.Time `json:"first_seen"`
	LastSeen       time.Time `json:"last_seen"`
	ItemIDs        []string  `json:"item_ids,omitempty"`
	Sources        []string  `json:"sources,omitempty"`
	PrimarySource  string    `json:"primary_source"`
	ThreadID       string    `json:"thread_id"`
	DeliveredAt    time.Time `json:"delivered_at,omitempty"`
	DeliveredScore float64   `json:"delivered_score,omitempty"`
	MatchedRules   []string  `json:"matched_rules,omitempty"`
	State          string    `json:"state"`
}

// Thread 同主体 + 同类别 + 时间窗口内的事件集合。
type Thread struct {
	ID        string    `json:"thread_id"`
	Key       string    `json:"key"`
	Entity    string    `json:"entity,omitempty"`
	Class     string    `json:"class"`
	StoryIDs  []string  `json:"story_ids,omitempty"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

// Decision 一次可解释的路由判定。
type Decision struct {
	StoryID      string    `json:"story_id"`
	ThreadID     string    `json:"thread_id,omitempty"`
	Lane         string    `json:"lane"`
	Class        string    `json:"class"`
	Score        float64   `json:"score"`
	BaseScore    float64   `json:"base_score"`
	MatchedRules []string  `json:"matched_rules,omitempty"`
	BudgetState  string    `json:"budget_state,omitempty"`
	Reason       string    `json:"reason,omitempty"`
	Mode         string    `json:"mode"`
	Duplicate    bool      `json:"duplicate,omitempty"`
	Title        string    `json:"title,omitempty"`
	Source       string    `json:"source,omitempty"`
	DecidedAt    time.Time `json:"decided_at"`
}

// Observation 是一条原始消息经过事件引擎后的结果。
type Observation struct {
	Story     *Story    `json:"story"`
	Decision  *Decision `json:"decision"`
	Duplicate bool      `json:"duplicate"`
}

// EventItem 是版次渲染用的事件条目。
type EventItem struct {
	StoryID   string    `json:"story_id"`
	ThreadID  string    `json:"thread_id"`
	Lane      string    `json:"lane"`
	Class     string    `json:"class"`
	Score     float64   `json:"score"`
	Title     string    `json:"title"`
	Source    string    `json:"source"`
	Link      string    `json:"link,omitempty"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Sources   []string  `json:"sources,omitempty"`
}

// SourceStat 是 source_contribution 的按日累计。
type SourceStat struct {
	Date             string    `json:"date"`
	Source           string    `json:"source"`
	Items            int64     `json:"items"`
	NewStories       int64     `json:"new_stories"`
	Duplicates       int64     `json:"duplicates"`
	Instant          int64     `json:"instant"`
	Defer            int64     `json:"defer"`
	Archive          int64     `json:"archive"`
	FreshnessSamples []int64   `json:"freshness_samples,omitempty"`
	LatestPublished  time.Time `json:"latest_published,omitempty"`
}

// Contribution 是跨日聚合后的单源贡献报表。
type Contribution struct {
	Source           string    `json:"source"`
	Items            int64     `json:"items"`
	NewStories       int64     `json:"new_stories"`
	Duplicates       int64     `json:"duplicates"`
	Instant          int64     `json:"instant"`
	Defer            int64     `json:"defer"`
	Archive          int64     `json:"archive"`
	RedundancyRate   float64   `json:"redundancy_rate"`    // duplicates / items
	SignalRate       float64   `json:"signal_rate"`        // (instant+defer) / items
	FreshnessMedianS int64     `json:"freshness_median_s"` // fetched_at - published_at
	LatestPublished  time.Time `json:"latest_published,omitempty"`
}
