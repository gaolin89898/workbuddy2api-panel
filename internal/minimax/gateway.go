package minimax

import (
	"context"
	"fmt"
	"strings"

	"github.com/linguo2625469/workbuddy2api-panel/internal/gateway"
)

func (s *Store) GatewayAccounts() []gateway.Account {
	out := []gateway.Account{}
	for _, a := range s.List() {
		out = append(out, gateway.Account{ID: a.UID, Nickname: a.Nickname, Disabled: a.Disabled})
	}
	return out
}

// GatewayCredential refreshes under the store's mutation lock, then releases it
// before inference. This method is never registered as a panel HTTP endpoint.
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
			return gateway.Credential{}, fmt.Errorf("minimax: account disabled")
		}
		if err := s.fresh(ctx, accounts, i); err != nil {
			return gateway.Credential{}, err
		}
		kind := a.AuthKind
		if kind == "" {
			kind = detectAuthKind(a.Credential)
		}
		if kind != AuthBearer {
			return gateway.Credential{}, fmt.Errorf("minimax: gateway requires OAuth bearer login")
		}
		c := gateway.Credential{ID: a.ID, Nickname: a.Nickname, UID: a.AccountUID, Token: a.Credential, AuthKind: a.AuthKind}
		if len(c.Token) > 7 && strings.EqualFold(c.Token[:7], "bearer ") {
			c.Token = strings.TrimSpace(c.Token[7:])
		}

		if c.Token == "" {
			return gateway.Credential{}, fmt.Errorf("minimax: missing credential")
		}
		return c, nil
	}
	return gateway.Credential{}, fmt.Errorf("minimax: account not found")
}
