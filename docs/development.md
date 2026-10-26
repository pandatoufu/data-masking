# 从源码构建与开发

[中文首页](../README.md) | [English](development.en.md) | [使用指南](usage.md) | [MCP 接入](mcp.md) | [配置](configuration.md) | 源码构建

## 开发前置

- Go 1.23.0 或更新版本，SDK/CLI 固定 Wails v2.11.0。
- 无 Node/npm 运行或构建依赖，`web/` 静态资源通过 `go:embed` 内嵌。
- GUI 原生 Linux 构建需要 C 编译器（gcc）、GTK3 和 WebKitGTK 4.1 开发库，使用 `CGO_ENABLED=1`。运行库与开发库不同；运行前提见[首页](../README.md#平台与格式)。
- Windows 原生 GUI 构建需要固定 Wails CLI；Linux → Windows 交叉构建不需要 MinGW、Node 或 GTK 生产链接。
- 仅构建 headless 不检查 Wails/GTK，也不生成 GUI 图标。

以下命令在仓库根目录执行，在 **POSIX shell 与 Windows PowerShell 中同形**；不需要 Bash 脚本。最终用户使用发布程序不需要安装 Go 或 Wails。

```sh
# GUI 开发前置：安装固定版本 CLI
go install github.com/wailsapp/wails/v2/cmd/wails@v2.11.0

# 无 GUI 业务回归，不初始化 WebView
go test ./...
go vet ./...

# 桌面标签类型检查，不链接原生 GUI 前端
go test -tags desktop,webkit2_41 ./...
go vet -tags desktop,webkit2_41 ./...

# 主机原生 GUI 构建
go run ./scripts/build

# Windows GUI 目标；Linux 主机可交叉构建
go run ./scripts/build -platform windows/amd64

# 仅 headless
go run ./scripts/build -headless-only
go run ./scripts/build -headless-only -platform windows/amd64

# 同版 GUI + headless，成功后写本平台 release.json
go run ./scripts/build -bundle
go run ./scripts/build -bundle -platform windows/amd64
```

`-headless-only` 与 `-bundle` 互斥，构建始终串行（同一工作区不得并发构建）。构建入口只支持 Linux/Windows amd64 主机和 `linux/amd64` / `windows/amd64` 目标；Windows 主机不能构建 Linux 目标。非法目标在写入文件前被拒绝。

## 构建检查与真实运行的区别

- `go test ./...`、`go vet ./...` 为无 GUI 回归，不启动 WebView。
- `go test -tags desktop ./...`、`go vet -tags desktop ./...`，以及上面的 `desktop,webkit2_41` 命令，只做桌面标签类型检查；无 `production` / `dev` / `bindings` 时，Wails 选择 Unix stub，不证明 GTK/WebKit 前端已编译。
- Linux 真实 GUI 生产链接由统一构建入口处理，要求 `CGO_ENABLED=1` 以及 `production,desktop,webkit2_41` tags 和原生开发库，缺少任一前提无法链接。
- Windows 交叉编译和 PE 资源核验不能替代实际 Windows GUI 测试；普通业务回归与历史 GUI 验收也不能证明新界面已验收。仅报告实际执行的环境与范围。
- 未加 `desktop` tag 的根 `main` 只是构建提示占位入口，不打开浏览器或启动 TCP 服务，不是可运行 GUI。裸 `go build` 不能替代带图标/manifest/版本资源的交付构建入口。

## 桌面绑定面

桌面进程只绑定窄 `desktop.Bridge`，不绑定整个 `App`，避免注入 setter 经反射进入 JS。当前绑定方法固定为：`SaveFile`、`ReportUnsaved`、`SetExitConfirmed`、`SetUIPreferences`、`AutoSaveMask`、`AutoSaveRestored`、`ListWorkspaceFiles(kind)`、`ReadWorkspaceFile(kind, name)`。

- `ListWorkspaceFiles(kind)` / `ReadWorkspaceFile(kind, name)` 只接受 `original`、`cards`、`pending`、`mapping` 四类固定用途与单个文件名；目标目录由用途决定，不接受 root、绝对/相对路径或子目录，只列举直属安全普通文件，显式读取后才返回单个文件的原始字节（Base64）。
- 自动落盘与目录读取都不新增 HTTP endpoint，也不接受任意路径；业务 API 仍只有 `/api/mask`、`/api/restore`、`/api/exit` 三个。
- 列表/读取复用保存与关闭的原子协调：处理、保存或已接受退出时返回可重试忙态，结束后一定释放，不推进 `unsaved_generation`。

## 交付验收测试二进制（无开发环境的最终用户）

对于没有 Go/Node/源码/Wails 的最终用户，默认验收方式由开发端编译目标平台的测试二进制，交付 exe、精确测试筛选和运行/保存日志命令；用户端只运行 exe。能交叉编译就交叉编译，不能则在目标平台预编译。本变更的 Windows junction/链接用例即按此交付。

Linux 开发端生成 Windows amd64 测试二进制（`-c` 只编译、不运行）：

```text
mkdir -p dist/windows-amd64/acceptance
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -tags desktop -o dist/windows-amd64/acceptance/workspace.test.exe ./internal/workspace
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -tags desktop -o dist/windows-amd64/acceptance/desktop.test.exe ./internal/desktop
```

Windows 开发端 PowerShell 等价（结束后恢复原有环境变量）：

```powershell
$saved = @{ GOOS=$env:GOOS; GOARCH=$env:GOARCH; CGO_ENABLED=$env:CGO_ENABLED }
try {
  $env:GOOS='windows'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'
  go test -c -tags desktop -o dist/windows-amd64/acceptance/workspace.test.exe ./internal/workspace
  go test -c -tags desktop -o dist/windows-amd64/acceptance/desktop.test.exe ./internal/desktop
} finally {
  foreach ($k in 'GOOS','GOARCH','CGO_ENABLED') {
    if ($null -eq $saved[$k]) { Remove-Item "Env:$k" -ErrorAction SilentlyContinue } else { Set-Item "Env:$k" $saved[$k] }
  }
}
```

用户端在交付目录内运行（PowerShell；`-test.*` 是编译后测试二进制自身的 flag。`--%` 让 PowerShell 停止解析后续参数，避免报 `flag provided but not defined: -test`）：

```powershell
.\workspace.test.exe --% -test.run=Junction -test.v=true -test.count=1
.\desktop.test.exe --% -test.run=TestWorkspaceInputWindowsRejects -test.v=true -test.count=1
```

- 预期用例：第一条为 `TestEnsureRejectsJunctionOnWindows`、`TestEnsureGUIRejectsRequiredJunctionOnWindows`、`TestEnsureGUIIgnoresPendingJunctionOnWindows`、`TestEnsureDirRejectsJunctionOnWindows`、`TestLockRejectsJunctionOnWindows`（5 个）；第二条为 `TestWorkspaceInputWindowsRejectsJunctionDir`、`TestWorkspaceInputWindowsRejectsFileLink`（2 个）。
- 控制台复制输出即可。`$LASTEXITCODE` 为测试进程退出码；**所有预期用例都出现且无 `--- SKIP` 才算通过，SKIP 不算通过**，须保存输出并说明原因。
- 如需保存日志：运行前执行 `Start-Transcript -Path windows-link-tests.log`，两条命令结束后执行 `Stop-Transcript`。不要在 `--%` 之后使用 `2>&1`、管道或重定向。
- Windows 创建符号链接可能要求 **Developer Mode** 或管理员权限；权限不足时按环境说明处理并重跑，不要修改系统策略来“通过”测试。
- 这些测试只写 `t.TempDir`，不初始化 GUI；测试二进制不替代 GUI 目检，也不代表所有测试都能在无运行时依赖的机器上执行。其他用例仍以开发者 `go test` 为准。
- 归档 `openspec/changes/archive/2026-10-19-cross-platform-mcp-headless/acceptance.md` 已用同样的 `--%` 写法在 Windows 10 验证过旧测试 exe；本变更的 Windows 测试二进制已由最终用户在 Windows 实跑，最终报告全部通过（详细输出与最终更正见变更 `acceptance.md`）。

## 产物布局

所有构建模式都按选定目标输出到 `dist/<os>-<arch>/`，不向 `dist/` 顶层写发布产物：

```text
dist/
|-- linux-amd64/
|   |-- data-masking
|   |-- data-masking-agent
|   +-- release.json
+-- windows-amd64/
    |-- data-masking.exe
    |-- data-masking-agent.exe
    +-- release.json
```

- 默认构建只写 GUI；`-headless-only` 只写 agent；`release.json` 仅在 `-bundle` 成功时生成。Linux → Windows 与 Windows 原生构建均输出到 `dist/windows-amd64/`。
- Go module 路径为 `github.com/pandatoufu/data-masking`；headless 的版本注入符号是该路径下的 `internal/buildinfo.Version`，随 `wails.json` 的 `info.productVersion` 生成。
- 两个 GUI 目标（`linux/amd64`、`windows/amd64`）都通过固定 Wails v2.11.0 的 `-trimpath` 构建，headless 的 `go build` 同样保留 `-trimpath`，产物不嵌入本机文件系统路径。`-trimpath` 只移除路径、不擦除源码字符串或依赖信息，交付前仍须扫描二进制与包内容。
- headless 使用 `CGO_ENABLED=0`，不加 desktop/production GUI tags 或 `-H windowsgui`，编译 `cmd/data-masking-agent`。
- bundle 先构建 GUI 再构建 headless，两者成功并校验后才发布到同平台目录，最后写包含版本、目标及文件 SHA-256 的 `release.json`。失败不把旧文件或半套产物宣称为新发布；单入口构建不新建或改写 bundle 清单。
- 构建只补建当前平台目录、更新本次受管文件，不清空目录，不删除、迁移或修改其他平台目录、旧顶层产物或既有 `cards/`、`mapping/`、`masked/`、`original/`、`restored/`、`pending/`、`.agent-state/` 运行数据。旧顶层产物不会自动迁移或双写。
- 版本唯一来源为 `wails.json` 的 `info.productVersion`；构建时注入 headless 的 `internal/buildinfo.Version`，agent 的 `--version` 与 MCP `serverInfo.version` 一致。

## 图标与资源保护

Windows 目标通过固定 Wails CLI 内嵌六尺寸 ICO、manifest 和版本信息。构建入口刷新受管派生 `build/windows/icon.ico`，保留同目录手工 manifest / metadata，不因旧 ICO 存在沿用旧图案，并拒绝预存 `data-masking-res.syso`；只清理本次临时资源。

重新生成内嵌图标（POSIX shell / PowerShell 同形）：

```sh
go run ./scripts/genicon
```

静态页面及资源内嵌于 GUI 单 executable，无需旁置页面或图标；Windows 仍需系统 WebView2，Linux 仍需 GTK3/WebKitGTK 4.1 与正常图形/session bus 环境。不要将“单程序交付”写成“所有新机零依赖”。

## 手动公开发布

公开分支 `github-public`（GitHub 默认分支）是**独立根的公开快照历史**，与开发主库 `main` 没有共同祖先，也不通过合并桥接。记 `S` 为选定且干净的开发提交（版本已就绪）、`P` 为迁移前的旧公开 tip、`R` 为清理后的首个独立根公开提交：

```text
development: A --> B --> S --> S2
                        :      :
                      export export
                        :      :
new public:             R ---> R2

old public:  P0 --> P1 --> P    (迁移后不再被分支或 tag 引用)
```

- 导出 `S` 的受跟踪文件到全新隔离目录，不复制 `.git`、Git 对象、remote、reflog 或用户运行数据。
- 在全新 Git 仓库创建 `R`，**父提交数量必须为 0**；`R2` 只有 `R` 一个父提交。禁止 merge / pull / cherry-pick 开发历史或旧公开历史，禁止把 `main` 合入公开分支。
- 公开树只排除根路径 `/.opencode/`、`/openspec/`、`/AGENTS.md`、`/CHANGELOG.md`；除这四项与 `.gitignore` 外，`R` 的受跟踪树必须与 `S` 完全一致（内容、文件类型、模式及删除项）。
- 公开 `.gitignore` 由 `S` 的基础规则加这四条精确根路径构成；`main` 的 `.gitignore` 不添加它们，开发文件在开发主库继续跟踪，开发历史不删除、不改写。
- 公开历史不含开发祖先，但排除文件树**不是**保密手段；任何不能公开的内容都不能写进导出源。
- 公开 commit 的 author/committer 与 annotated tag 的 tagger 使用 `pandatoufu` <`8235988+pandatoufu@users.noreply.github.com`>，只在候选仓库局部配置，不修改全局身份。
- `S` SHA、导出清单、`R` 与各包 SHA 等私有对应关系只保存在开发侧验收记录，不进入公开提交、Release 正文或公开文件。
- 功能验收按变更实际影响确定：本次发布准备只改版本与发布资料，**不要求重跑完整 GUI/MCP 手工功能验收**；引用既有证据须注明来源与“本次未重跑”。
- 本流程全部手动执行，仓库没有自动发布、自动打包或自动 CHANGELOG 工作流；下面的命令只描述人工步骤。尖括号占位符（`<S>`、`<P>`、`<export-dir>` 等）需替换为实际 SHA 或路径后再执行，不要原样粘贴。

### 1. 版本与 CHANGELOG（在 `main`）

- 产品版本唯一来源是 `wails.json` 的 `info.productVersion`；`build/windows/info.json` 与 headless 注入都复用它，不手工改其他版本文件，也不批量替换测试 fixture 里的版本字面量。
- `CHANGELOG.md` 只在 `main` 跟踪维护。开发期间记入 `[Unreleased]`，发布前归并进本次版本节（Added / Changed / Fixed / Security 等）；正式日期以实际发布为准，未发布写“未发布”。
- 变化基线是上一个公开 tag 的文件树，不是 `main` 上的同名 tag。用跨根比较并排除公开排除项，逐条核对后描述：

```sh
git diff v1.1 <S> -- . ':(exclude).opencode' ':(exclude)openspec' ':(exclude)AGENTS.md' ':(exclude)CHANGELOG.md'
```

不要把旧版本已有的能力（例如 `v1.1` 已含的 headless MCP 与 DOCX 处理）写成新增，也不要虚构旧版本日期。

### 2. 导出干净快照并创建独立根 `R`

前置检查（`S` 已提交、工作区干净；导出目录必须是开发工作区之外的全新空目录）：

```sh
git status --porcelain
git rev-parse <S>
```

导出 `S` 的受跟踪文件，移除四个公开排除路径，并追加公开 `.gitignore` 规则：

```sh
mkdir -p <export-dir>
git archive --format=tar <S> | tar -x -C <export-dir>
rm -rf <export-dir>/.opencode <export-dir>/openspec <export-dir>/AGENTS.md <export-dir>/CHANGELOG.md
printf '%s\n' '/.opencode/' '/openspec/' '/AGENTS.md' '/CHANGELOG.md' >> <export-dir>/.gitignore
```

Windows PowerShell 等价：

```powershell
New-Item -ItemType Directory -Force <export-dir> | Out-Null
git archive --format=tar --output=<export-dir>\source.tar <S>
tar -xf <export-dir>\source.tar -C <export-dir>
Remove-Item <export-dir>\source.tar
Remove-Item -Recurse -Force <export-dir>\.opencode, <export-dir>\openspec, <export-dir>\AGENTS.md, <export-dir>\CHANGELOG.md
Add-Content -Path <export-dir>\.gitignore -Value @('/.opencode/','/openspec/','/AGENTS.md','/CHANGELOG.md') -Encoding ascii
```

不要用 PowerShell `>` / `Out-File` 重写 `.gitignore`，以免写入 BOM 破坏首条规则。

在导出目录初始化全新仓库，只设置**局部**公开身份并提交 `R`：

```sh
git -C <export-dir> init -b github-public
git -C <export-dir> config user.name "pandatoufu"
git -C <export-dir> config user.email "8235988+pandatoufu@users.noreply.github.com"
git -C <export-dir> config commit.gpgsign false
git -C <export-dir> config tag.gpgsign false
git -C <export-dir> config core.hooksPath .git/empty-hooks
git -C <export-dir> add -A
git -C <export-dir> commit -m "release: v1.2.0 (source <S 完整 SHA>)"
git -C <export-dir> rev-list --parents -n 1 HEAD   # 只输出一个 SHA：父提交数为 0
git -C <export-dir> cat-file -p HEAD               # author/committer 均为 noreply 身份
```

`core.hooksPath` 指向不存在的空目录，避免用户全局钩子把私人身份或签名带进候选对象；候选仓库不启用任何签名配置。提交前另可用 `git -C <export-dir> var GIT_AUTHOR_IDENT` / `GIT_COMMITTER_IDENT` 核对生效身份。

白名单核对（跨仓库比较，不把开发对象带进候选仓库）：

```sh
git -C <dev-repo> ls-tree -r --full-tree <S> | awk -F'\t' '$2 !~ /^(\.opencode\/|openspec\/|AGENTS\.md$|CHANGELOG\.md$|\.gitignore$)/' > <work-dir>/dev-tree.txt
git -C <export-dir> ls-tree -r --full-tree HEAD | awk -F'\t' '$2 != ".gitignore"' > <work-dir>/public-tree.txt
diff -u <work-dir>/dev-tree.txt <work-dir>/public-tree.txt    # 必须无输出
git -C <dev-repo> show <S>:.gitignore > <work-dir>/dev.gitignore
diff -u <work-dir>/dev.gitignore <export-dir>/.gitignore       # 只多出追加的四行
git -C <export-dir> ls-files .opencode openspec AGENTS.md CHANGELOG.md   # 必须为空
git -C <export-dir> cat-file -e <P>   # 预期失败：旧公开对象不在新仓库
```

`ls-tree` 输出含 mode / type / SHA / path，因此内容、文件类型与可执行模式都参与比较；`diff` 有任何输出都说明导出不完整，停止并排查。`<dev-repo>` 指开发主库仓库根。

### 3. 后续版本的干净单亲快照

每次都从**干净的公开远端重新 clone** 作为候选，不复用旧 clone 或被污染的目录：

```sh
git clone --branch github-public https://github.com/pandatoufu/data-masking.git <export2-dir>
```

清空索引后用下一次选定 `S2` 的导出覆盖工作树，再补齐排除项与 `.gitignore`：

```sh
git -C <export2-dir> rm -r -q --cached .
git archive --format=tar <S2> | tar -x -C <export2-dir>
rm -rf <export2-dir>/.opencode <export2-dir>/openspec <export2-dir>/AGENTS.md <export2-dir>/CHANGELOG.md
printf '%s\n' '/.opencode/' '/openspec/' '/AGENTS.md' '/CHANGELOG.md' >> <export2-dir>/.gitignore
git -C <export2-dir> add -A
git -C <export2-dir> commit -m "release: <版本> (source <S2 完整 SHA>)"
git -C <export2-dir> rev-list --parents -n 1 HEAD   # 只有上一个公开提交一个父提交
```

- `S2` 的 `.gitignore` 会覆盖 clone 中的公开版本，因此每次都要重新追加四行。
- 白名单核对与首次相同，把 `<S>` 换成 `<S2>`、`<export-dir>` 换成 `<export2-dir>`。
- 不 merge、不 pull 合并、不 cherry-pick 旧公开或开发提交来“修复不同源”；`R2` 的唯一父提交必须是 `R`。

### 4. 首次迁移的定向远端操作（需另行授权）

写操作前重新枚举远端与旧版本对象，记录预期旧 `P` 完整 SHA、Release ID 与附件 ID/名称：

```sh
git fetch github
git ls-remote --heads --tags github
git rev-parse github/github-public     # 必须等于记录的 P
gh release view v1.1 --repo pandatoufu/data-masking --json tagName,assets
```

出现未知 head/tag、未知 Release 或附件、保护规则拒绝时暂停，不扩大删除范围。首次替换只用针对精确旧 SHA 的租约，禁止裸 `--force`、`--mirror` 或批量推送所有 tag：

```sh
git -C <export-dir> remote add github https://github.com/pandatoufu/data-masking.git
git -C <export-dir> push --force-with-lease=refs/heads/github-public:<P> github HEAD:refs/heads/github-public
```

旧版本定向移除（先删 Release 后删 tag；删除前确认附件只有预期两个，删除后回读确认）：

```sh
gh release delete v1.1 --repo pandatoufu/data-masking --yes
git -C <export-dir> push github :refs/tags/v1.1
```

新 tag 使用同一局部 noreply 身份创建 annotated tag，并推送到 GitHub：

```sh
git -C <export-dir> tag -a v1.2.0 -m "data-masking v1.2.0" <R>
git -C <export-dir> cat-file -p refs/tags/v1.2.0   # tagger 为 noreply 身份
git -C <export-dir> push github refs/tags/v1.2.0
```

任一步失败都暂停：不把含旧信息的分支/tag/附件恢复为默认回滚，也不静默移动已发布 tag。

### 5. 从 `R` 串行构建并白名单打包

只在最终 `R` 的干净受跟踪树上构建（即在导出仓库完成提交后的 `R` 上构建；不在 `S` 上构建后声称来源为 `R`）。两个目标串行执行：

```sh
go run ./scripts/build -bundle
go run ./scripts/build -bundle -platform windows/amd64
git status --porcelain
git diff --stat
```

构建会写忽略的 `dist/` 输出；`git status --porcelain` 不得出现受跟踪改动，否则先解决再重新确定 `R` 并重建。两个 GUI 目标都以固定 Wails v2.11.0 的 `-trimpath` 构建，headless 同样使用 `-trimpath`；构建参数不含本机路径不等于源码字符串干净，交付前仍须扫描二进制与包内容。

每个平台只收集三项（GUI、agent、该平台 `release.json`），从干净临时打包目录收集，不压缩整个 `dist/`、不包含验收测试 exe 或任何用户数据目录。Linux tar.xz 在 Linux 开发端生成（保留可执行位）：

```sh
mkdir -p <pack-dir>/linux-amd64
cp dist/linux-amd64/data-masking dist/linux-amd64/data-masking-agent dist/linux-amd64/release.json <pack-dir>/linux-amd64/
tar -C <pack-dir>/linux-amd64 -cJf data-masking-linux-amd64-v1.2.0.tar.xz data-masking data-masking-agent release.json
```

Windows zip 在 Windows 开发端生成（PowerShell）：

```powershell
New-Item -ItemType Directory -Force <pack-dir>\windows-amd64 | Out-Null
Copy-Item dist\windows-amd64\data-masking.exe, dist\windows-amd64\data-masking-agent.exe, dist\windows-amd64\release.json <pack-dir>\windows-amd64\
Compress-Archive -Path <pack-dir>\windows-amd64\data-masking.exe, <pack-dir>\windows-amd64\data-masking-agent.exe, <pack-dir>\windows-amd64\release.json -DestinationPath data-masking-windows-amd64-v1.2.0.zip
```

核验归档成员只有三项，并核对版本与 SHA-256（归档文件名须与交付记录一致；改名时同步更新下列命令）：

```sh
tar -tJf data-masking-linux-amd64-v1.2.0.tar.xz
sha256sum data-masking-linux-amd64-v1.2.0.tar.xz dist/linux-amd64/data-masking dist/linux-amd64/data-masking-agent
```

```powershell
Expand-Archive -Path data-masking-windows-amd64-v1.2.0.zip -DestinationPath <verify-dir>
Get-ChildItem <verify-dir>
Get-FileHash -Algorithm SHA256 data-masking-windows-amd64-v1.2.0.zip, dist\linux-amd64\data-masking, dist\linux-amd64\data-masking-agent
(Get-Item dist\windows-amd64\data-masking.exe).VersionInfo.ProductVersion
```

交叉核对：程序 `productVersion`、`CHANGELOG` 的 `1.2.0` 节、tag `v1.2.0`、Windows PE 版本资源、agent `--version` 与 MCP `serverInfo.version`、两份 `release.json` 的 `version` / `target` / `files[].sha256`，以及两个归档文件的 SHA-256。这些版本、目标平台、文件树/包内容与哈希核验是正式发布的必要项。

**功能验收范围**：本次发布准备只改版本与发布资料，不修改产品功能，因此**不要求重跑完整 GUI/MCP 手工功能验收**；未重跑不是本版缺陷，也不作为发布阻塞。既有功能的验收证据保存在开发主库的 OpenSpec 归档中（公开树不含该目录），引用时须注明记录路径与对应版本/提交，并明确是历史记录、本次未重复执行，不得表述为本轮运行通过。新构建可做启动/版本冒烟（GUI 启动、agent `--version`、MCP `serverInfo.version`），只记录实际执行范围，不扩大成完整业务闭环门槛；后续涉及产品功能的版本按实际影响另定验收范围。

### 6. Release 与回读核验

Release 只在 GitHub 创建，正文从**开发提交** `S` 的 `CHANGELOG.md` 提取（公开树没有该文件），只取 `1.2.0` 节，不含 `Unreleased` 或开发过程记录。POSIX shell：

```sh
git show "<S>:CHANGELOG.md" | awk '/^## \[1\.2\.0\]/{f=1;next} /^## \[/{f=0} f' > release-notes-v1.2.0.md
```

Windows PowerShell 等价（显式写 UTF-8 无 BOM）：

```powershell
$lines = @(git show "<S>:CHANGELOG.md")
$body = New-Object System.Collections.Generic.List[string]
$inSection = $false
foreach ($line in $lines) {
  if ($line -match '^## \[1\.2\.0\]') { $inSection = $true; continue }
  if ($line -match '^## \[') { $inSection = $false; continue }
  if ($inSection) { $body.Add($line) }
}
[System.IO.File]::WriteAllLines((Join-Path $PWD 'release-notes-v1.2.0.md'), $body, (New-Object System.Text.UTF8Encoding($false)))
```

创建 GitHub Release 时使用该正文与两个归档，设为 Latest；随后回读并分层核验：

```sh
gh release view v1.2.0 --repo pandatoufu/data-masking --json tagName,body,assets
gh release list --repo pandatoufu/data-masking
git ls-remote --heads --tags https://github.com/pandatoufu/data-masking.git
git clone --branch github-public https://github.com/pandatoufu/data-masking.git <verify-clone-dir>
git -C <verify-clone-dir> rev-list --count --all
git -C <verify-clone-dir> cat-file -e <P>   # 预期失败：旧公开对象不可达
```

- **原仓库可控引用**：`github-public` 只含独立根 lineage，公开 tag 只有指向 `R` 的 `v1.2.0`，旧 `v1.1` 的 tag 与 Release 已不存在。
- **新交付物**：Release 正文与本次 `CHANGELOG` 版本节一致，两个附件名称与 SHA-256 与本地构建一致，未上传实施测试包。
- **外部残留**：已知 fork、旧 SHA 直链、缓存和他人克隆仍可能存在，不能由原仓库清理结论覆盖，也不承诺全网收回。需要联系 fork 所有者或 GitHub Support 时另行取得授权。
