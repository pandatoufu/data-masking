# 配置与审计

[中文首页](../README.md) | [English](configuration.en.md) | [使用指南](usage.md) | [MCP 接入](mcp.md) | 配置 | [源码构建](development.md)

## 配置路径

用户配置位于 `os.UserConfigDir()` 下：

- Windows：`%AppData%\data-masking\config.json`
- Linux：通常为 `~/.config/data-masking/config.json`；设置 `XDG_CONFIG_HOME` 时以该目录为准。

这与[程序旁的工作区](usage.md#工作区目录)不同；工作区位置不可配置。以下仅为虚构配置示例，请用户在本机编辑，不让模型读取真实词库或配置。

```json
{
  "max_file_size": 52428800,
  "high_freq_threshold": 20,
  "idle_timeout_minutes": 30,
  "custom_words": ["示例项目代号"],
  "profiles": {
    "my-profile": { "phone": true, "api_key": true }
  }
}
```

## 字段与入口差异

| 字段 | 含义 |
| --- | --- |
| `max_file_size` | 纯文本、DOCX、PPTX 共用的单文件字节上限，默认 52428800（50 MiB）；显式正值优先 |
| `high_freq_threshold` | 高频词确认阈值，默认 20 |
| `idle_timeout_minutes` | 旧配置兼容字段，桌面应用不会据此自动退出 |
| `custom_words` | GUI 常备词，与本次手输/导入词共同参与；headless 完全忽略此字段，改读 `cards/` 直属 TXT |
| `profiles` | 同名方案以用户配置为准，可新增方案；该方案未写出的规则为关闭，不是与原方案逐项叠加 |

- 六类规则 key：`phone`、`idcard`、`email`、`ip`、`api_key`、`custom`。GUI 的自定义保留 token `__custom` 不可被配置覆盖。
- GUI 读取失败或 JSON 无效时保留兼容的默认回退行为；大小和阈值等正值才覆盖默认。
- `max_file_size` 只控制上传前文件字节数，GUI 手选、固定目录快捷读取与 headless 文档输入使用同一有效值。已经写入 `10485760` 等旧值的配置仍按该值执行，不会因默认值变化被自动迁移或覆盖；需要 50 MiB 范围时请自行在本机调整。
- DOCX/PPTX 的实际解压总量固定为每包 200 MiB（209715200 字节），不由 `max_file_size` 配置，也不是进程内存上限，见[使用指南](usage.md#支持的文件格式与限制)。
- headless 配置不存在时用默认；文件存在但不可读、JSON 无效、顶层非对象或生效字段非法时，拒绝本次操作，不静默换策略。`custom_words` 无论何种类型一律忽略。
- MCP 的档位必须由用户明确选择且在本机有效，不能靠让 agent 读配置来发现。cards 的编码、词数、整批拒绝与 custom 开关行为见 [MCP 输入与规则](mcp.md#输入与规则)。

## 审计日志

配置目录下的 `data-masking/audit/audit.jsonl` 仅记录元数据：`rule_id`、占位符、命中计数、时间戳、文件指纹。**不记录原文值或映射关系**；审计写失败不阻断主流程，会提供相应反馈。

审计日志与含明文原文对照的 mapping 不同。mapping 必须仅留本机，不发送给模型或上传在线服务。
