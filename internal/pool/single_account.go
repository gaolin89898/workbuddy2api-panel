package pool

import (
	"sort"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
)

func (p *Pool) singleAccountAllowsLocked(e *entry, now time.Time) bool {
	realm := e.a.Realm()
	if p.strategyForRealmLocked(realm) != SingleAccount {
		return true
	}
	cursor := p.scheduling[realm+"\x1f"]
	return cursor.Strategy == SingleAccount && cursor.UID == e.a.UID && !p.singleAccountRetired(e, now)
}

// singleAccountRetired distinguishes exhaustion/manual withdrawal from temporary
// unavailability. A rate limit, full lease or failed request must never borrow a
// second identity. Unknown balances are tried until the upstream confirms 402.
func (p *Pool) singleAccountRetired(e *entry, now time.Time) bool {
	return e.disabled || e.paused ||
		(e.coolKind == CoolHard && now.Before(e.until)) ||
		(e.creditsKnown && e.credits <= 0) ||
		(e.creditsKnown && p.creditFloor > 0 && e.credits < p.creditFloor)
}

// pickSingleAccountLocked uses one persistent cursor per realm, shared by every
// model and conversation. Caller holds p.mu; Acquire rechecks this cursor under
// the same lock so a delayed pick cannot acquire the previous identity.
func (p *Pool) pickSingleAccountLocked(tried map[string]bool, model, realm string, now time.Time) *auth.Auth {
	current := p.singleAccountCandidateLocked(model, realm, now)
	if current == nil {
		return nil
	}
	key := realm + "\x1f"
	cursor := scheduleCursor{Strategy: SingleAccount, UID: current.a.UID}
	if p.scheduling[key] != cursor {
		p.scheduling[key] = cursor
		p.dirty.Store(true)
	}
	if tried[current.a.UID] || !current.healthyForModel(now, model) ||
		p.floorBlockedForRealmModel(current, model, realm, now) || p.inFlightFull(current) {
		return nil
	}
	current.lastUsed = now
	p.pickSeq++
	current.usedSeq = p.pickSeq
	return current.a
}

// Read-only candidate selection also keeps availability probes consistent with
// forwarding, without advancing the cursor or sending any upstream requests.
func (p *Pool) singleAccountCandidateLocked(model, realm string, now time.Time) *entry {
	key := realm + "\x1f"
	cursor := p.scheduling[key]
	current := p.byUID[cursor.UID]
	if cursor.Strategy != SingleAccount || (current != nil && current.a.Realm() != realm) {
		current = nil
		cursor = scheduleCursor{Strategy: SingleAccount}
	}
	// Drain outstanding work before activating a different account, including
	// requests started before a hot strategy change.
	for _, e := range p.byUID {
		if e.a.Realm() == realm && e != current && e.inFlight.Load() > 0 {
			return nil
		}
	}
	if current == nil || p.singleAccountRetired(current, now) {
		if current != nil && current.inFlight.Load() > 0 {
			return nil
		}
		order := p.accountOrder.Domestic
		if realm == "global" {
			order = p.accountOrder.International
		}
		rank := map[string]int{}
		for _, uid := range order {
			if _, exists := rank[uid]; !exists {
				rank[uid] = len(rank)
			}
		}
		less := func(a, b string) bool {
			ar, aok := rank[a]
			br, bok := rank[b]
			if aok != bok {
				return aok
			}
			if aok {
				return ar < br
			}
			return a < b
		}
		var candidates []*entry
		for _, e := range p.byUID {
			if e.a.Realm() == realm && !p.singleAccountRetired(e, now) && e.healthyForModel(now, model) {
				candidates = append(candidates, e)
			}
		}
		if len(candidates) == 0 {
			return nil
		}
		sort.Slice(candidates, func(i, j int) bool { return less(candidates[i].a.UID, candidates[j].a.UID) })
		current = candidates[0]
		if cursor.UID != "" {
			for _, e := range candidates {
				if less(cursor.UID, e.a.UID) {
					current = e
					break
				}
			}
		}
	}
	return current
}
