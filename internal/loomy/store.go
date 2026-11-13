package loomy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Account struct {
	ID               string    `json:"id"`
	AccountUID       string    `json:"account_uid,omitempty"`
	Nickname         string    `json:"nickname"`
	Credential       string    `json:"credential"`
	Disabled         bool      `json:"disabled"`
	Credits          float64   `json:"credits"`
	DailyBalance     float64   `json:"daily_balance"`
	BalanceKnown     bool      `json:"balance_known"`
	BalanceUpdatedAt time.Time `json:"balance_updated_at"`
	LastPeriod       string    `json:"last_period"`
	LastError        string    `json:"last_error,omitempty"`
	// TodayEarned / PetScore / ReachedTier 是最近一次快照或签到时的当日进度
	// 展示字段（服务端权威到账 + 本机桌面端达成档）。
	TodayEarned float64 `json:"today_earned"`
	PetScore    float64 `json:"pet_score"`
	ReachedTier string  `json:"reached_tier,omitempty"`
}

type Status struct {
	UID              string    `json:"uid"`
	AccountUID       string    `json:"account_uid"`
	Nickname         string    `json:"nickname"`
	Provider         string    `json:"provider"`
	Realm            string    `json:"realm"`
	Disabled         bool      `json:"disabled"`
	Credits          float64   `json:"credits"`
	DailyBalance     float64   `json:"daily_balance"`
	BalanceKnown     bool      `json:"balance_known"`
	BalanceUpdatedAt time.Time `json:"balance_updated_at"`
	CheckinDone      bool      `json:"checkin_done"`
	CheckinSupported bool      `json:"checkin_supported"`
	TodayEarned      float64   `json:"today_earned"`
	PetScore         float64   `json:"pet_score"`
	ReachedTier      string    `json:"reached_tier,omitempty"`
	LastError        string    `json:"last_error,omitempty"`
}

func status(a Account) Status {
	return Status{UID: a.ID, AccountUID: a.AccountUID, Nickname: a.Nickname, Provider: "loomy", Realm: "cn", Disabled: a.Disabled, Credits: a.Credits, DailyBalance: a.DailyBalance, BalanceKnown: a.BalanceKnown, BalanceUpdatedAt: a.BalanceUpdatedAt, CheckinDone: a.LastPeriod != "" && a.LastPeriod == RewardPeriod(time.Now()), TodayEarned: a.TodayEarned, PetScore: a.PetScore, ReachedTier: a.ReachedTier, LastError: a.LastError}
}

type Store struct {
	mu             sync.RWMutex
	op             sync.Mutex
	path           string
	accounts       []Account
	client         *Client
	refreshEnabled atomic.Bool
}

func Open(path string, c *Client) (*Store, error) {
	if c == nil {
		c = NewClient()
	}
	s := &Store{path: path, client: c, accounts: []Account{}}
	s.refreshEnabled.Store(true)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil || json.Unmarshal(raw, &s.accounts) != nil {
		return nil, fmt.Errorf("读取 Loomy 账号存档失败")
	}
	return s, nil
}
func (s *Store) SetRefreshEnabled(v bool) { s.refreshEnabled.Store(v) }
func (s *Store) List() []Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Status, 0, len(s.accounts))
	for _, a := range s.accounts {
		out = append(out, status(a))
	}
	return out
}
func (s *Store) snapshot() []Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Account(nil), s.accounts...)
}
func (s *Store) commit(accounts []Account) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fmt.Errorf("创建 Loomy 存档目录失败")
	}
	raw, err := json.MarshalIndent(accounts, "", "  ")
	if err != nil {
		return fmt.Errorf("保存 Loomy 账号失败")
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".loomy-*.tmp")
	if err != nil {
		return fmt.Errorf("创建 Loomy 存档失败")
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
		return fmt.Errorf("写入 Loomy 存档失败")
	}
	if os.Rename(f.Name(), s.path) != nil {
		return fmt.Errorf("替换 Loomy 存档失败")
	}
	s.mu.Lock()
	s.accounts = append([]Account(nil), accounts...)
	s.mu.Unlock()
	return nil
}

func accountID(uid string) string {
	sum := sha256.Sum256([]byte(uid))
	return "loomy-" + hex.EncodeToString(sum[:12])
}

func credentialFingerprint(credential string) string {
	sum := sha256.Sum256([]byte(credential))
	return "loomy-" + hex.EncodeToString(sum[:12])
}

// sameIdentity 依据账号身份（讯飞 userid）或同一凭证判断是否为同一账号，
// 以便端点确认前后（uid 未知/已知）都能正确去重。
func sameIdentity(a, b Account) bool {
	if a.AccountUID != "" && b.AccountUID != "" {
		return a.AccountUID == b.AccountUID
	}
	return strings.TrimSpace(a.Credential) == strings.TrimSpace(b.Credential)
}

func (s *Store) lock(ctx context.Context) error {
	for !s.op.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	if ctx.Err() != nil {
		s.op.Unlock()
		return ctx.Err()
	}
	return nil
}

// Add 校验并保存账号：session 通过服务端快照验证，身份取讯飞 userid
// （本机宠物打工记录按 userid 分桶，因此必填）。
func (s *Store) Add(ctx context.Context, a Account, expected string) (Status, []string, error) {
	if err := s.lock(ctx); err != nil {
		return Status{}, nil, err
	}
	defer s.op.Unlock()
	credential := strings.TrimSpace(a.Credential)
	a.Credential = credential
	if !cleanCredential(credential) || len(a.Nickname) > 240 {
		return Status{}, nil, fmt.Errorf("Loomy 凭证或昵称格式无效")
	}
	a.AccountUID = strings.TrimSpace(a.AccountUID)
	if a.AccountUID == "" {
		return Status{}, nil, fmt.Errorf("Loomy 账号缺少 userid，请填写 auth-session.json 中的 userid")
	}
	name := a.Nickname
	warnings := []string{}
	seen := map[string]bool{}
	appendWarning := func(text string) {
		if text != "" && !seen[text] {
			seen[text] = true
			warnings = append(warnings, text)
		}
	}
	// 快照验证：登录失效直接拒收，避免保存必然失败的账号。
	snap, err := s.client.snapshot(ctx, credential)
	if err != nil {
		if errors.Is(err, ErrAuthExpired) {
			return Status{}, nil, fmt.Errorf("Loomy 会话已失效：请确认 Loomy 桌面端已登录（必要时退出后重新登录），再点击「重新读取」")
		}
		appendWarning(err.Error())
	} else {
		a.TodayEarned = snap.Status.EarnedPointsToday
		if daily, e := localPetWork(a.AccountUID); e == nil {
			a.PetScore, a.ReachedTier = daily.PetWorkScore, reachedTier(snap, daily.PetWorkScore)
		}
	}
	if name != "" {
		a.Nickname = name
	}
	if a.Nickname == "" {
		a.Nickname = "Loomy 账号"
	}
	accounts := s.snapshot()
	index := -1
	if expected != "" {
		for i, old := range accounts {
			if old.ID == expected {
				index = i
				break
			}
		}
		if index < 0 {
			return Status{}, nil, os.ErrNotExist
		}
	} else {
		for i, old := range accounts {
			if sameIdentity(old, a) {
				index = i
				break
			}
		}
	}
	if index >= 0 {
		old := accounts[index]
		// 更新凭证前校验平台身份：新旧 userid 不同说明新凭证属于另一个账号，
		// 直接覆盖会把 A 账号的记录（ID、结算状态、余额历史）错换成 B。
		if expected != "" && old.AccountUID != "" && a.AccountUID != old.AccountUID {
			return Status{}, nil, fmt.Errorf("新凭证属于另一个 Loomy 账号（userid %s），与原账号不一致；请移除原账号后单独添加", a.AccountUID)
		}
		a.ID = old.ID
		a.Disabled = old.Disabled
		a.Credits, a.DailyBalance, a.BalanceKnown = old.Credits, old.DailyBalance, old.BalanceKnown
		a.BalanceUpdatedAt = old.BalanceUpdatedAt
		a.LastPeriod = old.LastPeriod
		accounts[index] = a
	} else {
		a.ID = accountID(a.AccountUID)
		accounts = append(accounts, a)
	}
	if err := s.refreshBalance(ctx, &a); err != nil {
		appendWarning(err.Error())
	}
	if ctx.Err() != nil {
		return Status{}, nil, ctx.Err()
	}
	// refreshBalance 修改的是局部副本，落盘前必须写回列表，否则保存的仍是
	// 查询前的旧余额。
	if index >= 0 {
		accounts[index] = a
	} else {
		accounts[len(accounts)-1] = a
	}
	if err := s.commit(accounts); err != nil {
		return Status{}, nil, err
	}
	return status(a), warnings, nil
}

// reachedTier 返回 score 已达成的最高档位 id（未达成任何档位返回空）。
func reachedTier(snap Snapshot, score float64) string {
	highest := ""
	for _, t := range snap.Tiers {
		if score >= t.Score {
			highest = t.TierID
		}
	}
	return highest
}

// refreshBalance 刷新积分余额并落到账号。
func (s *Store) refreshBalance(ctx context.Context, a *Account) error {
	value, daily, err := s.client.balance(ctx, a.Credential)
	if err != nil {
		return err
	}
	a.Credits, a.DailyBalance, a.BalanceKnown, a.BalanceUpdatedAt = value, daily, true, time.Now()
	return nil
}

// applyProgress 把服务端快照与本机当日进度写进账号展示字段。
func applyProgress(a *Account, snap Snapshot, daily localDaily) {
	a.TodayEarned = snap.Status.EarnedPointsToday
	a.PetScore = daily.PetWorkScore
	a.ReachedTier = reachedTier(snap, daily.PetWorkScore)
}

// checkin 忠实补领：只申请本机桌面端当日已达成、服务端尚未发放的档位。
// 全部处理完成（无欠账）才落 LastPeriod；服务端拒绝则保留待下轮重试。
func (s *Store) checkin(ctx context.Context, a *Account) error {
	snap, err := s.client.snapshot(ctx, a.Credential)
	if err != nil {
		return err
	}
	if !snap.Enabled {
		return fmt.Errorf("Loomy 宠物打工活动未开启或已结束")
	}
	daily, err := localPetWork(a.AccountUID)
	if err != nil {
		return err
	}
	if daily.BusinessDate != snap.BusinessDate {
		return fmt.Errorf("本机使用记录仍为 %s，今日（%s）尚无已达成档位", daily.BusinessDate, snap.BusinessDate)
	}
	candidates := []tier{}
	for _, t := range snap.Tiers {
		if daily.PetWorkScore >= t.Score && !contains(snap.Status.RewardedTierIDs, t.TierID) {
			candidates = append(candidates, t)
		}
	}
	applyProgress(a, snap, daily)
	if snap.Status.DailyRewardCapped || len(candidates) == 0 {
		// 当下没有欠账：记为已结算；本机分数随使用增长后，下一轮调度仍会复查
		// （RunAll 对 loomy 不按 CheckinDone 跳过）。
		a.LastPeriod = snap.BusinessDate
		return nil
	}
	for _, t := range candidates {
		result, err := s.client.claim(ctx, a.Credential, snap, t.TierID)
		if err != nil {
			return err
		}
		a.TodayEarned = result.Status.EarnedPointsToday
	}
	a.LastPeriod = snap.BusinessDate
	return nil
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func (s *Store) Action(ctx context.Context, id, action string) (Status, string, error) {
	if err := s.lock(ctx); err != nil {
		return Status{}, "", err
	}
	defer s.op.Unlock()
	accounts := s.snapshot()
	for i := range accounts {
		a := &accounts[i]
		if a.ID != id {
			continue
		}
		var err error
		warning := ""
		switch action {
		case "remove":
			return Status{}, "", s.commit(append(accounts[:i], accounts[i+1:]...))
		case "disable":
			a.Disabled = true
		case "revive":
			a.Disabled = false
			a.LastError = ""
		case "checkin", "balance":
			if action == "checkin" && a.Disabled {
				return status(*a), "", fmt.Errorf("该 Loomy 账号已停用")
			}
			if action == "balance" {
				if e := s.refreshBalance(ctx, a); e != nil {
					err = e
				}
				if e := s.refreshProgress(ctx, a); e != nil {
					warning = e.Error()
				}
			}
			if action == "checkin" {
				err = s.checkin(ctx, a)
				if err == nil && s.refreshEnabled.Load() {
					if e := s.refreshBalance(ctx, a); e != nil {
						warning = e.Error()
					}
				}
			}
			a.LastError = warning
			if err != nil {
				a.LastError = err.Error()
			}
		default:
			return Status{}, "", fmt.Errorf("不支持的 Loomy 操作")
		}
		if e := s.commit(accounts); e != nil {
			return Status{}, "", e
		}
		return status(*a), warning, err
	}
	return Status{}, "", os.ErrNotExist
}

// refreshProgress 尽力回读服务端快照与本机进度，仅用于展示字段。
func (s *Store) refreshProgress(ctx context.Context, a *Account) error {
	snap, err := s.client.snapshot(ctx, a.Credential)
	if err != nil {
		return err
	}
	a.TodayEarned = snap.Status.EarnedPointsToday
	if daily, e := localPetWork(a.AccountUID); e == nil {
		a.PetScore, a.ReachedTier = daily.PetWorkScore, reachedTier(snap, daily.PetWorkScore)
	}
	return nil
}

// RunAll 遍历账号执行动作。宠物打工的欠账随当日使用增长而变化，
// 因此 checkin 不按 CheckinDone 跳过（无欠账时本轮是快速无操作）。
func (s *Store) RunAll(ctx context.Context, action string) []string {
	errors := []string{}
	for _, a := range s.List() {
		if ctx.Err() != nil {
			break
		}
		if a.Disabled {
			continue
		}
		_, _, err := s.Action(ctx, a.UID, action)
		if err != nil {
			errors = append(errors, a.UID+": "+err.Error())
			log.Printf("loomy: %s 失败 %s", action, err)
		} else {
			log.Printf("loomy: %s 完成", action)
		}
	}
	return errors
}
