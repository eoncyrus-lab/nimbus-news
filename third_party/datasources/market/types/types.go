package types

import "time"

// MarketQuote 行情数据
type MarketQuote struct {
	Symbol        string    `json:"symbol"`
	Name          string    `json:"name"`
	Price         float64   `json:"price"`
	Change        float64   `json:"change"`
	ChangePercent float64   `json:"change_percent"`
	Volume        int64     `json:"volume,omitempty"`
	MarketCap     float64   `json:"market_cap,omitempty"`
	High          float64   `json:"high,omitempty"`
	Low           float64   `json:"low,omitempty"`
	Open          float64   `json:"open,omitempty"`
	PrevClose     float64   `json:"prev_close,omitempty"`
	UpdateTime    time.Time `json:"update_time"`
	Currency      string    `json:"currency,omitempty"`
	Exchange      string    `json:"exchange,omitempty"`
	AssetType     string    `json:"asset_type,omitempty"` // stock/crypto/forex/commodity
}

// IndexQuote 指数行情
type IndexQuote struct {
	Symbol        string    `json:"symbol"`
	Name          string    `json:"name"`
	Price         float64   `json:"price"`
	Change        float64   `json:"change"`
	ChangePercent float64   `json:"change_percent"`
	UpdateTime    time.Time `json:"update_time"`
}

// SecurityAnalysis 证券分析
type SecurityAnalysis struct {
	Symbol      string    `json:"symbol"`
	Name        string    `json:"name"`
	AssetType   string    `json:"asset_type"`
	Price       float64   `json:"price"`
	RSI         float64   `json:"rsi,omitempty"`
	MACD        float64   `json:"macd,omitempty"`
	MACDSignal  float64   `json:"macd_signal,omitempty"`
	MACDHist    float64   `json:"macd_hist,omitempty"`    // MACD 柱状图
	MA5         float64   `json:"ma5,omitempty"`          // 5日均线
	MA10        float64   `json:"ma10,omitempty"`         // 10日均线
	MA20        float64   `json:"ma20,omitempty"`
	MA50        float64   `json:"ma50,omitempty"`
	MA200       float64   `json:"ma200,omitempty"`
	EMA12       float64   `json:"ema12,omitempty"`        // 12日指数均线
	EMA26       float64   `json:"ema26,omitempty"`        // 26日指数均线
	BollUpper   float64   `json:"boll_upper,omitempty"`   // 布林带上轨
	BollMiddle  float64   `json:"boll_middle,omitempty"`  // 布林带中轨
	BollLower   float64   `json:"boll_lower,omitempty"`   // 布林带下轨
	KDJ_K       float64   `json:"kdj_k,omitempty"`        // KDJ K值
	KDJ_D       float64   `json:"kdj_d,omitempty"`        // KDJ D值
	KDJ_J       float64   `json:"kdj_j,omitempty"`        // KDJ J值
	ATR         float64   `json:"atr,omitempty"`          // 真实波幅
	Volume      int64     `json:"volume,omitempty"`       // 成交量
	VolumeMA5   int64     `json:"volume_ma5,omitempty"`   // 5日成交量均值
	VolumeRatio float64   `json:"volume_ratio,omitempty"` // 量比
	Support     float64   `json:"support,omitempty"`
	Resistance  float64   `json:"resistance,omitempty"`
	High52Week  float64   `json:"high_52week,omitempty"`  // 52周最高
	Low52Week   float64   `json:"low_52week,omitempty"`   // 52周最低
	Trend       string    `json:"trend,omitempty"`        // bullish/bearish/neutral
	TrendSignal string    `json:"trend_signal,omitempty"`
	UpdateTime  time.Time `json:"update_time"`
}

// SentimentData 市场情绪数据
type SentimentData struct {
	Market       string    `json:"market"` // crypto/stock
	FearGreed    int       `json:"fear_greed,omitempty"`
	FearGreedTag string    `json:"fear_greed_tag,omitempty"` // Extreme Fear/Fear/Neutral/Greed/Extreme Greed
	VIX          float64   `json:"vix,omitempty"`
	UpdateTime   time.Time `json:"update_time"`
}

// KLineItem K线数据
type KLineItem struct {
	Time   time.Time `json:"time"`
	Open   float64   `json:"open"`
	High   float64   `json:"high"`
	Low    float64   `json:"low"`
	Close  float64   `json:"close"`
	Volume int64     `json:"volume"`
}

// IPOInfo IPO信息
type IPOInfo struct {
	Symbol       string    `json:"symbol"`
	Name         string    `json:"name"`
	Exchange     string    `json:"exchange"`
	IPODate      time.Time `json:"ipo_date"`
	PriceRange   string    `json:"price_range,omitempty"`
	OfferPrice   float64   `json:"offer_price,omitempty"`
	Shares       int64     `json:"shares,omitempty"`
	Status       string    `json:"status,omitempty"` // upcoming/priced/withdrawn
	Industry     string    `json:"industry,omitempty"`
	Underwriters string    `json:"underwriters,omitempty"`
}

// NewsItem 新闻条目（用于搜索结果）
type NewsItem struct {
	Title      string    `json:"title"`
	Summary    string    `json:"summary,omitempty"`
	Source     string    `json:"source"`
	Link       string    `json:"link,omitempty"`
	PublishAt  time.Time `json:"publish_at"`
	Sentiment  string    `json:"sentiment,omitempty"` // positive/negative/neutral
	Symbols    []string  `json:"symbols,omitempty"`
}

// QuoteRecord 行情记录（原 news internal/store 的持久化行类型；
// news 侧以 type alias 指回此定义，bolt 存储实现留在 news）
type QuoteRecord struct {
	Symbol        string    `json:"symbol"`
	Name          string    `json:"name"`
	Price         float64   `json:"price"`
	Change        float64   `json:"change"`
	ChangePercent float64   `json:"change_percent"`
	Volume        int64     `json:"volume"`
	High          float64   `json:"high"`
	Low           float64   `json:"low"`
	Open          float64   `json:"open"`
	PrevClose     float64   `json:"prev_close"`
	MarketCap     float64   `json:"market_cap,omitempty"`
	Currency      string    `json:"currency,omitempty"`
	Exchange      string    `json:"exchange,omitempty"`
	AssetType     string    `json:"asset_type,omitempty"`
	Timestamp     time.Time `json:"timestamp"`
}
