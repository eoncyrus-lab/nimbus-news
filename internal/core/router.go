package core

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Ricaardo/nimbus-os/news/internal/channel"
	"github.com/Ricaardo/nimbus-os/news/internal/config"
	"github.com/Ricaardo/nimbus-os/news/internal/digest"
	"github.com/Ricaardo/nimbus-os/news/internal/model"
)

// Router 消息路由器
// 负责协调新闻推送和用户消息的路由
type Router struct {
	channels     *channel.Manager
	newsEngine   *NewsEngine
	digestWorker *digestDeliveryWorker

	ctx        context.Context
	cancelFunc context.CancelFunc
}

func (r *Router) SetDigestStore(store digest.Store) {
	r.digestWorker = nil
	deliveryStore, ok := store.(digestDeliveryStore)
	if ok {
		r.digestWorker = newDigestDeliveryWorker(deliveryStore, r.channels, digestDeliveryWorkerOptions{})
	}
}

// NewRouter 创建路由器
func NewRouter(channels *channel.Manager) *Router {
	return &Router{
		channels: channels,
	}
}

// SetNewsEngine 设置新闻引擎
func (r *Router) SetNewsEngine(engine *NewsEngine) {
	r.newsEngine = engine
}

// Start 启动路由器
func (r *Router) Start(ctx context.Context) error {
	r.ctx, r.cancelFunc = context.WithCancel(ctx)

	// 启动所有渠道
	if err := r.channels.StartAll(r.ctx); err != nil {
		return fmt.Errorf("start channels failed: %w", err)
	}

	// 启动新闻引擎（standby/admin 只初始化状态，不摄入不投递）
	if r.newsEngine != nil {
		r.newsEngine.Start(r.ctx)
	}
	if r.digestWorker != nil && r.isActive() {
		r.digestWorker.Start(r.ctx)
	}

	slog.Info("router started")
	return nil
}

func (r *Router) isActive() bool {
	return r.newsEngine == nil || r.newsEngine.Role() == config.RoleActive
}

// Promote 人工把 standby 提升为 active：启动源调度与 digest 投递。
func (r *Router) Promote() bool {
	if r.newsEngine == nil || !r.newsEngine.Promote() {
		return false
	}
	if r.digestWorker != nil {
		r.digestWorker.Start(r.ctx)
	}
	slog.Info("router promoted to active")
	return true
}

// Stop 停止路由器
func (r *Router) Stop() {
	slog.Info("router stopping")

	// 停止新闻引擎
	if r.newsEngine != nil {
		r.newsEngine.Stop()
	}

	// Drain or cancel any claimed attempt before stopping its target channel.
	if r.digestWorker != nil {
		r.digestWorker.Stop()
	}

	if r.cancelFunc != nil {
		r.cancelFunc()
	}

	// 停止所有渠道
	r.channels.StopAll()

	slog.Info("router stopped")
}

// DispatchNews 分发新闻到指定渠道
func (r *Router) DispatchNews(ctx context.Context, news *model.Message, channelNames []string) error {
	if leaseID := news.GetStringMetadata("digest_lease_id"); leaseID != "" {
		if r.digestWorker == nil {
			return fmt.Errorf("digest delivery worker unavailable")
		}
		if _, err := r.digestWorker.store.PrepareDelivery(ctx, leaseID, news, channelNames, time.Now().Add(24*time.Hour)); err != nil {
			return err
		}
		r.digestWorker.Wake()
		return nil
	}
	return r.channels.Broadcast(ctx, news, channelNames)
}

// DispatchNewsDetailed 分发并返回发送失败的渠道，供补推队列只重投失败渠道。
func (r *Router) DispatchNewsDetailed(ctx context.Context, news *model.Message, channelNames []string) ([]string, error) {
	if leaseID := news.GetStringMetadata("digest_lease_id"); leaseID != "" {
		if r.digestWorker == nil {
			return append([]string(nil), channelNames...), fmt.Errorf("digest delivery worker unavailable")
		}
		if _, err := r.digestWorker.store.PrepareDelivery(ctx, leaseID, news, channelNames, time.Now().Add(24*time.Hour)); err != nil {
			return append([]string(nil), channelNames...), err
		}
		r.digestWorker.Wake()
		return nil, nil
	}
	return r.channels.BroadcastDetailed(ctx, news, channelNames)
}

// DispatchNewsToAll 分发新闻到所有可发送渠道
func (r *Router) DispatchNewsToAll(ctx context.Context, news *model.Message) error {
	return r.channels.BroadcastAll(ctx, news)
}

// GetChannelManager 获取渠道管理器
func (r *Router) GetChannelManager() *channel.Manager {
	return r.channels
}

// DigestDeliverySnapshot returns delivery health independently of source fetch health.
func (r *Router) DigestDeliverySnapshot() DigestDeliverySnapshot {
	if r.digestWorker == nil {
		return DigestDeliverySnapshot{}
	}
	return r.digestWorker.Snapshot()
}

func (r *Router) DigestAdminStore() digest.AdminStore {
	if r.digestWorker == nil {
		return nil
	}
	store, _ := r.digestWorker.store.(digest.AdminStore)
	return store
}

// TriggerSource 手动触发指定源（测试用）
func (r *Router) TriggerSource(name string) error {
	if r.newsEngine == nil {
		return fmt.Errorf("news engine not available")
	}
	return r.newsEngine.TriggerSource(name)
}

func (r *Router) WakeDigestDelivery() {
	if r.digestWorker != nil {
		r.digestWorker.Wake()
	}
}
