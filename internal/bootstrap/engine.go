package bootstrap

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/Ricaardo/nimbus-os/datasources/market"
	"github.com/Ricaardo/nimbus-os/news/internal/alert"
	"github.com/Ricaardo/nimbus-os/news/internal/channel"
	"github.com/Ricaardo/nimbus-os/news/internal/config"
	"github.com/Ricaardo/nimbus-os/news/internal/core"
	"github.com/Ricaardo/nimbus-os/news/internal/digest"
	"github.com/Ricaardo/nimbus-os/news/internal/event"
	"github.com/Ricaardo/nimbus-os/news/internal/filter"
	"github.com/Ricaardo/nimbus-os/news/internal/llm"
	"github.com/Ricaardo/nimbus-os/news/internal/source"
	"github.com/Ricaardo/nimbus-os/news/internal/store"

	bolt "go.etcd.io/bbolt"
)

// InitRouter creates the router and news engine.
// runtimeRole 解析生效角色：NIMBUS_RUNTIME_ROLE 环境变量优先（双实例同配置、
// 由宿主机注入角色），否则用配置文件里的 runtime.role。
func runtimeRole(cfg *config.PlatformConfig) string {
	role := strings.TrimSpace(os.Getenv("NIMBUS_RUNTIME_ROLE"))
	if role == "" {
		role = cfg.Runtime.Role
	}
	switch role {
	case config.RoleActive, config.RoleStandby, config.RoleAdmin:
		return role
	case "":
		return config.RoleActive
	default:
		slog.Warn("unknown runtime role, falling back to active", "role", role)
		return config.RoleActive
	}
}

func InitRouter(
	cfg *config.PlatformConfig,
	channels *channel.Manager,
	filterChain *filter.Chain,
	asyncEval *filter.AsyncEvaluator,
	st store.Store,
	digestStore digest.Store,
	newsStore *store.NewsStore,
	quoteStore *store.QuoteStore,
	enhancer *llm.NewsEnhancer,
) (*core.Router, *core.NewsEngine, error) {

	router := core.NewRouter(channels)
	router.SetDigestStore(digestStore)

	sourceConfigs := buildSourceConfigs(cfg)
	healthConfig := source.HealthConfig{
		Enabled:            cfg.SourceHealth.Enabled,
		DegradedThreshold:  cfg.SourceHealth.DegradedThreshold,
		UnhealthyThreshold: cfg.SourceHealth.UnhealthyThreshold,
		EmptyThreshold:     cfg.SourceHealth.EmptyThreshold,
		RecoveryInterval:   cfg.GetRecoveryInterval(),
		RenotifyInterval:   cfg.GetRenotifyInterval(),
		NotifyChannels:     cfg.SourceHealth.NotifyChannels,
	}

	eventEngine, err := buildEventEngine(cfg, st)
	if err != nil {
		return nil, nil, err
	}

	engine, err := core.NewNewsEngine(core.NewsEngineConfig{
		Sources:        sourceConfigs,
		Events:         eventEngine,
		FilterChain:    filterChain,
		Store:          st,
		DigestStore:    digestStore,
		NewsStore:      newsStore,
		QuoteStore:     quoteStore,
		Enhancer:       enhancer,
		AsyncEval:      asyncEval,
		Router:         router,
		HealthConfig:   healthConfig,
		MirrorChannels: cfg.MirrorChannels,
		Role:           runtimeRole(cfg),
	})
	if err != nil {
		return nil, nil, err
	}
	router.SetNewsEngine(engine)

	// Health notifier
	if healthConfig.Enabled && len(healthConfig.NotifyChannels) > 0 {
		notifier := source.NewDefaultHealthNotifier(router, healthConfig.NotifyChannels)
		engine.SetHealthNotifier(notifier)
		slog.Info("health monitor enabled", "notify_channels", healthConfig.NotifyChannels)
	} else if healthConfig.Enabled {
		slog.Info("health monitor enabled", "notify_channels", "none")
	}

	return router, engine, nil
}

// buildEventEngine 按配置构造事件模型；mode=off 时返回 nil，不产生任何开销。
func buildEventEngine(cfg *config.PlatformConfig, st store.Store) (*event.Engine, error) {
	if cfg == nil || cfg.Events.Mode == "" || cfg.Events.Mode == "off" {
		return nil, nil
	}
	quietStart, quietEnd, err := config.ParseClockRange(cfg.Events.QuietStart, cfg.Events.QuietEnd)
	if err != nil {
		return nil, fmt.Errorf("events quiet hours: %w", err)
	}
	eventStore := event.Store(event.NewMemoryStore())
	storage := "memory"
	if dbProvider, ok := st.(interface{ DB() *bolt.DB }); ok && dbProvider.DB() != nil {
		eventStore = event.NewBoltEventStore(dbProvider.DB())
		storage = "bolt"
	} else {
		slog.Warn("event model has no bolt store; state will not survive restart")
	}
	engine, err := event.NewEngine(event.Config{
		Mode:              cfg.Events.Mode,
		Location:          cfg.Events.Location,
		QuietStart:        quietStart,
		QuietEnd:          quietEnd,
		DailyBudget:       cfg.Events.DailyBudget,
		DailyHardLimit:    cfg.Events.DailyHardLimit,
		ClassReservation:  cfg.Events.ClassReservation,
		InstantThreshold:  cfg.Events.InstantThreshold,
		DeferThreshold:    cfg.Events.DeferThreshold,
		QuietBypassScore:  cfg.Events.QuietBypassScore,
		StoryWindowHours:  cfg.Events.StoryWindowHours,
		ThreadWindowHours: cfg.Events.ThreadWindowHours,
		UpgradeDelta:      cfg.Events.UpgradeDelta,
		RetentionDays:     cfg.Events.RetentionDays,
	}, event.DefaultRules(), eventStore)
	if err != nil {
		return nil, fmt.Errorf("init event model: %w", err)
	}
	slog.Info("event model initialized", "mode", cfg.Events.Mode, "storage", storage)
	return engine, nil
}

// InitAlert initializes the alert engine.
func InitAlert(cfg *config.PlatformConfig, db *bolt.DB, marketService *market.CachedService) (*alert.Engine, error) {
	var alertEngine *alert.Engine
	if cfg.Alert.Enabled {
		alertStore, err := alert.NewStore(db)
		if err != nil {
			return nil, fmt.Errorf("init alert store: %w", err)
		}
		alertEngine = alert.NewEngine(alertStore, alert.EngineConfig{
			CheckInterval:    cfg.GetAlertCheckInterval(),
			MaxAlertsPerUser: cfg.Alert.MaxAlertsPerUser,
			DefaultCooldown:  cfg.GetAlertDefaultCooldown(),
			Enabled:          true,
		})
		alertEngine.SetMarketProvider(marketService)
		slog.Info("alert engine initialized")
	}
	return alertEngine, nil
}

func buildSourceConfigs(cfg *config.PlatformConfig) []core.SourceConfig {
	var configs []core.SourceConfig
	for _, srcCfg := range cfg.Sources {
		if !srcCfg.IsEnabled() {
			slog.Info("source skipped", "name", srcCfg.Name, "type", srcCfg.Type)
			continue
		}

		aiEnhance := false
		if srcCfg.Options != nil {
			if v, ok := srcCfg.Options["ai_enhance"].(bool); ok {
				aiEnhance = v
			}
		}

		var schedule []string
		if srcCfg.Options != nil {
			if s, ok := srcCfg.Options["schedule"].([]interface{}); ok {
				for _, v := range s {
					if str, ok := v.(string); ok {
						schedule = append(schedule, str)
					}
				}
			}
		}
		if len(srcCfg.Schedule) > 0 {
			schedule = srcCfg.Schedule
		}

		configs = append(configs, core.SourceConfig{
			Name:            srcCfg.Name,
			Type:            srcCfg.Type,
			URL:             srcCfg.URL,
			Interval:        srcCfg.Interval,
			Schedule:        schedule,
			Channels:        srcCfg.Sinks,
			AIEnhance:       aiEnhance,
			TradingDaysOnly: srcCfg.TradingDaysOnly,
			DeliveryMode:    srcCfg.DeliveryMode,
			BriefingTarget:  srcCfg.BriefingTarget,
			Routing:         srcCfg.Routing.DeepCopy(),
			Options:         srcCfg.Options,
		})
	}
	return configs
}
