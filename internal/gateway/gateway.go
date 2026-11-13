// Package gateway adapts native desktop providers to OpenAI Chat Completions.
// Credentials stay inside the process; catalog and errors never contain them.
package gateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type Account struct {
	ID, Nickname string
	Disabled     bool
}
type Credential struct{ ID, Nickname, UID, Token, RefreshToken, MachineID, DeviceID, AuthKind, ProjectID string }
type Source interface {
	GatewayAccounts() []Account
	GatewayCredential(context.Context, string) (Credential, error)
}
type Model struct {
	ID     string
	Fields map[string]any
	wire   map[string]any
}

func (m Model) Entry(provider string) map[string]any {
	out := map[string]any{"id": provider + "/" + m.ID, "object": "model", "owned_by": provider, "provider": provider}
	for k, v := range m.Fields {
		out[k] = v
	}
	return out
}

type catalog struct {
	token  [32]byte
	until  time.Time
	models []Model
}
type accountState struct {
	inFlight int
	until    time.Time
	last     uint64
}
type binding struct {
	account string
	until   time.Time
}
type Router struct {
	Sources map[string]Source
	HTTP    *http.Client
	// Bases are fixed production endpoints by default, replaceable by tests.
	Bases       map[string]string
	MaxInFlight int
	Cooldown    time.Duration
	mu          sync.Mutex
	cache       map[string]catalog
	flights     map[string]chan struct{}
	states      map[string]*accountState
	sticky      map[string]binding
	signatures  map[string]string
	seq         uint64
}

func (r *Router) Configure(maxInFlight int, cooldown time.Duration) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.MaxInFlight = maxInFlight
	r.Cooldown = cooldown
}

// Availability reports local scheduling state without contacting providers.
func (r *Router) Availability() map[string]map[string]int {
	out := map[string]map[string]int{}
	if r == nil {
		return out
	}
	for p, s := range r.Sources {
		if s == nil {
			continue
		}
		accounts := s.GatewayAccounts()
		counts := map[string]int{"total": len(accounts), "healthy": 0, "disabled": 0, "cooling": 0, "in_flight_full": 0}
		r.mu.Lock()
		for _, a := range accounts {
			st := r.states[p+"/"+a.ID]
			switch {
			case a.Disabled:
				counts["disabled"]++
			case st != nil && time.Now().Before(st.until):
				counts["cooling"]++
			case st != nil && r.MaxInFlight > 0 && st.inFlight >= r.MaxInFlight:
				counts["in_flight_full"]++
			default:
				counts["healthy"]++
			}
		}
		r.mu.Unlock()
		out[p] = counts
	}
	return out
}

func New(sources map[string]Source) *Router {
	return &Router{Sources: sources, HTTP: &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, Bases: map[string]string{
		"loomy": "https://loomyad.xunfei.cn/api/v1", "minimax": "https://agent.minimax.cn/mavis/api/v1/llm/v1", "qoder": "https://gateway.qoder.com.cn", "trae": "https://trae-api-cn.mchost.guru", "antigravity": "https://daily-cloudcode-pa.googleapis.com", "doubao": "https://www.doubao.com",
	}, MaxInFlight: 2, Cooldown: time.Minute, cache: map[string]catalog{}, flights: map[string]chan struct{}{}, states: map[string]*accountState{}, sticky: map[string]binding{}, signatures: map[string]string{}}
}
func Split(model string) (provider, bare string, ok bool) {
	provider, bare, ok = strings.Cut(model, "/")
	switch provider {
	case "loomy", "minimax", "qoder", "trae", "antigravity", "doubao":
		return provider, bare, ok
	default:
		return "", "", false
	}
}

type Error struct {
	Status   int
	Message  string
	Rejected bool
}

func (e *Error) Error() string                 { return e.Message }
func failure(status int, message string) error { return &Error{Status: status, Message: message} }
func rejection(status int, message string) error {
	return &Error{Status: status, Message: message, Rejected: true}
}
func Status(err error) int {
	var e *Error
	if errors.As(err, &e) {
		if e.Status >= 400 && e.Status <= 599 {
			return e.Status
		}
	}
	return http.StatusBadGateway
}
func id() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }
func arr(v any) []any          { a, _ := v.([]any); return a }
func str(v any) string         { s, _ := v.(string); return s }
func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}
func flag(v any) bool { b, _ := v.(bool); return b }
func clone(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (r *Router) catalog(ctx context.Context, p string, c Credential) ([]Model, error) {
	key := p + "/" + c.ID
	token := sha256.Sum256([]byte(c.Token + "\x00" + c.ProjectID))
	for {
		r.mu.Lock()
		hit, ok := r.cache[key]
		if ok && hit.token == token && time.Now().Before(hit.until) {
			r.mu.Unlock()
			return hit.models, nil
		}
		if wait, ok := r.flights[key]; ok {
			r.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-wait:
				continue
			}
		}
		r.flights[key] = make(chan struct{})
		r.mu.Unlock()
		break
	}
	defer func() { r.mu.Lock(); close(r.flights[key]); delete(r.flights, key); r.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	models, err := r.fetchModels(ctx, p, c)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.cache[key] = catalog{token: token, until: time.Now().Add(5 * time.Minute), models: models}
	r.mu.Unlock()
	return models, nil
}

// Models unions enabled accounts' catalogs; one provider failure cannot hide others.
func (r *Router) Models(ctx context.Context) []map[string]any {
	return r.ModelsForSupplier(ctx, "auto")
}

// ModelsForSupplier never reads credentials or catalogs of other applications.
func (r *Router) ModelsForSupplier(ctx context.Context, supplier string) []map[string]any {
	if r == nil {
		return nil
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	all := map[string]map[string]any{}
	for p, s := range r.Sources {
		if s == nil || !SupplierAllowsModel(supplier, p+"/") {
			continue
		}
		wg.Add(1)
		go func(p string, s Source) {
			defer wg.Done()
			for _, a := range s.GatewayAccounts() {
				if a.Disabled || ctx.Err() != nil {
					continue
				}
				c, err := s.GatewayCredential(ctx, a.ID)
				if err != nil {
					continue
				}
				ms, err := r.catalog(ctx, p, c)
				if err != nil {
					continue
				}
				mu.Lock()
				for _, m := range ms {
					all[p+"/"+m.ID] = m.Entry(p)
				}
				mu.Unlock()
			}
		}(p, s)
	}
	wg.Wait()
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, all[k])
	}
	return out
}

type Attempt struct {
	Provider, Account, Nickname, Model string
	Status                             int
	Started                            time.Time
	TTFB                               time.Duration
	Usage                              map[string]any
	Err                                error
}
type Emit func(map[string]any) error

func (r *Router) pick(p, session string, accounts []Account, tried map[string]bool) (Account, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	best := -1
	var bs *accountState
	preferred := r.sticky[p+"/"+session]
	max := r.MaxInFlight
	for i, a := range accounts {
		if a.Disabled || tried[a.ID] {
			continue
		}
		key := p + "/" + a.ID
		st := r.states[key]
		if st == nil {
			st = &accountState{}
			r.states[key] = st
		}
		if (max > 0 && st.inFlight >= max) || now.Before(st.until) {
			continue
		}
		if best < 0 || st.inFlight < bs.inFlight || (st.inFlight == bs.inFlight && st.last < bs.last) {
			best = i
			bs = st
		}
		if session != "" && preferred.account == a.ID && now.Before(preferred.until) {
			best = i
			bs = st
			break
		}
	}
	if best < 0 {
		return Account{}, false
	}
	r.seq++
	bs.inFlight++
	bs.last = r.seq
	return accounts[best], true
}
func (r *Router) release(p, session string, a Account, status int, success bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.states[p+"/"+a.ID]
	st.inFlight--
	if status == 401 || status == 403 || status == 429 || status >= 500 {
		d := r.Cooldown
		if d <= 0 {
			d = time.Minute
		}
		st.until = time.Now().Add(d)
	}
	if success && session != "" {
		if len(r.sticky) >= 2048 {
			for k, v := range r.sticky {
				if time.Now().After(v.until) {
					delete(r.sticky, k)
				}
			}
			if len(r.sticky) >= 2048 {
				for k := range r.sticky {
					delete(r.sticky, k)
					break
				}
			}
		}
		r.sticky[p+"/"+session] = binding{a.ID, time.Now().Add(30 * time.Minute)}
	}
}

// Chat always consumes the native stream. Retry only explicit rejections before
// output; disconnects and ambiguous transport failures must never replay a chat.
func (r *Router) Chat(ctx context.Context, body []byte, session string, emit Emit, observe func(Attempt)) error {
	var request map[string]any
	if json.Unmarshal(body, &request) != nil || request == nil {
		return failure(400, "invalid JSON request")
	}
	p, bare, ok := Split(str(request["model"]))
	if !ok || bare == "" {
		return failure(400, "invalid provider/model")
	}
	if len(arr(request["messages"])) == 0 {
		return failure(400, "messages must be a non-empty array")
	}
	if n, ok := request["n"]; ok && num(n) != 1 {
		return failure(400, "provider supports n=1 only")
	}
	source := r.Sources[p]
	if source == nil {
		return failure(503, p+": provider is unavailable")
	}
	request["model"] = bare
	tried := map[string]bool{}
	var last error
	for count := 0; count < 3; count++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		a, ok := r.pick(p, session, source.GatewayAccounts(), tried)
		if !ok {
			break
		}
		tried[a.ID] = true
		attempt := Attempt{Provider: p, Account: a.ID, Nickname: a.Nickname, Model: p + "/" + bare, Started: time.Now()}
		c, err := source.GatewayCredential(ctx, a.ID)
		if err != nil {
			err = rejection(503, p+": account authorization unavailable")
		}
		var selected Model
		if err == nil {
			var ms []Model
			ms, err = r.catalog(ctx, p, c)
			if err != nil && ctx.Err() == nil {
				err = rejection(503, p+": model catalog unavailable")
			}
			if err == nil {
				found := false
				for _, m := range ms {
					if m.ID == bare {
						selected = m
						found = true
						break
					}
				}
				if !found {
					err = rejection(404, p+": model is unavailable for this account")
				}
			}
		}
		emitted := false
		if err == nil {
			upstreamSession := ""
			if session != "" {
				sum := sha256.Sum256([]byte(p + "/" + c.ID + "/" + session))
				s := hex.EncodeToString(sum[:16])
				upstreamSession = s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
			}
			err = r.chat(ctx, p, c, selected, request, upstreamSession, func(chunk map[string]any) error {
				chunk["model"] = p + "/" + bare
				if u := obj(chunk["usage"]); u != nil {
					attempt.Usage = u
				}
				if attempt.TTFB == 0 {
					attempt.TTFB = time.Since(attempt.Started)
				}
				emitted = true
				return emit(chunk)
			})
		}
		attempt.Err = err
		attempt.Status = 200
		if err != nil {
			attempt.Status = Status(err)
		}
		r.release(p, session, a, attempt.Status, err == nil)
		if observe != nil {
			observe(attempt)
		}
		if err == nil {
			return nil
		}
		last = err
		if emitted || ctx.Err() != nil {
			return err
		}
		var e *Error
		if !errors.As(err, &e) || !e.Rejected || (e.Status != 401 && e.Status != 403 && e.Status != 404 && e.Status != 429 && e.Status < 500) {
			return err
		}
	}
	if last != nil {
		return last
	}
	return failure(503, fmt.Sprintf("%s: no available account (disabled, cooling down or busy)", p))
}
