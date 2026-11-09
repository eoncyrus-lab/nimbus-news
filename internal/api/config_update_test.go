package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ricaardo/nimbus-os/news/internal/config"
)

func TestUpdateConfigReturnsBadRequestForInvalidDelivery(t *testing.T) {
	server := newConfigTestServer(t)
	for _, body := range []string{
		`{"sources":[{"name":"bad","type":"rss","delivery_mode":"later"}]}`,
		`{"sources":[{"name":"bad","type":"rss","delivery_mode":"digest","briefing_target":"later"}]}`,
	} {
		assertConfigStatus(t, server, http.MethodPut, "/api/config", body, http.StatusBadRequest)
	}
}

func TestUpdateConfigReturnsBadRequestForUnsafeNewsStoreBounds(t *testing.T) {
	server := newConfigTestServer(t)
	for _, body := range []string{
		`{"Store":{"News":{"MaxItems":-1,"TTL":1}}}`,
		`{"Store":{"News":{"MaxItems":1000001,"TTL":1}}}`,
		`{"Store":{"News":{"MaxItems":1,"TTL":-1}}}`,
		`{"Store":{"News":{"MaxItems":1,"TTL":31536001}}}`,
	} {
		assertConfigStatus(t, server, http.MethodPut, "/api/config", body, http.StatusBadRequest)
	}
}

func TestCreateSourceConfigErrorStatuses(t *testing.T) {
	server := newConfigTestServer(t)
	tests := []struct {
		name   string
		body   string
		status int
	}{
		{name: "invalid delivery mode", body: `{"name":"bad-mode","type":"rss","delivery_mode":"later"}`, status: http.StatusBadRequest},
		{name: "invalid briefing target", body: `{"name":"bad-target","type":"rss","delivery_mode":"digest","briefing_target":"later"}`, status: http.StatusBadRequest},
		{name: "duplicate", body: `{"name":"existing","type":"rss"}`, status: http.StatusConflict},
		{name: "bind error", body: `{"name":`, status: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertConfigStatus(t, server, http.MethodPost, "/api/sources", tt.body, tt.status)
		})
	}
}

func TestUpdateSourceConfigErrorStatuses(t *testing.T) {
	server := newConfigTestServer(t)
	tests := []struct {
		name   string
		path   string
		body   string
		status int
	}{
		{name: "invalid delivery mode", path: "/api/sources/existing", body: `{"type":"rss","delivery_mode":"later"}`, status: http.StatusBadRequest},
		{name: "invalid briefing target", path: "/api/sources/existing", body: `{"type":"rss","delivery_mode":"digest","briefing_target":"later"}`, status: http.StatusBadRequest},
		{name: "missing", path: "/api/sources/missing", body: `{"type":"rss"}`, status: http.StatusNotFound},
		{name: "bind error", path: "/api/sources/existing", body: `{"type":`, status: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertConfigStatus(t, server, http.MethodPut, tt.path, tt.body, tt.status)
		})
	}
}

func TestSourceRoutingAPIRoundTrip(t *testing.T) {
	server := newConfigTestServer(t)
	body := `{"type":"rss","url":"https://example.com/feed","interval":60,"sinks":[],"routing":{"ai_policy":"required","on_ai_failure":"digest","direct":{"min_score":8.5,"max_per_day":4,"priority":80},"digest":{"min_score":6.5,"max_per_day":2,"priority":40,"briefing_target":"us_preview"},"default":"silent"},"options":{"nested":{"keep":true}}}`
	assertConfigStatus(t, server, http.MethodPut, "/api/sources/existing", body, http.StatusOK)

	request := httptest.NewRequest(http.MethodGet, "/api/sources/existing", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	recorder := httptest.NewRecorder()
	server.GetRouter().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Source config.SourceConfig `json:"source"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	routing := response.Source.Routing
	if routing == nil || routing.Direct == nil || routing.Digest == nil ||
		*routing.Direct.MinScore != 8.5 || routing.Direct.MaxPerDay != 4 ||
		routing.Digest.BriefingTarget != "us_preview" || routing.OnAIFailure != "digest" {
		t.Fatalf("routing did not round-trip: %+v", routing)
	}
	if nested, ok := response.Source.Options["nested"].(map[string]interface{}); !ok || nested["keep"] != true {
		t.Fatalf("options did not round-trip: %+v", response.Source.Options)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/sources", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	recorder = httptest.NewRecorder()
	server.GetRouter().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"routing"`) ||
		!strings.Contains(recorder.Body.String(), `"nested"`) {
		t.Fatalf("source list omitted editable fields: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestUpdateSourcePreservesRedactedAndOmittedSecrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "platform.yaml")
	t.Setenv("SOURCE_API_KEY", "expanded-api-key")
	t.Setenv("SOURCE_TOKEN", "expanded-token")
	raw := `sources:
  - name: existing
    type: rss
    options:
      api_key: ${SOURCE_API_KEY}
      nested:
        token: ${SOURCE_TOKEN}
        safe: original
`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewConfigManager(path)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(manager, 0)
	body := `{"type":"rss","options":{"api_key":"***","nested":{"safe":"changed"}}}`
	assertConfigStatus(t, server, http.MethodPut, "/api/sources/existing", body, http.StatusOK)

	updated, found := manager.GetSourceConfig("existing")
	if !found {
		t.Fatal("updated source missing")
	}
	if updated.Options["api_key"] != "expanded-api-key" {
		t.Fatal("redacted API key was not preserved")
	}
	nested, ok := updated.Options["nested"].(map[string]interface{})
	if !ok || nested["token"] != "expanded-token" || nested["safe"] != "changed" {
		t.Fatalf("nested options=%#v", updated.Options["nested"])
	}
	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(persisted), "***") || strings.Contains(string(persisted), "expanded-api-key") ||
		strings.Contains(string(persisted), "expanded-token") {
		t.Fatal("redaction sentinel or expanded secret was persisted")
	}
	if !strings.Contains(string(persisted), "${SOURCE_API_KEY}") || !strings.Contains(string(persisted), "${SOURCE_TOKEN}") {
		t.Fatal("environment-backed secret references were not preserved")
	}
}

func TestSourceAPIsRejectMaskedCreateAndCredentialCommands(t *testing.T) {
	server := newConfigTestServer(t)
	tests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/sources", `{"name":"masked","type":"rss","options":{"api_key":"***"}}`},
		{http.MethodPost, "/api/sources", `{"name":"credential-command","type":"script","options":{"command":"python3 report.py --api-key literal"}}`},
		{http.MethodPut, "/api/sources/existing", `{"type":"script","options":{"command":"python3 report.py --token=literal"}}`},
	}
	for _, test := range tests {
		assertConfigStatus(t, server, test.method, test.path, test.body, http.StatusBadRequest)
	}
}

// TestNewsAggregateSourcesConfigured 验证新闻聚合源配置形态:
// 2026-11-09 起不再有独立的 aggregate/pre-market/closing/us-* 源：
// 三个按市场时段的 session 源各推一条，成员在 options.members 里组装。
func TestSessionBriefingSourcesConfigured(t *testing.T) {
	cfg, err := config.LoadPlatform(filepath.Join("..", "..", "config.platform.yaml.example"))
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]*config.SourceConfig)
	for i := range cfg.Sources {
		byName[cfg.Sources[i].Name] = &cfg.Sources[i]
	}
	want := []struct {
		name    string
		sched   []string
		session string
	}{
		{"session-morning", []string{"09:20", "09:40", "11:35", "15:05"}, "morning"},
		{"session-close", []string{"16:10"}, "close"},
		{"session-us", []string{"21:00"}, "us"},
		{"session-week", []string{"20:00"}, "week"},
	}
	for _, w := range want {
		src, ok := byName[w.name]
		if !ok {
			t.Fatalf("session source %s missing", w.name)
		}
		if src.Type != "session" {
			t.Fatalf("%s type=%s", w.name, src.Type)
		}
		if len(src.Schedule) != len(w.sched) {
			t.Fatalf("%s schedule=%v want %v", w.name, src.Schedule, w.sched)
		}
		for i := range w.sched {
			if src.Schedule[i] != w.sched[i] {
				t.Fatalf("%s schedule=%v want %v", w.name, src.Schedule, w.sched)
			}
		}
		if session, _ := src.Options["session"].(string); session != w.session {
			t.Fatalf("%s session=%v", w.name, src.Options["session"])
		}
		members, _ := src.Options["members"].([]interface{})
		if len(members) < 1 {
			t.Fatalf("%s members=%d, want at least 1", w.name, len(members))
		}
		if src.Routing != nil {
			t.Fatalf("%s must not carry typed routing", w.name)
		}
	}
	for _, legacy := range []string{"pre-market-briefing", "closing-briefing", "us-preview", "us-macro-report", "news-aggregate-morning", "news-aggregate-evening", "econ-calendar"} {
		if _, ok := byName[legacy]; ok {
			t.Fatalf("legacy scheduled source %s still configured", legacy)
		}
	}
}

func newConfigTestServer(t *testing.T) *Server {
	t.Helper()
	path := filepath.Join(t.TempDir(), "platform.yaml")
	if err := os.WriteFile(path, []byte("sources:\n  - name: existing\n    type: rss\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewConfigManager(path)
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(manager, 0)
}

func assertConfigStatus(t *testing.T, server *Server, method, path, body string, want int) {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.GetRouter().ServeHTTP(recorder, request)
	if recorder.Code != want {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, want, recorder.Body.String())
	}
}

// R6：filters 等只在启动时构建的配置段，写入后必须如实返回 restart_required，
// 不能返回 200 + "updated" 让调用方以为已经生效。
func TestConfigSectionUpdatesReportRestartRequired(t *testing.T) {
	server := newConfigTestServer(t)
	for _, tc := range []struct{ path, body string }{
		{"/api/config/filters", `{"enabled":true}`},
		{"/api/config/alert", `{"enabled":true}`},
		{"/api/config/health", `{"enabled":true}`},
	} {
		request := httptest.NewRequest(http.MethodPut, tc.path, strings.NewReader(tc.body))
		request.RemoteAddr = "127.0.0.1:12345"
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		server.GetRouter().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", tc.path, recorder.Code, recorder.Body.String())
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatalf("%s body=%s", tc.path, recorder.Body.String())
		}
		if _, ok := payload["restart_required"]; !ok {
			t.Fatalf("%s must report restart_required, body=%s", tc.path, recorder.Body.String())
		}
	}
}
