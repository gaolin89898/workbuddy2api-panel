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

	"github.com/linguo2625469/workbuddy2api-panel/internal/loomy"
)

// Loomy 无浏览器授权流程（桌面端登录依赖短信与 HMAC 签名），账号来源只有
// 本机桌面端会话导入（仅本机）与粘贴凭证两种。
func (p *Panel) loomyRoutes() {
	p.mux.HandleFunc("POST /panel/api/loomy/accounts", p.withAuth(p.loomyAdd))
	p.mux.HandleFunc("POST /panel/api/loomy/local_import", p.withAuth(p.loomyLocalImport))
	p.mux.HandleFunc("POST /panel/api/loomy/accounts/{uid}/{action}", p.withAuth(p.loomyAction))
	p.mux.HandleFunc("POST /panel/api/loomy/checkin_all", p.withAuth(p.loomyCheckinAll))
	p.mux.HandleFunc("POST /panel/api/loomy/balance_all", p.withAuth(p.loomyBalanceAll))
}
func (p *Panel) loomyAccounts() []loomy.Status {
	if p.cfg.Loomy == nil {
		return []loomy.Status{}
	}
	return p.cfg.Loomy.List()
}
func (p *Panel) loomyReady(w http.ResponseWriter) bool {
	if p.cfg.Loomy == nil {
		writeErr(w, 501, "Loomy 账号管理未启用")
		return false
	}
	return true
}
func decodeLoomy(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if dec.Decode(v) != nil || dec.Decode(new(any)) != io.EOF {
		writeErr(w, 400, "Loomy 表单格式无效")
		return false
	}
	return true
}
func (p *Panel) loomyAdd(w http.ResponseWriter, r *http.Request) {
	if !p.loomyReady(w) {
		return
	}
	var input struct {
		Credential string `json:"credential"`
		Nickname   string `json:"nickname"`
		UID        string `json:"uid"`
		AccountUID string `json:"account_uid"`
	}
	if !decodeLoomy(w, r, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	a, warnings, err := p.cfg.Loomy.Add(ctx, loomy.Account{Credential: input.Credential, Nickname: input.Nickname, AccountUID: input.AccountUID}, input.UID)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	log.Printf("loomy: 账号已添加或更新")
	writeJSON(w, 200, map[string]any{"ok": true, "account": a, "warnings": warnings})
}

// loomyLocalImport 读取本机已登录 Loomy 桌面端的会话并导入（仅限本机请求）。
func (p *Panel) loomyLocalImport(w http.ResponseWriter, r *http.Request) {
	if !p.loomyReady(w) || !desktopLocal(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	session, err := loomy.FindLocalSession()
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	a, warnings, err := p.cfg.Loomy.Add(ctx, loomy.Account{Credential: session.Session, AccountUID: session.UserID, Nickname: session.Phone}, "")
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	log.Printf("loomy: 本机会话已导入")
	writeJSON(w, 200, map[string]any{"ok": true, "account": a, "warnings": warnings})
}
func (p *Panel) loomyAction(w http.ResponseWriter, r *http.Request) {
	if !p.loomyReady(w) {
		return
	}
	action := r.PathValue("action")
	if action != "checkin" && action != "balance" && action != "remove" && action != "disable" && action != "revive" {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 100*time.Second)
	defer cancel()
	a, warning, err := p.cfg.Loomy.Action(ctx, r.PathValue("uid"), action)
	if errors.Is(err, os.ErrNotExist) {
		writeErr(w, 404, "Loomy 账号不存在")
		return
	}
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	log.Printf("loomy: %s 完成", action)
	writeJSON(w, 200, map[string]any{"ok": true, "account": a, "checkin_done": a.CheckinDone, "balance_error": warning})
}
func (p *Panel) loomyCheckinAll(w http.ResponseWriter, r *http.Request) {
	if !p.loomyReady(w) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		p.cfg.Loomy.RunAll(ctx, "checkin")
	}()
	log.Printf("loomy: 手动签到已开始")
	writeJSON(w, 200, map[string]any{"ok": true, "started": true})
}
func (p *Panel) loomyBalanceAll(w http.ResponseWriter, r *http.Request) {
	if !p.loomyReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	errs := p.cfg.Loomy.RunAll(ctx, "balance")
	writeJSON(w, 200, map[string]any{"ok": true, "accounts": p.loomyAccounts(), "errors": errs})
}
