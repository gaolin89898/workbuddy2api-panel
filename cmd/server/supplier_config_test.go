package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/livecfg"
	"github.com/linguo2625469/workbuddy2api-panel/internal/pool"
	"github.com/linguo2625469/workbuddy2api-panel/internal/scheduler"
	"github.com/linguo2625469/workbuddy2api-panel/internal/upstream"
)

func TestSupplierConfigDefaultPersistenceAndHotApply(t *testing.T) {
	for _, raw := range []string{`{}`, `{"gateway":{"supplier":""}}`} {
		cfg, err := ParseConfig([]byte(raw))
		if err != nil || cfg.Gateway.Supplier != "auto" {
			t.Fatalf("automatic default missing: cfg=%+v err=%v", cfg, err)
		}
	}
	path := filepath.Join(t.TempDir(), "config.json")
	initial, err := ParseConfig([]byte(`{"pool":{"max_in_flight":7}}`))
	if err != nil {
		t.Fatal(err)
	}
	initialJSON, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, initialJSON, 0600); err != nil {
		t.Fatal(err)
	}
	live := livecfg.New(livecfg.Snapshot{})
	p := pool.New("")
	up, sch := upstream.New(), scheduler.New(scheduler.Config{})
	for _, selected := range []string{"loomy", "workbuddy-global", "auto"} {
		restart, err := saveConfig([]byte(`{"gateway":{"supplier":"`+selected+`"}}`), path, live, p, up, sch)
		if err != nil || live.Load().GatewaySupplier != selected {
			t.Fatalf("selection not hot applied: %s restart=%v err=%v", selected, restart, err)
		}
		for _, key := range restart {
			if strings.HasPrefix(key, "gateway") {
				t.Fatalf("supplier unexpectedly requires restart: %s", key)
			}
		}
		raw, _ := os.ReadFile(path)
		cfg, err := ParseConfig(raw)
		if err != nil || cfg.Gateway.Supplier != selected || cfg.Pool.MaxInFlight != 7 {
			t.Fatalf("selection was not saved or unrelated config changed: %s err=%v", raw, err)
		}
	}
	before, _ := os.ReadFile(path)
	if _, err := saveConfig([]byte(`{"gateway":{"supplier":"unknown"}}`), path, live, p, up, sch); err == nil {
		t.Fatal("invalid supplier accepted")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) || live.Load().GatewaySupplier != "auto" {
		t.Fatal("invalid supplier changed persisted or runtime configuration")
	}
}
