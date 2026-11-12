package panel

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:accounts_dist
var accountAssets embed.FS

// vueRoutes 统一账号端（Vue 构建产物 accounts_dist）。
// 两种模式都挂 /panel/：账号管理页面始终从这里进入。
func (p *Panel) vueRoutes() {
	p.mux.HandleFunc("GET /panel/{$}", func(w http.ResponseWriter, r *http.Request) {
		index, _ := accountAssets.ReadFile("accounts_dist/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
	assets, _ := fs.Sub(accountAssets, "accounts_dist")
	files := http.StripPrefix("/panel/", http.FileServer(http.FS(assets)))
	for _, path := range []string{"/panel/accounts.js", "/panel/index.css", "/panel/chunks/"} {
		p.mux.Handle("GET "+path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache")
			files.ServeHTTP(w, r)
		}))
	}
}

// classicRoutes 经典网关控制台（旧 index.html + app.js，API 路径为绝对 /panel/api/*，
// 挂到子路径无需改动）。迁移期保留独立地址 /panel/classic/，与 Vue 并存；
// Vue 页面功能补齐后再删除（见统一后台重构第五阶段）。
func (p *Panel) classicRoutes() {
	p.mux.HandleFunc("GET /panel/classic/{$}", p.index)
	p.mux.HandleFunc("GET /panel/classic/app.js", p.appScript)
	p.mux.HandleFunc("GET /panel/classic/mobile.css", p.mobileStyle)
	p.mux.HandleFunc("GET /panel/classic/mobile.js", p.mobileScript)
}
