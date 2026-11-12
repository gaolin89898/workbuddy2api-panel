package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestDesktopReusesWildcardServiceBeforeBinding(t *testing.T) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.Write([]byte(`{"ok":true,"service":"workbuddy-accounts"}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer wildcard-test" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"accounts":[]}`))
	}))
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	defer server.Close()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	cfg := Default()
	cfg.Listen = ":" + port
	cfg.APIKey = "wildcard-test"
	cfg.AuthDir = filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(cfg.AuthDir, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	origin, detach, err := startDesktopService("unused.json", cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	detach()
	if origin != "http://127.0.0.1:"+port || !desktopServiceReady(context.Background(), origin, cfg.APIKey, instanceFingerprint("unused.json", cfg)) {
		t.Fatal("did not reuse existing wildcard service")
	}
}

func TestDesktopServiceLifecycleAndReuse(t *testing.T) {
	root := t.TempDir()
	cfg := Default()
	cfg.Listen = "127.0.0.1:0"
	cfg.AuthDir = filepath.Join(root, "auths")
	cfg.StateFile = filepath.Join(root, "data", "state.json")
	cfg.APIKey = "desktop-test-secret"
	cfg.Schedule.CheckinEnabled = false
	origin, stop, err := startDesktopService(filepath.Join(root, "config.json"), cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	instance := instanceFingerprint(filepath.Join(root, "config.json"), cfg)
	if !desktopServiceReady(context.Background(), origin, cfg.APIKey, instance) {
		t.Fatal("owned server not ready")
	}
	if desktopServiceReady(context.Background(), origin, "wrong-key", instance) {
		t.Fatal("accepted service with different credentials")
	}
	u, _ := url.Parse(origin)
	reuseConfig := *cfg
	reuseConfig.Listen = u.Host
	reused, detach, err := startDesktopService(filepath.Join(root, "config.json"), &reuseConfig, false)
	if err != nil || reused != origin {
		t.Fatalf("service reuse = %q, %v", reused, err)
	}
	detach()
	if !desktopServiceReady(context.Background(), origin, cfg.APIKey, instance) {
		t.Fatal("closing desktop stopped an independently running service")
	}
	stop()
	if desktopServiceReady(context.Background(), origin, cfg.APIKey, instance) {
		t.Fatal("owned service remained running after desktop exit")
	}
}

func TestDesktopServiceSkipsOccupiedForeignPort(t *testing.T) {
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"ok":true,"service":"another-app"}`)) }))
	defer foreign.Close()
	root := t.TempDir()
	u, _ := url.Parse(foreign.URL)
	cfg := Default()
	cfg.Listen = u.Host
	cfg.AuthDir = filepath.Join(root, "auths")
	cfg.StateFile = filepath.Join(root, "data", "state.json")
	cfg.APIKey = "test-secret"
	cfg.Schedule.CheckinEnabled = false
	origin, stop, err := startDesktopService(filepath.Join(root, "config.json"), cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if origin == foreign.URL || !desktopServiceReady(context.Background(), origin, cfg.APIKey, instanceFingerprint(filepath.Join(root, "config.json"), cfg)) {
		t.Fatal("did not start a separate loopback service")
	}
}

func TestDesktopExternalURLs(t *testing.T) {
	for _, raw := range []string{"https://qoder.cn/device/authorize?a=b", "https://www.trae.cn/"} {
		if !validDesktopExternalURL(raw) {
			t.Fatalf("valid URL rejected: %s", raw)
		}
	}
	for _, raw := range []string{"file:///C:/Windows/notepad.exe", "javascript:alert(1)", "cmd://calc", "https://user:password@example.com", "https://", "https://example.com\n"} {
		if validDesktopExternalURL(raw) {
			t.Fatalf("unsafe URL accepted: %q", raw)
		}
	}
}

// 同端口、同密钥、但 instance 指纹不同（不同账号数据）的服务不得复用：
// 桌面窗口必须另起自己的服务，而不是把别的数据目录当成本配置的后台。
func TestDesktopServiceRejectsForeignInstance(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.Write([]byte(`{"ok":true,"service":"workbuddy-console","gateway":true,"instance":"different-data"}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"accounts":[]}`))
	}))
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	defer server.Close()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	root := t.TempDir()
	cfg := Default()
	cfg.Listen = ":" + port
	cfg.APIKey = "test-secret"
	cfg.AuthDir = filepath.Join(root, "auths")
	cfg.StateFile = filepath.Join(root, "data", "state.json")
	cfg.Schedule.CheckinEnabled = false
	origin, stop, err := startDesktopService(filepath.Join(root, "config.json"), cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if origin == "http://127.0.0.1:"+port {
		t.Fatal("reused a service that holds a different account dataset")
	}
}
