package doubao

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestAccountLifecycleIdentityAndCredentialIsolation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/passport/account/info/v2/" || r.Method != "GET" {
			t.Errorf("unexpected account endpoint: %s %s", r.Method, r.URL.Path)
		}
		cookie, _ := r.Cookie("sessionid")
		uid := "1234567890123456789"
		if cookie != nil && cookie.Value == "other-private-session" {
			uid = "9876543210987654321"
		}
		if cookie != nil && cookie.Value == "expired-private-session" {
			w.Write([]byte(`{"message":"error","data":{"error_code":13,"user_id":0,"description":"expired-private-session"}}`))
			return
		}
		w.Write([]byte(`{"message":"success","data":{"user_id":` + uid + `,"screen_name":"测试豆包"}}`))
	}))
	defer upstream.Close()
	path := filepath.Join(t.TempDir(), "doubao.json")
	s, err := Open(path, &Client{HTTP: upstream.Client(), Base: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	a, err := s.Add(ctx, Account{Cookie: "first-private-session"}, "")
	if err != nil || a.AccountUID != "1234567890123456789" || a.CheckinSupported || a.BalanceKnown {
		t.Fatalf("bad identity or capabilities: %+v %v", a, err)
	}
	if _, err = s.Action(ctx, a.UID, "disable"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GatewayCredential(ctx, a.UID); err == nil {
		t.Fatal("disabled credential was selectable")
	}
	if _, err = s.Add(ctx, Account{Cookie: "second-private-session"}, ""); err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 1 || !s.List()[0].Disabled {
		t.Fatal("duplicate account or disabled flag lost on credential update")
	}
	if _, err = s.Add(ctx, Account{Cookie: "other-private-session"}, a.UID); err == nil {
		t.Fatal("allowed replacing account identity")
	}
	if _, err = s.Action(ctx, a.UID, "revive"); err != nil {
		t.Fatal(err)
	}
	credential, err := s.GatewayCredential(ctx, a.UID)
	if err != nil || !strings.Contains(credential.Token, "second-private-session") {
		t.Fatalf("credential not updated: %v", err)
	}
	if _, err = s.Add(ctx, Account{Cookie: "expired-private-session"}, a.UID); err == nil || strings.Contains(err.Error(), "expired-private-session") {
		t.Fatal("expired credential accepted or leaked")
	}
	credential, _ = s.GatewayCredential(ctx, a.UID)
	if !strings.Contains(credential.Token, "second-private-session") {
		t.Fatal("failed validation overwrote valid credential")
	}
	raw, _ := json.Marshal(s.List())
	if strings.Contains(string(raw), "private-session") || strings.Contains(string(raw), "sessionid") {
		t.Fatal("status leaks credentials")
	}
	reopened, err := Open(path, &Client{HTTP: upstream.Client(), Base: upstream.URL})
	if err != nil || len(reopened.List()) != 1 {
		t.Fatalf("persistence failed: %v", err)
	}
	if _, err = s.Action(ctx, a.UID, "remove"); err != nil || len(s.List()) != 0 {
		t.Fatal("remove failed", err)
	}
}

func TestNormalizeCookie(t *testing.T) {
	for _, input := range []string{"private-session", "sessionid=private-session; ttwid=web-id", "Cookie: sessionid=private-session; sessionid_ss=private-session"} {
		cookie, err := NormalizeCookie(input)
		if err != nil || !strings.Contains(cookie, "sessionid=private-session") || !strings.Contains(cookie, "sessionid_ss=private-session") {
			t.Errorf("normalize failed: %v", err)
		}
	}
	for _, input := range []string{"", "ttwid=only", "sessionid=", "sessionid=x\r\nInjected: y", "sessionid=x; sessionid=y", "sessionid=x; sessionid_ss=y", strings.Repeat("x", 33<<10)} {
		if _, err := NormalizeCookie(input); err == nil {
			t.Error("invalid cookie accepted")
		}
	}
}

func TestRefreshFailureIsVisibleWithoutLeakingCookie(t *testing.T) {
	valid := true
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if valid {
			w.Write([]byte(`{"data":{"user_id":"42"}}`))
		} else {
			w.Write([]byte(`{"message":"error","data":{"user_id":0,"description":"private-session"}}`))
		}
	}))
	defer upstream.Close()
	s, _ := Open(filepath.Join(t.TempDir(), "accounts.json"), &Client{HTTP: upstream.Client(), Base: upstream.URL})
	a, err := s.Add(context.Background(), Account{Cookie: "private-session"}, "")
	if err != nil {
		t.Fatal(err)
	}
	valid = false
	if _, err := s.Action(context.Background(), a.UID, "balance"); err == nil {
		t.Fatal("refresh should fail")
	}
	status := s.List()[0]
	if status.LastError == "" || strings.Contains(status.LastError, "private-session") {
		t.Fatal("refresh error missing or exposes secret")
	}
	valid = true
	if _, err := s.Action(context.Background(), a.UID, "balance"); err != nil || s.List()[0].LastError != "" {
		t.Fatal("refresh should recover", err)
	}
}
