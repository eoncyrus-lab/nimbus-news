// Package history persists daily 观复 metric readings for percentile
// computation across days.
//
// Why: ETF flow, mempool depth, funding rate 等指标没有公开历史 API，必须自己
// 每天采集一行才能算"今天值在过去 N 天的分位"。BTC 价格历史用 Binance
// kline 每次都能拉，不进 history.db。
//
// Schema 单一表，date+key 复合主键 → 同一天重复写入以最新值覆盖。
//
// 用法:
//
//	s, _ := history.Open("~/.guanfu/history.db")
//	defer s.Close()
//	s.Record("2026-05-02", "mempool_mb", 12.3)
//	q, n, _ := s.QuantileAsOf("mempool_mb", 12.3, 730, "2026-05-02")
//	if n >= 30 { panel.Indicator.Quantile = q }
package history

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite"
)

// Store 是 SQLite 历史指标存储
type Store struct {
	db   *sql.DB
	path string
}

// Open 打开（必要时创建）SQLite DB。
// path 为空时默认 $HOME/.guanfu/history.db（兼容老路径 ~/.coinman/history.db
// 在迁移期内可手动 mv 过去）。
func Open(path string) (*Store, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("UserHomeDir: %w", err)
		}
		path = filepath.Join(home, ".guanfu", "history.db")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("sql.Open: %w", err)
	}

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS daily_metrics (
			date  TEXT NOT NULL,
			key   TEXT NOT NULL,
			value REAL NOT NULL,
			PRIMARY KEY (date, key)
		);
		CREATE INDEX IF NOT EXISTS idx_metrics_key_date ON daily_metrics(key, date);
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}

	return &Store{db: db, path: path}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// RecordMany 批量写入今日多个 key（同一天，同一日期，最新值覆盖旧值）
func (s *Store) RecordMany(date string, kv map[string]float64) error {
	if s == nil || s.db == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`
		INSERT INTO daily_metrics(date, key, value) VALUES(?, ?, ?)
		ON CONFLICT(date, key) DO UPDATE SET value = excluded.value
	`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	for k, v := range kv {
		if _, err := stmt.Exec(date, k, v); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// ValueAt returns the stored value for a given date+key, or (0, false) if not found.
func (s *Store) ValueAt(date, key string) (float64, bool) {
	if s == nil || s.db == nil {
		return 0, false
	}
	date = normalizeAsOfDate(date)
	var v float64
	err := s.db.QueryRow(
		`SELECT value FROM daily_metrics WHERE key=? AND date=date(?)`, key, date,
	).Scan(&v)
	if err != nil {
		return 0, false
	}
	return v, true
}

// QuantileAsOf 返回 currentValue 在 asOfDate 及其过去 lookbackDays 天 key 历史值中的分位 [0,1]。
// asOfDate 用 panel 数据日期，而不是 SQLite date('now')，这样回放和测试不会漂移。
func (s *Store) QuantileAsOf(key string, currentValue float64, lookbackDays int, asOfDate string) (q float64, count int, err error) {
	if s == nil || s.db == nil {
		return 0, 0, nil
	}
	asOfDate = normalizeAsOfDate(asOfDate)
	rows, err := s.db.Query(`
		SELECT value FROM daily_metrics
		WHERE key = ?
		  AND date <= date(?)
		  AND date >= date(?, ?)
		ORDER BY date DESC
	`, key, asOfDate, asOfDate, fmt.Sprintf("-%d days", lookbackDays))
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()

	var samples []float64
	for rows.Next() {
		var v float64
		if err := rows.Scan(&v); err != nil {
			return 0, 0, err
		}
		samples = append(samples, v)
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	count = len(samples)
	if count == 0 {
		return 0, 0, nil
	}
	sort.Float64s(samples)
	// 二分定位：<= currentValue 的样本占比
	idx := sort.Search(count, func(i int) bool { return samples[i] > currentValue })
	return float64(idx) / float64(count), count, nil
}

func normalizeAsOfDate(asOfDate string) string {
	if asOfDate == "" {
		return time.Now().UTC().Format("2006-01-02")
	}
	if t, err := time.Parse("2006-01-02", asOfDate); err == nil {
		return t.UTC().Format("2006-01-02")
	}
	return asOfDate
}
