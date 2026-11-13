package panel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/doubao"
)

func (p *Panel) doubaoRoutes() {
	p.webLoginRoutes("doubao")
	p.mux.HandleFunc("POST /panel/api/doubao/accounts", p.withAuth(p.doubaoAdd))
	p.mux.HandleFunc("POST /panel/api/doubao/accounts/{uid}/{action}", p.withAuth(p.doubaoAction))
	p.mux.HandleFunc("POST /panel/api/doubao/balance_all", p.withAuth(p.doubaoRefreshAll))
}
func (p *Panel) doubaoAccounts() []doubao.Status {
	if p.cfg.Doubao == nil {
		return []doubao.Status{}
	}
	return p.cfg.Doubao.List()
}
func (p *Panel) doubaoReady(w http.ResponseWriter) bool {
	if p.cfg.Doubao == nil {
		writeErr(w, 501, "豆包账号管理未启用")
		return false
	}
	return true
}
func (p *Panel) doubaoAdd(w http.ResponseWriter, r *http.Request) {
	if !p.doubaoReady(w) {
		return
	}
	var input struct {
		UID      string `json:"uid"`
		Nickname string `json:"nickname"`
		Cookie   string `json:"cookie"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if dec.Decode(&input) != nil || dec.Decode(new(any)) != io.EOF {
		writeErr(w, 400, "豆包表单格式无效")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	a, err := p.cfg.Doubao.Add(ctx, doubao.Account{Nickname: input.Nickname, Cookie: input.Cookie}, input.UID)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	log.Printf("doubao: 账号已添加或更新")
	writeJSON(w, 200, map[string]any{"ok": true, "account": a})
}
func (p *Panel) doubaoAction(w http.ResponseWriter, r *http.Request) {
	if !p.doubaoReady(w) {
		return
	}
	action := r.PathValue("action")
	if action != "remove" && action != "disable" && action != "revive" && action != "balance" {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	a, err := p.cfg.Doubao.Action(ctx, r.PathValue("uid"), action)
	if errors.Is(err, os.ErrNotExist) {
		writeErr(w, 404, "豆包账号不存在")
		return
	}
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	log.Printf("doubao: %s 完成", action)
	writeJSON(w, 200, map[string]any{"ok": true, "account": a})
}
func (p *Panel) doubaoRefreshAll(w http.ResponseWriter, r *http.Request) {
	if !p.doubaoReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	failures := []string{}
	for _, a := range p.cfg.Doubao.List() {
		if a.Disabled {
			continue
		}
		if _, err := p.cfg.Doubao.Action(ctx, a.UID, "balance"); err != nil {
			failures = append(failures, err.Error())
		}
		if ctx.Err() != nil {
			break
		}
	}
	writeJSON(w, 200, map[string]any{"ok": len(failures) == 0, "accounts": p.doubaoAccounts(), "errors": failures})
}
