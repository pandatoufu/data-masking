# MCP integration and job reference

[中文](mcp.md) | [English home](../README.en.md) | [Usage guide](usage.en.md) | MCP guide | [Configuration](configuration.en.md) | [Building from source](development.en.md)

Headless runs as a **local stdio MCP subprocess**, exposing `prepare_document`, `confirm_operation` and `restore_document`. It does not chat, browse directories or hand originals or mappings to the model.

## Client setup

Client configuration needs **only the executable's full path**. Do not configure a working directory, project directory or subdirectories. These are generic `mcpServers` examples; specific clients may use different formats. This repository does not claim compatibility testing with any particular client.

Windows:

```json
{
  "mcpServers": {
    "data-masking": {
      "command": "C:\\Tools\\data-masking\\data-masking-agent.exe"
    }
  }
}
```

Linux:

```json
{
  "mcpServers": {
    "data-masking": {
      "command": "/home/you/opt/data-masking/data-masking-agent"
    }
  }
}
```

- Configuration has the same structure on both platforms; only `command` differs. Headless has no GTK/WebKit graphical dependency; Linux requires executable permission.
- Place the program in a **user-writable location separate from the model project**, such as `C:\Tools\data-masking\` on Windows or `~/opt/data-masking/` on Linux. An unwritable location fails explicitly; it is not silently relocated.
- The workspace is determined by the executable's directory, not the client's launch directory. GUI and headless placed together share directories and no-overwrite primitives: the GUI initializes five directories and creates `pending/` independently for quick input, while headless still requires six directories at startup. See the full [workspace contract](usage.en.md#workspace-directories).
- Check the version locally with `./data-masking-agent --version` in a POSIX shell or `.\data-masking-agent.exe --version` in Windows PowerShell. This neither starts MCP nor creates directories.
- Clients may not display initialization `instructions`. Safety guidance is also included in all three tool descriptions, but does not guarantee model compliance or constitute a permission boundary.

## Inputs and rules

- **Fixed source entry**: users place originals in the program's `original/` directory; calls pass one filename, such as `report.docx`. `../secret.txt`, `sub/notes.txt`, `C:\x.txt`, `\\server\share\a.txt`, `report.txt:ads`, `NUL.txt`, `notes.txt.` and trailing spaces are rejected. Rejection does not read out-of-bounds targets or echo private full paths. The agent neither needs nor should pre-read the original.
- **Name range matches the core**: `source_name` and `response_path` share the unified name policy: `.docx` and plain-text candidates are accepted (any extension or none, including dotfiles such as `.env` and `.gitignore`), while known-unsupported names such as `pdf`, `doc`/`docm`, `odt`, `rtf`, `ppt`/`pptx` and `xls`/`xlsx` are rejected. An accepted name does not mean the content was validated; encoding, size and DOCX package gates still run after reading. Project masked copies are named `input.masked.<ext>`, with extensionless names and dotfiles falling back to `input.masked.txt`.
- **Explicit project path for every call**: `project_path` must be an existing absolute directory explicitly specified by the user before that call. Do not guess from `cwd` or treat the workspace as the project. Paths equal to, ancestors of or descendants of the program workspace are rejected.
- **A profile is required and must be explicitly selected by the user**: use a key present in local `config.json` (built-in `strict` / `balanced` / `minimal` / `custom`, or a user-defined key). There is no silent fallback to `balanced`. If the user has not selected a profile, the agent must ask first; if already explicit, do not ask again. Never guess or default to `balanced`. Built-in balanced leaves email/IP/ID-card numbers untouched; strict only enables the six existing rules and **is not a safety guarantee**.
- Missing, empty-string or `null` `profile` immediately returns `failed` / `invalid_request`, with this fixed Chinese message: “缺少脱敏档位 profile。请先询问用户选择本机有效档位，明确选择后再调用；不得自行选择档位或原样重复调用。” It means: “Missing masking profile. Ask the user to select a valid local profile before calling; do not choose one yourself or repeat the unchanged call.”
- A nonempty profile that does not exist locally (including an unmatched whitespace-only string) returns `failed` / `invalid_profile` after path/configuration prechecks, with this fixed Chinese message: “profile 无效。请先询问用户确认本机配置中有效的档位名，明确选择后再调用；不得自行选择档位或原样重复调用。” It means: “Invalid profile. Ask the user to confirm a valid profile name in local configuration before calling; do not choose one yourself or repeat the unchanged call.”
- Both errors require asking the user first: **do not retry unchanged or switch profiles to retry**. Other parameter errors still use generic `invalid_request` and do not all indicate a missing profile. A client's required-field schema may block a missing parameter before the server receives it; initialization and parameter descriptions therefore also require asking first.
- **Do not have the agent read configuration to discover profiles**: built-in names are only defaults; local configuration can override them or define others. Have the user confirm a valid profile instead of letting the agent open `config.json` or read `custom_words`.
- **Headless custom words come only from direct-child TXT files in `cards/`**: `custom_words` in `config.json` is entirely ignored, not merged or migrated. Old words do not take effect or enter confirmation fingerprints; manual migration is not required. Each `prepare_document` rereads cards when custom is enabled, without recursion or reading subdirectories/non-TXT files.
  - One word per line, trimmed, with blank lines ignored. Files are merged in ascending filename-byte order and then line order; exact byte-level deduplication preserves first-occurrence order. The `.txt` extension is case-insensitive.
  - Strict UTF-8 decoding is tried first, then strict GBK; NUL / UTF-16 is rejected. Limits: ≤ 1 MiB per file, ≤ 256 characters per word, ≤ 1000 words after merging and deduplication.
  - If custom is enabled but there is no direct-child TXT file, the result is `failed` / `invalid_config`: “自定义词规则已启用，请先在程序目录的 cards 文件夹放入有效 TXT 词库文件。” This asks the user to place a valid TXT word list in the program's cards directory first.
  - If any TXT is empty/all blank lines, over a limit, incorrectly encoded, unreadable, a link, directory or special file, the entire batch is rejected: “cards 词库无效或无法读取，请在本机检查 TXT 文件的编码、内容及输入限制后重试。” This asks the user to check encoding, contents and input limits locally. Bad files are not skipped; there is no cache or fallback to configured words.
  - When custom is disabled, no word files are listed or read and the word set is empty. Empty/bad TXT files do not affect the operation; the directory itself remains subject to startup safety checks.
  - Tools do not require the agent to read word lists or originals, and do not accept caller-supplied sensitive-word arrays.
- Missing configuration uses built-in defaults. Existing configuration that is unreadable, invalid JSON, a non-object at the top level or invalid in effective size/threshold fields rejects the operation rather than silently changing policy. `custom_words` is ignored whether an array, object, scalar or null; GUI configuration behavior is unchanged.

> Built-in balanced enables custom, so users should prepare valid local cards TXT files before using balanced or strict with default configuration.

## Cooperative no-read guidance and normal project workflow

MCP initialization `instructions` and all three tool descriptions include the same guidance: the agent must not use file reading, search, terminal or other tools to read, preview or upload contents of `original/`, `cards/`, `mapping/`, `restored/`, or private records in `.agent-state/`. Only the local program processes these. For configuration/word-list errors, ask users to check locally; do not direct the agent to open `config.json` or submit word values or contents.

Only after delivery (`delivered`) may the agent read the masked copy returned as `project_file`. This is not the original or mapping and does not imply whole-package or semantic-level non-disclosure. The agent may read/write replies normally in the specified project and pass `response_path` to `restore_document`; the local program restores using the paired mapping. Afterwards, use only anonymous status and do not read restored contents.

These are **cooperative soft constraints, not a permission boundary or OS sandbox**. Clients may ignore initialization instructions and models may not comply. Guidance does not restrict other client tools or block malicious programs running as the same user. Both risk confirmations must be referred to the user; the agent cannot approve them itself.

## Jobs, pairing and recovery

- Each `prepare_document` generates a random `job_id`, binding the original, effective policy and paired mapping. Pairing is not guessed from filenames; callers cannot specify a mapping.
- GUI results placed directly in `masked/`, `mapping/`, `restored/` do not automatically become headless jobs. Job records exist only in the software's `.agent-state/` directory.
- After restart, old unconfirmed jobs fail and old credentials become invalid; run `prepare_document` again. Delivered/restored jobs and their pairings can recover across restarts.
- Historical jobs are not cleaned up automatically. After exiting GUI/headless, manage complete jobs yourself; do not delete paired mappings individually.
- The only guarantee is that incomplete state is not treated as success; there is no promise of zero leftovers after power loss or disk transactions.

## Two types of confirmation

`prepare_document` may pause at two stages. Their statuses, messages and authorizations are not interchangeable:

| Stage | Status and disclosure | Available actions |
| --- | --- | --- |
| High-frequency confirmation before replacement | `awaiting_frequency_confirmation`; only the number of over-threshold words, descending frequencies and threshold, without actual words/locations/context; no masking or artifacts yet | `continue_masking` / `cancel` |
| Residual confirmation after masking verification, before delivery | `awaiting_residual_confirmation`; discloses residual counts and the risk of sensitive content; private pair saved, no project copy yet | `deliver_with_residual_risk` / `cancel` |

The agent must ask the user and call `confirm_operation` only after an explicit answer, never approve itself. There is no generic `force` parameter. Credentials bind the session, job and confirmation type. Changes to files, rules, word lists or project return `confirmation_stale` and terminate the job; prepare again.

This is **cooperative confirmation**, preventing mistakes in cooperative scenarios, not authenticated human identity or an OS sandbox. Malicious same-user programs may still read files directly.

## Results and return-trip boundaries

- Restored results exist only locally in `restored/<job_id>/<attempt_id>/`. The agent receives only status, restored counts and filtered anonymous warnings, not restored contents; there is no download endpoint.
- Each job has **one successful return trip**: retrying the same reply bytes idempotently returns the original status. Restoring different bytes afterwards returns `invalid_state` without overwriting the successful result.
- The following warnings are independent reference signals. They do not block local results or necessarily mean the wrong mapping was used:
  - `text_mismatch`: restored DOCX text differs from the mapping's `SourceTextSHA256`; comparison is text, not whole ZIP bytes.
  - `sha_mismatch`: restored plain-text byte SHA differs from the original.
  - `cross_format`: cross-format input lacks a same-format text baseline, such as a text reply for a DOCX source or vice versa.
  - `residual_reverse`: leftover placeholders; `audit_failed`: audit failure. Both are separate warnings.
  - Same-byte retries preserve original warning categories, fixed messages and `audit_failed`, without new files or repeated processing.
- DOCX restoration recovers only mapped text, not comments, revision history or Word layout. Unchecked content such as images and properties is outside the guarantee.
- Unknown sensitive information and semantic-level non-disclosure are not guaranteed; results indicate only rule-based processing. See the [home page](../README.en.md#security-and-usage-boundaries) for full DOCX and restoration boundaries.
