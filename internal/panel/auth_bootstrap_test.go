package panel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthBootstrapLocalAccess(t *testing.T) {
	for _, tc := range []struct {
		name, host, remote, header, value string
		allowed                           bool
	}{
		{name: "IPv4", host: "127.0.0.1:7863", remote: "127.0.0.1:50000", allowed: true},
		{name: "localhost", host: "localhost:7863", remote: "127.0.0.1:50000", allowed: true},
		{name: "IPv6", host: "[::1]:7863", remote: "[::1]:50000", allowed: true},
		{name: "LAN", host: "192.168.1.10:7863", remote: "192.168.1.20:50000"},
		{name: "rebound host", host: "evil.example", remote: "127.0.0.1:50000"},
		{name: "forwarded", host: "127.0.0.1:7863", remote: "127.0.0.1:50000", header: "X-Forwarded-For", value: "192.168.1.20"},
		{name: "cross-site", host: "127.0.0.1:7863", remote: "127.0.0.1:50000", header: "Sec-Fetch-Site", value: "cross-site"},
		{name: "foreign origin", host: "127.0.0.1:7863", remote: "127.0.0.1:50000", header: "Origin", value: "https://evil.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 旧面板密码配置不再阻止本机自动连接。
			p := New(Config{Version: "test", APIKey: "actual-key", PanelUser: "admin", PanelPass: "secret"})
			req := httptest.NewRequest("GET", "http://"+tc.host+"/panel/api/auth/bootstrap", nil)
			req.RemoteAddr = tc.remote
			if tc.header != "" {
				req.Header.Set(tc.header, tc.value)
			}
			rec := httptest.NewRecorder()
			p.ServeHTTP(rec, req)
			var out struct {
				APIKey       string `json:"api_key"`
				KeyAvailable bool   `json:"key_available"`
				Token        string `json:"token"`
			}
			if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
				t.Fatalf("bootstrap: %d %s", rec.Code, rec.Body.String())
			}
			wantKey := ""
			if tc.allowed {
				wantKey = "actual-key"
			}
			if out.KeyAvailable != tc.allowed || out.APIKey != wantKey || out.Token != "" {
				t.Fatalf("unexpected bootstrap: %+v", out)
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("key response must not be cached")
			}
		})
	}
}

func TestAuthBootstrapWithoutAPIKey(t *testing.T) {
	for _, password := range []string{"", "old-password"} {
		p := New(Config{Version: "test", PanelUser: "admin", PanelPass: password})
		req := httptest.NewRequest("GET", "http://localhost:7863/panel/api/auth/bootstrap", nil)
		req.RemoteAddr = "127.0.0.1:50000"
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)
		var out struct {
			APIKey       string `json:"api_key"`
			KeyAvailable bool   `json:"key_available"`
			Token        string `json:"token"`
		}
		if json.Unmarshal(rec.Body.Bytes(), &out) != nil || !out.KeyAvailable || out.APIKey != "" || out.Token == "" {
			t.Fatalf("empty key bootstrap: %s", rec.Body.String())
		}
		follow := httptest.NewRequest("GET", "/panel/api/config", nil)
		follow.Header.Set("Authorization", "Bearer "+out.Token)
		rec = httptest.NewRecorder()
		p.ServeHTTP(rec, follow)
		if rec.Code == http.StatusUnauthorized {
			t.Fatal("automatic session rejected")
		}
	}
}

func TestAuthBootstrapSessionReturnsActualKey(t *testing.T) {
	p := New(Config{Version: "test", APIKey: "actual-key"})
	token, _, err := p.newSession()
	if err != nil {
		t.Fatal(err)
	}
	for _, credential := range []string{token, "actual-key"} {
		req := httptest.NewRequest("GET", "/panel/api/auth/bootstrap", nil)
		req.Header.Set("Authorization", "Bearer "+credential)
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)
		var out struct {
			APIKey string `json:"api_key"`
		}
		if json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.APIKey != "actual-key" {
			t.Fatalf("displayed key must be API key, not session: %s", rec.Body.String())
		}
	}
}
