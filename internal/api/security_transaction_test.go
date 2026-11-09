package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Ricaardo/nimbus-os/news/internal/config"
	"github.com/Ricaardo/nimbus-os/news/internal/core"
)

func TestManagementEndpointsRejectUntrustedBrowserOrigins(t *testing.T) {
	server := newConfigTestServer(t)
	for _, path := range []string{
		"/api/config",
		"/api/llm",
		"/api/channels",
		"/api/sources",
		"/api/keys",
		"/api/digest/stats",
		"/api/status",
		"/api/schedule",
	} {
		for _, method := range []string{http.MethodGet, http.MethodOptions} {
			req := httptest.NewRequest(method, path, nil)
			req.RemoteAddr = "127.0.0.1:12345"
			req.Header.Set("Origin", "https://evil.example")
			rec := httptest.NewRecorder()
			server.GetRouter().ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s status=%d want=%d", method, path, rec.Code, http.StatusForbidden)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
				t.Fatalf("%s %s exposed CORS origin %q", method, path, got)
			}
		}
	}
}

func TestManagementEndpointsAllowTrustedLocalOriginAndOriginlessCLI(t *testing.T) {
	server := newConfigTestServer(t)
	for _, origin := range []string{"", "http://localhost:5173", "https://127.0.0.1:8080", "http://[::1]:3000"} {
		req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		server.GetRouter().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("origin=%q status=%d body=%s", origin, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Fatalf("origin=%q allow-origin=%q", origin, got)
		}
	}
}

func TestManagementResponsesRecursivelyRedactSecrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "platform.yaml")
	t.Setenv("API_SECURITY_AUTH", "Bearer expanded-secret")
	t.Setenv("API_SECURITY_SOURCE_KEY", "expanded-api-key")
	t.Setenv("API_SECURITY_LLM_KEY", "expanded-llm-key")
	raw := `
channels:
  - name: secure-channel
    type: discord
    webhook: https://secret.example/main
    options:
      webhook_plain: https://secret.example/plain
      webhooks:
        - https://secret.example/one
      nested:
        Authorization: ${API_SECURITY_AUTH}
        safe: visible
sources:
  - name: secure-source
    type: rss
    options:
      api_key: ${API_SECURITY_SOURCE_KEY}
      nested:
        password: expanded-password
        safe: visible
llm:
  api_key: ${API_SECURITY_LLM_KEY}
`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewConfigManager(path)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(manager, 0)

	for _, endpoint := range []string{
		"/api/config",
		"/api/channels/secure-channel",
		"/api/sources/secure-source",
	} {
		req := httptest.NewRequest(http.MethodGet, endpoint, nil)
		req.RemoteAddr = "127.0.0.1:12345"
		rec := httptest.NewRecorder()
		server.GetRouter().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", endpoint, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		for _, secret := range []string{
			"secret.example", "expanded-secret", "expanded-api-key",
			"expanded-password", "expanded-llm-key",
		} {
			if strings.Contains(body, secret) {
				t.Fatalf("%s leaked %q in %s", endpoint, secret, body)
			}
		}
		if !strings.Contains(body, "visible") && endpoint != "/api/config" {
			t.Fatalf("%s removed safe nested data: %s", endpoint, body)
		}
	}
}

func TestManagementResponseRedactionPreservesDynamicSinkNames(t *testing.T) {
	value := map[string]interface{}{
		"sinks": map[string]interface{}{
			"discord-webhook": map[string]interface{}{"pending": float64(0)},
		},
		"webhook": "https://secret.example/hook",
	}

	redactResponseValue(value)
	if value["webhook"] != "***" {
		t.Fatalf("webhook secret was not redacted: %#v", value["webhook"])
	}
	sinks, ok := value["sinks"].(map[string]interface{})
	if !ok {
		t.Fatalf("sinks were unexpectedly redacted: %#v", value["sinks"])
	}
	if _, ok := sinks["discord-webhook"].(map[string]interface{}); !ok {
		t.Fatalf("dynamic sink name was unexpectedly redacted: %#v", sinks)
	}
}

func TestManagementResponseFlushesRecoveredPanicStatus(t *testing.T) {
	server := newConfigTestServer(t)
	server.router.GET("/api/config/panic", func(*gin.Context) {
		panic("management handler failed")
	})

	req := httptest.NewRequest(http.MethodGet, "/api/config/panic", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	server.GetRouter().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want=%d body=%q", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Length"); got != "0" {
		t.Fatalf("content-length=%q want=0 body=%q", got, rec.Body.String())
	}
}

func TestDeleteSourceRollsBackConfigWhenRuntimeRemovalFails(t *testing.T) {
	server := newConfigTestServer(t)
	server.removeSource = func(string) error {
		return core.ErrSourceConflict
	}

	assertConfigStatus(t, server, http.MethodDelete, "/api/sources/existing", "", http.StatusConflict)
	if _, ok := server.configManager.GetSourceConfig("existing"); !ok {
		t.Fatal("runtime failure left source deleted from config")
	}
}

func TestDeleteSourceReportsRuntimeAndRollbackFailures(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "platform.yaml")
	if err := os.WriteFile(path, []byte("sources:\n  - name: existing\n    type: rss\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewConfigManager(path)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(manager, 0)
	server.removeSource = func(string) error {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		return errors.New("generation changed")
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/sources/existing", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	server.GetRouter().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "restart_required") ||
		!strings.Contains(body, "generation changed") ||
		!strings.Contains(body, "config rollback failed") {
		t.Fatalf("compound failure was not reported clearly: %s", body)
	}
}

// R8：配置了 NIMBUS_ADMIN_TOKEN 后，管理路径必须携带令牌；
// loopback 不再单独放行（隧道/双实例下 RemoteAddr 恒为 127.0.0.1）。
func TestManagementEndpointsRequireAdminTokenWhenConfigured(t *testing.T) {
	t.Setenv("NIMBUS_ADMIN_TOKEN", "s3cret-admin-token")
	server := newConfigTestServer(t)

	unauthorized := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	unauthorized.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	server.GetRouter().ServeHTTP(rec, unauthorized)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want=401 body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
		t.Fatalf("WWW-Authenticate=%q", got)
	}

	for _, header := range []struct{ name, value string }{
		{"Authorization", "Bearer s3cret-admin-token"},
		{"X-Admin-Token", "s3cret-admin-token"},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set(header.name, header.value)
		rec := httptest.NewRecorder()
		server.GetRouter().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", header.name, rec.Code, rec.Body.String())
		}
	}

	// 错误令牌同样拒绝。
	bad := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	bad.RemoteAddr = "127.0.0.1:12345"
	bad.Header.Set("Authorization", "Bearer wrong")
	rec = httptest.NewRecorder()
	server.GetRouter().ServeHTTP(rec, bad)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token status=%d want=401", rec.Code)
	}

	// 本机浏览器 UI 仍可用：可信 Origin 不走令牌。
	ui := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	ui.RemoteAddr = "127.0.0.1:12345"
	ui.Header.Set("Origin", "http://localhost:5173")
	rec = httptest.NewRecorder()
	server.GetRouter().ServeHTTP(rec, ui)
	if rec.Code != http.StatusOK {
		t.Fatalf("trusted local origin status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// 双实例角色：standby 只读，promote 仅 standby 可用；admin 允许 FetchOnly 但禁止 trigger。
func TestRuntimeRoleGuardsAndPromotion(t *testing.T) {
	server := newConfigTestServer(t)
	server.SetRuntimeRole("standby")
	server.SetNewsEngine(nil)

	call := func(method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		server.GetRouter().ServeHTTP(rec, req)
		return rec
	}

	if rec := call(http.MethodPost, "/api/runtime/promote"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("promote without hook status=%d body=%s", rec.Code, rec.Body.String())
	}
	server.SetPromoteFunc(func() bool { return true })
	if rec := call(http.MethodPost, "/api/runtime/promote"); rec.Code != http.StatusOK {
		t.Fatalf("promote status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := call(http.MethodGet, "/api/runtime"); !strings.Contains(rec.Body.String(), "active") {
		t.Fatalf("role after promote=%s", rec.Body.String())
	}
	if rec := call(http.MethodPost, "/api/runtime/promote"); rec.Code != http.StatusConflict {
		t.Fatalf("second promote status=%d", rec.Code)
	}

	server.SetRuntimeRole("standby")
	if rec := call(http.MethodPost, "/api/sources/main/fetch"); rec.Code != http.StatusForbidden {
		t.Fatalf("standby fetch status=%d", rec.Code)
	}
	if rec := call(http.MethodPost, "/api/sources/main/trigger"); rec.Code != http.StatusForbidden {
		t.Fatalf("standby trigger status=%d", rec.Code)
	}
}
