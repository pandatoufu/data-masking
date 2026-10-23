// Package main 是 headless stdio MCP 入口：不启动 GUI、不监听 TCP、不打开浏览器。
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/pandatoufu/data-masking/internal/agent"
	agentmcp "github.com/pandatoufu/data-masking/internal/agent/mcp"
	"github.com/pandatoufu/data-masking/internal/audit"
	"github.com/pandatoufu/data-masking/internal/buildinfo"
	"github.com/pandatoufu/data-masking/internal/config"
	"github.com/pandatoufu/data-masking/internal/workspace"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		// stderr 只输出固定事件码，不含业务参数或错误原文。
		fmt.Fprintln(os.Stderr, "data-masking-agent: startup_failed")
		os.Exit(1)
	}
}

func run(args []string) error {
	for _, a := range args {
		switch a {
		case "--version", "-version":
			// --version 不启动 MCP、不建目录。
			fmt.Fprintln(os.Stdout, buildinfo.Effective())
			return nil
		default:
			return errors.New("unsupported flag")
		}
	}

	root, err := workspace.ExecutableRoot()
	if err != nil {
		return err
	}
	if err := workspace.EnsureHeadless(root); err != nil {
		return err
	}

	app := agent.NewApp(root, userConfigPath())
	if p, err := audit.DefaultPath(); err == nil {
		app.Audit = audit.New(p)
	}
	if err := app.Open(); err != nil {
		return err
	}
	defer func() { _ = app.Close() }()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	server := agentmcp.NewServer(app)
	return server.Run(ctx)
}

// userConfigPath 返回用户配置路径；解析失败时返回空串（使用内置默认）。
func userConfigPath() string {
	p, err := config.UserConfigPath()
	if err != nil {
		return ""
	}
	return p
}
