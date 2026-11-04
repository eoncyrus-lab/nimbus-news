package market

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// SymbolInfo 标的信息
type SymbolInfo struct {
	Symbol   string `json:"symbol"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Type     string `json:"type"`     // stock, crypto, futures, forex, index, etf
	Market   string `json:"market"`   // CN, HK, US, CRYPTO, FX, INDEX
	Exchange string `json:"exchange"` // SSE, SZSE, HKEX, NYSE, NASDAQ, BINANCE 等
}

// SymbolIndex 标的索引
type SymbolIndex struct {
	mu        sync.RWMutex
	bySymbol  map[string]*SymbolInfo // symbol -> info
	byCode    map[string]*SymbolInfo // code -> info
	byName    map[string]*SymbolInfo // name -> info (精确匹配)
	allNames  []nameEntry            // 所有名称 (用于模糊搜索)
	loaded    bool
}

type nameEntry struct {
	Name   string
	Symbol string
}

var (
	globalIndex     *SymbolIndex
	globalIndexOnce sync.Once
)

// GetSymbolIndex 获取全局索引实例
func GetSymbolIndex() *SymbolIndex {
	globalIndexOnce.Do(func() {
		globalIndex = NewSymbolIndex()
		// 尝试多个路径加载索引文件
		paths := getSymbolIndexPaths()
		for _, path := range paths {
			if err := globalIndex.LoadFromFile(path); err == nil {
				fmt.Printf("Symbol index loaded from: %s\n", path)
				return
			}
		}
		fmt.Printf("Warning: failed to load symbol index from any path: %v\n", paths)
	})
	return globalIndex
}

// getSymbolIndexPaths 返回可能的索引文件路径列表
func getSymbolIndexPaths() []string {
	const indexFile = "data/index/symbols.json"
	var paths []string

	// 1. 当前工作目录
	if cwd, err := os.Getwd(); err == nil {
		paths = append(paths, filepath.Join(cwd, indexFile))
	}

	// 2. 可执行文件所在目录
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		paths = append(paths, filepath.Join(exeDir, indexFile))
		// 可执行文件可能在 cmd/platform/ 下，尝试向上两级
		paths = append(paths, filepath.Join(exeDir, "../..", indexFile))
	}

	// 3. 环境变量指定的目录
	if dataDir := os.Getenv("NEWS_DATA_DIR"); dataDir != "" {
		paths = append(paths, filepath.Join(dataDir, "index/symbols.json"))
	}

	// 4. 常见的相对路径
	paths = append(paths, indexFile)
	paths = append(paths, "../data/index/symbols.json")
	paths = append(paths, "../../data/index/symbols.json")

	return paths
}

// NewSymbolIndex 创建新索引
func NewSymbolIndex() *SymbolIndex {
	return &SymbolIndex{
		bySymbol: make(map[string]*SymbolInfo),
		byCode:   make(map[string]*SymbolInfo),
		byName:   make(map[string]*SymbolInfo),
		allNames: make([]nameEntry, 0),
	}
}

// LoadFromFile 从文件加载索引
func (idx *SymbolIndex) LoadFromFile(path string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read file failed: %w", err)
	}

	var symbols []SymbolInfo
	if err := json.Unmarshal(data, &symbols); err != nil {
		return fmt.Errorf("unmarshal failed: %w", err)
	}

	// 构建索引
	idx.bySymbol = make(map[string]*SymbolInfo, len(symbols))
	idx.byCode = make(map[string]*SymbolInfo, len(symbols))
	idx.byName = make(map[string]*SymbolInfo, len(symbols))
	idx.allNames = make([]nameEntry, 0, len(symbols))

	for i := range symbols {
		info := &symbols[i]
		upperSymbol := strings.ToUpper(info.Symbol)
		upperCode := strings.ToUpper(info.Code)

		idx.bySymbol[upperSymbol] = info
		idx.byCode[upperCode] = info
		idx.byName[info.Name] = info
		idx.allNames = append(idx.allNames, nameEntry{Name: info.Name, Symbol: info.Symbol})
	}

	idx.loaded = true
	fmt.Printf("Symbol index loaded: %d symbols\n", len(symbols))
	return nil
}

// IsLoaded 检查索引是否已加载
func (idx *SymbolIndex) IsLoaded() bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.loaded
}

// LookupBySymbol 通过 symbol 查找
func (idx *SymbolIndex) LookupBySymbol(symbol string) *SymbolInfo {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.bySymbol[strings.ToUpper(symbol)]
}

// LookupByCode 通过 code 查找
func (idx *SymbolIndex) LookupByCode(code string) *SymbolInfo {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.byCode[strings.ToUpper(code)]
}

// LookupByName 通过名称精确查找
func (idx *SymbolIndex) LookupByName(name string) *SymbolInfo {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.byName[name]
}

// Search 搜索标的（支持代码、名称、模糊匹配）
func (idx *SymbolIndex) Search(query string) *SymbolInfo {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if !idx.loaded {
		return nil
	}

	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}

	upperQuery := strings.ToUpper(query)

	// 1. 精确匹配 symbol
	if info, ok := idx.bySymbol[upperQuery]; ok {
		return info
	}

	// 2. 精确匹配 code
	if info, ok := idx.byCode[upperQuery]; ok {
		return info
	}

	// 3. 精确匹配名称
	if info, ok := idx.byName[query]; ok {
		return info
	}

	// 4. 模糊匹配名称（包含查询字符串）
	var matches []struct {
		info  *SymbolInfo
		score int
	}

	for _, entry := range idx.allNames {
		name := entry.Name
		// 完全匹配得分最高
		if name == query {
			if info, ok := idx.byName[name]; ok {
				return info
			}
		}

		// 名称以查询开头
		if strings.HasPrefix(name, query) {
			if info, ok := idx.byName[name]; ok {
				matches = append(matches, struct {
					info  *SymbolInfo
					score int
				}{info, 100 - len(name)}) // 短名称优先
			}
			continue
		}

		// 名称包含查询
		if strings.Contains(name, query) {
			if info, ok := idx.byName[name]; ok {
				matches = append(matches, struct {
					info  *SymbolInfo
					score int
				}{info, 50 - len(name)})
			}
		}
	}

	if len(matches) > 0 {
		// 按得分排序
		sort.Slice(matches, func(i, j int) bool {
			return matches[i].score > matches[j].score
		})
		return matches[0].info
	}

	return nil
}

// SearchMultiple 搜索多个匹配结果
func (idx *SymbolIndex) SearchMultiple(query string, limit int) []*SymbolInfo {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if !idx.loaded || query == "" {
		return nil
	}

	query = strings.TrimSpace(query)
	upperQuery := strings.ToUpper(query)

	var results []*SymbolInfo
	seen := make(map[string]bool)

	addResult := func(info *SymbolInfo) {
		if info != nil && !seen[info.Symbol] {
			seen[info.Symbol] = true
			results = append(results, info)
		}
	}

	// 1. 精确匹配
	addResult(idx.bySymbol[upperQuery])
	addResult(idx.byCode[upperQuery])
	addResult(idx.byName[query])

	// 2. 前缀匹配
	for _, entry := range idx.allNames {
		if len(results) >= limit {
			break
		}
		if strings.HasPrefix(entry.Name, query) {
			addResult(idx.byName[entry.Name])
		}
	}

	// 3. 包含匹配
	for _, entry := range idx.allNames {
		if len(results) >= limit {
			break
		}
		if strings.Contains(entry.Name, query) {
			addResult(idx.byName[entry.Name])
		}
	}

	return results
}

// ResolveSymbol 解析用户输入为标准 symbol
// 返回: (symbol, found)
func (idx *SymbolIndex) ResolveSymbol(input string) (string, bool) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", false
	}

	// 检查是否包含中文
	hasChinese := false
	for _, r := range input {
		if unicode.Is(unicode.Han, r) {
			hasChinese = true
			break
		}
	}

	// 如果是中文，优先搜索
	if hasChinese {
		info := idx.Search(input)
		if info != nil {
			return info.Symbol, true
		}
		return "", false
	}

	// 英文/数字输入，尝试各种匹配
	upperInput := strings.ToUpper(input)

	// 精确匹配 symbol
	if info := idx.LookupBySymbol(upperInput); info != nil {
		return info.Symbol, true
	}

	// 精确匹配 code
	if info := idx.LookupByCode(upperInput); info != nil {
		return info.Symbol, true
	}

	// 尝试添加后缀匹配 A 股
	if len(input) == 6 {
		if input[0] == '6' {
			if info := idx.LookupBySymbol(input + ".SS"); info != nil {
				return info.Symbol, true
			}
		} else if input[0] == '0' || input[0] == '3' {
			if info := idx.LookupBySymbol(input + ".SZ"); info != nil {
				return info.Symbol, true
			}
		}
	}

	// 尝试添加后缀匹配港股
	if len(input) <= 4 {
		paddedCode := fmt.Sprintf("%04s", input)
		if info := idx.LookupBySymbol(paddedCode + ".HK"); info != nil {
			return info.Symbol, true
		}
	}

	// 尝试加密货币
	if info := idx.LookupBySymbol(upperInput + "USDT"); info != nil {
		return info.Symbol, true
	}

	return "", false
}

// Count 返回索引中的标的数量
func (idx *SymbolIndex) Count() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.bySymbol)
}

// GetByMarket 获取指定市场的所有标的
func (idx *SymbolIndex) GetByMarket(market string) []*SymbolInfo {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	var result []*SymbolInfo
	market = strings.ToUpper(market)
	for _, info := range idx.bySymbol {
		if strings.ToUpper(info.Market) == market {
			result = append(result, info)
		}
	}
	return result
}

// GetByType 获取指定类型的所有标的
func (idx *SymbolIndex) GetByType(symbolType string) []*SymbolInfo {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	var result []*SymbolInfo
	symbolType = strings.ToLower(symbolType)
	for _, info := range idx.bySymbol {
		if strings.ToLower(info.Type) == symbolType {
			result = append(result, info)
		}
	}
	return result
}
