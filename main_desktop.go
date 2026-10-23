//go:build desktop

// Command data-masking 桌面入口：使用同一 core 与 server.Handler，承载独立桌面窗口。
package main

import (
	"fmt"
	"os"

	"github.com/pandatoufu/data-masking/internal/desktop"
)

func main() {
	if err := desktop.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "桌面启动失败：", err)
		os.Exit(1)
	}
}
