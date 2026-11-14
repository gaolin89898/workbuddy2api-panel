package doubao

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func checkinUpstream(t *testing.T, checkinBody, groupBody string) *Client {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/alice/ug/campaign/landing/checkin" {
			w.Write([]byte(checkinBody))
			return
		}
		if r.URL.Path == "/alice/ug/campaign/v3/detail/" {
			w.Write([]byte(groupBody))
			return
		}
		t.Errorf("unexpected upstream call: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(404)
	}))
	t.Cleanup(upstream.Close)
	return &Client{HTTP: upstream.Client(), Base: upstream.URL}
}

var checkinGroupFixture = `{"code":0,"data":{"progress":{"total_amount":4100},"tasks":[{"task_kind":"chat_activity","title":"每日完成一项工作任务"},{"task_kind":"daily_signin","task_status":3,"daily_checkin":{"checked_in_today":true,"last_checkin_date":"2026-11-14","continuous_days":2,"total_days":15,"reward":{"reward_amount":200,"reward_type":"quota_grant"}}}]}}`

func TestCheckinParsesResultAndStatus(t *testing.T) {
	c := checkinUpstream(t, `{"code":0,"data":{"checked_in":true,"checkin_date":"2026-11-14","continuous_days":3,"total_days":15,"reward_amount":200,"reward_type":"quota_grant"}}`, checkinGroupFixture)
	result, err := c.Checkin(context.Background(), &Account{Cookie: "sessionid=x"})
	if err != nil || !result.CheckedIn || result.CheckinDate != "2026-11-14" || result.ContinuousDays != 3 || result.TotalDays != 15 || result.Idempotent || result.RewardAmount != 200 || result.RewardType != "quota_grant" {
		t.Fatalf("bad checkin result: %+v %v", result, err)
	}
	status, err := c.CheckinGroupStatus(context.Background(), &Account{Cookie: "sessionid=x"})
	if err != nil || !status.CheckedInToday || status.LastCheckinDate != "2026-11-14" || status.ContinuousDays != 2 || status.TotalDays != 15 || status.Points != 4100 || status.RewardAmount != 200 {
		t.Fatalf("bad group status: %+v %v", status, err)
	}
}

func TestCheckinIdempotentRejectsAndMalformed(t *testing.T) {
	idempotent := `{"code":0,"data":{"checked_in":true,"checkin_date":"2026-11-14","continuous_days":2,"idempotent":true,"reward_amount":200,"reward_type":"quota_grant"}}`
	c := checkinUpstream(t, idempotent, checkinGroupFixture)
	result, err := c.Checkin(context.Background(), &Account{Cookie: "sessionid=x"})
	if err != nil || !result.Idempotent || !result.CheckedIn {
		t.Fatalf("idempotent checkin should succeed: %+v %v", result, err)
	}
	rejected := checkinUpstream(t, `{"code":7,"msg":"login required"}`, checkinGroupFixture)
	if _, err := rejected.Checkin(context.Background(), &Account{Cookie: "sessionid=x"}); err == nil || !strings.Contains(err.Error(), "7") {
		t.Fatalf("expected rejection error, got %v", err)
	}
	unchecked := checkinUpstream(t, `{"code":0,"data":{"checked_in":false}}`, checkinGroupFixture)
	if _, err := unchecked.Checkin(context.Background(), &Account{Cookie: "sessionid=x"}); err == nil {
		t.Fatal("expected incomplete checkin error")
	}
	malformed := checkinUpstream(t, "not-json", checkinGroupFixture)
	if _, err := malformed.Checkin(context.Background(), &Account{Cookie: "sessionid=x"}); err == nil {
		t.Fatal("expected malformed response error")
	}
	if _, err := c.CheckinGroupStatus(context.Background(), &Account{Cookie: "sessionid=x"}); err != nil {
		t.Fatalf("group status with nil progress should pass: %v", err)
	}
}

func TestCheckinDayUsesBeijingDate(t *testing.T) {
	// 2026-11-14 22:30 UTC is already 2026-11-15 in Beijing.
	now := time.Date(2026, 11, 14, 22, 30, 0, 0, time.UTC)
	if got := CheckinDay(now); got != "2026-11-15" {
		t.Fatalf("CheckinDay = %q, want 2026-11-15", got)
	}
}

func TestStoreCheckinReadsClientCheckinWhenRejected(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/alice/ug/campaign/landing/checkin":
			w.Write([]byte(checkinRejectedBody))
		case "/alice/ug/campaign/v3/detail/":
			w.Write([]byte(checkinClientSignedFixture))
		case "/alice/ug/campaign/landing/get":
			w.Write([]byte(checkinDesktopCardFixture))
		case "/passport/account/info/v2/":
			w.Write([]byte(`{"message":"success","data":{"user_id":"77","screen_name":"豆包二号"}}`))
		case "/alice/commerce/sale/subscription/quota/summary/":
			w.Write([]byte(`{"code":0,"data":{"current_subscription":{"display":{"short_name":"免费体验"}},"incentive_quota_section":{"window_limit_groups":[]}}}`))
		default:
			t.Errorf("unexpected upstream call: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	s, err := Open(filepath.Join(t.TempDir(), "accounts.json"), &Client{HTTP: upstream.Client(), Base: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Add(context.Background(), Account{Cookie: "sessionid=second"}, "")
	if err != nil {
		t.Fatal(err)
	}
	// 平台拒绝领取，但客户端已完成签到：按回读入账，不报错。
	if _, err = s.Action(context.Background(), a.UID, "checkin"); err != nil {
		t.Fatalf("client-signed fallback should succeed: %v", err)
	}
	got := s.List()[0]
	if got.LastCheckinDate != "2026-11-14" || !got.CheckinDone || got.CheckinContinuous != 1 {
		t.Fatalf("fallback state not recorded: %+v", got)
	}
}

func TestStoreCheckinSurfacesPlatformSideError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/alice/ug/campaign/landing/checkin":
			w.Write([]byte(checkinRejectedBody))
		case "/alice/ug/campaign/v3/detail/":
			w.Write([]byte(checkinUnsignedFixture))
		case "/alice/ug/campaign/landing/get":
			w.Write([]byte(checkinDesktopCardFixture))
		case "/passport/account/info/v2/":
			w.Write([]byte(`{"message":"success","data":{"user_id":"88","screen_name":"豆包三号"}}`))
		case "/alice/commerce/sale/subscription/quota/summary/":
			w.Write([]byte(`{"code":0,"data":{"current_subscription":{"display":{"short_name":"免费体验"}},"incentive_quota_section":{"window_limit_groups":[]}}}`))
		default:
			t.Errorf("unexpected upstream call: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	s, err := Open(filepath.Join(t.TempDir(), "accounts.json"), &Client{HTTP: upstream.Client(), Base: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Add(context.Background(), Account{Cookie: "sessionid=third"}, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Action(context.Background(), a.UID, "checkin")
	if err == nil || !strings.Contains(err.Error(), "平台侧") {
		t.Fatalf("expected desktop-only message, got %v", err)
	}
}

var checkinRejectedBody = `{"code":710010702,"msg":"系统错误","message":"system error"}`

var checkinClientSignedFixture = `{"code":0,"data":{"progress":{"total_amount":200},"tasks":[{"task_kind":"daily_signin","daily_checkin":{"checked_in_today":true,"last_checkin_date":"2026-11-14","continuous_days":1,"total_days":3,"reward":{"reward_amount":200,"reward_type":"quota_grant"}}}]}}`

var checkinUnsignedFixture = `{"code":0,"data":{"progress":{"total_amount":0},"tasks":[{"task_kind":"daily_signin","daily_checkin":{"checked_in_today":false,"last_checkin_date":"","continuous_days":0,"total_days":0}}]}}`

var checkinDesktopCardFixture = `{"code":0,"data":{"chosen_type":3,"landing_cards":[{"card_code":"web_pc_download_banner","subtitle":"前往豆包桌面端即可领取","task_code":"daily_signin"}]}}`
