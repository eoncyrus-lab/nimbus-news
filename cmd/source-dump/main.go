// Command source-dump 按配置逐个拉取数据源并输出统一的 JSON 视图。
// 用于核对每个源的实际输出格式，不落库、不推送、不启动调度。
//
//	go run ./cmd/source-dump -config config.dump.yaml -out /tmp/source-dump
//	go run ./cmd/source-dump -config config.dump.yaml -name bwe-tradfi
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/news/internal/config"
	"github.com/Ricaardo/nimbus-os/news/internal/model"
	"github.com/Ricaardo/nimbus-os/news/internal/source"
)

type messageView struct {
	ID           string                 `json:"id"`
	Type         string                 `json:"type"`
	Title        string                 `json:"title"`
	Content      string                 `json:"content"`
	ContentRunes int                    `json:"content_runes"`
	ContentTrunc bool                   `json:"content_truncated"`
	ShortContent string                 `json:"short_content,omitempty"`
	Link         string                 `json:"link,omitempty"`
	ImageURL     string                 `json:"image_url,omitempty"`
	VideoURL     string                 `json:"video_url,omitempty"`
	Source       string                 `json:"source,omitempty"`
	SourceType   string                 `json:"source_type,omitempty"`
	Tags         []string               `json:"tags,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
	CreateTime   string                 `json:"create_time,omitempty"`
	FetchTime    string                 `json:"fetch_time,omitempty"`
}

type sourceReport struct {
	Name      string        `json:"name"`
	Type      string        `json:"type"`
	Status    string        `json:"status"`
	Error     string        `json:"error,omitempty"`
	ElapsedMS int64         `json:"elapsed_ms"`
	Count     int           `json:"count"`
	Messages  []messageView `json:"messages,omitempty"`
}

type options struct {
	configPath string
	outDir     string
	only       string
	timeout    time.Duration
	maxContent int
}

func main() {
	var opt options
	flag.StringVar(&opt.configPath, "config", "config.dump.yaml", "platform config path")
	flag.StringVar(&opt.outDir, "out", "", "write one JSON file per source into this directory")
	flag.StringVar(&opt.only, "name", "", "only fetch the named source")
	flag.DurationVar(&opt.timeout, "timeout", 5*time.Minute, "per-source timeout")
	flag.IntVar(&opt.maxContent, "max-content", 4000, "truncate content to N runes in the dump")
	flag.Parse()

	cfg, err := config.LoadPlatform(opt.configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load config:", err)
		os.Exit(1)
	}

	selected := 0
	var failed int
	for _, srcCfg := range cfg.Sources {
		if !srcCfg.IsEnabled() {
			continue
		}
		if opt.only != "" && srcCfg.Name != opt.only {
			continue
		}
		selected++
		report := fetchOne(srcCfg, opt)
		if report.Status != "ok" {
			failed++
		}
		printSummary(report)
		if opt.outDir != "" {
			if err := writeReport(opt.outDir, report); err != nil {
				fmt.Fprintln(os.Stderr, "write report:", err)
			}
		}
	}
	if selected == 0 {
		fmt.Fprintln(os.Stderr, "no matching enabled sources")
		os.Exit(1)
	}
	if failed > 0 {
		os.Exit(2)
	}
}

func fetchOne(cfg config.SourceConfig, opt options) sourceReport {
	report := sourceReport{Name: cfg.Name, Type: cfg.Type}
	start := time.Now()

	src, err := source.Create(source.Config{
		Name:    cfg.Name,
		Type:    cfg.Type,
		URL:     cfg.URL,
		Options: cfg.Options,
	})
	if err != nil {
		report.Status = "create_error"
		report.Error = err.Error()
		report.ElapsedMS = time.Since(start).Milliseconds()
		return report
	}

	ctx, cancel := context.WithTimeout(context.Background(), opt.timeout)
	defer cancel()

	var messages []*model.Message
	if fetcher, ok := src.(source.ContextFetcher); ok {
		messages, err = fetcher.FetchContext(ctx)
	} else {
		messages, err = src.Fetch()
	}
	if err != nil {
		report.Status = "fetch_error"
		report.Error = err.Error()
	}
	if ctx.Err() == context.DeadlineExceeded {
		report.Status = "timeout"
		report.Error = fmt.Sprintf("timeout after %s", opt.timeout)
	}
	if report.Status == "" {
		report.Status = "ok"
	}
	report.ElapsedMS = time.Since(start).Milliseconds()
	report.Count = len(messages)
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		report.Messages = append(report.Messages, viewMessage(msg, opt.maxContent))
	}
	return report
}

func viewMessage(msg *model.Message, maxContent int) messageView {
	content := msg.Content
	runes := []rune(content)
	truncated := false
	if maxContent > 0 && len(runes) > maxContent {
		content = string(runes[:maxContent])
		truncated = true
	}
	view := messageView{
		ID:           msg.ID,
		Type:         string(msg.Type),
		Title:        msg.Title,
		Content:      content,
		ContentRunes: len(runes),
		ContentTrunc: truncated,
		ShortContent: msg.ShortContent,
		Link:         msg.Link,
		ImageURL:     msg.ImageURL,
		VideoURL:     msg.VideoURL,
		Source:       msg.Source,
		SourceType:   msg.SourceType,
		Tags:         msg.Tags,
		Metadata:     msg.Metadata,
	}
	if !msg.CreateTime.IsZero() {
		view.CreateTime = msg.CreateTime.Format(time.RFC3339)
	}
	if !msg.FetchTime.IsZero() {
		view.FetchTime = msg.FetchTime.Format(time.RFC3339)
	}
	return view
}

func printSummary(report sourceReport) {
	line := fmt.Sprintf("%-20s %-16s %-12s %6dms count=%d", report.Name, report.Type, report.Status, report.ElapsedMS, report.Count)
	if report.Error != "" {
		line += "  err=" + truncate(report.Error, 160)
	}
	fmt.Println(line)
}

func writeReport(dir string, report sourceReport) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, sanitize(report.Name)+".json")
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func sanitize(name string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, name)
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
