# Configuration and audit

[中文](configuration.md) | [English home](../README.en.md) | [Usage guide](usage.en.md) | [MCP guide](mcp.en.md) | Configuration | [Building from source](development.en.md)

## Configuration paths

User configuration lives under `os.UserConfigDir()`:

- Windows: `%AppData%\data-masking\config.json`
- Linux: usually `~/.config/data-masking/config.json`; when `XDG_CONFIG_HOME` is set, that directory is used instead.

This is separate from the [workspace beside the executable](usage.en.md#workspace-directories), whose location is not configurable. The following is a fictional example only. Users should edit configuration locally; do not let the model read real word lists or configuration.

```json
{
  "max_file_size": 52428800,
  "high_freq_threshold": 20,
  "idle_timeout_minutes": 30,
  "custom_words": ["Example project codename"],
  "profiles": {
    "my-profile": { "phone": true, "api_key": true }
  }
}
```

## Fields and entry-point differences

| Field | Meaning |
| --- | --- |
| `max_file_size` | Per-file byte limit shared by plain text, DOCX and PPTX; default 52428800 (50 MiB); an explicit positive value wins |
| `high_freq_threshold` | High-frequency word confirmation threshold; default 20 |
| `idle_timeout_minutes` | Legacy compatibility field; the desktop app does not automatically exit based on it |
| `custom_words` | Persistent GUI words, used alongside typed/imported words for the current run; headless ignores this field entirely and reads direct-child TXT files in `cards/` instead |
| `profiles` | User configuration overrides profiles with the same name and can add profiles; omitted rules in a configured profile are disabled, not merged with the original profile |

- The six rule keys are `phone`, `idcard`, `email`, `ip`, `api_key`, `custom`. The GUI's reserved custom token `__custom` cannot be overridden by configuration.
- GUI retains its compatible default fallback behavior if configuration cannot be read or JSON is invalid; only positive size and threshold values override defaults.
- `max_file_size` controls the pre-upload file size only; GUI file picking, fixed-directory quick reads and headless document input all use the same effective value. A configuration that already contains an old value such as `10485760` keeps that limit and is never migrated or overwritten because the default changed; adjust it locally if you want the 50 MiB range.
- DOCX/PPTX extraction is fixed at 200 MiB (209715200 bytes) per package. It is not configured by `max_file_size` and is not a process memory limit; see the [usage guide](usage.en.md#supported-file-formats-and-limits).
- Headless uses defaults if no configuration exists. If a file exists but is unreadable, has invalid JSON, has a non-object top level or contains invalid effective fields, the operation is rejected rather than silently changing policy. `custom_words` is ignored regardless of its type.
- MCP profiles must be explicitly selected by the user and valid locally; do not have the agent read configuration to discover them. For cards encoding, word limits, batch rejection and custom toggling, see [MCP inputs and rules](mcp.en.md#inputs-and-rules).

## Audit log

`data-masking/audit/audit.jsonl` under the user configuration directory records metadata only: `rule_id`, placeholders, hit counts, timestamps and file fingerprints. **It does not record original values or mapping relationships.** Audit write failures do not block the main workflow and are reported to the user.

The audit log is distinct from the mapping, which contains plaintext originals. Keep mappings local; never send them to a model or upload them to an online service.
