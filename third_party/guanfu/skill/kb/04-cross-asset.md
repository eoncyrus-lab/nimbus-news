# 跨资产联动 (Cross-Asset Transmission)

> 解释 BTC 与黄金、股票、美元、波动率和油价 proxy 的关系。实时值和 source 以 `cross_asset` / `macro` 域为准。

---

## 适用时机

当以下情况出现时加载本文：

- `btc_spy_corr_30d` 或 `spx_correlation_30d` 明显升高/降低。
- `btc_gold_ratio` 接近历史极端。
- `rel_strength_90d_gold` 显示 BTC 显著跑赢/跑输黄金。
- `uup_price`、`vixy_price`、`oil_proxy_usd` 或 `wti_crude_usd` 出现异常。
- `tlt_60d_trend_pct` 60 日变化绝对值 > 5%（长端利率快速再定价）。
- BTC 与 SPX/Gold 走势方向冲突。

---

## 数据源边界

- 黄金主源通常是 Binance PAXG；GLD 是 Futu 扩展数据。
- QQQ/SPY/UUP/VIXY/GLD/USO 通常来自 Futu，失败时部分资产可走 Yahoo fallback。
- `oil_proxy_usd` 是 USO ETF proxy；`wti_crude_usd` 才是 WTI 近月期货。不要混用。
- 任何跨资产比率都要先看 `source` 和 `updated_at`。

---

## BTC vs 黄金

### 关系框架

| 时间尺度 | 常见关系 | 解释 |
|---|---|---|
| 年 | 同受法币贬值/流动性影响 | 共享货币属性叙事 |
| 月 | 风险偏好轮动 | risk-on 时 BTC 跑赢，避险时黄金跑赢 |
| 日 | 可同涨同跌也可背离 | 取决于冲击类型 |

### `cross_asset.btc_gold_ratio`

历史阈值（按 2017-2024 年数据校准）：

| 区间 | 解读 |
|---|---|
| `< 5` | BTC 极弱，历史深熊区（2018-12, 2022-11） |
| `5-10` | BTC 偏弱 |
| `10-20` | 中性 |
| `20-30` | BTC 偏强 |
| `> 30` | BTC 极强，风险偏好可能过热（2021-04 ~38, 2024-03 ~33） |

> **黄金独立行情会让 ratio 整体漂移**：例如 2024-2025 年黄金独立上行（央行储备购金 + 实际利率回落），即使 BTC 走平 ratio 也会下移。引用阈值时必须同时检查 `gld_etf_price` 趋势——若黄金 60 日涨幅 > 10%，ratio 阈值整体下调一档。

组合优先于单点：

- `btc_gold_ratio <10` + 矿工投降结束：底部确认增强。
- `btc_gold_ratio >30` + funding/OI 过热：顶部风险增强。
- 黄金上涨、BTC 下跌、SPX 下跌：BTC 未被市场当作避险资产，需要降权“数字黄金”叙事。

---

## BTC vs 美股

### 相关性不是方向

`cross_asset.btc_spy_corr_30d` / `macro.spx_correlation_30d` 表示解释变量权重。**阈值表见 `kb/01-macro-transmission.md` 的 SPX 相关性节**——本文不重复。

注意 `cross_asset.btc_spy_corr_30d` 与 `macro.spx_correlation_30d` 同名同义，分别在 cross_asset 和 macro 域下是历史原因；读盘时只引用其中一个值。

### 四象限

| SPX | BTC | 解释 |
|---|---|---|
| 上涨 | 上涨 | 风险偏好一致，增量信息有限 |
| 下跌 | 下跌 | 宏观去风险，需区分流动性冲击还是基本面恶化 |
| 下跌 | 上涨 | BTC 独立叙事强，偏正面 |
| 上涨 | 下跌 | 加密内部问题或 BTC 相对弱，需查事件 |

---

## BTC vs 美元

`cross_asset.uup_price` 是 DXY proxy（追踪 ICE U.S. Dollar Index 期货），不等同于 FRED `DTWEXBGS`（贸易加权美元）。

**美元四象限解读框架（利差/避险/贸易/去美元化）见 `kb/01-macro-transmission.md` 的美元传导链节**——本文只补 cross_asset 域的具体使用。

数据使用：
- `cross_asset.uup_price` 上涨 + `macro.dxy_60d_trend_pct` 上行：美元逆风更可信。
- `cross_asset.uup_price` 上涨但 BTC 也涨：可能是独立叙事或全球避险并存，需看黄金和 SPX。
- UUP 数据 stale 时，优先用 FRED `dxy_60d_trend_pct`，但承认 60 日窗口的滞后性。

---

## BTC vs 长端美债 (TLT)

`cross_asset.tlt_price` / `cross_asset.tlt_60d_trend_pct` 跟踪 iShares 20+ Year Treasury Bond ETF，是**长端利率**的实时代理（与 30Y 收益率反向）。

### 为什么对 BTC 重要

长端利率 = BTC 这类无现金流资产的**折现率分母**。长端利率上行直接压低风险资产估值。它是宏观传导链中**最直接作用于 BTC 估值的环节**：

```
财政赤字 / 通胀预期 / Fed 政策 → 30Y 收益率 → TLT 价格 → BTC 估值分母
```

### 与 `macro.real_yield_10y_pct` 的分工

两者**互补不替代**：

| 指标 | 反映 | 用途 |
|---|---|---|
| `tlt_60d_trend_pct` | 名义长端利率方向 | 判断长端债市再定价方向和速度 |
| `real_yield_10y_pct` | 通胀调整后的实际利率 | 判断持有现金/债券的真实机会成本 |

名义利率上行可能来自：
- 实际利率上升（紧缩）→ TLT 跌 + real_yield 升 → BTC **双重逆风**
- 通胀预期上升（滞胀）→ TLT 跌 + real_yield 不动甚至下降 → 含义不同（黄金可能跑赢 BTC）

读盘必须**两个一起看**，不能只看 TLT 就下结论。

### 组合判读

| 组合 | 含义 |
|---|---|
| `tlt_60d_trend_pct < -10%` + `real_yield_10y_pct > 2%` | 长端利率冲击 + 实际利率高位 → BTC 估值最大压力（2022-Q3 / 2023-Q3 典型） |
| `tlt_60d_trend_pct < -10%` + `real_yield_10y_pct < 1%` | 名义利率上行但实际利率不动 → 通胀预期反弹（滞胀疑虑），BTC vs 黄金读盘要谨慎 |
| `tlt_60d_trend_pct > +10%` + `dxy_60d_trend_pct < 0` + `real_yield_10y_pct` 回落 | 三重宽松信号（衰退避险 / 政策转鸽），BTC 估值顺风 |
| `tlt_60d_trend_pct > +10%` + `hy_spread_bps` 扩大 | 衰退避险流入长端债 + 信用压力 → BTC 短期跟跌（risk-off），中期看政策反应 |
| TLT 暴跌 + 美股暴跌 + DXY 走强 | 经典紧缩冲击模式，转 `kb/06-regime-taxonomy.md` 紧缩冲击节 |

### 历史样本锚

- **2020-08 高点 TLT ~170**：COVID 应对 + Fed 零利率 + 大规模 QE，长端利率跌到 ~1.2%。BTC 同期处于减半后第一波上涨初期。
- **2022-10 低点 TLT ~88**：Fed 50bp/75bp 连续加息，30Y 收益率冲到 ~4.4%。BTC 同期跌至 $15.5k 周期低点。
- **2023-10 低点 TLT ~82**：财政部加大长端发行 + 通胀粘性，30Y 收益率冲到 ~5%。BTC 在 $26k-30k 区间震荡，但因 ETF 预期未深跌。
- **2024-09 反弹 TLT ~100**：Fed 50bp 降息预期 + 经济放缓，长端利率回落到 ~4%。BTC 同期上行到 ATH 附近。

### 失效条件

- TLT 跟踪 20+ 年期债券，**对短端利率（Fed 政策利率）不敏感**。短端再定价应看 2Y 收益率 / SHY ETF（guanfu 不直接提供）。
- 财政部供应冲击（拍卖结果差、Quarterly Refunding 公告）会让 TLT 短期偏离基本面。
- TLT 是 USD 计价，外资美债持有人减持时 TLT 可能跌，但需结合 `dxy_60d_trend_pct` 判断是利率冲击还是货币冲击。

---

## 波动率与恐慌

VIXY 是 VIX 短期期货 ETF proxy，存在期货展期损耗，不等同于 VIX 点位。

读盘规则：

- `vixy_price` 或外部 VIX 高位 + BTC/SPX 相关高：系统性恐慌传导概率上升。
- VIX 高但 BTC 抗跌：BTC 独立性增强，但需要 ETF/稳定币确认。
- VIX 平静 + funding/OI 过热：市场自满，尾部风险上升。

若进入极端恐慌，转 `09-crisis-playbook.md`。

---

## Oil proxy / WTI

油价影响 BTC 的路径通常是：

```
油价/能源冲击 → 通胀预期 → Fed 反应 → 实际利率/美元 → BTC
```

读盘规则：

- `oil_proxy_60d_trend_pct` 或 `wti_crude_60d_trend_pct` 上行本身不是 BTC 利空；要看是否推升实际利率和美元。
- `wti_crude_usd >100` + `real_yield >2%` + SPX 下跌：滞胀压力，需要降低常规模型置信。
- source 为 `futu:US.USO` 时，只能说油价 proxy，不说桶价。

---

## 和 guanfu 联动

每次跨资产分析回答四个问题：

1. BTC 当前是宏观联动还是自身叙事？
2. 如果宏观联动，主导变量是股票、美元、利率、黄金还是油价？
3. BTC 相对黄金和 SPX 是强还是弱？
4. 数据源是否 stale、fallback 或缺失？

输出时用“跨资产证据支持/反证某结论”，不要让单个相关性直接决定方向。
