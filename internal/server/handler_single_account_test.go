package server

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
	"github.com/linguo2625469/workbuddy2api-panel/internal/pool"
	"github.com/linguo2625469/workbuddy2api-panel/internal/session"
)

func singleAccountTestPool(t *testing.T) *pool.Pool {
	t.Helper()
	p := testPoolWith(
		&auth.Auth{UID: "a", AccessToken: "a", ExpiresAt: 9999999999},
		&auth.Auth{UID: "b", AccessToken: "b", ExpiresAt: 9999999999},
	)
	if err := p.SetScheduling(pool.RegionStrategies{Domestic: pool.SingleAccount}, pool.RegionAccountOrder{}); err != nil {
		t.Fatal(err)
	}
	return p
}

func singleAccountChat(h *Handler, model, conv string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	body := fmt.Sprintf(`{"model":%q,"messages":[],"metadata":{"conversation_id":%q}}`, model, conv)
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
	return rec
}

func TestSingleAccountIgnoresOldStickyAndSharesAllConversations(t *testing.T) {
	p := singleAccountTestPool(t)
	selected := ""
	up := newFakeUpstream(t, func(authz string) (int, string, bool) {
		selected = strings.TrimPrefix(authz, "Bearer ")
		return 200, sseOK, true
	})
	sess := session.New(session.Config{TTL: time.Minute, Available: p.AvailableUIDs})
	sess.Bind("old-conversation", "b")
	h := NewHandler(Config{Pool: p, Upstream: up, Session: sess})
	for _, conv := range []string{"new-one", "old-conversation", "new-two"} {
		rec := singleAccountChat(h, "cn:m", conv)
		if rec.Code != 200 || selected != "a" {
			t.Fatalf("conv=%s status=%d selected=%s body=%s", conv, rec.Code, selected, rec.Body.String())
		}
	}
	p.SetCredits("a", 0, 1000)
	for _, model := range []string{"cn:m", "cn:other"} {
		rec := singleAccountChat(h, model, "old-conversation")
		if rec.Code != 200 || selected != "b" {
			t.Fatalf("exhaustion did not switch all conversations/models: status=%d selected=%s", rec.Code, selected)
		}
	}
}

func TestSingleAccountOnlyRotatesOnCreditExhaustion(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		rotate     bool
	}{
		{"credit", `{"code":14018,"msg":"Credits exhausted"}`, 429, true},
		{"rate-limit", `{"error":{"message":"rate limit exceeded"}}`, 429, false},
		{"server-error", `{"error":{"message":"internal server error"}}`, 500, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := singleAccountTestPool(t)
			var calls []string
			up := newFakeUpstream(t, func(authz string) (int, string, bool) {
				calls = append(calls, authz)
				if authz == "Bearer a" {
					return tc.status, tc.body, false
				}
				return 200, sseOK, true
			})
			h := NewHandler(Config{Pool: p, Upstream: up})
			rec := singleAccountChat(h, "cn:m", "one")
			if tc.rotate {
				if rec.Code != 200 || len(calls) != 2 || calls[1] != "Bearer b" {
					t.Fatalf("credit rotation: status=%d calls=%v body=%s", rec.Code, calls, rec.Body.String())
				}
			} else if rec.Code == 200 || len(calls) != 1 || calls[0] != "Bearer a" {
				t.Fatalf("temporary failure borrowed another identity: status=%d calls=%v", rec.Code, calls)
			}
		})
	}
}

func TestSingleAccountCapacityDoesNotFallbackAcrossRealm(t *testing.T) {
	auth.SetGlobalEnabled(true)
	t.Cleanup(func() { auth.SetGlobalEnabled(true) })
	p := singleAccountTestPool(t)
	p.Add(&auth.Auth{UID: "global", Domain: "workbuddy.ai", AccessToken: "global", ExpiresAt: 9999999999})
	p.SetCredits("global", 1000, 1000)
	p.SetCrossDomainFallback(true)
	p.SetMaxInFlight(1)
	active := p.PickExcludingForRealm(nil, "m", "cn")
	if active == nil || !p.Acquire(active.UID) {
		t.Fatal("acquire active")
	}
	defer p.Release(active.UID)
	up := newFakeUpstream(t, func(authz string) (int, string, bool) {
		t.Errorf("full single-account pool forwarded to %s", authz)
		return 200, sseOK, true
	})
	h := NewHandler(Config{Pool: p, Upstream: up})
	if rec := singleAccountChat(h, "m", "one"); rec.Code != 503 {
		t.Fatalf("full pool status=%d body=%s", rec.Code, rec.Body.String())
	}
}
