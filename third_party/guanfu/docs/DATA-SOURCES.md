# 数据源与配置 (guanfu v3)

> 覆盖 4 核心资产 + 任意美股 + 宏观 + 情绪。全部 JSON 日频存档在 `~/.guanfu/prices/`。统一 `guanfu refresh` 刷新,首次全量 / 后续增量。

---

## 一、统一刷新框架 (`guanfu refresh`)

Source 实现 `client.Source` 接口 (`Key / DisplayName / Refresh`),按 dependency order 串行执行。

```bash
guanfu refresh              # 全部刷新
guanfu refresh --dry-run    # 列出将要刷新的 source,不真正拉数据
guanfu refresh --only=btc,fred_dxy    # 只刷指定 key(逗号分隔)
guanfu refresh --skip=gold_cot        # 跳过指定 key
```

**增量协议** (`staleThreshold`):
- `last_date` ≤ 24h → `mode="skip"`,不发请求
- `last_date` 缺失 / > 24h → fetch `lastDate+1 → today`(FRED / Yahoo)
- CAPE 月频 → 28 天 TTL
- Stock 30h TTL(覆盖周末 gap)

**Forecast feature lookup 不做无限 forward-fill**:进入 kNN 的外部序列有最大滞后门槛,超过就视为该日特征缺失。当前门槛:CBOE put/call 5d、FRED 7d、CAPE 45d、COT 10d、VIXY/ETF 5d。这避免 CBOE 2019-2025 历史缺口被错误当成有效特征。

**失败容忍**:单个 source 失败不影响其他;`fail` 状态在最终表中 surface,CLI exit code 1,但 stdout 保留所有结果。

**刷新状态语义**:

| 字段 | 值 | 含义 |
|---|---|---|
| `mode` | `skip` / `full` / `incremental` / `fail` | 本次是否发起并完成数据更新 |
| `skip_reason` | `fresh` / `config` / `no_new_data` | fresh=数据够新;config=缺 API key / 本地脚本;no_new_data=源端暂无新增观察 |
| `stale` | `true/false` | 本地 `last_date` 是否超过该源时效阈值 |
| `action` | `ignore` / `configure` / `refresh` / `investigate` | 人应该怎么处理这行 |
| `impact` | `forecast` / `market_reading` / `both` / `optional` | 影响走势推演、读盘上下文,还是可选增强 |

`guanfu refresh` 的人类表会用 `*` 标记 stale `last_date`。`config skip + stale=true` 表示“不是今天可忽略的 fresh skip”,需要配置 key 或接受该源缺失。

---

## 二、源分级与时效阈值

### Required for Core Behavior

| Key | Impact | Freshness | 缺失影响 |
|---|---|---:|---|
| `btc` | both | 1d | BTC panel / forecast 无法可靠运行 |
| `qqq` / `spy` / `gold` | both | 1d | 对应资产 panel / forecast 无法可靠运行 |
| `usd_cny` | market_reading | 3d | CNY 投资者视角缺失,不影响 forecast |

### Forecast Feature Sources

| Key | Impact | Freshness | 用途 |
|---|---|---:|---|
| `spx_cape` | forecast | 45d | QQQ/SPY 估值特征 |
| `fred_dgs10` / `fred_dxy` / `fred_hy_spread` / `fred_yield_curve` | forecast | 7d | QQQ/SPY 宏观特征 |
| `fred_dfii10` / `fred_breakeven` / `fred_dxy` | forecast | 7d | Gold 宏观特征 |
| `gold_cot` | forecast | 10d | Gold 头寸特征 |
| `vixy` / `uup` / `tlt` | forecast / market_reading | 5d | 波动率、美元和长债代理 |
| `stooq_putcall` | forecast | 5d | QQQ/SPY 期权情绪特征:ratio / 30d change / 252d percentile；storage key 兼容旧名，源为 CBOE |

### Optional / Context Sources

| Key | Impact | Freshness | 缺失影响 |
|---|---|---:|---|
| `fred_fed_funds` / `fred_ecb_deposit_rate` / `fred_boj_call_rate` / `fred_pboc_interbank_rate` | market_reading | 45d | 央行面板降级;stale 时只作宏观背景,不作 forecast 信号 |
| `fred_dgs3mo` | both | 7d | 缺失时 forecast baseline fallback 到 flat 4.5% |
| `defillama_stablecoin_supply` | optional | 1d | 稳定币供应读盘缺失 |
| `cmc_market_context` | market_reading | 1d | CMC 独立市场上下文缺失;不影响 forecast |
| `deribit_options` | market_reading | 1d | BTC DVOL/skew 期权读盘缺失;不影响 forecast |
| `coinbase_btc` | optional | 1d | BTC 美国现货 bid proxy 缺失 |
| `stock_*` | both | 30h | 对应任意美股 panel / forecast 缺失 |

## 三、数据源清单(按刷新顺序)

### 核心价格(2)

| Key | 资产 | 源 | 频率 | 起点 | 费用 |
|---|---|---|---|---|---|
| `btc` | BTC | CoinMetrics `PriceUSD` + Binance 增量 | 日 | 2010-07-18 | 免费 |
| `gold` | 黄金 | Yahoo `GC=F`(COMEX gold futures,前身是 `XAUUSD=X`,2025 退市后切换) | 日 | 2000-08-30 | 免费 |

### Yahoo ETF / 指数(6)

| Key | 标的 | 用途 |
|---|---|---|
| `qqq` | QQQ | Nasdaq-100 ETF |
| `spy` | SPY | S&P 500 ETF |
| `vixy` | ^VIX | 波动率指数 |
| `tlt` | TLT | 20Y+ Treasury ETF |
| `uup` | UUP | USD Bullish ETF(DXY 实时代理) |
| `usd_cny` | CNY=X | USD/CNY 汇率 |

### FRED 宏观(13,需 `FRED_API_KEY`)

| Key | Series | 用途 | 起点 |
|---|---|---|---|
| `fred_dxy` | `DTWEXBGS` | Trade-weighted USD (Broad) | 2006-01-01 |
| `fred_fed_funds` | `DFF` | Effective federal funds rate | 1981-01-01 |
| `fred_dgs10` | `DGS10` | 10Y Treasury yield | 1990-01-01 |
| `fred_dgs3mo` | `DGS3MO` | 3M Treasury yield / risk-free baseline | 1981-09-01 |
| `fred_dfii10` | `DFII10` | 10Y TIPS / real yield | 2003-01-01 |
| `fred_yield_curve` | `T10Y2Y` | 10Y-2Y spread | 1976-06-01 |
| `fred_breakeven` | `T10YIE` | 10Y breakeven inflation | 2003-01-01 |
| `fred_hy_spread` | `BAMLH0A0HYM2` | BofA US HY OAS | 1996-12-31 |
| `fred_tga` | `WTREGEN` | Treasury General Account | 2005-01-05 |
| `fred_rrp` | `RRPONTSYD` | Overnight Reverse Repo | 2013-02-04 |
| `fred_ecb_deposit_rate` | `ECBDFR` | ECB deposit facility rate | 1999-01-01 |
| `fred_boj_call_rate` | `IRSTCI01JPM156N` | Japan overnight call/interbank rate | 1985-07-01 |
| `fred_pboc_interbank_rate` | `IRSTCI01CNM156N` | China overnight call/interbank rate | 1990-01-01 |

FRED 注册: <https://fred.stlouisfed.org>(免费,个人使用无限额)。

### 期权 / 情绪

| Key | 源 | 用途 | 费用 |
|---|---|---|---|
| `deribit_options` | Deribit public API | refresh group;写入 `deribit_dvol` / `deribit_skew_25d_pct` / `deribit_skew_expiry_days` | 免费 |
| `deribit_dvol` | Deribit DVOL OHLC | BTC implied-volatility market reading | 免费 |
| `deribit_skew_25d_pct` | Deribit option chain | BTC 25Δ put IV - call IV;可为负,用 signed series 保存 | 免费 |
| `stooq_putcall` | CBOE official historical CSV + Daily Market Statistics | CBOE total put/call ratio;QQQ/SPY forecast feature + panel positioning | 免费,无需 key |

**CBOE put/call 口径**:`stooq_putcall` 只是 legacy storage key,默认来源已经不是 Stooq。当前默认源直接拉 CBOE 官方 `totalpcarchive.csv` + `totalpc.csv`,并从 CBOE Daily Market Statistics 页面补最近窗口。官方 CSV 覆盖 2003-10-17 → 2019-10-04；2019-10-07 之后没有稳定 bulk CSV,所以默认只抓最近约 420 个自然日,足够计算当前 ratio、30d change 和 252-observation percentile。若未来需要完整 2019-2025 每日历史,应新增可缓存的全量 daily-page backfill,不要重新依赖 Stooq key。

### 估值(1,Python script)

| Key | 源 | 频率 | 起点 |
|---|---|---|---|
| `spx_cape` | Shiller Online Data XLS | 月 | 1871-01 |

CAPE import: `scripts/import_cape.py`(一次性拉 XLS 转 JSON,28d TTL)。

### 任意美股(动态,`stock_*`)

通过 `guanfu import-stock TICKER [DAYS]` 或 `guanfu stock TICKER` 自动触发:

```bash
guanfu import-stock AAPL 3650   # 手动全量拉 10 年
guanfu stock MSFT               # 首次使用时自动 fetch 10 年
```

命名空间:`stock_<lowercase_ticker>.json`(如 `stock_aapl.json`)。严格校验 — 不允许与核心资产 key 冲突。30h TTL(覆盖周末)。

---

## 四、BTC 专用源(未进 refresh 框架,独立拉取)

这些源在 BTC panel build 时即时拉取,不走 `guanfu refresh`,走各自的 TTL 缓存:

| 数据 | 源 | 用途 |
|---|---|---|
| BTC ETF 净流入 | [SoSoValue](https://sosovalue.com) API | 7d / 30d 净流入 |
| 总市值 / BTC 市占率 / 稳定币市值 | [CoinGecko](https://www.coingecko.com) | Flow / Market share |
| 恐慌贪婪指数 | [alternative.me](https://alternative.me/crypto/fear-and-greed-index/) | Positioning |
| 哈希率 / 难度 / mempool | [mempool.space](https://mempool.space) | Network |
| ETH/Top50 kline / 资金费率 / OI | Binance | Positioning + 山寨季 |
| MVRV / NUPL | CoinMetrics community | Valuation(付费 tier 需 `COINMETRICS_API_KEY`) |

**注**:这些源未统一 refresh 是技术债(v3 Track I4),但它们的 TTL 机制和 refresh 框架功能等价,用户无感。

### CMC market context(refresh 源)

`cmc_market_context` 使用 `CMC_API_KEY` 调 CoinMarketCap Pro API latest endpoints,仅落市场观察层数据:

| Key | Endpoint | Impact |
|---|---|---|
| `cmc_total_market_cap_usd` / `cmc_total_volume_24h_usd` | `/v1/global-metrics/quotes/latest` | market_reading |
| `cmc_altcoin_market_cap_usd` / `cmc_altcoin_volume_24h_usd` | `/v1/global-metrics/quotes/latest` | market_reading |
| `cmc_btc_dominance_pct` / `cmc_eth_dominance_pct` | `/v1/global-metrics/quotes/latest` | market_reading |
| `cmc_btc_price_usd` / `cmc_btc_volume_24h_usd` / `cmc_btc_market_cap_usd` | `/v2/cryptocurrency/quotes/latest?id=1` | market_reading |

本源是 CMC 独立交叉检查和后续 CEX/DEX 扩展入口,**不替代** `btc` 的 CoinMetrics+Binance 核心价格历史。

---

## 五、CMC / CoinGecko 候选源评估

结论:两者都有用,但**不应该直接替代当前核心价格 / forecast 管道**。优先级如下:

| 源 | 适合接入 | 不适合作为 |
|---|---|---|
| CoinGecko | 当前已用于 BTC global / stablecoin / dominance;可继续作为 keyless fallback 或 onchain DEX 候选观察源 | 需要强 SLA 的核心 forecast 历史源 |
| CoinMarketCap(CMC) | 已新增 `cmc_market_context`;后续可扩 CEX/DEX 覆盖、exchange assets、trending / listings、DEX token/pair OHLCV | BTC/QQQ/SPY/Gold 核心资产价格的首选源 |

落地规则:

1. CMC / CoinGecko 的 DEX 数据只进入**市场读盘/观察层**;除非能拿到稳定历史序列并通过 ablation,否则不进入 forecast feature bundle。
2. 对 BTC,有价值的候选是 CEX/DEX 流动性、exchange assets / proof-of-reserves、global metrics、fear/greed alternate source,而不是又接一个 BTC spot price。
3. 对 altcoin / memecoin,它们属于 guanfu 明确非目标资产;应由外部 cmc/okx/dex 工具消费,guanfu 只把 BTC 市场结构摘要作为背景。
4. 如果接 CMC,环境变量建议用 `CMC_API_KEY`,并把所有结果标 `impact=market_reading` 直到回测证明有效。

---

## 六、本地网关(可选)

### Futu OpenD — 实时 ETF 报价

Go 端无法直连 OpenD(需加密握手),通过 Python bridge 代理:

```bash
pip install futu-api
mkdir -p ~/.guanfu
curl -sL https://raw.githubusercontent.com/Ricaardo/guanfu/main/pkg/client/futu_bridge.py \
  -o ~/.guanfu/futu_bridge.py
```

用途: QQQ / SPY / GLD / UUP / VIXY / USO (oil proxy) 实时报价,Yahoo 降级 fallback。

| 环境变量 | 默认 | 作用 |
|---|---|---|
| `FUTU_GATEWAY` | `127.0.0.1:11111` | OpenD 地址 |
| `FUTU_ENABLED=0` | 未设置 | 禁用 Futu,直接走 Yahoo |
| `FUTU_BRIDGE` | (自动探测) | 自定义 bridge 路径 |

探测顺序:`$FUTU_BRIDGE` → 二进制同目录 → `~/.guanfu/` → `~/.config/guanfu/`。

---

## 七、环境变量速查

完整列表（~20 个变量，含默认值 + 说明）见 [`deployment.md`](deployment.md) § 环境变量速查。与数据源直接相关的核心变量：

| 变量 | 作用 | 影响 |
|---|---|---|
| `FRED_API_KEY` | FRED 宏观数据 | 缺失时 Macro 域为 placeholder |
| `COINMETRICS_API_KEY` | MVRV / NUPL 付费 tier | 缺失时 Valuation 缺 MVRV Z |
| `CMC_API_KEY` | CoinMarketCap market context + 后续 CEX/DEX 候选源 | 缺失时 `cmc_market_context` config skip |
| `GUANFU_NO_HISTORY=1` | 禁用 history.db | 跳过 15 个非价格指标的 q 分位 |

---

## 八、降级路径

| 首选源不可用 | 降级到 |
|---|---|
| FRED 不可用 | TLT 替代实际利率;UUP 替代 DXY |
| Futu 不可用 | Yahoo 替代 QQQ / SPY / UUP |
| CoinMetrics 付费不可用 | 社区 tier 最多拿到价格,MVRV / NUPL 变 missing |
| Yahoo `XAUUSD=X` 退市 | Yahoo `GC=F`(已切换,历史扩到 2000+) |
| Deribit 不可用 | BTC 期权读盘跳过 DVOL/skew,不影响价格或 forecast |
| CBOE put/call 不可用 | QQQ/SPY put/call 特征缺失,forecast bundle 自动降级 |
| CMC 不可用 / 无 `CMC_API_KEY` | CMC market context config skip,不影响核心 BTC 价格或 forecast |

**`source_health` 面板** — 每个 source 显式带 ok / partial / stale / missing / warning 状态,`as_of` 时间,`fallback_used` 标记,`impact`(forecast / market_reading / both / optional)和 `note`。读盘前先查 `source_health`,避免用 stale / fallback 数据做强结论。

---

## 九、history.db(SQLite)— 非价格指标历史分位

ETF 净流入 / mempool / 资金费率 / 宏观这类**没有公开历史 API** 的指标,guanfu 通过 `~/.guanfu/history.db` 每天采集一行,攒够样本后回填 `q` 字段。

**采集的 15 个指标**:
- Flow: `etf_net_flow_7d_usd`, `etf_net_flow_30d_usd`, `etf_total_assets_usd`, `stablecoin_market_cap_usd`, `stablecoin_supply_30d_pct`
- Network: `mempool_mb`, `hash_rate_ehs`, `difficulty_change_pct`
- Positioning: `funding_rate_pct`, `oi_to_mc`, `fear_greed`
- Macro: `dxy_60d_trend_pct`, `real_yield_10y_pct`, `m2_yoy`, `spx_correlation_30d`, `usd_cny`, `usd_cny_60d_trend_pct`, `global_rate_*`

**时效**:
- 第 1 天:入库,无 q
- 第 30 天:开始显示 q
- 第 365 天:q 完全有意义
- 第 730 天:回看窗口上限,老数据滚动淘汰

BTC 价格相关指标(`sma_200w_dev` / `mayer_multiple` / AHR / 技术指标)的 q 由 BTC 全历史日线直接算,**不进** history.db。

---

## 十、数据目录结构

```
~/.guanfu/
├── prices/                       # JSON 日频存档(refresh 输出)
│   ├── btc.json / qqq.json / spy.json / gold.json
│   ├── btc_mvrv.json / btc_txcnt.json / btc_hashrate.json
│   ├── fred_dxy.json / fred_dgs10.json / fred_dfii10.json / ...
│   ├── spx_cape.json
│   ├── gold_cot.json
│   ├── cmc_total_market_cap_usd.json / cmc_btc_dominance_pct.json / cmc_btc_price_usd.json
│   ├── deribit_dvol.json / deribit_skew_25d_pct.json / deribit_skew_expiry_days.json
│   ├── stooq_putcall.json
│   ├── vixy.json / tlt.json / uup.json / usd_cny.json
│   ├── fred_fed_funds.json / fred_ecb_deposit_rate.json / fred_boj_call_rate.json / fred_pboc_interbank_rate.json
│   └── stock_aapl.json / stock_msft.json / ...    # 任意美股
├── history.db                     # SQLite:15 非价格指标历史分位(730d 滚动)
├── panels/                        # (可选)每日盘面 archive,供 guanfu-similar 使用
│   └── YYYY-MM-DD.json
├── cache/                         # guanfu-backtest / 开发工具缓存
├── claims/                        # Claim + Intent ledger
│   └── YYYY-MM/YYYY-MM-DD-{asset}-{horizon}.json
├── alerts/                        # watch 触发记录
└── portfolio.json                 # opt-in 组合上下文
```

---

## 十一、新增数据源的 checklist

1. 实现 `client.Source` 接口:`Key / DisplayName / Refresh(ctx, store)`
   - 在已有 `*_history.go` 里加(如新增 FRED series),或新建一个文件(如新源家族)
2. 在 `cmd/guanfu/cli_refresh.go: allRefreshSources()` 注册
3. 跑 `go test ./pkg/client` 验证 parser / source 行为
4. 跑 `guanfu refresh --only=<new_key> --dry-run` 确认列表
5. 跑真实 refresh,验证 `~/.guanfu/prices/<new_key>.json` 写入正确
6. 如果新源驱动某个 forecast feature,在 `pkg/forecast/features/bundles.go` 的对应 `XExtractors` 函数里加 extractor,**数据缺失时返回 `nil, false`**(bundles 会自动 skip)
7. 跑 `TestBacktestBundles` 确认回归预算内(见 `docs/archive/v3/guanfu-v3-todo.md` 回归预算表)

---

## 十二、变更记录

| 版本 | 日期 | 变更 |
|---|---|---|
| v1 | 2025-Q4 | 初版 26 数据集 |
| v2 | 2026-05-09 | 统一 refresh 框架 (23 Source) + 任意美股 (`stock_*`) + Gold 切 GC=F |
| **v3** | **2026-05-09** | **对齐 v3 roadmap**:30+ 数据集,新 BTC ad-hoc 源独立 section,`source_health` 强化,数据目录结构明示 Track K/L 未来扩展点 |
| **v4** | **2026-05-10** | **source impact / refresh skip reason / CNY investor lens / CMC+CoinGecko 候选源规则** |
| **v5** | **2026-05-11** | **CBOE 官方无 key put/call 源替代 Stooq 默认路径；Deribit/CMC/put-call 文档、source impact 和回测口径同步** |
