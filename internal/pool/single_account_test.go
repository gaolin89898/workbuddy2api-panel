package pool

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
)

func TestSingleAccountSharedAcrossModelsAndCosts(t *testing.T) {
	p := orderedPool(t, SingleAccount)
	p.SetCostExploreInterval(time.Nanosecond)
	p.SetCredits("a", 100, 100)
	p.NoteModelCost("a", "m", 1, 1000)
	p.NoteModelCost("b", "m", 0, 1000)
	for _, model := range []string{"m", "other", "", "m"} {
		wantPick(t, p, nil, model, "cn", "a")
	}
	if p.costExploreEvents != 0 {
		t.Fatal("single-account routing must not explore another identity")
	}
	if p.PickByUIDForModel("b", "m") != nil || p.Acquire("b") {
		t.Fatal("direct or sticky paths borrowed another identity")
	}
	if a := p.PickExcluding(nil); a == nil || a.UID != "a" {
		t.Fatal("search/legacy routing did not share the domestic account")
	}
	wantPick(t, p, nil, "m", "global", "x")
}

func TestSingleAccountTemporaryFailuresAndCapacityNeverBorrow(t *testing.T) {
	p := orderedPool(t, SingleAccount)
	wantPick(t, p, nil, "m", "cn", "a")
	p.SetMaxInFlight(1)
	if !p.Acquire("a") {
		t.Fatal("acquire active")
	}
	wantPick(t, p, nil, "m", "cn", "")
	if p.ServableForRealm("cn") || p.ServableForModelRealm("m", "cn") != 0 {
		t.Fatal("full active account reported spare accounts as available")
	}
	p.Release("a")
	wantPick(t, p, map[string]bool{"a": true}, "m", "cn", "")
	p.CooldownSoftForModel("a", time.Hour, time.Now().Add(time.Hour), "m", "6004")
	wantPick(t, p, nil, "m", "cn", "")
	wantPick(t, p, nil, "other", "cn", "a")
	p.Cooldown("a", CoolSoft, time.Hour, "429")
	wantPick(t, p, nil, "other", "cn", "")
	p.Revive("a")
	wantPick(t, p, nil, "m", "cn", "a")
}

func TestSingleAccountExhaustionDrainsAndRejectsStaleAcquire(t *testing.T) {
	p := orderedPool(t, SingleAccount)
	p.SetCredits("a", 1, 1)
	wantPick(t, p, nil, "m", "cn", "a")
	if !p.Acquire("a") {
		t.Fatal("acquire active")
	}
	p.NoteModelCost("a", "m", 1, 1000)
	wantPick(t, p, nil, "m", "cn", "")
	p.Release("a")
	wantPick(t, p, nil, "other", "cn", "b")
	p.SetCredits("a", 100, 100) // refill cannot steal the active account
	if p.Acquire("a") {
		t.Fatal("delayed acquire reactivated the previous identity")
	}
	wantPick(t, p, nil, "m", "cn", "b")
	p.Cooldown("b", CoolHard, time.Hour, "402")
	wantPick(t, p, map[string]bool{"b": true}, "m", "cn", "c")
}

func TestSingleAccountUnknownAndConfirmedZeroBalances(t *testing.T) {
	p := orderedPool(t, SingleAccount)
	p.SetCredits("a", 0, 0)
	wantPick(t, p, nil, "m", "cn", "b") // b has not fetched a balance
	p.SetCredits("b", 0, 0)
	wantPick(t, p, nil, "m", "cn", "c")
	p.SetCreditFloor(10)
	p.SetCredits("c", 5, 100)
	wantPick(t, p, nil, "m", "cn", "d")
	for _, uid := range []string{"d", "e", "f", "g"} {
		p.SetCredits(uid, 0, 100)
	}
	wantPick(t, p, nil, "m", "cn", "")
}

func TestSingleAccountConcurrentRequestsShareIdentity(t *testing.T) {
	p := orderedPool(t, SingleAccount)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a := p.PickExcludingForRealm(nil, "m", "cn")
			if a == nil || a.UID != "a" {
				t.Error("concurrent pick escaped active account")
				return
			}
			if p.Acquire(a.UID) {
				p.Release(a.UID)
			}
		}()
	}
	wg.Wait()
}

func TestSingleAccountHotEnableWaitsForPreviousTraffic(t *testing.T) {
	p := orderedPool(t, RoundRobin)
	if !p.Acquire("b") {
		t.Fatal("acquire old traffic")
	}
	p.SetScheduling(RegionStrategies{SingleAccount, SingleAccount}, RegionAccountOrder{})
	wantPick(t, p, nil, "m", "cn", "")
	p.Release("b")
	wantPick(t, p, nil, "m", "cn", "a")
}

func TestSingleAccountPersistenceAndManualWithdrawal(t *testing.T) {
	fp := filepath.Join(t.TempDir(), "state.json")
	p := New(fp)
	accounts := []*auth.Auth{{UID: "a"}, {UID: "b"}, {UID: "c"}}
	p.SyncToDir(accounts)
	strategies := RegionStrategies{SingleAccount, SingleAccount}
	p.SetScheduling(strategies, RegionAccountOrder{Domestic: []string{"c", "b", "a"}})
	wantPick(t, p, nil, "m", "cn", "c")
	p.SetCredits("c", 0, 0)
	wantPick(t, p, nil, "other", "cn", "b")
	p.Close()
	q := New(fp)
	defer q.Close()
	q.SyncToDir(accounts)
	q.SetScheduling(strategies, RegionAccountOrder{Domestic: []string{"c", "b", "a"}})
	wantPick(t, q, nil, "m", "cn", "b")
	if !q.byUID["c"].creditsKnown {
		t.Fatal("confirmed zero balance was lost on restart")
	}
	q.Pause("b")
	wantPick(t, q, nil, "m", "cn", "a")
	q.Remove("a")
	wantPick(t, q, nil, "m", "cn", "")
}
