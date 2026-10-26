# MCP 接入与任务参考

[中文首页](../README.md) | [English](mcp.en.md) | [使用指南](usage.md) | MCP 接入 | [配置](configuration.md) | [源码构建](development.md)

headless 以 **stdio MCP 本地子进程**运行，提供 `prepare_document`、`confirm_operation`、`restore_document` 三个工具，不聊天、不浏览目录，也不把原文或 mapping 交给模型。

## 客户端接入

客户端配置里**只需要 executable 的完整路径**，不要配置工作目录、项目目录或任何子目录。以下是通用 `mcpServers` 示例；具体客户端可能使用不同配置格式，本仓库不声称任何具体客户端已通过兼容性测试。

Windows：

```json
{
  "mcpServers": {
    "data-masking": {
      "command": "C:\\Tools\\data-masking\\data-masking-agent.exe"
    }
  }
}
```

Linux：

```json
{
  "mcpServers": {
    "data-masking": {
      "command": "/home/you/opt/data-masking/data-masking-agent"
    }
  }
}
```

- 两平台配置形态相同，仅 `command` 路径不同。headless 无 GTK/WebKit 图形依赖；Linux 需要 executable 权限。
- 程序须放在**用户可写且与模型项目分离**的位置，如 Windows `C:\Tools\data-masking\`、Linux `~/opt/data-masking/`；不可写时明确失败，不会静默迁移。
- 工作区由 executable 所在目录决定，与客户端启动目录无关。GUI 与 headless 同址时共享目录及无覆盖原语：GUI 初始化五目录并独立补建 `pending/` 作为快捷输入目录，headless 启动仍要求六目录，完整契约见[工作区目录](usage.md#工作区目录)。
- 本机检查版本：POSIX shell 使用 `./data-masking-agent --version`，Windows PowerShell 使用 `.\data-masking-agent.exe --version`；不启动 MCP、不创建目录。
- 客户端不一定展示初始化 `instructions`；安全指引同时写入三个工具说明，但不保证模型遵守，也不构成权限隔离。

## 输入与规则

- **原件入口固定**：用户将原件放进程序的 `original/`，调用只传单个文件名，如 `report.docx` 或 `slides.pptx`。`../secret.txt`、`sub/notes.txt`、`C:\x.txt`、`\\server\share\a.txt`、`report.txt:ads`、`NUL.txt`、`notes.txt.` 或结尾空格都会被拒绝；拒绝时不读取越界目标，也不回显私有完整路径。agent 不需要、也不应预先读取原文。
- **名称范围与核心一致**：`source_name` 与 `response_path` 复用统一名称策略：接受 `.docx`、`.pptx` 与纯文本候选（不限后缀、可无后缀，含 `.env`、`.gitignore` 等点文件），按名称拒绝 `pdf`、`doc`/`docm`、`odt`、`rtf`、`ppt`/`pptm`、`xls`/`xlsx` 等已知不支持格式；名称允许不代表内容已验证，读取后仍执行编码、大小与 DOCX/PPTX 包门禁。项目脱敏副本统一命名为 `input.masked.<ext>`，PPTX 为 `input.masked.pptx`，无后缀与点文件回退 `input.masked.txt`。
- **项目路径每次显式指定**：`project_path` 必须是用户在本次调用前明确指定的现有绝对目录；不使用 `cwd` 猜测，不把工作区当项目。与程序工作区相同或互为祖先/后代的路径被拒绝。
- **profile 必填且必须由用户明确选择**：使用本机 `config.json` 中存在的档位 key（内置 `strict` / `balanced` / `minimal` / `custom`，或用户自建项），不会静默回退 `balanced`。用户未明确档位时 agent 必须先问，已明确时不重复询问，不自行猜测或默认 `balanced`。内置 balanced 不处理 email/IP/身份证号；strict 只是启用既有六类规则，**不是安全保证**。
- `profile` 缺失、空字符串或 `null` 时立即返回 `failed` / `invalid_request`，固定提示：“缺少脱敏档位 profile。请先询问用户选择本机有效档位，明确选择后再调用；不得自行选择档位或原样重复调用。”
- 非空但本机不存在的档位（包括未匹配的纯空白字符串）在路径/配置前置校验通过后返回 `failed` / `invalid_profile`，固定提示：“profile 无效。请先询问用户确认本机配置中有效的档位名，明确选择后再调用；不得自行选择档位或原样重复调用。”
- 上述两类错误都要先问用户，**不要原样重试，也不要换档重试**。其他参数错误仍用通用 `invalid_request`，不一概归因于缺档位。客户端可能按必填 schema 在发送前拦截缺参数，服务端收不到请求，故初始化及参数说明也要求先问用户。
- **不要让 agent 读配置发现档位**：内置名称只是默认值，本机配置可覆盖同名档位或自建新档位。由用户确认有效档位，不让 agent 打开 `config.json` 或读取 `custom_words`。
- **headless 自定义词只来自 `cards/` 直属 TXT**：完全忽略、不合并、不迁移 `config.json` 的 `custom_words`；旧词不生效、不进入确认指纹，无需手工迁移。每次 `prepare_document` 在 custom 启用时重新读取，不递归、不读取子目录或非 TXT。
  - 每行一个词，trim 行首尾空白、忽略空行；按文件名字节升序及文件内行序合并，字节级精确去重并保留首次出现顺序；`.txt` 扩展名大小写不敏感。
  - 先严格解码 UTF-8，失败再严格解码 GBK；拒绝 NUL / UTF-16。单文件 ≤ 1 MiB、单词 ≤ 256 字符、合并去重后 ≤ 1000 词。
  - custom 启用但无直属 TXT 时返回 `failed` / `invalid_config`：“自定义词规则已启用，请先在程序目录的 cards 文件夹放入有效 TXT 词库文件。”
  - 任一 TXT 为空/全空行、超限、编码错误、不可读、为链接/目录/特殊文件时整批拒绝：“cards 词库无效或无法读取，请在本机检查 TXT 文件的编码、内容及输入限制后重试。”不跳过坏文件、不缓存、不回退配置词库。
  - custom 关闭时不列举、不读取任何词文件，词集为空，空/坏 TXT 不影响操作；目录自身仍在启动安全检查范围。
  - 工具不要求 agent 读取词库正文/原文，不接受调用方提交敏感词数组。
- 配置不存在时用内置默认；存在但不可读、JSON 无效、顶层非对象或大小/阈值等生效字段非法时拒绝该次操作，不偷偷换策略。`custom_words` 无论数组、对象、标量或 null 均忽略；GUI 配置行为不变。

> 内置 balanced 启用 custom，因此使用默认配置的 balanced 或 strict 前，也应由用户在本机准备有效 cards TXT。

## 合作式禁读指引与正常项目闭环

MCP 初始化 `instructions` 和三个工具说明包含同一段指引：agent 不得用文件读取、搜索、终端或其他工具读取、预览、上传 `original/`、`cards/`、`mapping/`、`restored/` 正文及 `.agent-state/` 私有记录。这些只由本地程序处理。配置/词库错误请用户在本机检查，不引导 agent 打开 `config.json`、提交词值或正文。

只有交付（`delivered`）之后，agent 才可读取返回的 `project_file` 脱敏副本。它不等于原件或 mapping，也不代表整包或语义无泄露。agent 可在指定项目内正常读写回复，将 `response_path` 交给 `restore_document`，由本机程序按配对 mapping 还原；随后只使用匿名状态，不读取 `restored/` 正文。

以上是**合作式软约束，不是权限隔离或 OS 沙箱**。客户端可能忽略初始化说明，模型也可能不遵守；指引不限制客户端其他工具，不能阻挡恶意同用户程序。两类风险确认均须转问用户，agent 不能自行批准。

## 任务、配对与恢复

- 每次 `prepare_document` 生成随机 `job_id`，绑定原件、实际策略和配对 mapping；不按文件名猜配对，不允许调用方指定 mapping。
- GUI 平铺在 `masked/`、`mapping/`、`restored/` 的结果不会自动成为 headless 任务；任务记录仅在软件的 `.agent-state/`。
- 重启后未确认的旧任务失败，旧凭据作废，需重新 `prepare_document`；已交付和已还原任务及其配对可跨重启恢复。
- 历史任务不自动清理。退出 GUI/headless 后再自行管理完整任务，不单独删配对 mapping。
- 仅保证不把不完整状态当成功，不承诺断电零残留或磁盘事务。

## 两类确认

`prepare_document` 可能在两阶段暂停；状态、文案、授权不互通：

| 阶段 | 状态与披露 | 可选动作 |
| --- | --- | --- |
| 替换前高频确认 | `awaiting_frequency_confirmation`；仅超阈值词数、降序频次、阈值，不含具体词/位置/上下文；尚未脱敏，无产物 | `continue_masking` / `cancel` |
| 脱敏复核后、交付前残留确认 | `awaiting_residual_confirmation`；披露残留数量及可能含敏感内容的风险；私有配对已保存，项目无副本 | `deliver_with_residual_risk` / `cancel` |

agent 必须转问用户，得到明确答复后才调用 `confirm_operation`，不可自行批准，没有通用 `force` 参数。凭据绑定本会话、任务和类型；文件、规则、词库或项目变化会返回 `confirmation_stale` 并终止任务，需重新准备。

这是**合作式确认**，只防合作场景下的误操作，不是可认证人类身份或 OS 沙箱；恶意同用户程序仍可能直接读文件。

## 查看结果与回程边界

- 还原结果仅在本机 `restored/<job_id>/<attempt_id>/`，agent 只得到状态、还原数量、过滤后匿名告警，不会拿到还原正文，也没有下载接口。
- 每个任务只有**一次成功回程**：相同回复字节重试幂等返回原状态；不同字节再还原返回 `invalid_state`，不覆盖成功结果。
- 以下告警是独立参考信号，不阻断本地结果，也不等同拿错 mapping：
  - `text_mismatch`：DOCX 还原文字与 mapping 的 `SourceTextSHA256` 不同，比较文字而非整个 ZIP 字节。
  - `pptx_text_mismatch`：PPTX 同格式还原后的文字指纹与原件不同，比较实际还原后的覆盖文字，不代表还原失败或 mapping 配错。
  - `sha_mismatch`：纯文本还原字节 SHA 与原件不同。
  - `cross_format`：跨格式，缺同格式文字基线，如 DOCX 来源提交文本回复或反之。
  - `residual_reverse`：占位符残留；`audit_failed`：审计失败，均为另外独立告警。
  - `pptx_cleanup`：PPTX 实际删除的批注数（可为 0），不公开人员/部件细节；`pptx_history`：回程不恢复已清除批注；`unchecked_content`：存在未检查内容。
  - 同字节重试保留原告警分类、固定文案及 `audit_failed`，不新增文件、不重复处理。
- DOCX 只恢复映射文字，不恢复批注、修订历史与 Word 排版；图片、属性等未检查内容不在保证范围。
- PPTX 采用路径 A：只交付 `input.masked.pptx`，不交付 mapping；模型/客户端必须在项目中编辑该副本并保留占位符，再提交同格式 PPTX 回复。备注保留并双向处理，批注及相关人员信息按支持范围清除且不恢复；PPTX mapping 为 version 3，只接受 PPTX→PPTX 同格式回程。图表、SmartArt、嵌入对象、图片文字、属性与链接目标不检查，刷新可能重新引入未处理值；客户端不具备 PPTX 编辑能力时应如实说明，不能改用纯文本回复冒充路径 A。
- 不承诺发现未知敏感信息，也不承诺语义级无泄露；结果仅表示按规则处理。完整 DOCX/PPTX 与还原边界见[首页](../README.md#安全与使用边界)。
