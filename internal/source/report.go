package source

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Ricaardo/nimbus-os/news/internal/metrics"
)

// ScriptSpec 单个 Python 脚本的执行说明
type ScriptSpec struct {
	Key        string        // 输出 map 里的 key，供 template 查询
	Cmd        string        // 通常是 "python3"
	Args       []string      // 脚本路径 + CLI 参数
	Timeout    time.Duration // 默认 60s
	Optional   bool          // 失败是否允许（默认 true — 不阻断整个报告）
	SourceName string        // 调用方源名 (供 metrics 使用, 可选)
}

// ScriptResult 单个脚本执行结果
type ScriptResult struct {
	Key     string
	Raw     []byte          // 原始 stdout
	Parsed  json.RawMessage // 尝试 JSON 解析后的结果
	Err     error           // 执行错误（超时/非零退出/JSON 解析失败等）
	Latency time.Duration
}

// RunScripts 并发执行多个 Python 脚本, 单个失败不阻断其他
// 返回 map[key]ScriptResult; 调用方负责根据 result.Err 判断是否可用
func RunScripts(ctx context.Context, specs []ScriptSpec) map[string]*ScriptResult {
	results := make(map[string]*ScriptResult, len(specs))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, spec := range specs {
		wg.Add(1)
		go func(s ScriptSpec) {
			defer wg.Done()

			timeout := s.Timeout
			if timeout <= 0 {
				timeout = 60 * time.Second
			}
			runCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			start := time.Now()
			cmd := exec.CommandContext(runCtx, s.Cmd, s.Args...)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			stdout, err := cmd.Output()
			latency := time.Since(start)

			res := &ScriptResult{Key: s.Key, Raw: stdout, Latency: latency}
			status := "success"
			if err != nil {
				tail := lastLines(stderr.String(), 3, 400)
				if tail != "" {
					res.Err = fmt.Errorf("exec %s: %w: %s", s.Key, err, tail)
				} else {
					res.Err = fmt.Errorf("exec %s: %w", s.Key, err)
				}
				if runCtx.Err() == context.DeadlineExceeded {
					status = "timeout"
				} else {
					status = "error"
				}
			} else if len(stdout) > 0 {
				// 尝试 JSON 解析；部分脚本会在 JSON 前后打印 banner（如 baostock 的
				// login/logout success），先严格解析，失败再截取最外层 JSON 片段。
				// Python 的 json.dumps 默认允许 NaN/Infinity，Go 不接受，先归一化。
				body := sanitizeJSONNumbers(stdout)
				var raw json.RawMessage
				if jerr := json.Unmarshal(body, &raw); jerr == nil {
					res.Parsed = raw
				} else if extracted := extractJSONObject(body); extracted != nil {
					res.Parsed = extracted
				} else {
					res.Err = fmt.Errorf("parse json %s: %w", s.Key, jerr)
					status = "error"
				}
			}

			// metrics
			srcName := s.SourceName
			if srcName == "" {
				srcName = s.Key
			}
			metrics.ReportScriptTotal.WithLabelValues(srcName, s.Key, status).Inc()
			metrics.ReportScriptDuration.WithLabelValues(srcName, s.Key).Observe(latency.Seconds())

			mu.Lock()
			results[s.Key] = res
			mu.Unlock()
		}(spec)
	}

	wg.Wait()
	return results
}

// GetJSON 从结果 map 取指定 key 的 JSON, 到目标结构; 失败返回 error
func GetJSON(results map[string]*ScriptResult, key string, dst interface{}) error {
	r, ok := results[key]
	if !ok {
		return fmt.Errorf("key %s not in results", key)
	}
	if r.Err != nil {
		return r.Err
	}
	if len(r.Parsed) == 0 {
		return fmt.Errorf("key %s: empty parsed", key)
	}
	return json.Unmarshal(r.Parsed, dst)
}

// HasOK 判断某脚本是否成功执行并产出数据
func HasOK(results map[string]*ScriptResult, key string) bool {
	r, ok := results[key]
	return ok && r.Err == nil && len(r.Parsed) > 0
}

// SetSpecsSourceName 批量给 specs 填 SourceName (用于 metrics label)
func SetSpecsSourceName(specs []ScriptSpec, sourceName string) []ScriptSpec {
	for i := range specs {
		specs[i].SourceName = sourceName
	}
	return specs
}

// extractJSONObject 从混杂 stdout 中截取最外层 JSON 对象/数组。
// 脚本可能在 JSON 前后打印 banner，banner 里也可能带大括号；
// 因此按括号配对（忽略字符串内的括号）收集候选，返回最后一个可解析的。
func extractJSONObject(stdout []byte) json.RawMessage {
	var best json.RawMessage
	start, depth := -1, 0
	inString, escaped := false, false
	for i, b := range stdout {
		if inString {
			switch {
			case escaped:
				escaped = false
			case b == '\\':
				escaped = true
			case b == '"':
				inString = false
			}
			continue
		}
		switch b {
		case '"':
			if start >= 0 {
				inString = true
			}
		case '{', '[':
			if depth == 0 {
				start = i
			}
			depth++
		case '}', ']':
			if depth == 0 {
				continue
			}
			depth--
			if depth == 0 && start >= 0 {
				candidate := stdout[start : i+1]
				var raw json.RawMessage
				if err := json.Unmarshal(candidate, &raw); err == nil {
					best = raw
				}
				start = -1
			}
		}
	}
	return best
}

// lastLines 取文本末尾若干行并限长，用于把脚本 stderr 的关键信息带进错误。
func lastLines(text string, lines, maxChars int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	parts := strings.Split(text, "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	out := strings.Join(parts, " | ")
	runes := []rune(out)
	if len(runes) > maxChars {
		out = string(runes[len(runes)-maxChars:])
	}
	return out
}

// sanitizeJSONNumbers 把 Python 产出的 NaN / Infinity 字面量替换为 null。
// 字符串内的同名文本保持不变。
func sanitizeJSONNumbers(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString, escaped := false, false
	for i := 0; i < len(data); {
		b := data[i]
		if inString {
			out = append(out, b)
			switch {
			case escaped:
				escaped = false
			case b == '\\':
				escaped = true
			case b == '"':
				inString = false
			}
			i++
			continue
		}
		if b == '"' {
			inString = true
			out = append(out, b)
			i++
			continue
		}
		switch {
		case hasTokenAt(data, i, "-Infinity"):
			out = append(out, "null"...)
			i += len("-Infinity")
		case hasTokenAt(data, i, "Infinity"):
			out = append(out, "null"...)
			i += len("Infinity")
		case hasTokenAt(data, i, "NaN"):
			out = append(out, "null"...)
			i += len("NaN")
		default:
			out = append(out, b)
			i++
		}
	}
	return out
}

// hasTokenAt 判断 token 是否从 i 开始，且前后都是非标识符边界。
func hasTokenAt(data []byte, i int, token string) bool {
	if i+len(token) > len(data) || string(data[i:i+len(token)]) != token {
		return false
	}
	if i > 0 && isIdentByte(data[i-1]) {
		return false
	}
	if i+len(token) < len(data) && isIdentByte(data[i+len(token)]) {
		return false
	}
	return true
}

func isIdentByte(b byte) bool {
	return b == '_' || b == '$' || (b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
