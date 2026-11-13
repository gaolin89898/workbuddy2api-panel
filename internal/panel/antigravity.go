package panel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/antigravity"
)

func (p *Panel) antigravityRoutes() {
	p.webLoginRoutes("antigravity")
	p.mux.HandleFunc("POST /panel/api/antigravity/accounts", p.withAuth(p.antigravityAdd))
	p.mux.HandleFunc("POST /panel/api/antigravity/import", p.withAuth(p.antigravityImport))
	p.mux.HandleFunc("POST /panel/api/antigravity/accounts/{uid}/{action}", p.withAuth(p.antigravityAction))
	p.mux.HandleFunc("POST /panel/api/antigravity/balance_all", p.withAuth(p.antigravityRefreshAll))
}
func (p *Panel) antigravityAccounts() []antigravity.Status {
	if p.cfg.Antigravity == nil {
		return []antigravity.Status{}
	}
	return p.cfg.Antigravity.List()
}
func (p *Panel) antigravityReady(w http.ResponseWriter) bool {
	if p.cfg.Antigravity == nil {
		writeErr(w, 501, "Antigravity 账号管理未启用")
		return false
	}
	return true
}
func (p *Panel) antigravityAdd(w http.ResponseWriter, r *http.Request) {
	if !p.antigravityReady(w) {
		return
	}
	var input struct {
		UID          string `json:"uid"`
		Email        string `json:"email"`
		Nickname     string `json:"nickname"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ProjectID    string `json:"project_id"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if dec.Decode(&input) != nil || dec.Decode(new(any)) != io.EOF {
		writeErr(w, 400, "Antigravity 表单格式无效")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	a, err := p.cfg.Antigravity.Add(ctx, antigravity.Account{Email: input.Email, Nickname: input.Nickname, AccessToken: input.AccessToken, RefreshToken: input.RefreshToken, ProjectID: input.ProjectID}, input.UID)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	log.Printf("antigravity: 账号已添加或更新")
	writeJSON(w, 200, map[string]any{"ok": true, "account": a})
}
func (p *Panel) antigravityImport(w http.ResponseWriter, r *http.Request) {
	if !p.antigravityReady(w) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, 400, "Antigravity 授权 JSON 超过 4 MB 或读取失败")
		return
	}
	accounts, err := antigravity.ParseImport(raw)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	imported := 0
	failures := []string{}
	for i, a := range accounts {
		if _, err := p.cfg.Antigravity.Add(ctx, a, ""); err != nil {
			failures = append(failures, "第 "+strconv.Itoa(i+1)+" 条："+err.Error())
		} else {
			imported++
		}
		if ctx.Err() != nil {
			break
		}
	}
	log.Printf("antigravity: 授权导入完成，成功 %d，失败 %d", imported, len(failures))
	writeJSON(w, 200, map[string]any{"ok": len(failures) == 0, "imported": imported, "errors": failures})
}
func (p *Panel) antigravityAction(w http.ResponseWriter, r *http.Request) {
	if !p.antigravityReady(w) {
		return
	}
	action := r.PathValue("action")
	if action != "remove" && action != "disable" && action != "revive" && action != "balance" {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	a, err := p.cfg.Antigravity.Action(ctx, r.PathValue("uid"), action)
	if errors.Is(err, os.ErrNotExist) {
		writeErr(w, 404, "Antigravity 账号不存在")
		return
	}
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	log.Printf("antigravity: %s 完成", action)
	writeJSON(w, 200, map[string]any{"ok": true, "account": a})
}
func (p *Panel) antigravityRefreshAll(w http.ResponseWriter, r *http.Request) {
	if !p.antigravityReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	failures := []string{}
	for _, a := range p.cfg.Antigravity.List() {
		if a.Disabled {
			continue
		}
		if _, err := p.cfg.Antigravity.Action(ctx, a.UID, "balance"); err != nil {
			failures = append(failures, err.Error())
		}
		if ctx.Err() != nil {
			break
		}
	}
	writeJSON(w, 200, map[string]any{"ok": len(failures) == 0, "accounts": p.antigravityAccounts(), "errors": failures})
}
