package antigravity

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testOAuthLogin(t *testing.T, c *Client) *OAuthLogin {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l, err := newOAuthLogin(c, listener)
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	t.Cleanup(l.Close)
	return l
}

func TestOAuthDefaultsAndOverrides(t *testing.T) {
	t.Setenv("WB2A_ANTIGRAVITY_CLIENT_ID", "")
	t.Setenv("WB2A_ANTIGRAVITY_CLIENT_SECRET", "")
	c := NewClient()
	if c.OAuthClientID != defaultClientID || c.OAuthClientSecret != defaultClientSecret {
		t.Fatal("web login requires user configuration")
	}
	t.Setenv("WB2A_ANTIGRAVITY_CLIENT_ID", "custom-client")
	if NewClient().OAuthClientID != "custom-client" || NewClient().OAuthClientSecret != "" {
		t.Fatal("custom and default OAuth clients mixed")
	}
}

func TestOAuthCallbackValidationPKCEAndExchange(t *testing.T) {
	c := NewClient()
	l := testOAuthLogin(t, c)
	auth, _ := url.Parse(l.URL)
	q := auth.Query()
	if q.Get("state") == "" || q.Get("access_type") != "offline" || q.Get("redirect_uri") != l.redirect || q.Get("code_challenge_method") != "S256" {
		t.Fatal("invalid authorization URL")
	}
	challenge := sha256.Sum256([]byte(l.verifier))
	if q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(challenge[:]) || strings.Contains(l.URL, l.verifier) {
		t.Fatal("PKCE verifier exposed or incorrect")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("code") != "private-code" || r.Form.Get("code_verifier") != l.verifier || r.Form.Get("redirect_uri") != l.redirect || r.Form.Get("client_id") != c.OAuthClientID || r.Form.Get("grant_type") != "authorization_code" {
			t.Error("invalid token exchange")
		}
		fmt.Fprint(w, `{"access_token":"private-access","refresh_token":"private-refresh","expires_in":3600}`)
	}))
	defer upstream.Close()
	c.HTTP, c.TokenURL = upstream.Client(), upstream.URL
	for _, suffix := range []string{"?state=wrong&code=private-code", "?state=" + l.state + "&code=", "?state=" + l.state + "&state=other&code=private-code"} {
		resp, err := http.Get(l.redirect + suffix)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Fatal("invalid callback consumed session")
		}
	}
	callback := l.redirect + "?" + url.Values{"state": {l.state}, "code": {"private-code"}}.Encode()
	resp, err := http.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal("callback rejected")
	}
	resp, err = http.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 409 {
		t.Fatal("callback replay accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	a, err := l.Wait(ctx)
	if err != nil || a.AccessToken != "private-access" || a.RefreshToken != "private-refresh" || time.Until(a.ExpiresAt) < 59*time.Minute {
		t.Fatalf("exchange failed: %v", err)
	}
}

func TestOAuthDenialCancellationAndErrorRedaction(t *testing.T) {
	l := testOAuthLogin(t, NewClient())
	resp, err := http.Get(l.redirect + "?state=" + l.state + "&error=private-denial-details")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if _, err := l.Wait(context.Background()); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal("OAuth denial leaked details")
	}
	l = testOAuthLogin(t, NewClient())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.Wait(ctx); err != context.Canceled {
		t.Fatal("cancelled flow continued")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		fmt.Fprint(w, `{"error_description":"private-code-and-token"}`)
	}))
	defer upstream.Close()
	c := NewClient()
	c.HTTP, c.TokenURL = upstream.Client(), upstream.URL
	l = testOAuthLogin(t, c)
	l.callbacks <- oauthCallback{code: "private-code"}
	if _, err := l.Wait(context.Background()); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal("upstream OAuth error leaked details")
	}
}
