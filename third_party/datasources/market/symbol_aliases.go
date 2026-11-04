// symbol_aliases.go — hard-coded symbol alias/normalization table.
// Extracted from the former market/yahoo.go (YahooService removed); the
// alias map + A-share/HK/crypto suffix rules are still used by cached.go.
package market

import (
	"fmt"
	"regexp"
	"strings"
)

func normalizeSymbol(symbol string) string {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return ""
	}

	// 1. 优先使用索引库查询（覆盖 18000+ 标的）
	idx := GetSymbolIndex()
	if idx.IsLoaded() {
		if resolved, found := idx.ResolveSymbol(symbol); found {
			return resolved
		}
	}

	// 2. 如果索引库没有找到，使用硬编码别名表作为备选
	lowerSym := strings.ToLower(symbol)

	// ========== 加密货币 ==========
	cryptoAliases := map[string]string{
		// 主流币
		"比特币": "BTCUSDT", "btc": "BTCUSDT", "bitcoin": "BTCUSDT",
		"以太坊": "ETHUSDT", "eth": "ETHUSDT", "ethereum": "ETHUSDT",
		"sol": "SOLUSDT", "solana": "SOLUSDT",
		"bnb": "BNBUSDT", "币安币": "BNBUSDT",
		"xrp": "XRPUSDT", "瑞波": "XRPUSDT", "ripple": "XRPUSDT",
		"ada": "ADAUSDT", "cardano": "ADAUSDT",
		"avax": "AVAXUSDT", "avalanche": "AVAXUSDT",
		"dot": "DOTUSDT", "polkadot": "DOTUSDT", "波卡": "DOTUSDT",
		"matic": "MATICUSDT", "polygon": "MATICUSDT",
		"link": "LINKUSDT", "chainlink": "LINKUSDT",
		"atom": "ATOMUSDT", "cosmos": "ATOMUSDT",
		"uni": "UNIUSDT", "uniswap": "UNIUSDT",
		"ltc": "LTCUSDT", "litecoin": "LTCUSDT", "莱特币": "LTCUSDT",
		"etc": "ETCUSDT", "以太经典": "ETCUSDT",
		"xlm": "XLMUSDT", "stellar": "XLMUSDT",
		"trx": "TRXUSDT", "tron": "TRXUSDT", "波场": "TRXUSDT",
		"near": "NEARUSDT",
		"apt":  "APTUSDT", "aptos": "APTUSDT",
		"sui": "SUIUSDT",
		"arb": "ARBUSDT", "arbitrum": "ARBUSDT",
		"op": "OPUSDT", "optimism": "OPUSDT",
		"inj": "INJUSDT", "injective": "INJUSDT",
		"sei": "SEIUSDT",
		"ton": "TONUSDT", "toncoin": "TONUSDT",
		"fil": "FILUSDT", "filecoin": "FILUSDT",
		"ftm": "FTMUSDT", "fantom": "FTMUSDT",
		// Meme 币
		"doge": "DOGEUSDT", "dogecoin": "DOGEUSDT", "狗狗币": "DOGEUSDT",
		"shib": "SHIBUSDT", "柴犬币": "SHIBUSDT",
		"pepe":  "PEPEUSDT",
		"floki": "FLOKIUSDT",
		"bonk":  "BONKUSDT",
		"wif":   "WIFUSDT",
		"meme":  "MEMEUSDT",
		"turbo": "TURBOUSDT",
		"neiro": "NEIROUSDT",
		// 稳定币相关
		"usdt": "USDT", "泰达币": "USDT",
		"usdc": "USDC",
	}

	// ========== 美股 ==========
	usStockAliases := map[string]string{
		// 科技巨头
		"苹果": "AAPL", "apple": "AAPL",
		"微软": "MSFT", "microsoft": "MSFT",
		"谷歌": "GOOG", "google": "GOOG", "alphabet": "GOOGL",
		"亚马逊": "AMZN", "amazon": "AMZN",
		"特斯拉": "TSLA", "tesla": "TSLA",
		"英伟达": "NVDA", "nvidia": "NVDA", "老黄": "NVDA",
		"meta": "META", "facebook": "META", "脸书": "META",
		"奈飞": "NFLX", "netflix": "NFLX",
		// 半导体
		"amd": "AMD", "超威": "AMD",
		"intel": "INTC", "英特尔": "INTC",
		"高通": "QCOM", "qualcomm": "QCOM",
		"博通": "AVGO", "broadcom": "AVGO",
		"台积电": "TSM", "tsmc": "TSM",
		"asml": "ASML", "阿斯麦": "ASML",
		"美光": "MU", "micron": "MU",
		// AI 概念
		"palantir": "PLTR", "pltr": "PLTR",
		"snowflake":   "SNOW",
		"crowdstrike": "CRWD",
		"datadog":     "DDOG",
		// 互联网/软件
		"salesforce": "CRM",
		"adobe":      "ADBE",
		"oracle":     "ORCL", "甲骨文": "ORCL",
		"ibm": "IBM",
		"思科":  "CSCO", "cisco": "CSCO",
		// 电商/支付
		"shopify": "SHOP",
		"paypal":  "PYPL", "贝宝": "PYPL",
		"square": "SQ", "block": "SQ",
		"visa": "V", "维萨": "V",
		"mastercard": "MA", "万事达": "MA",
		// 社交/娱乐
		"snap":      "SNAP",
		"pinterest": "PINS",
		"roblox":    "RBLX",
		"spotify":   "SPOT",
		"迪士尼":       "DIS", "disney": "DIS",
		// 电动车/新能源
		"蔚来": "NIO", "nio": "NIO",
		"小鹏": "XPEV", "xpeng": "XPEV",
		"理想": "LI", "li auto": "LI",
		"rivian": "RIVN",
		"lucid":  "LCID",
		// 金融
		"摩根大通": "JPM", "jpmorgan": "JPM",
		"高盛": "GS", "goldman": "GS",
		"摩根士丹利": "MS",
		"美国银行":  "BAC", "bofa": "BAC",
		"花旗": "C", "citibank": "C",
		"富国": "WFC", "wells fargo": "WFC",
		"伯克希尔": "BRK-B", "berkshire": "BRK-B", "巴菲特": "BRK-B",
		// 消费
		"可口可乐": "KO", "coca cola": "KO",
		"百事": "PEP", "pepsi": "PEP",
		"麦当劳": "MCD", "mcdonalds": "MCD",
		"星巴克": "SBUX", "starbucks": "SBUX",
		"耐克": "NKE", "nike": "NKE",
		"沃尔玛": "WMT", "walmart": "WMT",
		"costco": "COST", "好市多": "COST",
		// 医药
		"强生": "JNJ", "johnson": "JNJ",
		"辉瑞": "PFE", "pfizer": "PFE",
		"默沙东": "MRK", "merck": "MRK",
		"礼来": "LLY", "eli lilly": "LLY",
		"诺和诺德": "NVO", "novo nordisk": "NVO",
		// 其他
		"波音": "BA", "boeing": "BA",
		"埃克森美孚": "XOM", "exxon": "XOM",
		"雪佛龙": "CVX", "chevron": "CVX",
	}

	// ========== 中概股 ==========
	chinaADRAliases := map[string]string{
		"阿里": "BABA", "alibaba": "BABA", "阿里巴巴": "BABA",
		"京东": "JD", "jd": "JD",
		"拼多多": "PDD", "pdd": "PDD", "pinduoduo": "PDD",
		"百度": "BIDU", "baidu": "BIDU",
		"网易": "NTES", "netease": "NTES",
		"哔哩哔哩": "BILI", "bilibili": "BILI", "b站": "BILI",
		"携程": "TCOM", "ctrip": "TCOM",
		"新东方": "EDU",
		"富途":  "FUTU", "futu": "FUTU",
		"老虎证券": "TIGR", "tiger": "TIGR",
		"唯品会": "VIPS",
		"爱奇艺": "IQ",
		"微博":  "WB", "weibo": "WB",
		"腾讯音乐": "TME",
		"贝壳":   "BEKE",
	}

	// ========== 港股 ==========
	hkStockAliases := map[string]string{
		"腾讯": "0700.HK", "tencent": "0700.HK", "腾讯控股": "0700.HK",
		"阿里港股": "9988.HK",
		"美团":   "3690.HK", "meituan": "3690.HK",
		"小米": "1810.HK", "xiaomi": "1810.HK",
		"京东港股": "9618.HK",
		"快手":   "1024.HK", "kuaishou": "1024.HK",
		"网易港股": "9999.HK",
		"比亚迪":  "1211.HK", "byd": "1211.HK",
		"中芯国际": "0981.HK", "smic": "0981.HK",
		"汇丰": "0005.HK", "hsbc": "0005.HK",
		"友邦": "1299.HK", "aia": "1299.HK",
		"港交所": "0388.HK", "hkex": "0388.HK",
		"中国平安": "2318.HK",
		"工商银行": "1398.HK", "icbc": "1398.HK",
		"建设银行": "0939.HK", "ccb": "0939.HK",
		"招商银行": "3968.HK", "cmb": "3968.HK",
		"中国移动": "0941.HK",
		"中国电信": "0728.HK",
		"中国联通": "0762.HK",
		"中石油":  "0857.HK", "petrochina": "0857.HK",
		"中海油": "0883.HK", "cnooc": "0883.HK",
	}

	// ========== A股 ==========
	aShareAliases := map[string]string{
		"茅台": "600519.SS", "贵州茅台": "600519.SS",
		"五粮液":  "000858.SZ",
		"宁德时代": "300750.SZ", "catl": "300750.SZ",
		"隆基绿能": "601012.SS", "隆基": "601012.SS",
		"中国平安a": "601318.SS",
		"招商银行a": "600036.SS",
		"兴业银行":  "601166.SS",
		"中信证券":  "600030.SS",
		"海康威视":  "002415.SZ", "hikvision": "002415.SZ",
		"立讯精密": "002475.SZ",
		"迈瑞医疗": "300760.SZ",
		"药明康德": "603259.SS",
		"恒瑞医药": "600276.SS",
		"中国中免": "601888.SS",
		"三一重工": "600031.SS",
		"美的集团": "000333.SZ", "midea": "000333.SZ",
		"格力电器": "000651.SZ", "格力": "000651.SZ", "gree": "000651.SZ",
		"比亚迪a": "002594.SZ",
		"长城汽车": "601633.SS",
		"万科":   "000002.SZ", "vanke": "000002.SZ",
		"保利发展":  "600048.SS",
		"中国神华":  "601088.SS",
		"中国石油a": "601857.SS",
		"工商银行a": "601398.SS",
		"建设银行a": "601939.SS",
		"农业银行":  "601288.SS",
		"中国银行a": "601988.SS",
	}

	// ========== 指数 ==========
	indexAliases := map[string]string{
		// 美国
		"标普": "^GSPC", "sp500": "^GSPC", "标普500": "^GSPC", "s&p500": "^GSPC",
		"纳指": "^IXIC", "nasdaq": "^IXIC", "纳斯达克": "^IXIC",
		"道指": "^DJI", "道琼斯": "^DJI", "djia": "^DJI", "dow": "^DJI",
		"罗素2000": "^RUT", "russell": "^RUT",
		"恐慌指数": "^VIX", "vix": "^VIX",
		// 中国
		"上证": "000001.SS", "shanghai": "000001.SS", "上证指数": "000001.SS", "sh": "000001.SS",
		"深证": "399001.SZ", "shenzhen": "399001.SZ", "深证成指": "399001.SZ", "sz": "399001.SZ",
		"创业板": "399006.SZ", "chinext": "399006.SZ",
		"科创50":  "000688.SS",
		"沪深300": "000300.SS", "hs300": "000300.SS", "csi300": "000300.SS",
		"中证500": "000905.SS", "csi500": "000905.SS",
		"中证1000": "000852.SS",
		// 香港
		"恒指": "^HSI", "hsi": "^HSI", "恒生指数": "^HSI", "hang seng": "^HSI",
		"恒生科技": "^HSTECH", "hstech": "^HSTECH",
		"国企指数": "^HSCE", "h股指数": "^HSCE",
		// 其他
		"日经": "^N225", "nikkei": "^N225", "日经225": "^N225",
		"德指": "^GDAXI", "dax": "^GDAXI",
		"富时100": "^FTSE", "ftse": "^FTSE",
		"欧洲斯托克50": "^STOXX50E",
	}

	// ========== 期货/商品 ==========
	commodityAliases := map[string]string{
		"黄金": "GC=F", "gold": "GC=F", "伦敦金": "GC=F",
		"白银": "SI=F", "silver": "SI=F",
		"原油": "CL=F", "oil": "CL=F", "wti": "CL=F", "美油": "CL=F",
		"布油": "BZ=F", "brent": "BZ=F", "布伦特": "BZ=F",
		"天然气": "NG=F", "natural gas": "NG=F",
		"铜": "HG=F", "copper": "HG=F",
		"铂金": "PL=F", "platinum": "PL=F",
		"钯金": "PA=F", "palladium": "PA=F",
		"玉米": "ZC=F", "corn": "ZC=F",
		"大豆": "ZS=F", "soybean": "ZS=F",
		"小麦": "ZW=F", "wheat": "ZW=F",
	}

	// ========== 外汇 ==========
	forexAliases := map[string]string{
		"美元指数": "DX-Y.NYB", "dxy": "DX-Y.NYB", "usdx": "DX-Y.NYB",
		"欧元美元": "EURUSD=X", "eurusd": "EURUSD=X",
		"美元日元": "USDJPY=X", "usdjpy": "USDJPY=X",
		"英镑美元": "GBPUSD=X", "gbpusd": "GBPUSD=X",
		"美元人民币": "USDCNY=X", "usdcny": "USDCNY=X",
		"离岸人民币": "USDCNH=X", "usdcnh": "USDCNH=X", "cnh": "USDCNH=X",
		"澳元美元": "AUDUSD=X", "audusd": "AUDUSD=X",
		"美元加元": "USDCAD=X", "usdcad": "USDCAD=X",
		"美元瑞郎": "USDCHF=X", "usdchf": "USDCHF=X",
	}

	// ========== ETF ==========
	etfAliases := map[string]string{
		"spy": "SPY", "标普etf": "SPY",
		"qqq": "QQQ", "纳指etf": "QQQ",
		"dia": "DIA", "道指etf": "DIA",
		"iwm": "IWM", "罗素etf": "IWM",
		"arkk": "ARKK", "ark创新": "ARKK",
		"gld": "GLD", "黄金etf": "GLD",
		"slv": "SLV", "白银etf": "SLV",
		"uso": "USO", "原油etf": "USO",
		"tlt": "TLT", "美债etf": "TLT",
		"vxx": "VXX", "恐慌etf": "VXX",
		"kweb": "KWEB", "中概etf": "KWEB",
		"fxi": "FXI", "中国etf": "FXI",
		"eem": "EEM", "新兴市场etf": "EEM",
	}

	// 合并所有别名表并查找
	allAliases := make(map[string]string)
	for k, v := range cryptoAliases {
		allAliases[k] = v
	}
	for k, v := range usStockAliases {
		allAliases[k] = v
	}
	for k, v := range chinaADRAliases {
		allAliases[k] = v
	}
	for k, v := range hkStockAliases {
		allAliases[k] = v
	}
	for k, v := range aShareAliases {
		allAliases[k] = v
	}
	for k, v := range indexAliases {
		allAliases[k] = v
	}
	for k, v := range commodityAliases {
		allAliases[k] = v
	}
	for k, v := range forexAliases {
		allAliases[k] = v
	}
	for k, v := range etfAliases {
		allAliases[k] = v
	}

	if val, ok := allAliases[lowerSym]; ok {
		return val
	}

	// A股自动添加后缀
	if matched, _ := regexp.MatchString(`^\d{6}$`, symbol); matched {
		if symbol[0] == '6' {
			return symbol + ".SS"
		} else if symbol[0] == '0' || symbol[0] == '3' {
			return symbol + ".SZ"
		}
	}
	// 港股
	if matched, _ := regexp.MatchString(`^\d{4}$`, symbol); matched {
		return fmt.Sprintf("%04s.HK", symbol)
	}

	// 加密货币简写自动补 USDT
	upperSym := strings.ToUpper(symbol)
	if len(symbol) >= 2 && len(symbol) <= 6 && !strings.Contains(symbol, ".") && !strings.Contains(symbol, "=") && !strings.HasPrefix(symbol, "^") {
		// 检查是否像加密货币代码
		if matched, _ := regexp.MatchString(`^[A-Za-z]+$`, symbol); matched {
			// 不是已知的股票代码，可能是加密货币
			if !isKnownStockSymbol(upperSym) {
				return upperSym + "USDT"
			}
		}
	}

	return symbol
}

func isKnownStockSymbol(symbol string) bool {
	knownStocks := map[string]bool{
		"AAPL": true, "MSFT": true, "GOOG": true, "GOOGL": true, "AMZN": true,
		"META": true, "TSLA": true, "NVDA": true, "AMD": true, "INTC": true,
		"NFLX": true, "QCOM": true, "AVGO": true, "ADBE": true, "CRM": true,
		"ORCL": true, "IBM": true, "CSCO": true, "PYPL": true, "SQ": true,
		"V": true, "MA": true, "JPM": true, "GS": true, "MS": true, "BAC": true,
		"C": true, "WFC": true, "KO": true, "PEP": true, "MCD": true, "SBUX": true,
		"NKE": true, "WMT": true, "COST": true, "JNJ": true, "PFE": true, "MRK": true,
		"LLY": true, "NVO": true, "BA": true, "XOM": true, "CVX": true,
		"BABA": true, "JD": true, "PDD": true, "BIDU": true, "NTES": true, "BILI": true,
		"NIO": true, "XPEV": true, "LI": true, "TCOM": true, "FUTU": true, "TIGR": true,
		"SPY": true, "QQQ": true, "DIA": true, "IWM": true, "ARKK": true, "GLD": true,
		"SLV": true, "USO": true, "TLT": true, "VXX": true, "KWEB": true, "FXI": true,
		"PLTR": true, "SNOW": true, "CRWD": true, "DDOG": true, "SHOP": true,
		"SNAP": true, "PINS": true, "RBLX": true, "SPOT": true, "DIS": true,
		"TSM": true, "ASML": true, "MU": true, "EDU": true, "VIPS": true, "IQ": true, "WB": true,
	}
	return knownStocks[symbol]
}
