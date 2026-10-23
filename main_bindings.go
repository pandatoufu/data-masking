//go:build bindings && !desktop

// Command data-masking bindings 入口：仅供 Wails CLI 生成绑定时编译。
// 它不初始化 WebView，也不打开浏览器；生成的绑定与桌面入口一致。
package main

import (
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"

	"github.com/pandatoufu/data-masking/internal/desktop"
)

func main() {
	_ = wails.Run(&options.App{
		Bind: []interface{}{&desktop.App{}},
	})
}
