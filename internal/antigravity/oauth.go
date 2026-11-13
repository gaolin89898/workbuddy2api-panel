package antigravity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"
)

// Public installed-application OAuth configuration, shared by the native client.
// These identify the application; user tokens are obtained only after consent.
const defaultClientID = "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com"
const defaultClientSecret = "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"

type oauthCallback struct {
	code   string
	denied bool
}

type OAuthLogin struct {
	URL                       string
	client                    *Client
	redirect, state, verifier string
	server                    *http.Server
	callbacks                 chan oauthCallback
	received                  atomic.Bool
}

func (s *Store) BeginOAuth() (*OAuthLogin, error) {
	if s.client.OAuthClientID == "" || s.client.OAuthClientSecret == "" {
		return nil, fmt.Errorf("Antigravity 自定义 OAuth 配置不完整，请同时设置客户端 ID 和 Secret，或移除覆盖配置")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:38121")
	if err != nil {
		return nil, fmt.Errorf("Antigravity 授权回调端口 38121 被占用，请关闭其他授权窗口后重试")
	}
	login, err := newOAuthLogin(s.client, listener)
	if err != nil {
		_ = listener.Close()
	}
	return login, err
}

func newOAuthLogin(client *Client, listener net.Listener) (*OAuthLogin, error) {
	var random [64]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, fmt.Errorf("创建 Antigravity 授权会话失败")
	}
	l := &OAuthLogin{client: client, redirect: "http://" + listener.Addr().String() + "/oauth-callback", state: base64.RawURLEncoding.EncodeToString(random[:32]), verifier: base64.RawURLEncoding.EncodeToString(random[32:]), callbacks: make(chan oauthCallback, 1)}
	challenge := sha256.Sum256([]byte(l.verifier))
	query := url.Values{
		"client_id": {client.OAuthClientID}, "redirect_uri": {l.redirect}, "response_type": {"code"},
		"scope":       {"openid https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/cclog https://www.googleapis.com/auth/experimentsandconfigs"},
		"access_type": {"offline"}, "prompt": {"consent select_account"}, "state": {l.state},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"},
	}
	l.URL = "https://accounts.google.com/o/oauth2/v2/auth?" + query.Encode()
	l.server = &http.Server{Handler: http.HandlerFunc(l.callback), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, MaxHeaderBytes: 16 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	go func() { _ = l.server.Serve(listener) }()
	return l, nil
}

func (l *OAuthLogin) callback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	u, _ := url.Parse(l.redirect)
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || r.Method != http.MethodGet || r.Host != u.Host || r.URL.Path != u.Path || len(r.URL.RawQuery) > 16<<10 || len(query["state"]) != 1 || subtle.ConstantTimeCompare([]byte(query.Get("state")), []byte(l.state)) != 1 {
		http.Error(w, "授权回调无效，请返回面板重新授权", http.StatusBadRequest)
		return
	}
	cb := oauthCallback{code: query.Get("code"), denied: query.Get("error") != ""}
	if !cb.denied && (len(query["code"]) != 1 || !validToken(cb.code)) {
		http.Error(w, "未收到有效授权码", http.StatusBadRequest)
		return
	}
	if !l.received.CompareAndSwap(false, true) {
		http.Error(w, "本次授权已接收", http.StatusConflict)
		return
	}
	l.callbacks <- cb
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, "<!doctype html><html lang=zh-CN><meta charset=utf-8><title>Antigravity 授权</title><p>授权结果已接收，请返回账号面板查看保存结果。</p></html>")
}

func (l *OAuthLogin) Close() { _ = l.server.Close() }

func (l *OAuthLogin) Wait(ctx context.Context) (Account, error) {
	var cb oauthCallback
	select {
	case <-ctx.Done():
		return Account{}, ctx.Err()
	case cb = <-l.callbacks:
	}
	if cb.denied {
		return Account{}, fmt.Errorf("Google 授权未完成，请重新登录并同意授权")
	}
	form := url.Values{"client_id": {l.client.OAuthClientID}, "client_secret": {l.client.OAuthClientSecret}, "grant_type": {"authorization_code"}, "code": {cb.code}, "code_verifier": {l.verifier}, "redirect_uri": {l.redirect}}
	var reply struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := l.client.request(ctx, "POST", l.client.TokenURL, "", []byte(form.Encode()), "application/x-www-form-urlencoded", &reply); err != nil {
		return Account{}, err
	}
	if !validToken(reply.AccessToken) || !validToken(reply.RefreshToken) || reply.ExpiresIn <= 0 || reply.ExpiresIn > 31536000 {
		return Account{}, fmt.Errorf("Antigravity 授权未返回有效令牌，请重新授权")
	}
	return Account{AccessToken: reply.AccessToken, RefreshToken: reply.RefreshToken, ExpiresAt: time.Now().Add(time.Duration(reply.ExpiresIn) * time.Second)}, nil
}
