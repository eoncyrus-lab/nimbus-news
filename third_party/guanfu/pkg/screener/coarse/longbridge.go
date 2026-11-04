// Longbridge screener HTTP client.
//
// Talks to a Longbridge MCP server via JSON-RPC 2.0 over HTTP
// (Streamable HTTP transport). Uses the screener_search tool to
// run preset strategies or custom Mode B filters.
//
// Configuration:
//   LONGBRIDGE_MCP_URL — MCP server base URL (default: http://127.0.0.1:8811/mcp)
//   LONGBRIDGE_API_KEY  — optional API key for authenticated endpoints

package coarse

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/go-resty/resty/v2"
)

// Preset strategy IDs for Longbridge screener_recommend_strategies.
const (
	StrategyBuffett     = "16" // 巴菲特策略: margin>40%, ROE>15%, mktcap>10B
	StrategyGraham      = "18" // 格雷厄姆策略: PE<15, PB<1.5, mktcap>100B
	StrategyLowPE       = "9"  // 低估值: PE<15, PB<1.5
	StrategyHighGrowth  = "7"  // 高盈利高成长: 双增长>80%
	StrategyCANSLIM     = "15" // CANSLIM: 多因子成长
	StrategyHighDiv     = "11" // 高股息: divyld>6%
	StrategyQuality     = "17" // 高质量盈利: ROE>10, la<30, growth>5
	StrategyLowDebtGrow = "10" // 高成长低杠杆
	StrategySmallGrowth = "14" // 小盘轻资产快速成长
)

// Market codes for Longbridge screener_search.
const (
	MarketHK = "HK"
	MarketUS = "US"
	MarketCN = "CN"
	MarketSG = "SG"
)

// rpcRequest is a JSON-RPC 2.0 request.
type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
	ID      int         `json:"id"`
}

// rpcResponse is a JSON-RPC 2.0 response.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	ID      int             `json:"id"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// toolCallParams is the MCP tools/call parameters.
type toolCallParams struct {
	Name      string      `json:"name"`
	Arguments interface{} `json:"arguments"`
}

// screenerSearchArgs is the arguments for screener_search.
type screenerSearchArgs struct {
	Market     string `json:"market"`
	StrategyID string `json:"strategy_id,omitempty"` // Mode A: preset
	Page       int    `json:"page,omitempty"`
	Size       int    `json:"size,omitempty"`
}

// screenerResult is the raw response from Longbridge screener_search.
type screenerResult struct {
	Total int                       `json:"total"`
	Items []screenerResultItem      `json:"items"`
}

type screenerResultItem struct {
	Symbol     string                   `json:"symbol"`
	Name       string                   `json:"name"`
	Indicators []screenerResultIndicator `json:"indicators"`
}

type screenerResultIndicator struct {
	Key   string  `json:"key"`
	Name  string  `json:"name"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

// LongbridgeClient calls the Longbridge MCP screener tools.
type LongbridgeClient struct {
	http   *resty.Client
	url    string
	apiKey string
}

// NewLongbridgeClient creates a client with defaults from environment.
// Default endpoint is the hosted Longbridge OpenAPI MCP (OAuth 2.1).
// Set LONGBRIDGE_API_KEY to a valid bearer token for authenticated access.
func NewLongbridgeClient() *LongbridgeClient {
	url := os.Getenv("LONGBRIDGE_MCP_URL")
	if url == "" {
		url = "https://openapi.longbridge.com/mcp"
	}
	apiKey := os.Getenv("LONGBRIDGE_API_KEY")
	client := resty.New().
		SetTimeout(30 * time.Second).
		SetHeader("Content-Type", "application/json")
	if apiKey != "" {
		client.SetHeader("Authorization", "Bearer "+apiKey)
	}
	return &LongbridgeClient{
		http:   client,
		url:    url,
		apiKey: apiKey,
	}
}

// NewLongbridgeClientWithURL creates a client with explicit server URL.
func NewLongbridgeClientWithURL(url, apiKey string) *LongbridgeClient {
	return &LongbridgeClient{
		http: resty.New().
			SetTimeout(30 * time.Second).
			SetHeader("Content-Type", "application/json"),
		url:    url,
		apiKey: apiKey,
	}
}

// RunStrategy runs a Longbridge preset screener strategy and returns coarse candidates.
// strategyID is one of the Strategy* constants; market is one of the Market* constants.
func (c *LongbridgeClient) RunStrategy(ctx context.Context, strategyID, market string, maxResults int) ([]model.ScreenedCandidate, error) {
	allCandidates := make([]model.ScreenedCandidate, 0)
	page := 0
	size := 50
	if maxResults > 0 && maxResults < size {
		size = maxResults
	}

	for {
		candidates, total, err := c.runStrategyPage(ctx, strategyID, market, page, size)
		if err != nil {
			return nil, fmt.Errorf("longbridge strategy %s page %d: %w", strategyID, page, err)
		}
		allCandidates = append(allCandidates, candidates...)

		// Stop if we've fetched all or hit the max.
		if len(allCandidates) >= total || (maxResults > 0 && len(allCandidates) >= maxResults) {
			break
		}
		page++
	}

	if maxResults > 0 && len(allCandidates) > maxResults {
		allCandidates = allCandidates[:maxResults]
	}
	return allCandidates, nil
}

func (c *LongbridgeClient) runStrategyPage(ctx context.Context, strategyID, market string, page, size int) ([]model.ScreenedCandidate, int, error) {
	args := screenerSearchArgs{
		Market:     market,
		StrategyID: strategyID,
		Page:       page,
		Size:       size,
	}

	raw, err := c.callScreenerSearch(ctx, args)
	if err != nil {
		return nil, 0, err
	}

	candidates := make([]model.ScreenedCandidate, 0, len(raw.Items))
	for _, item := range raw.Items {
		c := model.ScreenedCandidate{
			Symbol:     item.Symbol,
			Name:       item.Name,
			Market:     market,
			Source:     "longbridge",
			Indicators: make(map[string]float64, len(item.Indicators)),
		}
		for _, ind := range item.Indicators {
			c.Indicators[ind.Key] = ind.Value
			// Extract common fields
			switch ind.Key {
			case "prevclose":
				c.Price = ind.Value
			case "industry":
				c.Industry = fmt.Sprintf("%v", ind.Value)
			}
		}
		// Industry comes as a string in indicators, not float
		for _, ind := range item.Indicators {
			if ind.Key == "industry" {
				c.Industry = ind.Name // name field has the display value
			}
		}
		candidates = append(candidates, c)
	}
	return candidates, raw.Total, nil
}

// callScreenerSearch makes a single MCP tools/call for screener_search.
func (c *LongbridgeClient) callScreenerSearch(ctx context.Context, args screenerSearchArgs) (*screenerResult, error) {
	req := rpcRequest{
		JSONRPC: "2.0",
		Method:  "tools/call",
		Params: toolCallParams{
			Name:      "screener_search",
			Arguments: args,
		},
		ID: 1,
	}

	resp, err := c.http.R().
		SetContext(ctx).
		SetBody(req).
		Post(c.url)
	if err != nil {
		return nil, fmt.Errorf("mcp call failed: %w", err)
	}
	if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("mcp returned HTTP %d: %s", resp.StatusCode(), string(resp.Body()))
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(resp.Body(), &rpcResp); err != nil {
		return nil, fmt.Errorf("parse mcp response: %w (body: %s)", err, string(resp.Body()))
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("mcp error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	// MCP tools/call returns {content: [{type: "text", text: "..."}]}
	var toolResult struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(rpcResp.Result, &toolResult); err != nil {
		return nil, fmt.Errorf("parse tool result: %w", err)
	}

	if len(toolResult.Content) == 0 {
		return nil, fmt.Errorf("empty mcp tool response")
	}

	var result screenerResult
	if err := json.Unmarshal([]byte(toolResult.Content[0].Text), &result); err != nil {
		return nil, fmt.Errorf("parse screener result: %w (text: %s)", err, toolResult.Content[0].Text)
	}
	return &result, nil
}

// --- Heat / rank list ---

// HeatRankEntry is a single entry from Longbridge rank_list.
type HeatRankEntry struct {
	Symbol string `json:"symbol"`
	Name   string `json:"name"`
}

// rankListArgs is the arguments for rank_list.
type rankListArgs struct {
	Key string `json:"key"`
	Size int   `json:"size,omitempty"`
}

// FetchHeatList fetches a Longbridge rank_list (hot_all, hot_up, etc.)
// and returns the ranked symbols. key is the second_tags key from rank_categories,
// e.g. "hot_all-hk", "hot_up-us", "discuss_heat-hk".
func (c *LongbridgeClient) FetchHeatList(ctx context.Context, key string, size int) ([]HeatRankEntry, error) {
	if size <= 0 {
		size = 50
	}
	args := rankListArgs{Key: key, Size: size}
	req := rpcRequest{
		JSONRPC: "2.0",
		Method:  "tools/call",
		Params: toolCallParams{
			Name:      "rank_list",
			Arguments: args,
		},
		ID: 1,
	}

	resp, err := c.http.R().SetContext(ctx).SetBody(req).Post(c.url)
	if err != nil {
		return nil, fmt.Errorf("rank_list call: %w", err)
	}
	if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("rank_list HTTP %d: %s", resp.StatusCode(), string(resp.Body()))
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(resp.Body(), &rpcResp); err != nil {
		return nil, fmt.Errorf("parse rank_list response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("rank_list error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	var toolResult struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(rpcResp.Result, &toolResult); err != nil {
		return nil, fmt.Errorf("parse rank_list result: %w", err)
	}
	if len(toolResult.Content) == 0 {
		return nil, fmt.Errorf("empty rank_list response")
	}

	var items []HeatRankEntry
	if err := json.Unmarshal([]byte(toolResult.Content[0].Text), &items); err != nil {
		return nil, fmt.Errorf("parse rank_list items: %w", err)
	}
	return items, nil
}

// EnrichHeat adds heat/rank data to a candidate list by cross-referencing
// with Longbridge rank lists. This mutates candidates in place.
func (c *LongbridgeClient) EnrichHeat(ctx context.Context, market string, candidates []model.ScreenedCandidate) error {
	heatLists := []struct {
		key    string
		market string
	}{
		{"hot_all", market},
		{"hot_up", market},
		{"discuss_heat", market},
	}

	for _, hl := range heatLists {
		listKey := hl.key + "-" + strings.ToLower(hl.market)
		entries, err := c.FetchHeatList(ctx, listKey, 100)
		if err != nil {
			continue // heat is optional; don't fail the whole screen
		}
		rankMap := make(map[string]int, len(entries))
		for i, e := range entries {
			rankMap[e.Symbol] = i + 1 // 1-indexed rank
		}
		for i := range candidates {
			sym := candidates[i].Symbol
			if rank, ok := rankMap[sym]; ok {
				switch hl.key {
				case "hot_all":
					candidates[i].HeatRank = rank
				case "hot_up":
					candidates[i].HeatRising = true
				case "discuss_heat":
					candidates[i].DiscussHot = true
				}
			}
		}
	}
	return nil
}
