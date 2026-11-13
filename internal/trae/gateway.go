package trae

import (
	"context"
	"fmt"
	"time"

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
	for !s.op.TryLock() {
		select {
		case <-ctx.Done():
			return gateway.Credential{}, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	if ctx.Err() != nil {
		s.op.Unlock()
		return gateway.Credential{}, ctx.Err()
	}
	defer s.op.Unlock()
	accounts := s.snapshot()
	for i := range accounts {
		a := &accounts[i]
		if a.ID != id {
			continue
		}
		if a.Disabled {
			return gateway.Credential{}, fmt.Errorf("trae: account disabled")
		}
		if a.OAuth != nil {
			copyIdentity := *a.OAuth
			a.OAuth = &copyIdentity
		}
		beforeToken := a.Token
		var beforeIdentity OAuthIdentity
		if a.OAuth != nil {
			beforeIdentity = *a.OAuth
		}
		if err := s.client.authorize(ctx, a); err != nil {
			return gateway.Credential{}, err
		}
		if a.Token != beforeToken || (a.OAuth != nil && beforeIdentity != *a.OAuth) {
			if err := s.commit(accounts); err != nil {
				return gateway.Credential{}, err
			}
		}
		c := gateway.Credential{ID: a.ID, Nickname: a.Nickname, UID: a.AccountUID, Token: a.Token, DeviceID: a.DeviceID}
		if a.OAuth != nil {
			c.MachineID = a.OAuth.MachineID
		}
		if c.Token == "" {
			return gateway.Credential{}, fmt.Errorf("trae: missing credential")
		}
		return c, nil
	}
	return gateway.Credential{}, fmt.Errorf("trae: account not found")
}
