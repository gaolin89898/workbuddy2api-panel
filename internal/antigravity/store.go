package antigravity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/gateway"
)

type Account struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	Nickname     string    `json:"nickname"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	ProjectID    string    `json:"project_id"`
	Tier         string    `json:"tier"`
	Disabled     bool      `json:"disabled"`
}

type Status struct {
	UID              string    `json:"uid"`
	AccountUID       string    `json:"account_uid"`
	Nickname         string    `json:"nickname"`
	Provider         string    `json:"provider"`
	Realm            string    `json:"realm"`
	ProjectID        string    `json:"project_id"`
	Tier             string    `json:"tier"`
	Disabled         bool      `json:"disabled"`
	HasRefresh       bool      `json:"has_refresh"`
	CheckinSupported bool      `json:"checkin_supported"`
	BalanceKnown     bool      `json:"balance_known"`
	ExpiresAt        time.Time `json:"expires_at"`
}

func status(a Account) Status {
	return Status{UID: a.ID, AccountUID: a.Email, Nickname: a.Nickname, Provider: "antigravity", Realm: "global", ProjectID: a.ProjectID, Tier: a.Tier, Disabled: a.Disabled, HasRefresh: a.RefreshToken != "", ExpiresAt: a.ExpiresAt}
}

type Store struct {
	mu       sync.RWMutex
	op       sync.Mutex
	path     string
	accounts []Account
	client   *Client
}

func Open(path string, client *Client) (*Store, error) {
	if client == nil {
		client = NewClient()
	}
	s := &Store{path: path, accounts: []Account{}, client: client}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil || json.Unmarshal(raw, &s.accounts) != nil {
		return nil, fmt.Errorf("读取 Antigravity 账号存档失败")
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
		return fmt.Errorf("创建 Antigravity 存档目录失败")
	}
	raw, err := json.MarshalIndent(accounts, "", "  ")
	if err != nil {
		return fmt.Errorf("编码 Antigravity 存档失败")
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".antigravity-*.tmp")
	if err != nil {
		return fmt.Errorf("创建 Antigravity 存档失败")
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
		return fmt.Errorf("写入 Antigravity 存档失败")
	}
	if os.Rename(f.Name(), s.path) != nil {
		return fmt.Errorf("替换 Antigravity 存档失败")
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
	a.AccessToken = strings.TrimSpace(a.AccessToken)
	a.RefreshToken = strings.TrimSpace(a.RefreshToken)
	a.Email = strings.TrimSpace(a.Email)
	a.ProjectID = strings.TrimSpace(a.ProjectID)
	a.Nickname = strings.TrimSpace(a.Nickname)
	if a.AccessToken == "" && a.RefreshToken == "" || a.AccessToken != "" && !validToken(a.AccessToken) || a.RefreshToken != "" && !validToken(a.RefreshToken) || len(a.Nickname) > 240 || len(a.Email) > 320 || len(a.ProjectID) > 256 {
		return Status{}, fmt.Errorf("Antigravity 凭证格式无效")
	}
	if err := s.client.Inspect(ctx, &a); err != nil {
		return Status{}, err
	}
	accounts := s.snapshot()
	index := -1
	for i, old := range accounts {
		if expected != "" && old.ID == expected || expected == "" && strings.EqualFold(old.Email, a.Email) {
			index = i
			break
		}
	}
	if expected != "" && index < 0 {
		return Status{}, os.ErrNotExist
	}
	if index >= 0 {
		old := accounts[index]
		if !strings.EqualFold(old.Email, a.Email) {
			return Status{}, fmt.Errorf("新凭证属于另一个 Antigravity 账号，请单独添加")
		}
		a.ID = old.ID
		a.Disabled = old.Disabled
		if a.Nickname == "" {
			a.Nickname = old.Nickname
		}
		if a.RefreshToken == "" {
			a.RefreshToken = old.RefreshToken
		}
	} else {
		hash := sha256.Sum256([]byte(strings.ToLower(a.Email)))
		a.ID = "antigravity-" + hex.EncodeToString(hash[:12])
	}
	if a.Nickname == "" {
		a.Nickname = a.Email
	}
	if index >= 0 {
		accounts[index] = a
	} else {
		accounts = append(accounts, a)
	}
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	if err := s.commit(accounts); err != nil {
		return Status{}, err
	}
	return status(a), nil
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
		switch action {
		case "remove":
			accounts = append(accounts[:i], accounts[i+1:]...)
		case "disable":
			a.Disabled = true
		case "revive":
			a.Disabled = false
		case "balance":
			if err := s.client.Inspect(ctx, a); err != nil {
				return Status{}, err
			}
		default:
			return Status{}, fmt.Errorf("不支持的 Antigravity 账号操作")
		}
		if action != "remove" {
			result = status(*a)
		}
		if err := s.commit(accounts); err != nil {
			return Status{}, err
		}
		return result, nil
	}
	return Status{}, os.ErrNotExist
}

func (s *Store) GatewayAccounts() []gateway.Account {
	out := []gateway.Account{}
	for _, a := range s.List() {
		out = append(out, gateway.Account{ID: a.UID, Nickname: a.Nickname, Disabled: a.Disabled})
	}
	return out
}
func (s *Store) GatewayCredential(ctx context.Context, id string) (gateway.Credential, error) {
	if err := s.lock(ctx); err != nil {
		return gateway.Credential{}, err
	}
	defer s.op.Unlock()
	accounts := s.snapshot()
	for i := range accounts {
		a := &accounts[i]
		if a.ID != id {
			continue
		}
		if a.Disabled {
			return gateway.Credential{}, fmt.Errorf("Antigravity 账号已停用")
		}
		before := *a
		if err := s.client.Refresh(ctx, a); err != nil {
			return gateway.Credential{}, err
		}
		if a.ProjectID == "" {
			if err := s.client.Inspect(ctx, a); err != nil {
				return gateway.Credential{}, err
			}
		}
		if *a != before {
			if err := s.commit(accounts); err != nil {
				return gateway.Credential{}, err
			}
		}
		return gateway.Credential{ID: a.ID, Nickname: a.Nickname, UID: a.Email, Token: a.AccessToken, ProjectID: a.ProjectID}, nil
	}
	return gateway.Credential{}, os.ErrNotExist
}
