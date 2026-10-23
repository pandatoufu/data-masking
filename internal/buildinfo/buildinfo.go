// Package buildinfo 提供构建期注入的版本信息；唯一发布来源是 wails.json 的 productVersion。
package buildinfo

// Version 由构建器注入（-ldflags -X）。开发/测试默认 dev。
var Version = "dev"

// Effective 返回非空版本串。
func Effective() string {
	if Version == "" {
		return "dev"
	}
	return Version
}
