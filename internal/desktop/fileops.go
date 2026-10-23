package desktop

import "github.com/pandatoufu/data-masking/internal/workspace"

// TempFile/FileOps/OSFileOps 是共享工作区文件原语的类型别名，
// 保持既有测试与绑定层注入点不变；实际实现位于 internal/workspace。
type TempFile = workspace.TempFile

// FileOps 抽象保存协调器所需的文件系统原语，便于测试注入写入/Sync/Close/Link/清理故障。
// 实现 MUST NOT 覆盖已存在目标：发布必须使用无覆盖原语（硬链接）。
type FileOps = workspace.FileOps

// OSFileOps 是 FileOps 的本地操作系统实现。
type OSFileOps = workspace.OSFileOps
