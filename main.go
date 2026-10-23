//go:build !desktop && !bindings

// Command data-masking 无 desktop tag 时为开发提示入口：
// 发布产物必须使用本平台桌面构建（见 AGENTS.md），此入口不启动浏览器，也不启动业务服务。
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "该入口是开发占位，不提供桌面界面。")
	fmt.Fprintln(os.Stderr, "请使用本平台桌面构建后再运行：go run ./scripts/build")
	fmt.Fprintln(os.Stderr, "（Windows 与 Linux 命令相同；桌面依赖见 AGENTS.md。）")
	os.Exit(1)
}
