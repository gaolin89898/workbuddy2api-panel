package loomy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func overrideBases(t *testing.T, pointsURL string) {
	t.Helper()
	old := pointsBase
	pointsBase = pointsURL
	t.Cleanup(func() { pointsBase = old })
}

// overrideLocal 让 findLocalSession / localPetWork 指向临时目录。
func overrideLocal(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	oldGlob, oldRoaming := sessionGlob, roamingDir
	sessionGlob = filepath.Join(tmp, "public", "*", "userData", "auth-session.json")
	roamingDir = func() string { return tmp }
	t.Cleanup(func() { sessionGlob, roamingDir = oldGlob, oldRoaming })
	return tmp
}

func reply(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }

func readAll(r *http.Request) []byte {
	raw, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	return raw
}

const testUserID = "user-a"

// tierPoints 档位阈值到发奖积分的对照（与桌面端 observed 配置一致）。
var tierPoints = map[string]float64{"tier_1": 60, "tier_2": 100, "tier_3": 150}

func petWorkPayload(t *testing.T, businessDate string, rewarded []string, earned float64) map[string]any {
	t.Helper()
	// 服务端 lastGrant 指向当日最近一个发奖批次（非累计）。
	status := map[string]any{"earnedPointsToday": earned, "dailyRewardCapped": false, "rewardedTierIds": rewarded}
	if len(rewarded) > 0 {
		last := rewarded[len(rewarded)-1]
		status["lastGrant"] = map[string]any{"grantBatchId": "batch-" + last, "grantedPoints": tierPoints[last], "rewardedAt": 1794450000000, "grantedTierIds": []any{last}}
	} else {
		status["lastGrant"] = nil
	}
	return map[string]any{
		"code": "000000", "desc": "成功", "trace_id": "trace-1",
		"data": map[string]any{
			"enabled": true, "businessDate": businessDate,
			"config": map[string]any{
				"version": "pet-work-v1", "dailyMaxPoints": 1000, "sampleWindowDays": 28,
				"minimumActiveDays": 7, "defaultSessionBaseline": 2, "defaultDepthBaseline": 4,
				"minimumSessionBaseline": 1, "minimumDepthBaseline": 1,
				"sessionWeight": 0.4, "depthWeight": 0.6, "maxSessionIndex": 2, "maxDepthIndex": 2,
				"tiers": []any{
					map[string]any{"tierId": "tier_1", "score": 60},
					map[string]any{"tierId": "tier_2", "score": 100},
					map[string]any{"tierId": "tier_3", "score": 150},
				},
			},
			"status": status,
		},
	}
}

func claimPayload(businessDate, tier string, rewarded []string, earned float64, duplicate bool) map[string]any {
	status := map[string]any{"earnedPointsToday": earned, "dailyRewardCapped": false, "rewardedTierIds": rewarded}
	if len(rewarded) > 0 {
		status["lastGrant"] = map[string]any{"grantBatchId": "batch-" + tier, "grantedPoints": tierPoints[tier], "rewardedAt": 1794450000000, "grantedTierIds": []any{tier}}
	}
	result := map[string]any{
		"businessDate": businessDate, "granted": !duplicate, "duplicate": duplicate,
		"grantedPoints": 0.0, "grantedTierIds": []any{}, "grantBatchId": "", "status": status,
	}
	if duplicate {
		return map[string]any{"code": "000000", "desc": "成功", "data": result}
	}
	result["grantBatchId"] = "batch-" + tier
	result["grantedPoints"] = tierPoints[tier]
	result["grantedTierIds"] = []any{tier}
	return map[string]any{"code": "000000", "desc": "成功", "data": result}
}

// toData 把测试载荷的 data 转成解码信封的实际形态（map[string]json.RawMessage）。
func toData(t *testing.T, payload map[string]any) map[string]json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(payload["data"])
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]json.RawMessage
	if json.Unmarshal(raw, &data) != nil {
		t.Fatal("payload data must be an object")
	}
	return data
}

func writeLocalFixtures(t *testing.T, tmp, userID string, businessDate string, score float64) {
	t.Helper()
	sessionDir := filepath.Join(tmp, "public", "27f617a908cc", "userData")
	if err := os.MkdirAll(sessionDir, 0700); err != nil {
		t.Fatal(err)
	}
	session := fmt.Sprintf(`{"session":"session-token","userid":"%s","phone":"191****1347"}`, userID)
	if err := os.WriteFile(filepath.Join(sessionDir, "auth-session.json"), []byte(session), 0600); err != nil {
		t.Fatal(err)
	}
	if businessDate == "" {
		return
	}
	if err := os.MkdirAll(filepath.Join(tmp, "Loomy"), 0700); err != nil {
		t.Fatal(err)
	}
	key := userKey(userID)
	raw := fmt.Sprintf(`{"petWork":{"schemaVersion":1,"configCache":null,"users":{"%s":{"history":[],"daily":{"schemaVersion":1,"userKey":"%s","businessDate":"%s","sessions":{},"activeSessionCount":0,"extraValidTurnCount":0,"petWorkScore":%v,"highestReachedTierId":null,"rewardedTierIds":[],"pendingReachedTierId":null,"pendingRetryCount":0,"earnedPointsToday":0,"dailyRewardCapped":false,"lastGrant":null},"classificationDecisions":[]}}}}`, key, key, businessDate, score)
	if err := os.WriteFile(filepath.Join(tmp, "Loomy", "pet-work.json"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
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

// 黄金值来自本机 Loomy 桌面端 pet-work.json 的真实 userKey（2026-11-12 实测），
// 锁定「userKey = sha256(userid) 全量十六进制」这一私有协议事实。
func TestUserKeyGolden(t *testing.T) {
	const realUserID = "261112104434305478"
	const realKey = "67f41d81a3c43d37f8f694b09910accf769912865ba3917d25a8cf1350019c5b"
	if got := userKey(realUserID); got != realKey {
		t.Fatalf("userKey mismatch: %s", got)
	}
}

func TestRequestSendsTokenHeader(t *testing.T) {
	var gotToken, gotCT, gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("token")
		gotCT = r.Header.Get("Content-Type")
		gotMethod = r.Method
		gotPath = r.URL.Path
		reply(w, map[string]any{"code": "000000", "desc": "成功", "data": map[string]any{"balance": 1234, "dailyBalance": 1000, "list": []any{}}})
	}))
	defer srv.Close()
	overrideBases(t, srv.URL)
	c := NewClient()
	value, daily, err := c.balance(context.Background(), " session-token ")
	if err != nil {
		t.Fatal(err)
	}
	if value != 1234 || daily != 1000 {
		t.Fatalf("balance: %v daily: %v", value, daily)
	}
	if gotPath != "/api/v2/points/records" {
		t.Fatalf("balance must use personal records endpoint, got %s", gotPath)
	}
	if gotToken != "session-token" || gotMethod != "GET" || gotCT != "" {
		t.Fatalf("headers: token=%q ct=%q method=%s", gotToken, gotCT, gotMethod)
	}
}

func TestEnvelopeBusinessCodes(t *testing.T) {
	mode := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode {
		case "expired":
			reply(w, map[string]any{"code": "100002", "desc": "缺少 token"})
		case "failed":
			reply(w, map[string]any{"code": "200028", "desc": "档位未达成"})
		case "broken":
			_, _ = w.Write([]byte("not-json"))
		default:
			t.Errorf("unexpected mode %q", mode)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	overrideBases(t, srv.URL)
	c := NewClient()
	mode = "expired"
	if _, _, err := c.balance(context.Background(), "t"); !errors.Is(err, ErrAuthExpired) {
		t.Fatalf("100002 must map to ErrAuthExpired: %v", err)
	}
	mode = "failed"
	if _, _, err := c.balance(context.Background(), "t"); err == nil || !isBusinessError(err) {
		t.Fatalf("business code must surface as apiError: %v", err)
	}
	if _, _, err := c.balance(context.Background(), "t"); err == nil || strings.Contains(err.Error(), "档位未达成") {
		t.Fatalf("upstream desc must not be forwarded: %v", err)
	}
	mode = "broken"
	if _, _, err := c.balance(context.Background(), "t"); err == nil {
		t.Fatal("invalid json must fail")
	}
}

func TestSnapshotNormalization(t *testing.T) {
	today := RewardPeriod(time.Now())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/pet-work" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		reply(w, petWorkPayload(t, today, nil, 0))
	}))
	defer srv.Close()
	overrideBases(t, srv.URL)
	c := NewClient()
	snap, err := c.snapshot(context.Background(), "t")
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Enabled || snap.BusinessDate != today || snap.ConfigVersion != "pet-work-v1" || len(snap.Tiers) != 3 {
		t.Fatalf("snapshot: %+v", snap)
	}
	if snap.Tiers[0].TierID != "tier_1" || snap.Tiers[2].Score != 150 {
		t.Fatalf("tiers must sort ascending: %+v", snap.Tiers)
	}
	if len(snap.Status.RewardedTierIDs) != 0 || snap.Status.LastGrant != nil {
		t.Fatalf("status: %+v", snap.Status)
	}
	// 服务端档位乱序时按阈值排序（桌面端同规则）。
	broken := petWorkPayload(t, today, nil, 0)
	data := toData(t, broken)
	data["status"] = nil
	if _, err := normalizeSnapshot(data); err == nil {
		t.Fatal("missing status must fail")
	}
	data["status"] = json.RawMessage(`{"earnedPointsToday":-1,"dailyRewardCapped":false,"rewardedTierIds":[]}`)
	if _, err := normalizeSnapshot(data); err == nil {
		t.Fatal("negative earned points must fail")
	}
	data["status"] = json.RawMessage(`{"earnedPointsToday":0,"dailyRewardCapped":false,"rewardedTierIds":["tier_9"]}`)
	if _, err := normalizeSnapshot(data); err == nil {
		t.Fatal("unknown tier id must fail")
	}
}

func TestSnapshotAndClaimFlow(t *testing.T) {
	today := RewardPeriod(time.Now())
	rewarded := []string{}
	claims := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1/pet-work":
			reply(w, petWorkPayload(t, today, rewarded, float64(len(rewarded))*60))
		case r.Method == "POST" && r.URL.Path == "/api/v1/pet-work/rewards":
			var body struct {
				BusinessDate  string `json:"businessDate"`
				ConfigVersion string `json:"configVersion"`
				ReachedTierID string `json:"reachedTierId"`
			}
			if json.Unmarshal(readAll(r), &body) != nil {
				t.Errorf("invalid claim body")
				w.WriteHeader(400)
				return
			}
			if body.BusinessDate != today || body.ConfigVersion != "pet-work-v1" || body.ReachedTierID == "" {
				t.Errorf("claim body mismatch: %+v", body)
			}
			claims++
			earned := 0.0
			for _, id := range rewarded {
				earned += tierPoints[id]
			}
			if contains(rewarded, body.ReachedTierID) {
				// 重复领取：不计分不换批次。
				reply(w, claimPayload(today, body.ReachedTierID, rewarded, earned, true))
				return
			}
			rewarded = append(rewarded, body.ReachedTierID)
			reply(w, claimPayload(today, body.ReachedTierID, rewarded, earned+tierPoints[body.ReachedTierID], false))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	overrideBases(t, srv.URL)
	c := NewClient()
	snap, err := c.snapshot(context.Background(), "t")
	if err != nil {
		t.Fatal(err)
	}
	result, err := c.claim(context.Background(), "t", snap, "tier_1")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Granted || result.GrantedPoints != 60 || result.Status.LastGrant == nil {
		t.Fatalf("claim: %+v", result)
	}
	// 重复领取走 duplicate 分支：不计分、批次为空。
	result, err = c.claim(context.Background(), "t", snap, "tier_1")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Duplicate || result.Granted || result.GrantedPoints != 0 {
		t.Fatalf("duplicate claim: %+v", result)
	}
	if claims != 2 {
		t.Fatalf("claims: %d", claims)
	}
}

func TestClaimRejectionCodes(t *testing.T) {
	today := RewardPeriod(time.Now())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/pet-work":
			reply(w, petWorkPayload(t, today, nil, 0))
		case "/api/v1/pet-work/rewards":
			reply(w, map[string]any{"code": "200028", "desc": "未达到该档位"})
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	overrideBases(t, srv.URL)
	c := NewClient()
	snap, err := c.snapshot(context.Background(), "t")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.claim(context.Background(), "t", snap, "tier_1")
	if err == nil || !strings.Contains(err.Error(), "尚未达成") {
		t.Fatalf("200028 must translate to friendly text: %v", err)
	}
}

// TestStoreCheckinFaithfulFlow 忠实补领全流程：只领本机已达成且未发放的档位，
// 升序逐个申请；服务端拒绝时保留欠账（不落 LastPeriod）。
func TestStoreCheckinFaithfulFlow(t *testing.T) {
	tmp := overrideLocal(t)
	today := RewardPeriod(time.Now())
	rewarded := []string{}
	var claimOrder []string
	balanceHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1/pet-work":
			reply(w, petWorkPayload(t, today, rewarded, float64(len(rewarded))*60))
		case r.Method == "POST" && r.URL.Path == "/api/v1/pet-work/rewards":
			var body struct {
				ReachedTierID string `json:"reachedTierId"`
			}
			_ = json.Unmarshal(readAll(r), &body)
			if body.ReachedTierID != "tier_1" && body.ReachedTierID != "tier_2" && body.ReachedTierID != "tier_3" {
				t.Errorf("unexpected tier %q", body.ReachedTierID)
			}
			claimOrder = append(claimOrder, body.ReachedTierID)
			earned := 0.0
			for _, id := range rewarded {
				earned += tierPoints[id]
			}
			rewarded = append(rewarded, body.ReachedTierID)
			reply(w, claimPayload(today, body.ReachedTierID, rewarded, earned+tierPoints[body.ReachedTierID], false))
		case r.Method == "GET" && r.URL.Path == "/api/v2/points/records":
			balanceHits++
			reply(w, map[string]any{"code": "000000", "desc": "成功", "data": map[string]any{"balance": 3210, "list": []any{}}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	overrideBases(t, srv.URL)
	// 本机分数 130：tier_1 与 tier_2 已达成（阈值 60/100），tier_3（150）未达成。
	writeLocalFixtures(t, tmp, testUserID, today, 130)
	path := filepath.Join(t.TempDir(), "accounts.json")
	s, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	added, warnings, err := s.Add(context.Background(), Account{Credential: "session-token", Nickname: "", AccountUID: testUserID}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("add warnings: %v", warnings)
	}
	if !added.BalanceKnown || added.Credits != 3210 || added.PetScore != 130 || added.ReachedTier != "tier_2" {
		t.Fatalf("add: %+v", added)
	}
	public, _ := json.Marshal(s.List())
	if strings.Contains(string(public), "session-token") {
		t.Fatalf("credentials leaked into public status: %s", public)
	}
	if added.CheckinDone {
		t.Fatal("fresh account with pending tiers must not be done")
	}
	if errs := s.RunAll(context.Background(), "checkin"); len(errs) != 0 {
		t.Fatalf("runall: %v", errs)
	}
	if strings.Join(claimOrder, ",") != "tier_1,tier_2" {
		t.Fatalf("claims must ascend: %v", claimOrder)
	}
	if balanceHits == 0 {
		t.Fatal("checkin must refresh balance after claims")
	}
	st := s.List()[0]
	if !st.CheckinDone || st.TodayEarned != 160 {
		t.Fatalf("status after checkin: %+v", st)
	}
	// 全部档位已领：再次签到不产生新的 POST。
	claimOrder = nil
	before := balanceHits
	if errs := s.RunAll(context.Background(), "checkin"); len(errs) != 0 || len(claimOrder) != 0 {
		t.Fatalf("settled day must not claim again: %v %v", errs, claimOrder)
	}
	if balanceHits-before != 1 {
		t.Fatalf("settled day still refreshes balance: %d", balanceHits-before)
	}
}

// TestStoreCheckinRejectsKeepsPending：服务端拒绝时错误落账且不标记完成。
func TestStoreCheckinRejectsKeepsPending(t *testing.T) {
	tmp := overrideLocal(t)
	today := RewardPeriod(time.Now())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1/pet-work":
			reply(w, petWorkPayload(t, today, nil, 0))
		case r.Method == "POST" && r.URL.Path == "/api/v1/pet-work/rewards":
			reply(w, map[string]any{"code": "200028", "desc": "未达到该档位"})
		case r.Method == "GET" && r.URL.Path == "/api/v2/points/records":
			reply(w, map[string]any{"code": "000000", "data": map[string]any{"balance": 1, "list": []any{}}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	overrideBases(t, srv.URL)
	writeLocalFixtures(t, tmp, testUserID, today, 130)
	s, _ := Open(filepath.Join(t.TempDir(), "accounts.json"), nil)
	if _, _, err := s.Add(context.Background(), Account{Credential: "session-token", AccountUID: testUserID}, ""); err != nil {
		t.Fatal(err)
	}
	if errs := s.RunAll(context.Background(), "checkin"); len(errs) == 0 {
		t.Fatal("rejected claim must surface as error")
	}
	st := s.List()[0]
	if st.CheckinDone {
		t.Fatal("rejected claim must not mark period done")
	}
	if !strings.Contains(st.LastError, "尚未达成") {
		t.Fatalf("last error: %q", st.LastError)
	}
}

// TestStoreCheckinLocalGuards：本机记录缺失/跨日时忠实模式不申报任何档位。
func TestStoreCheckinLocalGuards(t *testing.T) {
	tmp := overrideLocal(t)
	today := RewardPeriod(time.Now())
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1/pet-work":
			requests++
			reply(w, petWorkPayload(t, today, nil, 0))
		case r.Method == "POST" && r.URL.Path == "/api/v1/pet-work/rewards":
			t.Error("no claim allowed without local reached tier")
			reply(w, map[string]any{"code": "000000"})
		case r.Method == "GET" && r.URL.Path == "/api/v2/points/records":
			reply(w, map[string]any{"code": "000000", "data": map[string]any{"balance": 1, "list": []any{}}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	overrideBases(t, srv.URL)
	// 本机 pet-work.json 缺失。
	writeLocalFixtures(t, tmp, testUserID, "", 0)
	s, _ := Open(filepath.Join(t.TempDir(), "accounts.json"), nil)
	if _, _, err := s.Add(context.Background(), Account{Credential: "session-token", AccountUID: testUserID}, ""); err != nil {
		t.Fatal(err)
	}
	if errs := s.RunAll(context.Background(), "checkin"); len(errs) == 0 || !strings.Contains(strings.Join(errs, ";"), "pet-work.json") {
		t.Fatalf("missing local record must fail clearly: %v", errs)
	}
	// 本机记录是昨日（跨日）。
	writeLocalFixtures(t, tmp, testUserID, "2026-11-11", 200)
	if errs := s.RunAll(context.Background(), "checkin"); len(errs) == 0 || !strings.Contains(strings.Join(errs, ";"), "2026-11-11") {
		t.Fatalf("stale local day must fail clearly: %v", errs)
	}
	if requests < 2 {
		t.Fatal("snapshot must be fetched before guard decisions")
	}
}

func TestStoreAddValidation(t *testing.T) {
	tmp := overrideLocal(t)
	today := RewardPeriod(time.Now())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/pet-work":
			if r.Header.Get("token") == "expired" {
				reply(w, map[string]any{"code": "100002"})
				return
			}
			reply(w, petWorkPayload(t, today, nil, 0))
		case "/api/v2/points/records":
			reply(w, map[string]any{"code": "000000", "data": map[string]any{"balance": 7, "list": []any{}}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	overrideBases(t, srv.URL)
	writeLocalFixtures(t, tmp, testUserID, today, 0)
	s, _ := Open(filepath.Join(t.TempDir(), "accounts.json"), nil)
	// 缺 userid 直接拒收（本机进度按 userid 分桶）。
	if _, _, err := s.Add(context.Background(), Account{Credential: "session-token"}, ""); err == nil || !strings.Contains(err.Error(), "userid") {
		t.Fatalf("missing userid must be rejected: %v", err)
	}
	// 登录失效的会话拒收。
	if _, _, err := s.Add(context.Background(), Account{Credential: "expired", AccountUID: testUserID}, ""); err == nil || !strings.Contains(err.Error(), "失效") {
		t.Fatalf("expired session must be rejected: %v", err)
	}
	// 非法字符拒收。
	if _, _, err := s.Add(context.Background(), Account{Credential: "bad\ntoken", AccountUID: testUserID}, ""); err == nil {
		t.Fatal("injected credential must be rejected")
	}
	added, _, err := s.Add(context.Background(), Account{Credential: "session-token", AccountUID: testUserID}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(added.UID, "loomy-") || added.Nickname != "Loomy 账号" {
		t.Fatalf("add defaults: %+v", added)
	}
	// 相同 userid 去重（更新而非新增）。
	again, _, err := s.Add(context.Background(), Account{Credential: "session-token", Nickname: "备注名", AccountUID: testUserID}, "")
	if err != nil || again.UID != added.UID || len(s.List()) != 1 {
		t.Fatalf("dedupe by userid: %+v err=%v", again, err)
	}
}

func TestFindLocalSession(t *testing.T) {
	tmp := overrideLocal(t)
	// 未登录：找不到会话。
	if _, err := findLocalSession(); err == nil {
		t.Fatal("missing session must fail")
	}
	writeLocalFixtures(t, tmp, testUserID, "", 0)
	found, err := findLocalSession()
	if err != nil || found.Session != "session-token" || found.UserID != testUserID || found.Phone != "191****1347" {
		t.Fatalf("findLocalSession: %+v err=%v", found, err)
	}
}

// writeSessionAt 写入一份 auth-session.json 并把修改时间设为 mtime。
func writeSessionAt(t *testing.T, dir, body string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "auth-session.json")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// TestFindLocalSessionPrefersNewest 覆盖多用户目录场景：按修改时间取最新，
// 且最新文件无效时回退到较旧的有效会话。
func TestFindLocalSessionPrefersNewest(t *testing.T) {
	tmp := overrideLocal(t)
	now := time.Now()
	// aaaa 字典序更靠前但更新更早：若实现退回「取第一个匹配」会命中 stale。
	writeSessionAt(t, filepath.Join(tmp, "public", "aaaa", "userData"),
		`{"session":"stale-token","userid":"stale","phone":"191****0001"}`, now.Add(-3*time.Hour))
	writeSessionAt(t, filepath.Join(tmp, "public", "bbbb", "userData"),
		`{"session":"fresh-token","userid":"fresh","phone":"191****0002"}`, now.Add(-1*time.Hour))

	found, err := findLocalSession()
	if err != nil || found.Session != "fresh-token" || found.UserID != "fresh" {
		t.Fatalf("应取最近写入的会话: %+v err=%v", found, err)
	}

	// 最新文件存在但内容无效（缺 session/userid）：回退到次新的有效会话。
	writeSessionAt(t, filepath.Join(tmp, "public", "cccc", "userData"),
		`{"session":"","userid":""}`, now)
	found, err = findLocalSession()
	if err != nil || found.Session != "fresh-token" {
		t.Fatalf("最新文件无效时应回退到有效会话: %+v err=%v", found, err)
	}
}

// TestStoreUpdateIdentityAndBalancePersist 锁定两个修复：更新凭证时新 userid
// 与原账号不一致必须拒绝（防 A 账号被 B 凭证顶替）；添加与更新时刷新到的余额
// 必须写回落盘，而不是留在被丢弃的局部副本里。
func TestStoreUpdateIdentityAndBalancePersist(t *testing.T) {
	tmp := overrideLocal(t)
	today := RewardPeriod(time.Now())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1/pet-work":
			reply(w, petWorkPayload(t, today, nil, 0))
		case r.Method == "GET" && r.URL.Path == "/api/v2/points/records":
			reply(w, map[string]any{"code": "000000", "data": map[string]any{"balance": 777, "dailyBalance": 66, "list": []any{}}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	overrideBases(t, srv.URL)
	writeLocalFixtures(t, tmp, testUserID, today, 0)
	path := filepath.Join(t.TempDir(), "accounts.json")
	s, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}

	added, warnings, err := s.Add(context.Background(), Account{Credential: "tok-a", AccountUID: testUserID}, "")
	if err != nil || len(warnings) != 0 {
		t.Fatalf("add: %v %v", err, warnings)
	}
	// 添加时刷新到的余额必须保存（而非留在局部副本）。
	if !added.BalanceKnown || added.Credits != 777 || added.DailyBalance != 66 {
		t.Fatalf("add must persist refreshed balance: %+v", added)
	}

	// B 账号凭证更新 A：拒绝，原记录不被替换。
	if _, _, err := s.Add(context.Background(), Account{Credential: "tok-b", AccountUID: "user-b"}, added.UID); err == nil || !strings.Contains(err.Error(), "不一致") {
		t.Fatalf("cross-account update must be rejected: %v", err)
	}
	if st := s.List()[0]; st.UID != added.UID {
		t.Fatal("original record replaced by another identity")
	}

	// 同 userid 的新凭证更新：放行，沿用本地 ID，余额刷新结果同样落盘。
	updated, _, err := s.Add(context.Background(), Account{Credential: "tok-a2", AccountUID: testUserID}, added.UID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.UID != added.UID || updated.Credits != 777 || updated.DailyBalance != 66 {
		t.Fatalf("update must persist refreshed balance: %+v", updated)
	}
	reopened, err := Open(path, nil)
	if err != nil || len(reopened.List()) != 1 || reopened.List()[0].Credits != 777 {
		t.Fatalf("persisted: %v %+v", err, reopened.List())
	}
}
