package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type staticSource struct {
	accounts    []Account
	credentials map[string]Credential
}

func (s *staticSource) GatewayAccounts() []Account { return s.accounts }
func (s *staticSource) GatewayCredential(ctx context.Context, id string) (Credential, error) {
	return s.credentials[id], ctx.Err()
}

// Explicit opt-in only. Uses stored tokens without refreshing or modifying login
// state. Example: GATEWAY_LIVE_PROVIDER=loomy go test ./internal/gateway -run TestLive -v
func TestLive(t *testing.T) {
	provider := os.Getenv("GATEWAY_LIVE_PROVIDER")
	if provider == "" {
		t.Skip("live provider probe is opt-in")
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "data", provider+"-accounts.json"))
	if err != nil {
		t.Fatal("account file unavailable")
	}
	var records []map[string]any
	if json.Unmarshal(bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf}), &records) != nil {
		t.Fatal("invalid account file")
	}
	source := &staticSource{credentials: map[string]Credential{}}
	for _, a := range records {
		c := Credential{ID: str(a["id"]), UID: str(a["account_uid"]), Nickname: str(a["nickname"]), Token: str(a["token"]), RefreshToken: str(a["refresh_token"]), MachineID: str(a["machine_id"]), DeviceID: str(a["device_id"]), AuthKind: str(a["auth_kind"])}
		if c.Token == "" {
			c.Token = str(a["credential"])
		}
		if provider == "doubao" {
			c.Token = str(a["cookie"])
		}
		if provider == "antigravity" {
			c.Token = str(a["access_token"])
			c.ProjectID = str(a["project_id"])
		}
		if oauth := obj(a["oauth"]); oauth != nil {
			c.MachineID = str(oauth["machine_id"])
		}
		source.credentials[c.ID] = c
		source.accounts = append(source.accounts, Account{ID: c.ID, Disabled: flag(a["disabled"])})
	}
	router := New(map[string]Source{provider: source})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	models := router.Models(ctx)
	t.Logf("%s catalog: %d chat models", provider, len(models))
	if len(models) == 0 {
		t.Fatal("empty catalog")
	}
	model := os.Getenv("GATEWAY_LIVE_MODEL")
	if model == "" {
		switch provider {
		case "loomy":
			model = "spark-x2.6"
		case "minimax":
			model = "MiniMax-M3"
		case "qoder":
			model = "fast"
		case "trae":
			for _, m := range models {
				t.Logf("available: %s", str(m["id"]))
			}
			model = str(models[0]["id"])[len(provider)+1:]
		}
	}
	req := map[string]any{"model": provider + "/" + model, "messages": []any{map[string]any{"role": "user", "content": "Reply only OK."}}, "max_tokens": 64, "reasoning_effort": "none"}
	if provider == "qoder" {
		delete(req, "reasoning_effort")
	}
	if provider == "trae" {
		delete(req, "reasoning_effort")
	}
	if os.Getenv("GATEWAY_LIVE_TOOLS") == "1" {
		req["messages"] = []any{map[string]any{"role": "user", "content": "Call echo with text OK."}}
		req["max_tokens"] = 256
		req["tools"] = []any{map[string]any{"type": "function", "function": map[string]any{"name": "echo", "description": "Echo text", "parameters": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}}}}}
		req["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": "echo"}}
	}
	body, _ := json.Marshal(req)
	var completion Completion
	frames := 0
	err = router.Chat(ctx, body, "", func(chunk map[string]any) error { frames++; return completion.Add(chunk) }, func(a Attempt) { t.Logf("attempt: status=%d usage_present=%v", a.Status, a.Usage != nil) })
	if err != nil {
		t.Fatal(err)
	}
	if len(arr(completion.Response()["choices"])) == 0 {
		t.Fatal("no completion")
	}
	if os.Getenv("GATEWAY_LIVE_TOOLS") == "1" {
		choice := obj(arr(completion.Response()["choices"])[0])
		if len(arr(obj(choice["message"])["tool_calls"])) != 1 {
			t.Fatal("expected one native tool call")
		}
		t.Log("native tool call received")
	}
	t.Logf("%s chat: %d frames, usage=%v", provider, frames, completion.Usage != nil)
}
