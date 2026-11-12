package minimax

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func overrideBases(t *testing.T, agentURL, creditURL, accountURL string) {
	t.Helper()
	oldAgent, oldCredit, oldAccount := agentBase, creditBase, accountBase
	agentBase, creditBase, accountBase = agentURL, creditURL, accountURL
	t.Cleanup(func() { agentBase, creditBase, accountBase = oldAgent, oldCredit, oldAccount })
}

// withFixedTime 固定签名时间（2026-11-12 10:30 北京时间），黄金值由此推得。
func withFixedTime(t *testing.T) {
	t.Helper()
	old := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 11, 12, 10, 30, 0, 0, china) }
	t.Cleanup(func() { nowFunc = old })
}

// fakeJWT 构造一个携带 account_id/sub 的访问令牌（签名段无意义）。
func fakeJWT(uid string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"account_id":"` + uid + `","sub":"` + uid + `"}`))
	return header + "." + payload + ".sig"
}

func reply(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }

func readAll(r *http.Request) []byte {
	raw, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	return raw
}

func TestRewardPeriodFollowsBeijingDate(t *testing.T) {
	for _, tc := range []struct{ instant, period string }{
		{"2026-11-01T15:59:59Z", "2026-11-01"},
		{"2026-11-01T16:00:00Z", "2026-11-02"},
		{"2026-11-01T23:00:00Z", "2026-11-02"},
	} {
		now, _ := time.Parse(time.RFC3339, tc.instant)
		if RewardPeriod(now) != tc.period {
			t.Fatalf("%s: %s", tc.instant, RewardPeriod(now))
		}
	}
}

func TestCredentialAuthKindDetection(t *testing.T) {
	for _, tc := range []struct{ credential, kind string }{
		{"sessionid=abc; uid=1", AuthCookie},
		{"Bearer abc.def", AuthBearer},
		{"bearer abc.def", AuthBearer},
		{"ey-job.abc.def", AuthBearer},
		{"plain-token", AuthBearer},
	} {
		if got := detectAuthKind(tc.credential); got != tc.kind {
			t.Fatalf("%s: got %s want %s", tc.credential, got, tc.kind)
		}
	}
}

func TestApplyCredentialHeaderPerKind(t *testing.T) {
	cases := []struct {
		credential, authKind, wantHeader string
	}{
		{"sessionid=abc; uid=1", AuthAuto, "Cookie"},
		{"sessionid=abc", AuthCookie, "Cookie"},
		{"tok-123", AuthAuto, "Authorization"},
		{"Bearer tok-123", AuthAuto, "Authorization"},
		{"tok-123", AuthBearer, "Authorization"},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get(tc.wantHeader) == "" {
				t.Errorf("%s: header %s missing", tc.credential, tc.wantHeader)
			}
			reply(w, map[string]any{"base_resp": map[string]any{"status_code": 0}, "data": map[string]any{"credits": 1}})
		}))
		overrideBases(t, srv.URL, srv.URL, srv.URL)
		c := NewClient()
		a := Account{Credential: tc.credential, AuthKind: tc.authKind}
		if err := c.balance(context.Background(), &a); err != nil {
			t.Fatalf("%s: %v", tc.credential, err)
		}
		srv.Close()
	}
}

// 黄金值由桌面端算法以固定时间 2026-11-12 10:30 北京时间独立推得。
func TestSignedRequestGoldenHeaders(t *testing.T) {
	withFixedTime(t)
	var gotQuery, gotYY, gotSig, gotTS string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		gotYY = r.Header.Get("yy")
		gotSig = r.Header.Get("x-signature")
		gotTS = r.Header.Get("x-timestamp")
		gotBody = readAll(r)
		reply(w, map[string]any{"base_resp": map[string]any{"status_code": 0}, "data": map[string]any{}})
	}))
	defer srv.Close()
	overrideBases(t, srv.URL, srv.URL, srv.URL)
	c := NewClient()
	a := Account{Credential: fakeJWT("504290628402294785"), AuthKind: AuthBearer}
	if _, err := c.signedRequest(context.Background(), "GET", "/minimax-cloud/api/v1/signin/status", a, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"device_platform=web", "biz_id=3", "app_id=3001", "version_code=22201",
		"unix=1794450600000", "timezone_offset=28800", "device_id=0",
		"user_id=504290628402294785", "client=desktop", "region=cn", "is_desktop=1", "desktop_version=3.1.14",
	} {
		if !strings.Contains(gotQuery, want) {
			t.Fatalf("query missing %s: %s", want, gotQuery)
		}
	}
	if gotYY != "9399fb45bb4a0b19a39f6d041b40758d" {
		t.Fatalf("yy mismatch: %s", gotYY)
	}
	if gotSig != "9f448bb6783af89cdfe7ef4e835fba14" {
		t.Fatalf("x-signature mismatch: %s", gotSig)
	}
	if gotTS != "1794450600" {
		t.Fatalf("x-timestamp mismatch: %s", gotTS)
	}
	if _, err := c.signedRequest(context.Background(), "POST", "/minimax-cloud/api/v1/signin/claim", a, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if string(gotBody) != "{}" {
		t.Fatalf("claim body mismatch: %s", gotBody)
	}
	if gotSig != "9d489d3b8903fa6a3366963b45868f56" {
		t.Fatalf("POST x-signature mismatch: %s", gotSig)
	}
	if gotYY != "eaf45542f3cdeaeb0fb66e4cb52dfb22" {
		t.Fatalf("POST yy mismatch: %s", gotYY)
	}
}

// signinPanelPayload 构造 7 天面板；todayStatus 覆盖 is_today 那天的状态。
func signinPanelPayload(todayStatus int) map[string]any {
	days := make([]any, 0, 7)
	for day := 1; day <= 7; day++ {
		status := 3
		if day == 3 {
			status = todayStatus
		}
		days = append(days, map[string]any{"day_no": day, "points": 100, "is_today": day == 3, "status": status})
	}
	return map[string]any{"base_resp": map[string]any{"status_code": 0}, "data": map[string]any{"scene": 2, "days": days}}
}

func TestSigninStatusAndClaimFlow(t *testing.T) {
	claims := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/minimax-cloud/api/v1/signin/status":
			status := 2
			if claims > 0 {
				status = 3
			}
			reply(w, signinPanelPayload(status))
		case r.Method == "POST" && r.URL.Path == "/minimax-cloud/api/v1/signin/claim":
			claims++
			reply(w, map[string]any{"base_resp": map[string]any{"status_code": 0}, "data": map[string]any{"claim_id": "c1", "claim_result": 1, "day_no": 3, "points": 100, "expire_at_ms": 1795000000000}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	overrideBases(t, srv.URL, srv.URL, srv.URL)
	c := NewClient()
	a := Account{Credential: fakeJWT("user-a"), AuthKind: AuthBearer}
	if err := c.checkin(context.Background(), &a, false); err != nil {
		t.Fatal(err)
	}
	if a.LastPeriod != "" {
		t.Fatal("probe must not mark period when today is claimable")
	}
	if err := c.checkin(context.Background(), &a, true); err != nil {
		t.Fatal(err)
	}
	if a.LastPeriod != RewardPeriod(time.Now()) {
		t.Fatalf("checkin not marked: %s", a.LastPeriod)
	}
	posts := claims
	if err := c.checkin(context.Background(), &a, true); err != nil {
		t.Fatal(err)
	}
	if claims != posts {
		t.Fatal("already-claimed day must not POST again")
	}
	if !status(a).CheckinDone {
		t.Fatal("checkin_done expected")
	}
}

func TestOAuthDeviceFlowAndRefresh(t *testing.T) {
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/oauth2/device/code":
			if r.FormValue("client_id") != oauthClientID || r.FormValue("scope") != oauthScope || r.FormValue("audience") != oauthAudience || r.FormValue("code_challenge_method") != "S256" || r.FormValue("code_challenge") == "" {
				t.Errorf("invalid device code request: %v", r.Form)
			}
			reply(w, map[string]any{"user_code": "ABCD-EFGH", "device_code": "dc-1", "verification_uri": "https://account.example/oauth-authorize", "expires_in": 300, "interval": 3})
		case "/oauth2/token":
			switch r.FormValue("grant_type") {
			case deviceGrantType:
				polls++
				if r.FormValue("device_code") != "dc-1" || r.FormValue("client_id") != oauthClientID || r.FormValue("code_verifier") == "" {
					t.Errorf("invalid poll request: %v", r.Form)
				}
				if polls == 1 {
					w.WriteHeader(400)
					reply(w, map[string]any{"error": "authorization_pending"})
					return
				}
				reply(w, map[string]any{"access_token": fakeJWT("user-a"), "refresh_token": "rt-1", "token_type": "Bearer", "expires_in": 86400})
			case "refresh_token":
				if r.FormValue("refresh_token") != "rt-old" || r.FormValue("client_id") != oauthClientID {
					t.Errorf("invalid refresh request: %v", r.Form)
				}
				reply(w, map[string]any{"access_token": fakeJWT("user-a"), "refresh_token": "rt-new", "token_type": "Bearer", "expires_in": 86400, "scope": oauthScope})
			default:
				w.WriteHeader(400)
			}
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	overrideBases(t, srv.URL, srv.URL, srv.URL)
	c := NewClient()
	auth, err := c.StartDeviceAuthorization(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if auth.UserCode != "ABCD-EFGH" || auth.DeviceCode != "dc-1" || auth.Interval != 3*time.Second || auth.ExpiresIn != 300*time.Second {
		t.Fatalf("authorization parse: %+v", auth)
	}
	sum := sha256.Sum256([]byte(auth.CodeVerifier))
	if base64.RawURLEncoding.EncodeToString(sum[:]) == "" {
		t.Fatal("missing code verifier")
	}
	if _, err := c.PollDeviceToken(context.Background(), auth); err == nil || !strings.Contains(err.Error(), "等待") {
		t.Fatalf("expected pending, got %v", err)
	}
	grant, err := c.PollDeviceToken(context.Background(), auth)
	if err != nil {
		t.Fatal(err)
	}
	if grant.UID() != "user-a" || grant.RefreshToken != "rt-1" {
		t.Fatalf("grant parse: %+v", grant)
	}
	refreshed, err := c.RefreshToken(context.Background(), "rt-old")
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.RefreshToken != "rt-new" || refreshed.ExpiresIn != 86400*time.Second {
		t.Fatalf("refresh parse: %+v", refreshed)
	}
}

// 真实抓包的响应形状（2026-11-12 实测）：积分明细在 credit/details（金额为字符串，
// type 1=购买 2=签到），token_plan_credit 字段在根级，资料在 data.userInfo。
func TestRealResponseShapes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/minimax-cloud/api/v1/credit/details":
			reply(w, map[string]any{
				"details": []any{
					map[string]any{"credit_type": 1, "granted_amount": "300.00", "remaining_amount": "220.00", "consumed_amount": "80.00", "expire_at_ms": 1799000000000},
					map[string]any{"credit_type": 2, "granted_amount": "2400.00", "remaining_amount": "2200.00", "consumed_amount": "200.00", "expire_at_ms": 1797004800000},
				},
				"total_count": 2,
				"base_resp":   map[string]any{"status_code": 0, "status_msg": "ok"},
			})
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/backend/account/token_plan_credit"):
			reply(w, map[string]any{
				"total_credits": 300, "used_credits": 80, "remaining_credits": 220,
				"api_key": "sk-cp-masked", "balance_breakdown": map[string]any{"total_balance": 220, "buckets": []any{}},
				"token_plan_credit_fallback_enabled": true, "enable_credit_first_for_high_cost": false,
				"base_resp": map[string]any{"status_code": 0, "status_msg": "success"},
			})
		case r.Method == "GET" && r.URL.Path == "/v1/api/user/info":
			reply(w, map[string]any{
				"data": map[string]any{"userInfo": map[string]any{
					"name": "MiniMax284053", "userID": "kpP3Mg67LZxa", "realUserID": "504290628402294785",
					"subUserName": "林九序", "phone": "masked",
				}},
				"statusInfo": map[string]any{"code": 0, "message": "成功"},
			})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	overrideBases(t, srv.URL, srv.URL, srv.URL)
	c := NewClient()
	a := Account{Credential: "opaque-token", AuthKind: AuthBearer}
	// 主源 credit/details：购买 + 签到积分汇总（与客户端「0 + 2,624」展示一致）。
	if err := c.balance(context.Background(), &a); err != nil || !a.BalanceKnown || a.Credits != 2420 || a.Total != 2700 {
		t.Fatalf("balance: %v %+v", err, a)
	}
	if err := c.identify(context.Background(), &a); err != nil {
		t.Fatal(err)
	}
	if a.AccountUID != "504290628402294785" || a.Nickname != "MiniMax284053" {
		t.Fatalf("identify: %+v", a)
	}
}

func TestStoreOAuthAddRefreshAndCheckin(t *testing.T) {
	claims := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/minimax-cloud/api/v1/signin/status":
			status := 2
			if claims > 0 {
				status = 3
			}
			reply(w, signinPanelPayload(status))
		case r.Method == "POST" && r.URL.Path == "/minimax-cloud/api/v1/signin/claim":
			claims++
			reply(w, map[string]any{"base_resp": map[string]any{"status_code": 0}, "data": map[string]any{"claim_id": "c1", "claim_result": 1, "day_no": 3, "points": 100, "expire_at_ms": 1795000000000}})
		case r.Method == "GET" && r.URL.Path == "/v1/api/user/info":
			reply(w, map[string]any{
				"data":       map[string]any{"userInfo": map[string]any{"name": "阿明", "realUserID": "user-a"}},
				"statusInfo": map[string]any{"code": 0},
			})
		case r.Method == "GET" && r.URL.Path == "/minimax-cloud/api/v1/credit/details":
			reply(w, map[string]any{"details": []any{map[string]any{"credit_type": 2, "granted_amount": "400.00", "remaining_amount": "400.00", "consumed_amount": "0.00", "expire_at_ms": 1797004800000}}, "total_count": 1, "base_resp": map[string]any{"status_code": 0}})
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/backend/account/token_plan_credit"):
			reply(w, map[string]any{"base_resp": map[string]any{"status_code": 0, "status_message": "success"}, "data": map[string]any{"credits": 120, "total": 500}})
		case r.Method == "POST" && r.URL.Path == "/oauth2/token":
			_ = r.ParseForm()
			reply(w, map[string]any{"access_token": fakeJWT("user-a"), "refresh_token": "rt-rotated", "token_type": "Bearer", "expires_in": 86400})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	overrideBases(t, srv.URL, srv.URL, srv.URL)
	path := filepath.Join(t.TempDir(), "accounts.json")
	s, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 令牌 1 分钟后过期：fresh 应触发续期并持久化轮换结果。
	grant := TokenGrant{AccessToken: fakeJWT("user-a"), RefreshToken: "rt-1", ExpiresIn: time.Minute}
	a, warnings, err := s.Add(context.Background(), Account{Credential: grant.AccessToken, RefreshToken: grant.RefreshToken, ExpiresAt: grant.Expiry(time.Now()), AuthKind: AuthBearer}, "")
	if err != nil {
		t.Fatal(err)
	}
	if a.AccountUID != "user-a" || !a.HasRefresh {
		t.Fatalf("add: %+v warnings=%v", a, warnings)
	}
	if !strings.HasPrefix(a.UID, "minimax-") || len(s.List()) != 1 {
		t.Fatalf("uid: %+v", a)
	}
	public, _ := json.Marshal(s.List())
	if strings.Contains(string(public), "sig") || strings.Contains(string(public), "rt-1") {
		t.Fatalf("credentials leaked into public status: %s", public)
	}
	accounts := s.snapshot()
	if err := s.fresh(context.Background(), accounts, 0); err != nil {
		t.Fatal(err)
	}
	if s.snapshot()[0].RefreshToken != "rt-rotated" {
		t.Fatal("refresh rotation not persisted")
	}
	reopened, err := Open(path, nil)
	if err != nil || len(reopened.List()) != 1 {
		t.Fatalf("reopen: %v", err)
	}
	if _, _, err := reopened.Action(context.Background(), a.UID, "disable"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reopened.Action(context.Background(), a.UID, "checkin"); err == nil || !strings.Contains(err.Error(), "停用") {
		t.Fatalf("disabled account must refuse checkin: %v", err)
	}
	if _, _, err := reopened.Action(context.Background(), a.UID, "revive"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reopened.Action(context.Background(), a.UID, "remove"); err != nil {
		t.Fatal(err)
	}
	reopened, _ = Open(path, nil)
	if len(reopened.List()) != 0 {
		t.Fatal("remove did not persist")
	}
}

func TestRunAllSkipsDisabledAndCompleted(t *testing.T) {
	claims := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/minimax-cloud/api/v1/signin/status":
			status := 2
			if claims > 0 {
				status = 3
			}
			reply(w, signinPanelPayload(status))
		case r.Method == "POST" && r.URL.Path == "/minimax-cloud/api/v1/signin/claim":
			claims++
			reply(w, map[string]any{"base_resp": map[string]any{"status_code": 0}, "data": map[string]any{"claim_id": "c1", "claim_result": 1, "day_no": 3, "points": 100, "expire_at_ms": 1795000000000}})
		case r.Method == "GET" && r.URL.Path == "/v1/api/user/info":
			reply(w, map[string]any{"base_resp": map[string]any{"status_code": 0}, "data": map[string]any{}})
		case r.Method == "GET" && r.URL.Path == "/minimax-cloud/api/v1/credit/details":
			reply(w, map[string]any{"details": []any{map[string]any{"credit_type": 2, "granted_amount": "400.00", "remaining_amount": "400.00", "consumed_amount": "0.00", "expire_at_ms": 1797004800000}}, "total_count": 1, "base_resp": map[string]any{"status_code": 0}})
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/backend/account/token_plan_credit"):
			reply(w, map[string]any{"base_resp": map[string]any{"status_code": 0}, "data": map[string]any{"credits": 10, "total": 100}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	overrideBases(t, srv.URL, srv.URL, srv.URL)
	s, _ := Open(filepath.Join(t.TempDir(), "accounts.json"), nil)
	if _, _, err := s.Add(context.Background(), Account{Credential: fakeJWT("user-a"), RefreshToken: "rt-a", ExpiresAt: time.Now().Add(time.Hour), AuthKind: AuthBearer}, ""); err != nil {
		t.Fatal(err)
	}
	second, _, err := s.Add(context.Background(), Account{Credential: fakeJWT("user-b"), RefreshToken: "rt-b", ExpiresAt: time.Now().Add(time.Hour), AuthKind: AuthBearer}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Action(context.Background(), second.UID, "disable"); err != nil {
		t.Fatal(err)
	}
	claims = 0
	if errs := s.RunAll(context.Background(), "checkin"); len(errs) != 0 || claims != 1 {
		t.Fatalf("runall: errs=%v claims=%d", errs, claims)
	}
	if got := s.List()[0]; !got.CheckinDone {
		t.Fatalf("checkin not marked: %+v", got)
	}
	claims = 0
	if errs := s.RunAll(context.Background(), "checkin"); len(errs) != 0 || claims != 0 {
		t.Fatalf("completed period must be skipped: errs=%v claims=%d", errs, claims)
	}
}

// TestStoreUpdateIdentityAndBalancePersist 锁定两个修复：更新凭证时新平台身份
// 与原账号不一致必须拒绝（防 A 账号被 B 凭证顶替）；添加与更新时查询到的余额、
// 签到状态必须写回落盘，而不是留在被丢弃的局部副本里。
func TestStoreUpdateIdentityAndBalancePersist(t *testing.T) {
	var userID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/minimax-cloud/api/v1/signin/status":
			// status=3 已签：Add 的探测回读应把今日已签状态写入账号并落盘。
			reply(w, signinPanelPayload(3))
		case r.Method == "POST" && r.URL.Path == "/minimax-cloud/api/v1/signin/claim":
			reply(w, map[string]any{"base_resp": map[string]any{"status_code": 0}, "data": map[string]any{"claim_id": "c1", "claim_result": 1, "day_no": 1, "points": 100, "expire_at_ms": 1797004800000}})
		case r.Method == "GET" && r.URL.Path == "/v1/api/user/info":
			reply(w, map[string]any{"data": map[string]any{"userInfo": map[string]any{"name": "账号-" + userID, "realUserID": userID}}, "statusInfo": map[string]any{"code": 0}})
		case r.Method == "GET" && r.URL.Path == "/minimax-cloud/api/v1/credit/details":
			reply(w, map[string]any{"details": []any{map[string]any{"credit_type": 2, "granted_amount": "400.00", "remaining_amount": "400.00", "consumed_amount": "0.00", "expire_at_ms": 1797004800000}}, "total_count": 1, "base_resp": map[string]any{"status_code": 0}})
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/backend/account/token_plan_credit"):
			reply(w, map[string]any{"base_resp": map[string]any{"status_code": 0}, "data": map[string]any{"credits": 0, "total": 0}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	overrideBases(t, srv.URL, srv.URL, srv.URL)
	path := filepath.Join(t.TempDir(), "accounts.json")
	s, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}

	userID = "user-a"
	added, _, err := s.Add(context.Background(), Account{Credential: "tok-a", AuthKind: AuthBearer}, "")
	if err != nil {
		t.Fatal(err)
	}
	// 添加时查询到的余额与签到结果必须保存（而非留在局部副本）。
	if !added.BalanceKnown || added.Credits != 400 || !added.CheckinDone {
		t.Fatalf("add must persist balance and checkin state: %+v", added)
	}

	// B 账号凭证更新 A：拒绝，原记录不被替换。
	userID = "user-b"
	if _, _, err := s.Add(context.Background(), Account{Credential: "tok-b", AuthKind: AuthBearer}, added.UID); err == nil || !strings.Contains(err.Error(), "不一致") {
		t.Fatalf("cross-account update must be rejected: %v", err)
	}
	if st := s.List()[0]; st.UID != added.UID {
		t.Fatal("original record replaced by another identity")
	}

	// A 自己的新凭证更新：放行，沿用本地 ID。
	userID = "user-a"
	updated, _, err := s.Add(context.Background(), Account{Credential: "tok-a2", AuthKind: AuthBearer}, added.UID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.UID != added.UID {
		t.Fatalf("update must keep local id: %+v", updated)
	}
}
