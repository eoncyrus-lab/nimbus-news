package event

import (
	"sort"
	"time"
)

// Contributions 汇总最近 days 天（含今天）的按源贡献。
//
// 指标口径：
//   - redundancy_rate = duplicates / items：该源有多少条目是其他源已经报过的事件；
//   - signal_rate     = (instant+defer) / items：该源有多少条目进入即时或版次；
//   - freshness_median_s = 抓取时间 - 发布时间的中位数，衡量该源的新鲜度。
func (e *Engine) Contributions(days int) []Contribution {
	if e == nil || !e.Enabled() {
		return nil
	}
	if days <= 0 {
		days = 7
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	cutoff := e.now().In(e.loc).AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	aggregate := make(map[string]*Contribution)
	freshness := make(map[string][]int64)
	merge := func(stat SourceStat) {
		if stat.Source == "" || stat.Date < cutoff {
			return
		}
		c := aggregate[stat.Source]
		if c == nil {
			c = &Contribution{Source: stat.Source}
			aggregate[stat.Source] = c
		}
		c.Items += stat.Items
		c.NewStories += stat.NewStories
		c.Duplicates += stat.Duplicates
		c.Instant += stat.Instant
		c.Defer += stat.Defer
		c.Archive += stat.Archive
		freshness[stat.Source] = append(freshness[stat.Source], stat.FreshnessSamples...)
		if stat.LatestPublished.After(c.LatestPublished) {
			c.LatestPublished = stat.LatestPublished
		}
	}

	loaded := false
	if e.store != nil {
		if items, err := e.store.List(statBucket, "stat:"); err == nil {
			loaded = true
			for _, item := range items {
				var stat SourceStat
				if jsonUnmarshal(item.Value, &stat) == nil {
					merge(stat)
				}
			}
		}
	}
	if !loaded {
		for _, stat := range e.stats {
			merge(*stat)
		}
	}

	out := make([]Contribution, 0, len(aggregate))
	for source, c := range aggregate {
		c.RedundancyRate = ratio(c.Duplicates, c.Items)
		c.SignalRate = ratio(c.Instant+c.Defer, c.Items)
		c.FreshnessMedianS = medianSeconds(freshness[source])
		out = append(out, *c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Items != out[j].Items {
			return out[i].Items > out[j].Items
		}
		return out[i].Source < out[j].Source
	})
	return out
}

func ratio(numerator, denominator int64) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func medianSeconds(samples []int64) int64 {
	if len(samples) == 0 {
		return 0
	}
	sorted := append([]int64(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// prune 删除超过保留期的内存记录；Bolt 侧由 envelope TTL 惰性过滤。
func (e *Engine) prune(now time.Time) {
	cutoff := now.Add(-e.retention())
	for id, story := range e.stories {
		if story.LastSeen.Before(cutoff) {
			delete(e.stories, id)
			if story.CanonicalURL != "" && e.byURL[story.CanonicalURL] == id {
				delete(e.byURL, story.CanonicalURL)
			}
		}
	}
	for id, thread := range e.threads {
		if thread.LastSeen.Before(cutoff) {
			delete(e.threads, id)
		}
	}
	for id, decision := range e.decisions {
		if decision.DecidedAt.Before(cutoff) {
			delete(e.decisions, id)
		}
	}
}
