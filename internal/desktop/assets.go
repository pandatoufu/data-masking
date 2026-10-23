package desktop

import (
	"net/http"
	"strings"
)

// allowedPages 是桌面宿主允许的页面路径集合。
// Wails v2.11.0 只对以 "/" 或 "/index.html" 结尾的 GET 路径注入 runtime/bindings，
// 因此还原页的规范路径必须是以 "/" 结尾的 /restore/。
var allowedPages = map[string]bool{
	"/":         true,
	"/restore/": true,
}

// DesktopHandler 包装既有 server.Handler，只做桌面路由适配：
//   - 旧 /restore 重定向到 /restore/；
//   - 其余未知的 index 型路径返回本应用 400 反馈，避免 Wails 把 404 替换为框架默认页；
//   - 其他路径与所有非 GET 请求原样交给业务 handler，不改变三个 API 语义。
func DesktopHandler(inner http.Handler) http.Handler {
	unknownPage := "未找到该页面。这是一个本地桌面应用，请使用界面内的脱敏或还原入口。"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if r.Method == http.MethodGet && path == "/restore" {
			http.Redirect(w, r, "/restore/", http.StatusFound)
			return
		}
		if r.Method == http.MethodGet && !allowedPages[path] && isIndexLike(path) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(unknownPage))
			return
		}
		inner.ServeHTTP(w, r)
	})
}

// isIndexLike 复刻 Wails runtime 注入匹配：以 "/" 或 "/index.html" 结尾。
func isIndexLike(path string) bool {
	if path == "" {
		path = "/"
	}
	return strings.HasSuffix(path, "/") || strings.HasSuffix(path, "/index.html")
}
