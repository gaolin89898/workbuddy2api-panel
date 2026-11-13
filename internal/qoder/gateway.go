package qoder

import (
	"context"
	"fmt"

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
			return gateway.Credential{}, fmt.Errorf("qoder: account disabled")
		}
		if err := s.fresh(ctx, accounts, i); err != nil {
			return gateway.Credential{}, err
		}
		c := gateway.Credential{ID: a.ID, Nickname: a.Nickname, UID: a.AccountUID, Token: a.Token, RefreshToken: a.RefreshToken, MachineID: a.MachineID}

		if c.Token == "" {
			return gateway.Credential{}, fmt.Errorf("qoder: missing credential")
		}
		return c, nil
	}
	return gateway.Credential{}, fmt.Errorf("qoder: account not found")
}
