// Package largefiles 只承载显式大文件验收测试（build tag `large`）。
//
// 默认 `go test ./...` 不编译或运行本包的大样本，避免每次回归生成多份 50/200 MiB 数据；
// 显式入口：`go test -tags large ./internal/largefiles -run 'TestLarge' -v`。
// 它不进入生产构建，不新增运行时依赖。
package largefiles
