package minimax

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
	AuthKind         string    `json:"auth_kind,omitempty"`
	RefreshToken     string    `json:"refresh_token,omitempty"`
	ExpiresAt        time.Time `json:"expires_at,omitempty"`
	Disabled         bool      `json:"disabled"`
	Credits          float64   `json:"credits"`
	Total            float64   `json:"credits_total"`
	BalanceKnown     bool      `json:"balance_known"`
	BalanceUpdatedAt time.Time `json:"balance_updated_at"`
	LastPeriod       string    `json:"last_period"`
	LastError        string    `json:"last_error,omitempty"`
}

type Status struct {
	UID              string    `json:"uid"`
	AccountUID       string    `json:"account_uid"`
	Nickname         string    `json:"nickname"`
	Provider         string    `json:"provider"`
	Realm            string    `json:"realm"`
	Disabled         bool      `json:"disabled"`
	Credits          float64   `json:"credits"`
	Total            float64   `json:"credits_total"`
	BalanceKnown     bool      `json:"balance_known"`
	BalanceUpdatedAt time.Time `json:"balance_updated_at"`
	CheckinDone      bool      `json:"checkin_done"`
	AuthKind         string    `json:"auth_kind"`
	HasRefresh       bool      `json:"has_refresh"`
	TokenExpiresAt   time.Time `json:"token_expires_at"`
	LastError        string    `json:"last_error,omitempty"`
}

func status(a Account) Status {
	return Status{UID: a.ID, AccountUID: a.AccountUID, Nickname: a.Nickname, Provider: "minimax", Realm: "cn", Disabled: a.Disabled, Credits: a.Credits, Total: a.Total, BalanceKnown: a.BalanceKnown, BalanceUpdatedAt: a.BalanceUpdatedAt, CheckinDone: a.LastPeriod != "" && a.LastPeriod == RewardPeriod(time.Now()), AuthKind: a.AuthKind, HasRefresh: a.RefreshToken != "", TokenExpiresAt: a.ExpiresAt, LastError: a.LastError}
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
		return nil, fmt.Errorf("读取 MiniMax 账号存档失败")
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
		return fmt.Errorf("创建 MiniMax 存档目录失败")
	}
	raw, err := json.MarshalIndent(accounts, "", "  ")
	if err != nil {
		return fmt.Errorf("保存 MiniMax 账号失败")
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".minimax-*.tmp")
	if err != nil {
		return fmt.Errorf("创建 MiniMax 存档失败")
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
		return fmt.Errorf("写入 MiniMax 存档失败")
	}
	if os.Rename(f.Name(), s.path) != nil {
		return fmt.Errorf("替换 MiniMax 存档失败")
	}
	s.mu.Lock()
	s.accounts = append([]Account(nil), accounts...)
	s.mu.Unlock()
	return nil
}

func accountID(uid string) string {
	sum := sha256.Sum256([]byte(uid))
	return "minimax-" + hex.EncodeToString(sum[:12])
}

// sameIdentity 依据账号身份（站点 uid）或同一凭证判断是否为同一账号，
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
func (s *Store) Add(ctx context.Context, a Account, expected string) (Status, []string, error) {
	if err := s.lock(ctx); err != nil {
		return Status{}, nil, err
	}
	defer s.op.Unlock()
	credential := strings.TrimSpace(a.Credential)
	a.Credential = credential
	if !cleanCredential(credential) || len(a.Nickname) > 240 {
		return Status{}, nil, fmt.Errorf("MiniMax 凭证或昵称格式无效")
	}
	if a.AuthKind != AuthAuto && a.AuthKind != AuthCookie && a.AuthKind != AuthBearer {
		return Status{}, nil, fmt.Errorf("MiniMax 凭证类型无效，可选 cookie 或 bearer")
	}
	name := a.Nickname
	uidWarn := ""
	if err := s.client.identify(ctx, &a); err != nil {
		var httpErr *responseError
		// 站点明确拒绝凭证时直接拒收，避免保存一个必然失败的账号。
		if errors.As(err, &httpErr) && (httpErr.code == 401 || httpErr.code == 403) {
			return Status{}, nil, fmt.Errorf("MiniMax 凭证无效或已失效，请重新授权")
		}
		uidWarn = err.Error()
	}
	if name != "" {
		a.Nickname = name
	}
	if a.Nickname == "" {
		a.Nickname = "MiniMax 账号"
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
		// 更新凭证前校验平台身份：站外 uid 都已知且不同，说明新凭证属于另一个
		// 账号，直接覆盖会把 A 账号的记录（ID、签到状态、余额历史）错换成 B。
		if expected != "" && old.AccountUID != "" && a.AccountUID != "" && old.AccountUID != a.AccountUID {
			return Status{}, nil, fmt.Errorf("新凭证属于另一个 MiniMax 账号（uid %s），与原账号不一致；请移除原账号后单独添加", a.AccountUID)
		}
		a.ID = old.ID
		a.Disabled = old.Disabled
		a.Credits, a.Total, a.BalanceKnown = old.Credits, old.Total, old.BalanceKnown
		a.BalanceUpdatedAt = old.BalanceUpdatedAt
		a.LastPeriod = old.LastPeriod
		accounts[index] = a
	} else {
		a.ID = accountID(a.AccountUID)
		if a.AccountUID == "" {
			a.ID = credentialFingerprint(credential)
		}
		accounts = append(accounts, a)
	}
	warnings := []string{}
	seen := map[string]bool{}
	appendWarning := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			warnings = append(warnings, s)
		}
	}
	appendWarning(uidWarn)
	if err := s.client.balance(ctx, &a); err != nil {
		appendWarning(err.Error())
	}
	if err := s.client.checkin(ctx, &a, false); err != nil {
		appendWarning(err.Error())
	}
	if ctx.Err() != nil {
		return Status{}, nil, ctx.Err()
	}
	// balance/checkin 修改的是局部副本，落盘前必须写回列表，否则保存的仍是
	// 查询前的旧余额与签到状态。
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

func credentialFingerprint(credential string) string {
	sum := sha256.Sum256([]byte(credential))
	return "minimax-" + hex.EncodeToString(sum[:12])
}

// fresh 在访问令牌临近过期时用刷新令牌续期，并先持久化轮换结果。
func (s *Store) fresh(ctx context.Context, accounts []Account, i int) error {
	a := &accounts[i]
	if a.RefreshToken == "" {
		return nil
	}
	if !a.ExpiresAt.IsZero() && a.ExpiresAt.After(time.Now().Add(5*time.Minute)) {
		return nil
	}
	grant, err := s.client.RefreshToken(ctx, a.RefreshToken)
	if err != nil {
		return err
	}
	a.Credential = grant.AccessToken
	a.AuthKind = AuthBearer
	a.RefreshToken = grant.RefreshToken
	if t := grant.Expiry(time.Now()); !t.IsZero() {
		a.ExpiresAt = t
	}
	// Persist rotated credentials before any subsequent operation.
	return s.commit(accounts)
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
				return status(*a), "", fmt.Errorf("该 MiniMax 账号已停用")
			}
			// 访问令牌临近过期先续期（余额刷新与签到同口径），避免保留了有效
			// 刷新令牌却在刷新余额时直接报登录失效。
			if e := s.fresh(ctx, accounts, i); e != nil {
				a.LastError = e.Error()
				if e := s.commit(accounts); e != nil {
					return Status{}, "", e
				}
				return status(*a), "", e
			}
			if action == "balance" {
				err = s.client.balance(ctx, a)
				if err == nil {
					if e := s.client.identify(ctx, a); e != nil {
						warning = e.Error()
					}
				}
			}
			if action == "checkin" {
				if !status(*a).CheckinDone {
					err = s.client.checkin(ctx, a, true)
				}
				if err == nil && s.refreshEnabled.Load() {
					if e := s.client.balance(ctx, a); e != nil {
						warning = e.Error()
					}
				}
			}
			a.LastError = warning
			if err != nil {
				a.LastError = err.Error()
			}
		default:
			return Status{}, "", fmt.Errorf("不支持的 MiniMax 操作")
		}
		if e := s.commit(accounts); e != nil {
			return Status{}, "", e
		}
		return status(*a), warning, err
	}
	return Status{}, "", os.ErrNotExist
}
func (s *Store) RunAll(ctx context.Context, action string) []string {
	errors := []string{}
	for _, a := range s.List() {
		if ctx.Err() != nil {
			break
		}
		if a.Disabled || action == "checkin" && a.CheckinDone {
			continue
		}
		_, _, err := s.Action(ctx, a.UID, action)
		if err != nil {
			errors = append(errors, a.UID+": "+err.Error())
			log.Printf("minimax: %s 失败 %s", action, err)
		} else {
			log.Printf("minimax: %s 完成", action)
		}
	}
	return errors
}
