package doubao

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/gateway"
)

type Account struct {
	ID                string    `json:"id"`
	AccountUID        string    `json:"account_uid"`
	Nickname          string    `json:"nickname"`
	Cookie            string    `json:"cookie"`
	Disabled          bool      `json:"disabled"`
	VerifiedAt        time.Time `json:"verified_at"`
	Credits           float64   `json:"credits"`
	Total             float64   `json:"credits_total"`
	Quotas            []Quota   `json:"quotas"`
	Plan              string    `json:"plan"`
	BalanceKnown      bool      `json:"balance_known"`
	BalanceUpdatedAt  time.Time `json:"balance_updated_at"`
	LastCheckinDate   string    `json:"last_checkin_date,omitempty"`
	CheckinContinuous int       `json:"checkin_continuous"`
	CheckinTotalDays  int       `json:"checkin_total_days"`
	CheckinPoints     float64   `json:"checkin_points,omitempty"`
	LastError         string    `json:"last_error,omitempty"`
}

// Status deliberately excludes all credentials.
type Status struct {
	UID               string    `json:"uid"`
	AccountUID        string    `json:"account_uid"`
	Nickname          string    `json:"nickname"`
	Provider          string    `json:"provider"`
	Realm             string    `json:"realm"`
	Disabled          bool      `json:"disabled"`
	CheckinSupported  bool      `json:"checkin_supported"`
	CheckinDone       bool      `json:"checkin_done"`
	LastCheckinDate   string    `json:"last_checkin_date,omitempty"`
	CheckinContinuous int       `json:"checkin_continuous"`
	CheckinTotalDays  int       `json:"checkin_total_days"`
	CheckinPoints     float64   `json:"checkin_points,omitempty"`
	BalanceKnown      bool      `json:"balance_known"`
	Credits           float64   `json:"credits"`
	Total             float64   `json:"credits_total"`
	Quotas            []Quota   `json:"quotas"`
	Plan              string    `json:"plan"`
	BalanceUpdatedAt  time.Time `json:"balance_updated_at"`
	VerifiedAt        time.Time `json:"verified_at"`
	LastError         string    `json:"last_error,omitempty"`
}

func status(a Account) Status {
	return Status{UID: a.ID, AccountUID: a.AccountUID, Nickname: a.Nickname, Provider: "doubao", Realm: "cn", Disabled: a.Disabled, VerifiedAt: a.VerifiedAt, CheckinSupported: true, CheckinDone: a.LastCheckinDate != "" && a.LastCheckinDate == CheckinDay(time.Now()), LastCheckinDate: a.LastCheckinDate, CheckinContinuous: a.CheckinContinuous, CheckinTotalDays: a.CheckinTotalDays, CheckinPoints: a.CheckinPoints, Credits: a.Credits, Total: a.Total, Quotas: append([]Quota{}, a.Quotas...), Plan: a.Plan, BalanceKnown: a.BalanceKnown, BalanceUpdatedAt: a.BalanceUpdatedAt, LastError: a.LastError}
}

type Store struct {
	mu             sync.RWMutex
	op             sync.Mutex
	path           string
	accounts       []Account
	client         *Client
	refreshEnabled atomic.Bool
}

// SetRefreshEnabled 控制签到成功后是否顺带刷新额度（调度批量签到时开启）。
func (s *Store) SetRefreshEnabled(enabled bool) { s.refreshEnabled.Store(enabled) }

func Open(path string, client *Client) (*Store, error) {
	if client == nil {
		client = NewClient()
	}
	s := &Store{path: path, client: client, accounts: []Account{}}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil || json.Unmarshal(raw, &s.accounts) != nil {
		return nil, fmt.Errorf("读取豆包账号存档失败")
	}
	return s, nil
}

func (s *Store) snapshot() []Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Account(nil), s.accounts...)
}

func (s *Store) List() []Status {
	out := []Status{}
	for _, a := range s.snapshot() {
		out = append(out, status(a))
	}
	return out
}

func (s *Store) lock(ctx context.Context) error {
	for !s.op.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	if ctx.Err() != nil {
		s.op.Unlock()
		return ctx.Err()
	}
	return nil
}

func (s *Store) commit(accounts []Account) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fmt.Errorf("创建豆包存档目录失败")
	}
	raw, err := json.MarshalIndent(accounts, "", "  ")
	if err != nil {
		return fmt.Errorf("编码豆包存档失败")
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".doubao-*.tmp")
	if err != nil {
		return fmt.Errorf("创建豆包存档失败")
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return fmt.Errorf("写入豆包存档失败")
	}
	if os.Rename(f.Name(), s.path) != nil {
		return fmt.Errorf("替换豆包存档失败")
	}
	s.mu.Lock()
	s.accounts = append([]Account(nil), accounts...)
	s.mu.Unlock()
	return nil
}

func (s *Store) Add(ctx context.Context, a Account, expected string) (Status, error) {
	if err := s.lock(ctx); err != nil {
		return Status{}, err
	}
	defer s.op.Unlock()
	var err error
	if a.Cookie, err = NormalizeCookie(a.Cookie); err != nil {
		return Status{}, err
	}
	a.Nickname = strings.TrimSpace(a.Nickname)
	if len(a.Nickname) > 240 {
		return Status{}, fmt.Errorf("豆包账号备注过长")
	}
	if err := s.client.Inspect(ctx, &a); err != nil {
		return Status{}, err
	}
	accounts := s.snapshot()
	index := -1
	for i, old := range accounts {
		if expected != "" && old.ID == expected || expected == "" && old.AccountUID == a.AccountUID {
			index = i
			break
		}
	}
	if expected != "" && index < 0 {
		return Status{}, os.ErrNotExist
	}
	if index >= 0 {
		old := accounts[index]
		if old.AccountUID != a.AccountUID {
			return Status{}, fmt.Errorf("新凭证属于另一个豆包账号，请单独添加")
		}
		a.ID, a.Disabled = old.ID, old.Disabled
		a.Credits, a.BalanceKnown, a.BalanceUpdatedAt = old.Credits, old.BalanceKnown, old.BalanceUpdatedAt
		a.Total, a.Quotas, a.Plan = old.Total, old.Quotas, old.Plan
	} else {
		hash := sha256.Sum256([]byte(a.AccountUID))
		a.ID = "doubao-" + hex.EncodeToString(hash[:12])
	}
	if a.Nickname == "" {
		a.Nickname = "豆包账号"
	}
	// A valid login remains usable when the separate quota service is down.
	// Keep the last successful balance and expose the query error in Status.
	_ = s.refreshBalance(ctx, &a)
	if index >= 0 {
		accounts[index] = a
	} else {
		accounts = append(accounts, a)
	}
	if ctx.Err() != nil {
		return Status{}, ctx.Err()
	}
	if err := s.commit(accounts); err != nil {
		return Status{}, err
	}
	return status(a), nil
}

func (s *Store) refreshBalance(ctx context.Context, a *Account) error {
	value, err := s.client.Balance(ctx, a)
	a.LastError = ""
	if err != nil {
		a.LastError = err.Error()
		return err
	}
	a.Credits, a.Total, a.BalanceKnown, a.BalanceUpdatedAt = value.Credits, value.Total, value.Known, time.Now()
	a.Quotas, a.Plan = value.Quotas, value.Plan
	return nil
}

func (s *Store) Action(ctx context.Context, id, action string) (Status, error) {
	if err := s.lock(ctx); err != nil {
		return Status{}, err
	}
	defer s.op.Unlock()
	accounts := s.snapshot()
	for i := range accounts {
		a := &accounts[i]
		if a.ID != id {
			continue
		}
		result := status(*a)
		var inspectErr error
		switch action {
		case "remove":
			accounts = append(accounts[:i], accounts[i+1:]...)
		case "disable":
			a.Disabled = true
		case "revive":
			a.Disabled = false
		case "balance":
			inspectErr = s.client.Inspect(ctx, a)
			if inspectErr != nil {
				a.LastError = inspectErr.Error()
			} else {
				inspectErr = s.refreshBalance(ctx, a)
			}
		case "checkin":
			if a.Disabled {
				return status(*a), fmt.Errorf("该豆包账号已停用")
			}
			// 服务端按天幂等，但当天已签时直接跳过网络请求。
			if !status(*a).CheckinDone {
				result2, checkinErr := s.client.Checkin(ctx, a)
				if checkinErr != nil {
					// 平台可能把该账号的签到路由到桌面端（网页领取被拒）。若任务组
					// 回读显示当天已签（桌面端/手机端已完成），按幂等回读入账。
					if group, gerr := s.client.CheckinGroupStatus(ctx, a); gerr == nil && group.CheckedInToday && group.LastCheckinDate != "" {
						a.LastCheckinDate = group.LastCheckinDate
						a.CheckinContinuous, a.CheckinTotalDays = group.ContinuousDays, group.TotalDays
						a.CheckinPoints = group.Points
						a.LastError = ""
						if s.refreshEnabled.Load() {
							_ = s.refreshBalance(ctx, a)
						}
					} else {
						// 客户端内点「立即签到」同样失败（用户实测），属平台侧账号活动异常；
						// 给出准确提示并保持每日自动重试。
						if strings.Contains(checkinErr.Error(), "710010702") {
							checkinErr = fmt.Errorf("平台签到服务对该账号返回系统错误（710010702）：在豆包客户端内点「立即签到」也一样失败，属平台侧账号活动异常；面板每天自动重试，平台恢复后会自动签到并计入额度")
						}
						a.LastError = checkinErr.Error()
						if commitErr := s.commit(accounts); commitErr != nil {
							return Status{}, commitErr
						}
						return status(*a), checkinErr
					}
				} else {
					a.LastCheckinDate = result2.CheckinDate
					a.CheckinContinuous, a.CheckinTotalDays = result2.ContinuousDays, result2.TotalDays
					// 累计获得点数与周期天数以任务组回读为准，失败不影响签到结果。
					if group, err := s.client.CheckinGroupStatus(ctx, a); err == nil {
						a.CheckinPoints = group.Points
						if group.TotalDays > a.CheckinTotalDays {
							a.CheckinTotalDays = group.TotalDays
						}
					}
					a.LastError = ""
					if s.refreshEnabled.Load() {
						_ = s.refreshBalance(ctx, a)
					}
				}
			}
		default:
			return Status{}, fmt.Errorf("不支持的豆包账号操作")
		}
		if ctx.Err() != nil {
			return Status{}, ctx.Err()
		}
		if action != "remove" {
			result = status(*a)
		}
		if err := s.commit(accounts); err != nil {
			return Status{}, err
		}
		return result, inspectErr
	}
	return Status{}, os.ErrNotExist
}

// RunAll 对全部启用账号执行签到或余额刷新，返回每个失败账号的错误文本。
func (s *Store) RunAll(ctx context.Context, action string) []string {
	failures := []string{}
	for _, a := range s.List() {
		if ctx.Err() != nil {
			break
		}
		if a.Disabled || action == "checkin" && a.CheckinDone {
			continue
		}
		if _, err := s.Action(ctx, a.UID, action); err != nil {
			failures = append(failures, a.UID+": "+err.Error())
			log.Printf("doubao: %s 失败 %s", action, err)
		} else {
			log.Printf("doubao: %s 完成", action)
		}
	}
	return failures
}

func (s *Store) GatewayAccounts() []gateway.Account {
	out := []gateway.Account{}
	for _, a := range s.List() {
		out = append(out, gateway.Account{ID: a.UID, Nickname: a.Nickname, Disabled: a.Disabled})
	}
	return out
}

func (s *Store) GatewayCredential(ctx context.Context, id string) (gateway.Credential, error) {
	if ctx.Err() != nil {
		return gateway.Credential{}, ctx.Err()
	}
	for _, a := range s.snapshot() {
		if a.ID == id && !a.Disabled {
			return gateway.Credential{ID: a.ID, Nickname: a.Nickname, UID: a.AccountUID, Token: a.Cookie}, nil
		}
	}
	return gateway.Credential{}, os.ErrNotExist
}
