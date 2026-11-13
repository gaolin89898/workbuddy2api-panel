package antigravity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOAuthRefreshPersistenceIdentityAndRedaction(t *testing.T) {
	t.Setenv("WB2A_ANTIGRAVITY_CLIENT_ID", "test-client-id")
	t.Setenv("WB2A_ANTIGRAVITY_CLIENT_SECRET", "test-client-secret")
	var refreshes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			refreshes.Add(1)
			r.ParseForm()
			if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("client_id") != "test-client-id" || r.Form.Get("client_secret") != "test-client-secret" || r.Form.Get("refresh_token") == "" {
				t.Error("invalid OAuth refresh")
			}
			fmt.Fprint(w, `{"access_token":"private-access","refresh_token":"private-rotated","expires_in":3600}`)
		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer private-access" {
				t.Error("userinfo lacks bearer token")
			}
			fmt.Fprint(w, `{"email":"owner@example.com"}`)
		case "/v1internal:loadCodeAssist":
			fmt.Fprint(w, `{"cloudaicompanionProject":{"id":"project-owned"},"paidTier":{"name":"Pro"}}`)
		default:
			t.Error("unexpected endpoint")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c := NewClient()
	c.HTTP = server.Client()
	c.TokenURL = server.URL + "/token"
	c.UserInfoURL = server.URL + "/userinfo"
	c.Base = server.URL
	path := filepath.Join(t.TempDir(), "accounts.json")
	s, err := Open(path, c)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Add(context.Background(), Account{RefreshToken: "private-refresh"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if a.AccountUID != "owner@example.com" || a.ProjectID != "project-owned" || !a.HasRefresh || a.CheckinSupported || a.BalanceKnown {
		t.Fatalf("bad public status: %+v", a)
	}
	raw, _ := json.Marshal(s.List())
	if strings.Contains(string(raw), "private-") || strings.Contains(string(raw), "refresh_token") {
		t.Fatal("status exposes OAuth tokens")
	}
	if _, err = s.Add(context.Background(), Account{AccessToken: "private-access", Email: "different@example.com"}, a.UID); err == nil {
		t.Fatal("account identity replacement accepted")
	}
	accounts := s.snapshot()
	accounts[0].ExpiresAt = time.Now().Add(-time.Minute)
	if err = s.commit(accounts); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			credential, err := s.GatewayCredential(context.Background(), a.UID)
			if err != nil || credential.Token != "private-access" || credential.ProjectID != "project-owned" {
				t.Error("gateway credential not refreshed")
			}
		}()
	}
	wg.Wait()
	if refreshes.Load() != 2 {
		t.Fatalf("concurrent refreshes were not merged: %d", refreshes.Load())
	}
	reloaded, err := Open(path, c)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(reloaded.snapshot()[0].ExpiresAt) < 50*time.Minute || reloaded.snapshot()[0].RefreshToken != "private-rotated" {
		t.Fatal("rotated credentials were not persisted")
	}
	if _, err = s.Action(context.Background(), a.UID, "disable"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GatewayCredential(context.Background(), a.UID); err == nil {
		t.Fatal("disabled account forwarded")
	}
	if !s.GatewayAccounts()[0].Disabled {
		t.Fatal("disabled flag missing from scheduler")
	}
	if _, err = s.Action(context.Background(), a.UID, "remove"); err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 0 {
		t.Fatal("remove did not persist")
	}
}

func TestRefreshFailureDoesNotLeakOrOverwrite(t *testing.T) {
	t.Setenv("WB2A_ANTIGRAVITY_CLIENT_ID", "test-client-id")
	t.Setenv("WB2A_ANTIGRAVITY_CLIENT_SECRET", "test-client-secret")
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		fmt.Fprint(w, `{"error":"invalid_grant","error_description":"private-user-token"}`)
	}))
	defer up.Close()
	c := NewClient()
	c.HTTP = up.Client()
	c.TokenURL = up.URL
	s, err := Open(filepath.Join(t.TempDir(), "accounts.json"), c)
	if err != nil {
		t.Fatal(err)
	}
	original := Account{ID: "a", AccessToken: "old-access", RefreshToken: "old-refresh", ExpiresAt: time.Now().Add(-time.Hour), ProjectID: "p"}
	if err = s.commit([]Account{original}); err != nil {
		t.Fatal(err)
	}
	_, err = s.GatewayCredential(context.Background(), "a")
	if err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal("refresh failure leaked upstream body")
	}
	if s.snapshot()[0] != original {
		t.Fatal("failed refresh changed account")
	}
	raw, err := os.ReadFile(s.path)
	if err != nil || !strings.Contains(string(raw), "old-refresh") {
		t.Fatal("existing credentials lost")
	}
}

func TestImportOAuthFormats(t *testing.T) {
	for _, raw := range []string{
		`{"type":"antigravity","email":"a@example.com","access_token":"a","refresh_token":"r","project_id":"p","expired":"2027-01-02T03:04:05Z"}`,
		`{"email":"a@example.com","token":{"access_token":"a","refresh_token":"r","project_id":"p","expiry_timestamp":1798859045}}`,
		`{"accounts":[{"provider":"workbuddy","access_token":"ignore"},{"provider":"antigravity","access_token":"a","refresh_token":"r","projectId":"p","expires_at":1798859045000}]}`,
	} {
		accounts, err := ParseImport([]byte(raw))
		if err != nil || len(accounts) != 1 {
			t.Fatalf("format not accepted: %v", err)
		}
		a := accounts[0]
		if a.AccessToken != "a" || a.RefreshToken != "r" || a.ProjectID != "p" || a.ExpiresAt.IsZero() {
			t.Fatal("incomplete imported authorization")
		}
	}
	for _, raw := range []string{`null`, `[]`, `{"type":"other","refresh_token":"x"}`, `{"accounts":[{"email":"a"}]}`, `{"access_token":"x"} {}`} {
		if _, err := ParseImport([]byte(raw)); err == nil {
			t.Fatal("invalid import accepted")
		}
	}
}
