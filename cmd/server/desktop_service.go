package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func desktopAddress(listen string) (string, error) {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("无法读取本地服务端口：%w", err)
	}
	return net.JoinHostPort("127.0.0.1", port), nil
}

// instanceFingerprint 同一份账号数据的标识：config / state / auths 三个路径的
// 摘要。桌面窗口复用已有服务时以它为准——不仅凭端口或 API Key 判断：同机两个
// 不同数据目录的服务（端口不同）不会互相误认，密钥相同但数据不同的服务也不会。
func instanceFingerprint(configPath string, cfg *Config) string {
	resolve := func(p string) string {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return p
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		resolve(configPath), resolve(cfg.StateFile), resolve(cfg.AuthDir),
	}, "\x00")))
	return hex.EncodeToString(sum[:8])
}

// desktopServiceReady 判断 origin 是否为可复用的本机后台：
//   - healthz 必须声明本服务身份（新统一身份 workbuddy-console；兼容旧
//     workbuddy-accounts / workbuddy2api）；
//   - healthz 带 instance 指纹时必须与期望一致（同一份账号数据）；
//   - 面板 overview 必须能用当前配置的管理密钥通过鉴权。
func desktopServiceReady(ctx context.Context, origin, key, instance string) bool {
	client := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, "GET", origin+"/healthz", nil)
	if err != nil {
		return false
	}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	var health struct {
		OK       bool   `json:"ok"`
		Service  string `json:"service"`
		Instance string `json:"instance"`
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&health)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || !health.OK {
		return false
	}
	switch health.Service {
	case "workbuddy-console", "workbuddy-accounts", "workbuddy2api":
	default:
		return false
	}
	// 新服务始终透出 instance：指纹不一致 = 不同账号数据，不复用。
	// 旧版本服务没有该字段（空串）时退回身份 + 密钥判断（既有语义）。
	if health.Instance != "" && instance != "" && health.Instance != instance {
		return false
	}
	request, _ = http.NewRequestWithContext(ctx, "GET", origin+"/panel/api/overview", nil)
	request.Header.Set("Authorization", "Bearer "+key)
	response, err = client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusOK
}

// startDesktopService 桌面窗口获取后台服务：
//   - 同配置的统一服务已在监听（实例指纹一致 + 密钥可用）→ 直接复用，窗口关闭
//     不影响它（detach 为空操作）；
//   - 端口被其他服务占用 → 在回环临时端口启动自己的服务（保留端口占用处理）。
func startDesktopService(configPath string, cfg *Config, gateway bool) (string, func(), error) {
	address, err := desktopAddress(cfg.Listen)
	if err != nil {
		return "", nil, err
	}
	origin := "http://" + address
	instance := instanceFingerprint(configPath, cfg)
	// Windows can allow a specific IPv4 bind beside an existing wildcard listener.
	if desktopServiceReady(context.Background(), origin, cfg.APIKey, instance) {
		return origin, func() {}, nil
	}
	listenAddress := address
	if connection, dialErr := net.DialTimeout("tcp", address, 200*time.Millisecond); dialErr == nil {
		connection.Close()
		listenAddress = "127.0.0.1:0"
	}
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return "", nil, fmt.Errorf("启动本地服务失败：%w", err)
		}
	}
	localConfig := *cfg
	localConfig.Listen = listener.Addr().String()
	origin = "http://" + localConfig.Listen
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- startUnifiedWithListener(ctx, configPath, &localConfig, listener, gateway) }()
	stop := sync.OnceFunc(func() { cancel(); <-done })
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	for {
		if desktopServiceReady(ctx, origin, cfg.APIKey, instance) {
			return origin, stop, nil
		}
		select {
		case err := <-done:
			cancel()
			return "", nil, fmt.Errorf("本地服务启动失败：%v", err)
		case <-deadline.C:
			stop()
			return "", nil, fmt.Errorf("等待本地服务启动超时")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func validDesktopExternalURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && !strings.ContainsAny(raw, "\x00\r\n")
}
