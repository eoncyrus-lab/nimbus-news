package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// ParseClockRange 把 "23:30"/"07:00" 解析成当日分钟数；空值使用默认 23:30–07:00。
func ParseClockRange(start, end string) (int, int, error) {
	parse := func(value, fallback string) (int, error) {
		if strings.TrimSpace(value) == "" {
			value = fallback
		}
		parts := strings.Split(value, ":")
		if len(parts) != 2 {
			return 0, fmt.Errorf("invalid clock %q (want HH:MM)", value)
		}
		hour, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil || hour < 0 || hour > 23 {
			return 0, fmt.Errorf("invalid hour in %q", value)
		}
		minute, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || minute < 0 || minute > 59 {
			return 0, fmt.Errorf("invalid minute in %q", value)
		}
		return hour*60 + minute, nil
	}
	startMinutes, err := parse(start, "23:30")
	if err != nil {
		return 0, 0, err
	}
	endMinutes, err := parse(end, "07:00")
	if err != nil {
		return 0, 0, err
	}
	return startMinutes, endMinutes, nil
}

// PlatformConfig 统一平台配置
// 运行角色：active 唯一摄入/推送；standby 只读常驻；admin 只读 + 手动 FetchOnly。
const (
	RoleActive  = "active"
	RoleStandby = "standby"
	RoleAdmin   = "admin"
)

// RuntimeConfig 运行形态配置
type RuntimeConfig struct {
	Role string `yaml:"role"` // active|standby|admin，空值等价 active
}

type PlatformConfig struct {
	Runtime      RuntimeConfig       `yaml:"runtime"`
	Server       ServerConfig        `yaml:"server"`
	Channels     []ChannelConfig     `yaml:"channels"`
	Sources      []SourceConfig      `yaml:"sources"`
	Market       MarketConfig        `yaml:"market"`
	LLM          LLMConfig           `yaml:"llm"`
	Filters      FiltersConfig       `yaml:"filters"`
	Store        PlatformStoreConfig `yaml:"store"`
	SourceHealth SourceHealthConfig  `yaml:"source_health"`
	Alert        AlertConfig         `yaml:"alert"`
	Events       EventsConfig        `yaml:"events"`

	// MirrorChannels 把所有通过过滤的新闻额外镜像到这些渠道（全量副本）。
	// 等价于给每个源的 channels 追加这些渠道。
	MirrorChannels []string `yaml:"mirror_channels"`

	// FileFingerprint fences reload previews against concurrent disk changes.
	// It is runtime metadata and is never serialized or returned by JSON APIs.
	FileFingerprint string `yaml:"-" json:"-"`
}

// EventsConfig 事件模型（Story/Thread/Decision）配置。
// mode=off 完全关闭；shadow 只记录判定不改推送；active 由事件车道接管 typed routing。
type EventsConfig struct {
	Mode              string  `yaml:"mode"` // off|shadow|active
	Location          string  `yaml:"location"`
	QuietStart        string  `yaml:"quiet_start"` // "23:30"
	QuietEnd          string  `yaml:"quiet_end"`   // "07:00"
	DailyBudget       int     `yaml:"daily_budget"`
	DailyHardLimit    int     `yaml:"daily_hard_limit"`
	ClassReservation  int     `yaml:"class_reservation"`
	InstantThreshold  float64 `yaml:"instant_threshold"`
	DeferThreshold    float64 `yaml:"defer_threshold"`
	QuietBypassScore  float64 `yaml:"quiet_bypass_score"`
	StoryWindowHours  int     `yaml:"story_window_hours"`
	ThreadWindowHours int     `yaml:"thread_window_hours"`
	UpgradeDelta      float64 `yaml:"upgrade_delta"`
	RetentionDays     int     `yaml:"retention_days"`
}

// AlertConfig 告警系统配置
type AlertConfig struct {
	Enabled          bool `yaml:"enabled"`
	CheckInterval    int  `yaml:"check_interval"` // 价格检查间隔（秒）
	MaxAlertsPerUser int  `yaml:"max_alerts_per_user"`
	DefaultCooldown  int  `yaml:"default_cooldown"` // 默认冷却时间（秒）
}

// SourceHealthConfig 数据源健康监控配置
type SourceHealthConfig struct {
	Enabled            bool     `yaml:"enabled"`
	DegradedThreshold  int      `yaml:"degraded_threshold"`       // 降级阈值（连续失败次数）
	UnhealthyThreshold int      `yaml:"unhealthy_threshold"`      // 不健康阈值
	EmptyThreshold     int      `yaml:"empty_degraded_threshold"` // 连续 0 条降级阈值（高频源，默认 6）
	RecoveryInterval   int      `yaml:"recovery_interval"`        // 恢复探测间隔（秒）
	RenotifyInterval   int      `yaml:"renotify_interval"`        // 持续不健康时的重新告警间隔（秒）
	NotifyChannels     []string `yaml:"notify_channels"`          // 告警通知渠道
}

// ServerConfig 服务配置
type ServerConfig struct {
	Name string `yaml:"name"`
	Port int    `yaml:"port"`
}

// ChannelConfig 渠道配置（重构后的统一渠道）
type ChannelConfig struct {
	Name    string                 `yaml:"name"`
	Type    string                 `yaml:"type"`              // wechat/telegram/discord/rest
	Mode    string                 `yaml:"mode"`              // push/receive/bidirectional
	Enabled *bool                  `yaml:"enabled,omitempty"` // 是否启用，默认 true
	Webhook string                 `yaml:"webhook,omitempty"`
	Options map[string]interface{} `yaml:"options,omitempty"`
}

// IsEnabled 检查渠道是否启用
func (c *ChannelConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// MarketConfig 行情配置
type MarketConfig struct {
	Default    string           `yaml:"default"`
	Providers  []ProviderConfig `yaml:"providers"`
	SmartCache SmartCacheConfig `yaml:"smart_cache"`
}

// SmartCacheConfig 智能缓存配置
type SmartCacheConfig struct {
	Enabled       bool `yaml:"enabled"`
	TradingTTL    int  `yaml:"trading_ttl"`     // 交易时段 TTL（秒）
	ClosedTTL     int  `yaml:"closed_ttl"`      // 休市 TTL（秒）
	PreMarketTTL  int  `yaml:"pre_market_ttl"`  // 盘前 TTL（秒）
	AfterHoursTTL int  `yaml:"after_hours_ttl"` // 盘后 TTL（秒）
	CryptoTTL     int  `yaml:"crypto_ttl"`      // 加密货币 TTL（秒）
	CacheSize     int  `yaml:"cache_size"`      // 缓存容量
}

// ProviderConfig 数据提供者配置
type ProviderConfig struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
}

// LLMConfig LLM 配置
type LLMConfig struct {
	Provider     string        `yaml:"provider"      json:"provider"`
	APIURL       string        `yaml:"api_url"       json:"api_url"`
	APIKey       string        `yaml:"api_key"       json:"api_key"`
	Model        string        `yaml:"model"         json:"model"`         // 主模型，默认 deepseek-v4-flash（日常: Chat/Enhance/AI Filter）
	HeavyModel   string        `yaml:"heavy_model"   json:"heavy_model"`   // 深度推理模型，默认 deepseek-v4-pro（Think/ThinkMax）
	ThinkingMode string        `yaml:"thinking_mode" json:"thinking_mode"` // 默认推理模式: thinking / thinking_max
	Enhance      EnhanceConfig `yaml:"enhance"       json:"enhance"`
}

// ModelName 兼容旧配置字段，返回主模型名称
func (c *LLMConfig) ModelName() string {
	if c.Model != "" {
		return c.Model
	}
	return "deepseek-v4-flash"
}

// ReasonerModel 兼容旧调用方，返回深度推理模型名称
func (c *LLMConfig) ReasonerModel() string {
	if c.HeavyModel != "" {
		return c.HeavyModel
	}
	return "deepseek-v4-pro"
}

// EnhanceConfig 新闻增强配置
type EnhanceConfig struct {
	Enabled    bool `yaml:"enabled"      json:"enabled"`
	MaxPerHour int  `yaml:"max_per_hour" json:"max_per_hour"`
}

// PlatformStoreConfig 存储配置（扩展版）
type PlatformStoreConfig struct {
	Type    string             `yaml:"type"`
	Path    string             `yaml:"path"`
	News    NewsStoreConfig    `yaml:"news"`
	Session SessionStoreConfig `yaml:"session"`
}

// NewsStoreConfig 新闻存储配置
type NewsStoreConfig struct {
	MaxItems int `yaml:"max_items"`
	TTL      int `yaml:"ttl"` // 秒
}

const (
	defaultNewsStoreMaxItems = 1000
	maxNewsStoreMaxItems     = 10_000
	defaultNewsStoreTTL      = 7 * 24 * 60 * 60
	maxNewsStoreTTL          = 365 * 24 * 60 * 60
)

// SessionStoreConfig 会话存储配置
type SessionStoreConfig struct {
	MaxHistory int `yaml:"max_history"`
	TTL        int `yaml:"ttl"` // 秒
}

// LoadPlatform 加载平台配置
func LoadPlatform(path string) (*PlatformConfig, error) {
	_, live, fingerprint, _, err := readPlatformFile(path)
	if err != nil {
		return nil, err
	}
	live.FileFingerprint = fingerprint
	return live, nil
}

// Validate checks invariants that must hold for both file reloads and API updates.
func (c *PlatformConfig) Validate() error {
	switch c.Runtime.Role {
	case "", RoleActive, RoleStandby, RoleAdmin:
	default:
		return fmt.Errorf("runtime.role must be one of active/standby/admin, got %q", c.Runtime.Role)
	}
	switch c.Events.Mode {
	case "", "off", "shadow", "active":
	default:
		return fmt.Errorf("events.mode must be one of off/shadow/active, got %q", c.Events.Mode)
	}
	if c.Events.Mode != "" && c.Events.Mode != "off" {
		if _, _, err := ParseClockRange(c.Events.QuietStart, c.Events.QuietEnd); err != nil {
			return fmt.Errorf("events quiet hours: %w", err)
		}
		if c.Events.DeferThreshold > 0 && c.Events.InstantThreshold > 0 &&
			c.Events.DeferThreshold >= c.Events.InstantThreshold {
			return fmt.Errorf("events.defer_threshold must be < instant_threshold")
		}
	}
	if c.Store.News.MaxItems <= 0 || c.Store.News.MaxItems > maxNewsStoreMaxItems {
		return fmt.Errorf("store.news.max_items must be between 1 and %d", maxNewsStoreMaxItems)
	}
	if c.Store.News.TTL <= 0 || c.Store.News.TTL > maxNewsStoreTTL {
		return fmt.Errorf("store.news.ttl must be between 1 and %d seconds", maxNewsStoreTTL)
	}
	sourceNames := make(map[string]struct{}, len(c.Sources))
	for _, src := range c.Sources {
		if src.Name == "" {
			return fmt.Errorf("source name cannot be empty")
		}
		if _, exists := sourceNames[src.Name]; exists {
			return fmt.Errorf("duplicate source name: %s", src.Name)
		}
		sourceNames[src.Name] = struct{}{}
		if err := validateDelivery(src); err != nil {
			return err
		}
	}
	return nil
}

// setDefaults 设置默认值
func (c *PlatformConfig) setDefaults() {
	if c.Runtime.Role == "" {
		c.Runtime.Role = RoleActive
	}
	if c.Server.Port == 0 {
		c.Server.Port = 8080
	}
	if c.Server.Name == "" {
		c.Server.Name = "investment-platform"
	}

	// 事件模型默认值
	if c.Events.Mode == "" {
		c.Events.Mode = "off"
	}
	if c.Events.Location == "" {
		c.Events.Location = "Asia/Shanghai"
	}
	if c.Events.QuietStart == "" {
		c.Events.QuietStart = "23:30"
	}
	if c.Events.QuietEnd == "" {
		c.Events.QuietEnd = "07:00"
	}
	if c.Events.DailyBudget <= 0 {
		c.Events.DailyBudget = 6
	}
	if c.Events.DailyHardLimit <= 0 {
		c.Events.DailyHardLimit = 8
	}
	if c.Events.ClassReservation <= 0 {
		c.Events.ClassReservation = 2
	}
	if c.Events.InstantThreshold <= 0 {
		c.Events.InstantThreshold = 75
	}
	if c.Events.DeferThreshold <= 0 {
		c.Events.DeferThreshold = 40
	}
	if c.Events.QuietBypassScore <= 0 {
		c.Events.QuietBypassScore = 85
	}
	if c.Events.StoryWindowHours <= 0 {
		c.Events.StoryWindowHours = 24
	}
	if c.Events.ThreadWindowHours <= 0 {
		c.Events.ThreadWindowHours = 12
	}
	if c.Events.UpgradeDelta <= 0 {
		c.Events.UpgradeDelta = 15
	}
	if c.Events.RetentionDays <= 0 {
		c.Events.RetentionDays = 30
	}

	// 源默认配置
	for i := range c.Sources {
		normalizeSourceRouting(c.Sources[i].Routing)
		// 只有既没有 interval 也没有 schedule 的源才设置默认 interval
		if c.Sources[i].Interval <= 0 && len(c.Sources[i].Schedule) == 0 {
			c.Sources[i].Interval = 120
		}
		if c.Sources[i].Routing == nil && c.Sources[i].DeliveryMode == "" {
			c.Sources[i].DeliveryMode = DeliveryDirect
		}
	}

	// 渠道默认模式
	for i := range c.Channels {
		if c.Channels[i].Mode == "" {
			c.Channels[i].Mode = "push"
		}
	}

	// 过滤器默认值
	if c.Filters.Dedup.TTL <= 0 {
		c.Filters.Dedup.TTL = 86400
	}

	// 语义去重默认值
	if c.Filters.Dedup.Semantic.Threshold <= 0 {
		c.Filters.Dedup.Semantic.Threshold = 0.6
	}
	if c.Filters.Dedup.Semantic.CacheSize <= 0 {
		c.Filters.Dedup.Semantic.CacheSize = 1000
	}
	if c.Filters.Dedup.Semantic.TimeWindow <= 0 {
		c.Filters.Dedup.Semantic.TimeWindow = 3600 // 1小时
	}

	// 存储默认值
	if c.Store.Type == "" {
		c.Store.Type = "bolt"
	}
	if c.Store.Path == "" {
		c.Store.Path = "data/platform.db"
	}
	if c.Store.News.MaxItems == 0 {
		c.Store.News.MaxItems = defaultNewsStoreMaxItems
	}
	if c.Store.News.TTL == 0 {
		c.Store.News.TTL = defaultNewsStoreTTL // 7天
	}
	if c.Store.Session.MaxHistory == 0 {
		c.Store.Session.MaxHistory = 20
	}
	if c.Store.Session.TTL == 0 {
		c.Store.Session.TTL = 86400 // 24小时
	}

	// LLM 默认值
	if c.LLM.Enhance.MaxPerHour == 0 {
		c.LLM.Enhance.MaxPerHour = 20
	}

	// 健康监控默认值
	if c.SourceHealth.DegradedThreshold == 0 {
		c.SourceHealth.DegradedThreshold = 3
	}
	if c.SourceHealth.UnhealthyThreshold == 0 {
		c.SourceHealth.UnhealthyThreshold = 5
	}
	if c.SourceHealth.RecoveryInterval == 0 {
		c.SourceHealth.RecoveryInterval = 300 // 5分钟
	}
	if c.SourceHealth.RenotifyInterval == 0 {
		c.SourceHealth.RenotifyInterval = 21600 // 6小时
	}

	// 告警系统默认值
	if c.Alert.CheckInterval == 0 {
		c.Alert.CheckInterval = 30 // 30秒
	}
	if c.Alert.MaxAlertsPerUser == 0 {
		c.Alert.MaxAlertsPerUser = 50
	}
	if c.Alert.DefaultCooldown == 0 {
		c.Alert.DefaultCooldown = 300 // 5分钟
	}

	// 智能缓存默认值
	if c.Market.SmartCache.TradingTTL == 0 {
		c.Market.SmartCache.TradingTTL = 30 // 交易时段 30 秒
	}
	if c.Market.SmartCache.ClosedTTL == 0 {
		c.Market.SmartCache.ClosedTTL = 600 // 休市 10 分钟
	}
	if c.Market.SmartCache.PreMarketTTL == 0 {
		c.Market.SmartCache.PreMarketTTL = 120 // 盘前 2 分钟
	}
	if c.Market.SmartCache.AfterHoursTTL == 0 {
		c.Market.SmartCache.AfterHoursTTL = 120 // 盘后 2 分钟
	}
	if c.Market.SmartCache.CryptoTTL == 0 {
		c.Market.SmartCache.CryptoTTL = 30 // 加密货币 30 秒
	}
	if c.Market.SmartCache.CacheSize == 0 {
		c.Market.SmartCache.CacheSize = 2000
	}
}

// expandEnvVars 展开环境变量
func (c *PlatformConfig) expandEnvVars() {
	c.LLM.APIURL = os.ExpandEnv(c.LLM.APIURL)
	c.LLM.APIKey = os.ExpandEnv(c.LLM.APIKey)

	for i := range c.Channels {
		c.Channels[i].Webhook = os.ExpandEnv(c.Channels[i].Webhook)
		if c.Channels[i].Options != nil {
			for k, v := range c.Channels[i].Options {
				c.Channels[i].Options[k] = expandEnvValue(v)
			}
		}
	}

	for i := range c.Sources {
		c.Sources[i].URL = os.ExpandEnv(c.Sources[i].URL)
		if c.Sources[i].Options != nil {
			for k, v := range c.Sources[i].Options {
				c.Sources[i].Options[k] = expandEnvValue(v)
			}
		}
	}
}

// expandEnvValue 递归展开配置值中的环境变量。
// （如 channel options 里的 webhooks: [${DISCORD_WEBHOOK_1}, ...]）
func expandEnvValue(v interface{}) interface{} {
	switch val := v.(type) {
	case string:
		return os.ExpandEnv(val)
	case []interface{}:
		for i, item := range val {
			val[i] = expandEnvValue(item)
		}
		return val
	case map[string]interface{}:
		for key, item := range val {
			val[key] = expandEnvValue(item)
		}
		return val
	default:
		return v
	}
}

// GetNewsTTL 获取新闻存储 TTL
func (c *PlatformConfig) GetNewsTTL() time.Duration {
	return time.Duration(c.Store.News.TTL) * time.Second
}

// GetSessionTTL 获取会话存储 TTL
func (c *PlatformConfig) GetSessionTTL() time.Duration {
	return time.Duration(c.Store.Session.TTL) * time.Second
}

// GetRecoveryInterval 获取恢复探测间隔
func (c *PlatformConfig) GetRecoveryInterval() time.Duration {
	return time.Duration(c.SourceHealth.RecoveryInterval) * time.Second
}

// GetRenotifyInterval 获取持续不健康时的重新告警间隔
func (c *PlatformConfig) GetRenotifyInterval() time.Duration {
	return time.Duration(c.SourceHealth.RenotifyInterval) * time.Second
}

// GetAlertCheckInterval 获取告警检查间隔
func (c *PlatformConfig) GetAlertCheckInterval() time.Duration {
	return time.Duration(c.Alert.CheckInterval) * time.Second
}

// GetAlertDefaultCooldown 获取告警默认冷却时间
func (c *PlatformConfig) GetAlertDefaultCooldown() time.Duration {
	return time.Duration(c.Alert.DefaultCooldown) * time.Second
}

// Print 打印配置信息
func (c *PlatformConfig) Print() {
	fmt.Printf("Platform Config: %s (port=%d)\n", c.Server.Name, c.Server.Port)
	fmt.Printf("  Channels: %d\n", len(c.Channels))
	for _, ch := range c.Channels {
		fmt.Printf("    - %s (type=%s, mode=%s)\n", ch.Name, ch.Type, ch.Mode)
	}
	fmt.Printf("  Sources: %d\n", len(c.Sources))
	for _, src := range c.Sources {
		fmt.Printf("    - %s (type=%s, interval=%ds)\n", src.Name, src.Type, src.Interval)
	}
	fmt.Printf("  LLM: %s (enhance=%v)\n", c.LLM.Provider, c.LLM.Enhance.Enabled)
}
