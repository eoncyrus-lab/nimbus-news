package channel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Ricaardo/nimbus-os/news/internal/metrics"
	"github.com/Ricaardo/nimbus-os/news/internal/model"
)

// Manager 渠道管理器
type Manager struct {
	channels map[string]Channel
	idem     IdempotencyStore
	mu       sync.RWMutex
}

// NewManager 创建渠道管理器
func NewManager() *Manager {
	return &Manager{
		channels: make(map[string]Channel),
	}
}

// Add 添加渠道
func (m *Manager) Add(ch Channel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels[ch.Name()] = ch
}

// Get 获取渠道
func (m *Manager) Get(name string) (Channel, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ch, ok := m.channels[name]
	return ch, ok
}

// GetByType 按类型获取渠道列表
func (m *Manager) GetByType(channelType string) []Channel {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []Channel
	for _, ch := range m.channels {
		if ch.Type() == channelType {
			result = append(result, ch)
		}
	}
	return result
}

// All 获取所有渠道
func (m *Manager) All() []Channel {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]Channel, 0, len(m.channels))
	for _, ch := range m.channels {
		result = append(result, ch)
	}
	return result
}

// SendChannels 获取所有可发送的渠道
func (m *Manager) SendChannels() []Channel {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []Channel
	for _, ch := range m.channels {
		mode := ch.Mode()
		if mode == ModePush || mode == ModeBidirectional {
			result = append(result, ch)
		}
	}
	return result
}

// ReceiveChannels 获取所有可接收的渠道
func (m *Manager) ReceiveChannels() []Channel {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []Channel
	for _, ch := range m.channels {
		mode := ch.Mode()
		if mode == ModeReceive || mode == ModeBidirectional {
			result = append(result, ch)
		}
	}
	return result
}

// StartAll 启动所有渠道
func (m *Manager) StartAll(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for name, ch := range m.channels {
		if err := ch.Start(ctx); err != nil {
			return fmt.Errorf("start channel %s failed: %w", name, err)
		}
	}
	return nil
}

// StopAll 停止所有渠道
func (m *Manager) StopAll() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, ch := range m.channels {
		ch.Stop()
	}
}

// SetMessageHandler 为所有可接收渠道设置消息处理器
func (m *Manager) SetMessageHandler(handler MessageHandler) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, ch := range m.channels {
		mode := ch.Mode()
		if mode == ModeReceive || mode == ModeBidirectional {
			ch.SetMessageHandler(handler)
		}
	}
}

// BroadcastDetailed 广播并返回实际发送失败的渠道名，供调用方只补推失败的那几个。
func (m *Manager) BroadcastDetailed(ctx context.Context, msg *model.Message, channelNames []string) ([]string, error) {
	m.mu.RLock()
	jobs := make([]sendJob, 0, len(channelNames))
	for _, name := range channelNames {
		ch, ok := m.channels[name]
		if !ok {
			continue
		}
		jobs = append(jobs, sendJob{name: name, channel: ch})
	}
	m.mu.RUnlock()
	return sendConcurrentDetailed(ctx, msg, jobs, func(ctx context.Context, job sendJob, msg *model.Message) error {
		return job.channel.Send(ctx, msg)
	})
}

// Broadcast 广播消息到指定渠道
func (m *Manager) Broadcast(ctx context.Context, msg *model.Message, channelNames []string) error {
	slog.Debug("DEBUG Broadcast: msg from=", "msg_source", msg.Source, "channelnames", channelNames)
	m.mu.RLock()
	jobs := make([]sendJob, 0, len(channelNames))
	for _, name := range channelNames {
		ch, ok := m.channels[name]
		if !ok {
			// 渠道可能被禁用，静默跳过
			continue
		}
		jobs = append(jobs, sendJob{name: name, channel: ch})
	}
	m.mu.RUnlock()
	return sendConcurrent(ctx, msg, jobs)
}

// SendTo sends to one explicitly named channel and preserves delivery errors.
func (m *Manager) SendTo(ctx context.Context, msg *model.Message, name string) error {
	return m.SendToIdempotent(ctx, msg, name, "")
}

// SendToIdempotent uses a channel's optional idempotency capability without
// changing the at-least-once semantics of ordinary webhook channels.
func (m *Manager) SendToIdempotent(ctx context.Context, msg *model.Message, name, key string) error {
	m.mu.RLock()
	ch, ok := m.channels[name]
	idem := m.idem
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("channel %s not found", name)
	}
	if key == "" {
		return sendConcurrent(ctx, msg, []sendJob{{name: name, channel: ch}})
	}
	// 持久化幂等：进程在"已发出但没来得及记录"之间崩溃时，重投会命中这里而被跳过。
	// 幂等键占用失败不阻断发送（宁可重复，不可丢），只是退化为 at-least-once。
	if idem != nil {
		if claimed, ok := m.claimIdempotency(idem, key); ok && !claimed {
			slog.Debug("channel send skipped by idempotency key", "channel", name, "key", key)
			return nil
		}
	}
	var sendErr error
	if sender, ok := ch.(IdempotentSender); ok {
		sendErr = sendConcurrentWith(ctx, msg, []sendJob{{name: name, channel: ch}}, func(ctx context.Context, job sendJob, msg *model.Message) error {
			return sender.SendIdempotent(ctx, msg, key)
		})
	} else {
		sendErr = sendConcurrent(ctx, msg, []sendJob{{name: name, channel: ch}})
	}
	if sendErr != nil && idem != nil {
		// 发送失败要归还幂等键，否则重试会把自己当成"已送达"永久跳过。
		if err := idem.Delete(idempotencyBucket, key); err != nil {
			slog.Warn("channel idempotency release failed", "channel", name, "key", key, "error", err)
		}
	}
	return sendErr
}

const (
	idempotencyBucket = "channel_idempotency"
	idempotencyTTL    = 7 * 24 * time.Hour
)

// IdempotencyStore 记录已送达的幂等键，要求跨进程重启有效（Bolt 之类）。
type IdempotencyStore interface {
	Exists(bucket, key string) (bool, error)
	Set(bucket, key string, ttl time.Duration) error
	Delete(bucket, key string) error
}

// AtomicClaimer 是 IdempotencyStore 的原子版本：并发重试只有一个能占位成功。
type AtomicClaimer interface {
	Claim(bucket, key string, ttl time.Duration) (bool, error)
}

// SetIdempotencyStore 注入持久化幂等存储，供 digest 送达去重使用。
func (m *Manager) SetIdempotencyStore(store IdempotencyStore) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.idem = store
}

// claimIdempotency 返回是否由本调用占到键；ok=false 表示存储不可用或出错，调用方应继续发送。
func (m *Manager) claimIdempotency(store IdempotencyStore, key string) (claimed bool, ok bool) {
	if claimer, isClaimer := store.(AtomicClaimer); isClaimer {
		got, err := claimer.Claim(idempotencyBucket, key, idempotencyTTL)
		if err != nil {
			slog.Warn("channel idempotency claim failed", "key", key, "error", err)
			return false, false
		}
		return got, true
	}
	seen, err := store.Exists(idempotencyBucket, key)
	if err != nil {
		slog.Warn("channel idempotency lookup failed", "key", key, "error", err)
		return false, false
	}
	if seen {
		return false, true
	}
	if err := store.Set(idempotencyBucket, key, idempotencyTTL); err != nil {
		slog.Warn("channel idempotency record failed", "key", key, "error", err)
		return false, false
	}
	return true, true
}

// Has reports whether a named channel is currently configured.
func (m *Manager) Has(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.channels[name]
	return ok
}

// BroadcastAll 广播消息到所有可发送渠道
func (m *Manager) BroadcastAll(ctx context.Context, msg *model.Message) error {
	channels := m.SendChannels()
	jobs := make([]sendJob, 0, len(channels))
	for _, ch := range channels {
		jobs = append(jobs, sendJob{name: ch.Name(), channel: ch})
	}
	return sendConcurrent(ctx, msg, jobs)
}

type sendJob struct {
	name    string
	channel Channel
}

func sendConcurrent(ctx context.Context, msg *model.Message, jobs []sendJob) error {
	return sendConcurrentWith(ctx, msg, jobs, func(ctx context.Context, job sendJob, msg *model.Message) error {
		return job.channel.Send(ctx, msg)
	})
}

func sendConcurrentWith(ctx context.Context, msg *model.Message, jobs []sendJob, send func(context.Context, sendJob, *model.Message) error) error {
	_, err := sendConcurrentDetailed(ctx, msg, jobs, send)
	return err
}

func sendConcurrentDetailed(ctx context.Context, msg *model.Message, jobs []sendJob, send func(context.Context, sendJob, *model.Message) error) ([]string, error) {
	errorsByIndex := make([]error, len(jobs))
	var wait sync.WaitGroup
	wait.Add(len(jobs))
	for index, job := range jobs {
		go func(index int, job sendJob) {
			defer wait.Done()
			startTime := time.Now()
			err := send(ctx, job, msg)
			metrics.PushDuration.WithLabelValues(job.name).Observe(time.Since(startTime).Seconds())
			if err != nil {
				metrics.PushTotal.WithLabelValues(job.name, "error").Inc()
				slog.Warn("Send to channel", "name", job.name, "err", err)
				errorsByIndex[index] = fmt.Errorf("send to channel %s: %w", job.name, err)
				return
			}
			metrics.PushTotal.WithLabelValues(job.name, "success").Inc()
		}(index, job)
	}
	wait.Wait()
	var failed []string
	for index, err := range errorsByIndex {
		if err != nil {
			failed = append(failed, jobs[index].name)
		}
	}
	return failed, errors.Join(errorsByIndex...)
}

// === 热更新支持 ===

// disabledChannels 存储被禁用的渠道实例
var disabledChannels = make(map[string]Channel)
var disabledMu sync.RWMutex

// EnableChannel 启用渠道
func (m *Manager) EnableChannel(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查是否已经在运行
	if _, exists := m.channels[name]; exists {
		return nil // 已启用
	}

	// 从禁用列表恢复
	disabledMu.Lock()
	ch, exists := disabledChannels[name]
	if exists {
		delete(disabledChannels, name)
	}
	disabledMu.Unlock()

	if !exists {
		return fmt.Errorf("channel %s not found in disabled list", name)
	}

	// 重新启动渠道
	if err := ch.Start(context.Background()); err != nil {
		return fmt.Errorf("start channel %s failed: %w", name, err)
	}

	m.channels[name] = ch
	slog.Info("Channel", "name", name)
	return nil
}

// DisableChannel 禁用渠道
func (m *Manager) DisableChannel(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ch, exists := m.channels[name]
	if !exists {
		return nil // 已禁用
	}

	// 停止渠道
	if err := ch.Stop(); err != nil {
		slog.Warn("Stop channel", "name", name, "err", err)
	}

	// 移到禁用列表
	delete(m.channels, name)

	disabledMu.Lock()
	disabledChannels[name] = ch
	disabledMu.Unlock()

	slog.Info("Channel", "name", name)
	return nil
}

// Remove 移除渠道
func (m *Manager) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ch, exists := m.channels[name]
	if !exists {
		// 可能在禁用列表中
		disabledMu.Lock()
		delete(disabledChannels, name)
		disabledMu.Unlock()
		return nil
	}

	// 停止并移除
	ch.Stop()
	delete(m.channels, name)

	slog.Info("Channel", "name", name)
	return nil
}

// AddWithStart 添加并启动渠道
func (m *Manager) AddWithStart(ctx context.Context, ch Channel) error {
	if err := ch.Start(ctx); err != nil {
		return fmt.Errorf("start channel %s failed: %w", ch.Name(), err)
	}

	m.mu.Lock()
	m.channels[ch.Name()] = ch
	m.mu.Unlock()

	slog.Info("Channel", "ch_name", ch.Name())
	return nil
}

// IsEnabled 检查渠道是否启用
func (m *Manager) IsEnabled(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.channels[name]
	return exists
}

// Count 返回活跃渠道数量
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.channels)
}
