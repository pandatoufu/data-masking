// Package web 通过 go:embed 内嵌单页双视图静态资源，使单二进制即完整应用。
package web

import "embed"

// FS 内嵌前端静态资源。
//
//go:embed static/index.html static/app.js static/style.css
var FS embed.FS
