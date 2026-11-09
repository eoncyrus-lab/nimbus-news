package event

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/Ricaardo/nimbus-os/news/internal/model"
)

// Config 是事件引擎的运行参数；零值由 NewEngine 补默认。
type Config struct {
	Mode              string
	Location          string
	QuietStart        int // 分钟数，23:30 = 1410
	QuietEnd          int // 07:00 = 420
	DailyBudget       int
	DailyHardLimit    int
	ClassReservation  int
	InstantThreshold  float64
	DeferThreshold    float64
	QuietBypassScore  float64
	StoryWindowHours  int
	ThreadWindowHours int
	UpgradeDelta      float64
	RetentionDays     int
}

func DefaultConfig() Config {
	return Config{
		Mode:              ModeOff,
		Location:          "Asia/Shanghai",
		QuietStart:        23*60 + 30,
		QuietEnd:          7 * 60,
		DailyBudget:       6,
		DailyHardLimit:    8,
		ClassReservation:  2,
		InstantThreshold:  75,
		DeferThreshold:    40,
		QuietBypassScore:  85,
		StoryWindowHours:  24,
		ThreadWindowHours: 12,
		UpgradeDelta:      15,
		RetentionDays:     30,
	}
}

func (c *Config) setDefaults() {
	d := DefaultConfig()
	if c.Mode == "" {
		c.Mode = d.Mode
	}
	if c.Location == "" {
		c.Location = d.Location
	}
	if c.QuietStart == 0 && c.QuietEnd == 0 {
		c.QuietStart, c.QuietEnd = d.QuietStart, d.QuietEnd
	}
	if c.DailyBudget <= 0 {
		c.DailyBudget = d.DailyBudget
	}
	if c.DailyHardLimit <= 0 {
		c.DailyHardLimit = d.DailyHardLimit
	}
	if c.DailyHardLimit < c.DailyBudget {
		c.DailyHardLimit = c.DailyBudget
	}
	if c.ClassReservation <= 0 {
		c.ClassReservation = d.ClassReservation
	}
	if c.InstantThreshold <= 0 {
		c.InstantThreshold = d.InstantThreshold
	}
	if c.DeferThreshold <= 0 {
		c.DeferThreshold = d.DeferThreshold
	}
	if c.QuietBypassScore <= 0 {
		c.QuietBypassScore = d.QuietBypassScore
	}
	if c.StoryWindowHours <= 0 {
		c.StoryWindowHours = d.StoryWindowHours
	}
	if c.ThreadWindowHours <= 0 {
		c.ThreadWindowHours = d.ThreadWindowHours
	}
	if c.UpgradeDelta <= 0 {
		c.UpgradeDelta = d.UpgradeDelta
	}
	if c.RetentionDays <= 0 {
		c.RetentionDays = d.RetentionDays
	}
}

// Engine 串行处理所有源的消息；内部用单锁保证预算与 Story 索引一致。
type Engine struct {
	cfg   Config
	rules Rules
	store Store
	loc   *time.Location
	ns    string // budget 命名空间：active / shadow
	now   func() time.Time

	mu        sync.Mutex
	stories   map[string]*Story
	byURL     map[string]string
	threads   map[string]*Thread
	decisions map[string]*Decision
	budget    *budgetState
	stats     map[string]*SourceStat
	lastPrune time.Time
}

// NewEngine 构造事件引擎并加载最近窗口内的 Story/Thread/Decision。
func NewEngine(cfg Config, rules Rules, store Store) (*Engine, error) {
	cfg.setDefaults()
	switch cfg.Mode {
	case ModeOff, ModeShadow, ModeActive:
	default:
		return nil, fmt.Errorf("event: unknown mode %q (want off/shadow/active)", cfg.Mode)
	}
	loc, err := time.LoadLocation(cfg.Location)
	if err != nil {
		loc = time.FixedZone("CST", 8*60*60)
	}
	if len(rules.Classes) == 0 {
		rules = DefaultRules()
	}
	e := &Engine{
		cfg:       cfg,
		rules:     rules,
		store:     store,
		loc:       loc,
		ns:        "active",
		now:       time.Now,
		stories:   make(map[string]*Story),
		byURL:     make(map[string]string),
		threads:   make(map[string]*Thread),
		decisions: make(map[string]*Decision),
		stats:     make(map[string]*SourceStat),
	}
	if cfg.Mode != ModeActive {
		e.ns = "shadow"
	}
	e.load()
	return e, nil
}

// SetClock 仅用于测试。
func (e *Engine) SetClock(now func() time.Time) { e.now = now }

func (e *Engine) Mode() string  { return e.cfg.Mode }
func (e *Engine) Enabled() bool { return e.cfg.Mode != ModeOff }
func (e *Engine) Active() bool  { return e.cfg.Mode == ModeActive }

func (e *Engine) retention() time.Duration {
	return time.Duration(e.cfg.RetentionDays) * 24 * time.Hour
}

func (e *Engine) storyWindow() time.Duration {
	return time.Duration(e.cfg.StoryWindowHours) * time.Hour
}

func (e *Engine) threadWindow() time.Duration {
	return time.Duration(e.cfg.ThreadWindowHours) * time.Hour
}

func (e *Engine) load() {
	if e.store == nil {
		return
	}
	items, err := e.store.List(storyBucket, "")
	if err != nil {
		slog.Warn("event: load stories failed", "error", err)
		return
	}
	cutoff := e.now().Add(-e.retention())
	for _, item := range items {
		var story Story
		if err := jsonUnmarshal(item.Value, &story); err != nil || story.ID == "" {
			continue
		}
		if story.LastSeen.Before(cutoff) {
			continue
		}
		e.stories[story.ID] = &story
		if story.CanonicalURL != "" && story.LastSeen.After(e.now().Add(-e.storyWindow())) {
			e.byURL[story.CanonicalURL] = story.ID
		}
	}
	threads, err := e.store.List(threadBucket, "")
	if err == nil {
		for _, item := range threads {
			var thread Thread
			if err := jsonUnmarshal(item.Value, &thread); err != nil || thread.ID == "" {
				continue
			}
			e.threads[thread.ID] = &thread
		}
	}
	decisions, err := e.store.List(decisionBucket, "")
	if err == nil {
		for _, item := range decisions {
			var decision Decision
			if err := jsonUnmarshal(item.Value, &decision); err != nil || decision.StoryID == "" {
				continue
			}
			e.decisions[decision.StoryID] = &decision
		}
	}
}

// Observe 把一条新闻归并进 Story/Thread，并给出可解释判定。
func (e *Engine) Observe(ctx context.Context, sourceName string, msg *model.Message) (*Observation, error) {
	if e == nil || !e.Enabled() || msg == nil || msg.Type != model.TypeNews {
		return nil, nil
	}
	_ = ctx
	e.mu.Lock()
	defer e.mu.Unlock()

	now := e.now()
	e.ensureBudgetLocked(now)
	if e.lastPrune.IsZero() || now.Sub(e.lastPrune) > time.Hour {
		e.prune(now)
		e.lastPrune = now
	}

	text := msg.Title + "\n" + msg.Content
	titleCore := TitleCore(msg.Title)
	if titleCore == "" {
		titleCore = TitleCore(msg.Content)
		if len(titleCore) > 120 {
			titleCore = titleCore[:120]
		}
	}
	canonical := CanonicalURL(msg.Link)
	entities := ExtractEntities(text)
	class, matchedRules, critical := e.rules.Classify(msg.Title, msg.Content)

	story, duplicate := e.findStoryLocked(canonical, titleCore, entities, text, now)
	if story == nil {
		story = e.createStory(sourceName, msg, titleCore, canonical, entities, class, matchedRules, critical, now)
	} else {
		e.mergeStoryLocked(story, sourceName, msg, entities, text, critical, now)
	}

	decision := e.decideLocked(story, sourceName, msg.Title, duplicate, critical)
	// 静默的重复观测不覆盖此前的 instant/defer 判定，否则版次会漏掉已推事件。
	effective := e.decisions[story.ID]
	if effective == nil {
		effective = decision
	}

	if err := e.persistLocked(story, effective); err != nil {
		slog.Warn("event: persist failed", "story", story.ID, "error", err)
	}
	e.recordStatLocked(sourceName, msg, story, decision, duplicate, now)

	storyCopy := *story
	decisionCopy := *decision
	return &Observation{Story: &storyCopy, Decision: &decisionCopy, Duplicate: duplicate}, nil
}

func (e *Engine) createStory(sourceName string, msg *model.Message, titleCore, canonical string, entities []string, class string, matched []string, critical bool, now time.Time) *Story {
	story := &Story{
		ID:            hashID(canonical, titleCore, now.Format("2006010215")),
		CanonicalURL:  canonical,
		TitleCore:     titleCore,
		Title:         msg.Title,
		Entities:      entities,
		Class:         class,
		FirstSeen:     now,
		LastSeen:      now,
		ItemIDs:       []string{msg.ID},
		Sources:       []string{sourceName},
		PrimarySource: sourceName,
		State:         "active",
		MatchedRules:  append([]string(nil), matched...),
	}
	base := e.rules.BaseScore(class)
	_, tierRule := e.rules.SourceBonus(sourceName)
	story.BaseScore = base + 5 // 类别基础分 + 首发；源等级分在 scoreText 里按最佳来源计
	if tierRule != "" {
		story.MatchedRules = append(story.MatchedRules, tierRule)
	}
	story.MatchedRules = append(story.MatchedRules, "first_seen")
	story.Score = e.scoreText(story, critical, msg.Title+"\n"+msg.Content)
	story.ThreadID = e.attachThreadLocked(story, now)
	e.stories[story.ID] = story
	if canonical != "" {
		e.byURL[canonical] = story.ID
	}
	return story
}

func (e *Engine) mergeStoryLocked(story *Story, sourceName string, msg *model.Message, entities []string, text string, critical bool, now time.Time) {
	story.LastSeen = now
	if msg.Title != "" && len(story.Title) < len(msg.Title) {
		story.Title = msg.Title
	}
	if !contains(story.ItemIDs, msg.ID) {
		story.ItemIDs = append(story.ItemIDs, msg.ID)
	}
	if !contains(story.Sources, sourceName) {
		story.Sources = append(story.Sources, sourceName)
	}
	for _, entity := range entities {
		if !contains(story.Entities, entity) && len(story.Entities) < 8 {
			story.Entities = append(story.Entities, entity)
		}
	}
	story.Score = e.scoreText(story, critical, text)
	if _, tierRule := e.rules.SourceBonus(sourceName); tierRule != "" && !contains(story.MatchedRules, tierRule) {
		story.MatchedRules = append(story.MatchedRules, tierRule+"@"+sourceName)
	}
}

// rescore 基于当前来源数补多源佐证分（每源 +5，上限 +10）。
func (e *Engine) scoreText(story *Story, critical bool, text string) float64 {
	score := story.BaseScore
	// 同一 Story 可能先被分析源报道、再被 wire 确认；按最佳来源等级计分。
	bestBonus := 0.0
	for _, source := range story.Sources {
		if bonus, _ := e.rules.SourceBonus(source); bonus > bestBonus {
			bestBonus = bonus
		}
	}
	score += bestBonus
	corroboration := len(story.Sources) - 1
	if corroboration > 2 {
		corroboration = 2
	}
	score += float64(corroboration * 5)
	if story.Class == ClassDataSurprise || story.Class == ClassMarketRegime {
		if HasSurprise(text) {
			score += 5
		}
	}
	if critical {
		if score < 85 {
			score = 85
		}
		story.MatchedRules = appendUnique(story.MatchedRules, "critical")
	}
	if score > 100 {
		score = 100
	}
	return score
}

func (e *Engine) decideLocked(story *Story, sourceName, title string, duplicate, critical bool) *Decision {
	decision := &Decision{
		StoryID:      story.ID,
		ThreadID:     story.ThreadID,
		Class:        story.Class,
		Score:        story.Score,
		BaseScore:    story.BaseScore,
		MatchedRules: append([]string(nil), story.MatchedRules...),
		Mode:         e.cfg.Mode,
		Duplicate:    duplicate,
		Title:        title,
		Source:       sourceName,
		DecidedAt:    e.now(),
	}
	if duplicate && !story.DeliveredAt.IsZero() && story.Score < story.DeliveredScore+e.cfg.UpgradeDelta {
		decision.Lane = LaneSilent
		decision.Reason = "already_delivered"
		e.keepPriorDecisionLocked(story, decision)
		return decision
	}

	lane := LaneArchive
	reason := "below_threshold"
	if story.Score >= e.cfg.InstantThreshold || critical {
		allowed, budgetState := e.allowInstant(story, critical)
		decision.BudgetState = budgetState
		if allowed {
			lane = LaneInstant
			reason = "score_threshold"
			if critical {
				reason = "critical"
			}
			e.spendInstant(story)
			story.DeliveredAt = e.now()
			story.DeliveredScore = story.Score
		} else {
			lane = LaneDefer
			reason = "budget:" + budgetState
		}
	} else if story.Score >= e.cfg.DeferThreshold {
		lane = LaneDefer
		reason = "defer_band"
	}
	if duplicate && lane != LaneInstant {
		decision.Lane = LaneSilent
		decision.Reason = "duplicate"
		e.keepPriorDecisionLocked(story, decision)
		return decision
	}
	decision.Lane = lane
	decision.Reason = reason
	e.decisions[story.ID] = decision
	return decision
}

// keepPriorDecisionLocked 保留已有的 instant/defer 判定，只更新分数与时间；
// silent 只用于本次观测的统计，不应把已进版次的事件抹掉。
func (e *Engine) keepPriorDecisionLocked(story *Story, decision *Decision) {
	if prior := e.decisions[story.ID]; prior != nil && prior.Lane != LaneSilent {
		kept := *prior
		kept.DecidedAt = decision.DecidedAt
		kept.Duplicate = true
		kept.Reason = decision.Reason
		kept.Score = story.Score
		e.decisions[story.ID] = &kept
		return
	}
	e.decisions[story.ID] = decision
}

func (e *Engine) findStoryLocked(canonical, titleCore string, entities []string, text string, now time.Time) (*Story, bool) {
	if canonical != "" {
		if id, ok := e.byURL[canonical]; ok {
			if story := e.stories[id]; story != nil && now.Sub(story.LastSeen) <= e.storyWindow() {
				return story, true
			}
		}
	}
	if titleCore == "" {
		return nil, false
	}
	tokens := TitleTokens(titleCore)
	numbers := NumberSignature(text)
	var best *Story
	bestScore := 0.0
	for _, story := range e.stories {
		if now.Sub(story.LastSeen) > e.storyWindow() {
			continue
		}
		similarity := Jaccard(tokens, TitleTokens(story.TitleCore))
		if similarity >= 0.85 && similarity > bestScore {
			best, bestScore = story, similarity
			continue
		}
		// 数字签名 + 两个共有实体：同一份数据/同一事件的不同写法。
		if IntersectionSize(numbers, NumberSignature(story.Title+"\n"+story.TitleCore)) >= 2 &&
			shareEntity(entities, story.Entities) >= 2 && bestScore < 0.8 {
			best, bestScore = story, 0.8
		}
	}
	return best, best != nil
}

func (e *Engine) attachThreadLocked(story *Story, now time.Time) string {
	entity := ""
	if len(story.Entities) > 0 {
		entity = story.Entities[0]
	}
	key := entity + "|" + story.Class
	if entity == "" {
		key = story.TitleCore[:minInt(24, len(story.TitleCore))] + "|" + story.Class
	}
	for _, thread := range e.threads {
		if thread.Key != key || now.Sub(thread.LastSeen) > e.threadWindow() {
			continue
		}
		thread.StoryIDs = append(thread.StoryIDs, story.ID)
		thread.LastSeen = now
		return thread.ID
	}
	thread := &Thread{
		ID:        hashID("thread", key, now.Format("2006010215")),
		Key:       key,
		Entity:    entity,
		Class:     story.Class,
		StoryIDs:  []string{story.ID},
		FirstSeen: now,
		LastSeen:  now,
	}
	e.threads[thread.ID] = thread
	return thread.ID
}

func (e *Engine) persistLocked(story *Story, decision *Decision) error {
	if e.store == nil {
		return nil
	}
	if err := PutState(e.store, storyBucket, story.ID, story, e.retention()); err != nil {
		return err
	}
	if story.CanonicalURL != "" {
		_ = e.store.Put(stateBucket, "url:"+story.CanonicalURL, []byte(story.ID), e.storyWindow())
	}
	if thread := e.threads[story.ThreadID]; thread != nil {
		if err := PutState(e.store, threadBucket, thread.ID, thread, e.retention()); err != nil {
			return err
		}
	}
	if decision != nil {
		if err := PutState(e.store, decisionBucket, decisionKey(story.ID), decision, e.retention()); err != nil {
			return err
		}
	}
	return nil
}

func decisionKey(storyID string) string {
	return storyID
}

func (e *Engine) recordStatLocked(sourceName string, msg *model.Message, story *Story, decision *Decision, duplicate bool, now time.Time) {
	date := now.In(e.loc).Format("2006-01-02")
	key := date + "|" + sourceName
	stat := e.stats[key]
	if stat == nil {
		stat = &SourceStat{Date: date, Source: sourceName}
		if e.store != nil {
			var loaded SourceStat
			if ok, _ := GetState(e.store, statBucket, "stat:"+key, &loaded); ok {
				stat = &loaded
			}
		}
		e.stats[key] = stat
	}
	stat.Items++
	if duplicate {
		stat.Duplicates++
	} else {
		stat.NewStories++
	}
	switch decision.Lane {
	case LaneInstant:
		stat.Instant++
	case LaneDefer:
		stat.Defer++
	case LaneArchive:
		stat.Archive++
	}
	if !msg.CreateTime.IsZero() && !msg.FetchTime.IsZero() {
		delay := msg.FetchTime.Sub(msg.CreateTime)
		if delay > 0 && delay < 7*24*time.Hour {
			if len(stat.FreshnessSamples) < 500 {
				stat.FreshnessSamples = append(stat.FreshnessSamples, int64(delay.Seconds()))
			}
		}
		if msg.CreateTime.After(stat.LatestPublished) {
			stat.LatestPublished = msg.CreateTime
		}
	}
	if e.store != nil {
		_ = PutState(e.store, statBucket, "stat:"+key, stat, e.retention())
	}
}

// EventsBetween 返回窗口内进入 instant/defer 的 Story，按 lane、分数排序。
func (e *Engine) EventsBetween(from, to time.Time, limit int) []EventItem {
	if e == nil || !e.Enabled() {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	var items []EventItem
	for _, story := range e.stories {
		if story.LastSeen.Before(from) || story.LastSeen.After(to) {
			continue
		}
		decision := e.decisions[story.ID]
		lane := LaneDefer
		if decision != nil {
			lane = decision.Lane
		}
		if lane != LaneInstant && lane != LaneDefer {
			continue
		}
		items = append(items, EventItem{
			StoryID: story.ID, ThreadID: story.ThreadID, Lane: lane, Class: story.Class,
			Score: story.Score, Title: story.Title, Source: story.PrimarySource,
			FirstSeen: story.FirstSeen, LastSeen: story.LastSeen, Sources: story.Sources,
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Lane != items[j].Lane {
			return items[i].Lane == LaneInstant
		}
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		return items[i].LastSeen.After(items[j].LastSeen)
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}

// Decisions 返回某天的判定记录（按分数降序）。
func (e *Engine) Decisions(date string) []Decision {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Decision
	for _, decision := range e.decisions {
		if date != "" && decision.DecidedAt.In(e.loc).Format("2006-01-02") != date {
			continue
		}
		out = append(out, *decision)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// Overview 返回今日预算与计数。
func (e *Engine) Overview() map[string]interface{} {
	if e == nil {
		return map[string]interface{}{"mode": ModeOff}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	e.ensureBudgetLocked(now)
	used := 0
	byClass := map[string]int{}
	if e.budget != nil {
		used = e.budget.Used
		for class, count := range e.budget.ClassUsed {
			byClass[class] = count
		}
	}
	laneCounts := map[string]int{}
	for _, decision := range e.decisions {
		if decision.DecidedAt.In(e.loc).Format("2006-01-02") == now.In(e.loc).Format("2006-01-02") {
			laneCounts[decision.Lane]++
		}
	}
	return map[string]interface{}{
		"mode":         e.cfg.Mode,
		"date":         now.In(e.loc).Format("2006-01-02"),
		"instant_used": used,
		"daily_budget": e.cfg.DailyBudget,
		"hard_limit":   e.cfg.DailyHardLimit,
		"by_class":     byClass,
		"lane_counts":  laneCounts,
		"stories":      len(e.stories),
		"threads":      len(e.threads),
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func appendUnique(values []string, target string) []string {
	if contains(values, target) {
		return values
	}
	return append(values, target)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func jsonUnmarshal(data []byte, dst interface{}) error {
	return json.Unmarshal(data, dst)
}
