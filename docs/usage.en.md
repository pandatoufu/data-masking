# Detailed usage guide

[中文](usage.md) | [English home](../README.en.md) | Usage guide | [MCP guide](mcp.en.md) | [Configuration](configuration.en.md) | [Building from source](development.en.md)

## Supported file formats and limits

| Item | Details |
| --- | --- |
| Plain text | Any extension, no extension, or dotfiles such as `.env` and `.gitignore` (case-insensitive) |
| Plain-text examples | `txt`, `md`, `csv`, `json`, `yaml`, code, logs (non-exhaustive) |
| Known unsupported | `pdf`, `doc`/`docm`, `odt`, `rtf`, legacy or macro/template/show formats such as `ppt`/`pptm`/`pot`/`pps`, `xls`/`xlsx`, `pages`/`numbers`/`key` and common container, program and image extensions; rejected by name, so convert or save as `.docx`/`.pptx` |
| Document extensions | `docx` (Transitional / Strict; macro-enabled, encrypted and digitally signed packages are rejected) |
| Presentation extensions | `pptx` (normal Transitional / Strict presentations only; `.ppt`, `.pptm`, templates/shows, encrypted or signed packages and unknown comment families are rejected) |
| File size | Plain text, DOCX and PPTX share a 50 MiB (52428800-byte) default per file, adjustable with the existing `max_file_size`; an explicit older value wins |
| Plain-text encoding | NUL / UTF-16 is rejected; strict UTF-8 is tried first, then strict GBK; otherwise rejected |
| Line endings | CRLF / LF are preserved per line, without normalization |
| DOCX extraction budgets | At most 2048 entries, 200 MiB (209715200 bytes) of actually extracted data across all parts, 20 MiB per XML part and 128 levels of XML nesting |
| PPTX extraction budgets | The same independent budgets: at most 2048 entries, 200 MiB (209715200 bytes) of actually extracted data across all parts, 20 MiB per XML part and 128 levels of XML nesting; only Store/Deflate is accepted |
| Word files | ≤ 1 MiB per file, ≤ 256 characters per word, ≤ 1000 participating words per run |

The name policy is a finite known-format set, not general format detection: unlisted extensions and extensionless files are plain-text candidates whose acceptance depends on the size and content gates; an accepted name does not mean the content was validated. Binary detection is only the NUL / UTF-16 plus strict-encoding heuristic and does not guarantee detecting every binary. Plain text is replaced literally, so code or structured data is not guaranteed to remain syntactically valid after replacement.

### GUI upload and temporary-resource boundary

- Mask and restore read documents and pasted content part by part in the backend under the effective `max_file_size`, keeping at most limit + 1 bytes for the over-limit decision instead of reading the whole document before rejecting it. The over-limit message reports only the limit and never fabricates a full byte count that was not retained.
- Word files keep their independent 1 MiB limit; when the rule is off, word input is still ignored silently and never fails because of its size.
- A normal GUI request does not cause the app to write the original multipart content to temporary files. If a compatibility caller has already parsed the form, its temporary resources are closed and removed before the request ends; a cleanup failure replaces the result with a localized message. Forced process termination and caching/spooling inside the WebView or operating system are outside the app's request-cleanup guarantee.
- The manual mapping entry keeps its existing parsing acceptance range; this change adds no new 32 MiB business hard limit there, while the `mapping/` quick directory keeps its 32 MiB transfer protection. Other form fields keep separate protocol budgets, but this change does not claim that every request field is strictly limited.
- The front end still holds the whole upload, the base64 response and any saved copy. Mapping, WebView and operating-system cache costs are not included in the document budgets. The 50 MiB file and 200 MiB extraction numbers are functional boundaries, not process memory limits.

### Large-file resources and measured boundary

The 50 MiB file limit and 200 MiB extraction budget are functional gates, not memory or time caps. One explicit measurement (2026-10-26, Ubuntu 26.04 LTS / Linux 7.0.14 x86_64 / AMD EPYC 16 vCPU / 7.3 GiB RAM, Go 1.26.0, headless test process from `go test -c -tags large` without GUI/WebView initialisation; median of 2 rounds per sample, 2 ms heap sampling) produced:

| Sample (real 50 MiB) | Mask + restore median | MemStats.TotalAlloc delta | Sampled Go heap peak |
| --- | --- | --- | --- |
| Plain text | about 25.3 s | about 577 MB | about 420 MB |
| DOCX (media-heavy) | about 0.70 s | about 1.86 GB | about 618 MB |
| PPTX (media-heavy) | about 0.80 s | about 1.87 GB | about 638 MB |
| 10 MiB plain-text baseline | about 5.4 s | about 115 MB | about 64 MB |
| 10 MiB DOCX baseline | about 0.21 s | about 373 MB | about 125 MB |
| 10 MiB PPTX baseline | about 0.16 s | about 379 MB | about 115 MB |

- The OS peak RSS measured in the same process (`VmHWM` from `/proc/<pid>/status`, polled every 50 ms) was about 647 MB. A separate GUI-handler path run (40 MiB text mask/restore, 40 MiB DOCX and an over-limit request) peaked at about 760 MB.
- `TotalAlloc` is cumulative allocation, not a peak. The Go heap peak is a sampled approximation and excludes the WebView, operating-system caches, mapping and front-end `ArrayBuffer`/base64 copies. Do not treat any figure as a fixed memory cap or performance promise.
- Plain text is markedly slower than DOCX/PPTX on large files (about 2.4 MB/s; rule-scanning cost grows with the file), so 50 MiB of text can take tens of seconds. The single-operation guard still prevents concurrent submissions.
- Not yet measured: the native Windows GUI/WebView, 50 MiB samples produced by real Word/PowerPoint, mapping and front-end buffers combined in a real browser, low-memory machines and network drives. Different hardware, Go versions and content change the results substantially.

### PPTX processing scope

- Both directions process native text in slides (including hidden slides and declared slides not referenced by the main sequence), layouts, slide masters, notes slides, notes masters and handout masters, covering titles, body text, ordinary text boxes, text in nested groups and native table cells.
- Speaker notes are preserved and processed in both directions. Comments and comment-specific people information are removed before text scanning and are never restored. Ordinary document author properties are outside comment-people cleanup.
- Hidden shapes and both `Choice`/`Fallback` storage branches are scanned; counts describe storage occurrences, not what is visible on screen. Numbers split across runs in one paragraph match and restore, but paragraphs, text bodies, shapes, cells, hard breaks and fields are hard boundaries and are never joined across.
- Page order/size/background, shape position/size/rotation/z-order, table grids and merges, themes, paragraph/run properties, images/media and animation/transition references are preserved. A cross-style match uses the first matched run's style while unmatched text and properties remain. Necessary escaping and `xml:space` differences are not format changes.
- **Not checked and not promised**: charts and their caches/data sources, SmartArt, embedded Excel/OLE objects, text in images, properties, field codes and link targets, plus review history other than comments. These parts are retained unchanged, but refreshing data/fields or some animations can reintroduce unprocessed values; results show a short unchecked summary and never claim the whole package is safe.
- Line wrapping, overflow, autofit and cross-run mixed-style distribution after replacement are not guaranteed to match the original. No PPTX renderer is provided. Plain-text replies cannot be written back into PPTX, and DOCX/PPTX conversion is not supported.

## Rules and profiles

There are six rule types: `phone` (mainland China mobile numbers), `idcard` (18-digit Chinese ID-card numbers), `email`, `ip`, `api_key` (prefixes such as `sk-`, `AKIA`, `ghp_`, `xox-`) and `custom` (custom words).

| Built-in profile | Behavior | Enabled rules |
| --- | --- | --- |
| All rules (`strict`) | Enables all six rules; not a guarantee of absolute safety | phone, idcard, email, ip, api_key, custom |
| Balanced (`balanced`, default) | Leaves email, IP and ID-card numbers untouched | phone, api_key, custom |
| API keys only (`minimal`) | Processes API keys only | api_key |
| Custom | Toggle each rule and select words for this run | All adjustable |

The three presets are read-only in the UI; select Custom to adjust rules. The GUI uses `__custom` internally for its custom profile; headless `profile` values are actual keys in the local configuration, as described in the [MCP guide](mcp.en.md). User configuration can override presets with the same name. The UI summary and rule details show the effective configuration; do not infer coverage from the profile name alone.

- Scan priority: `custom` > `idcard` > `phone` > `email` > `ip` > `api_key`. Matched spans are consumed and not replaced again.
- Boundary guards avoid matching ordinary words: for example, `risk-aware` and `task-off` do not match `api_key`.

### Custom words

- **Literal, exact, case-sensitive matching**, not regular expressions: `a.b` matches only `a.b`, not `axb`.
- Two inputs: type into the text box or import TXT. Words are separated by lines, trimmed, deduplicated and merged; blank lines are ignored.
- Limits: ≤ 1 MiB per word file, ≤ 256 characters per word, ≤ 1000 words in the current list and ≤ 1000 in the merged participating set. An invalid batch is rejected with line numbers, preserving the previous valid list.
- Words for the current run are selected by default; deselected words do not participate. Disabling `custom` disables the word area.
- Importing again replaces the previous file's words but retains typed words. Search only filters the display; bulk selection during a search affects only matching results. Clearing the search does not change selections.
- If a word exceeds the per-run frequency threshold (20 by default), a preview requires confirmation. Words for this run may be deselected; persistent words cannot be deselected in this panel. Results are recalculated using the current selections after confirmation.
- The GUI uses persistent `custom_words` from `config.json` and explicit TXT imports; it does not scan `cards/` automatically. Headless ignores `custom_words` entirely and reads only direct-child TXT files in `cards/`; see [MCP word-list rules](mcp.en.md#inputs-and-rules).

### Placeholders

Placeholders look like `[PHONE_1]`, with prefixes corresponding to rules:

| Rule | Prefix |
| --- | --- |
| phone | `PHONE` |
| idcard | `IDCARD` |
| email | `EMAIL` |
| ip | `IP` |
| api_key | `APIKEY` |
| custom | `CUSTOM` |

Within one masking run, the same original value under the same rule always uses the same placeholder. Numbers increase in first-occurrence order. Preflight rejects source text that already contains valid placeholder syntax to avoid collisions.

## Masking

1. Start the app; it opens on the Mask page.
2. Choose one file (any plain-text extension or none, or `.docx`/`.pptx`), or pick a direct file from `original/` in the dropdown beside the program. Windows supports click-to-select and drag-and-drop; Linux supports click-to-select only. A dropdown reads a file only after an explicit selection.
3. Select a profile: Balanced (`balanced`) is the default. For finer control, choose Custom and select individual rules and words.
4. Optionally type or import custom words, or pick a single TXT word card from `cards/`; check that the rule and desired words are enabled.
5. Run masking. If frequency or residual confirmation appears, inspect the prompt before continuing. Residual warnings include locations and counts; they do not mean the result is safe.
6. Keep both the masked copy and paired mapping. They are saved automatically when the workspace is available, and may still be saved separately. Saving only one leaves the other marked unsaved; no rerun is needed.

Successful DOCX results show actual comment-deletion / revision-acceptance status and counts, plus a short summary of unchecked content. PPTX shows separate comment/people/part/resource cleanup counts and its own unchecked summary, without reusing Word revision concepts. The full boundaries are on the [home page](../README.en.md#security-and-usage-boundaries), rather than permanently displayed before execution. Plain-text encoding is available in processing details.

## Restoring

1. Open the Restore page and pick a direct file from `pending/`, choose one manually with “choose file to restore”, or switch to text input and paste the reply. Only the active input is submitted.
2. Select the paired local `.mapping.json` (a direct JSON from `mapping/`, a manual choice, or “Use current mapping” for the latest successful masking result). Later masking does not silently replace the selected mapping; session mappings are not restored after exit.
3. Run restore and keep the result. It is saved automatically when the workspace is available, and Save As remains available.

- Placeholder matching supports case / full-width best-effort matching; placeholders rewritten or translated by the model cannot be recovered.
- Unknown leftover placeholders produce a warning, possibly indicating the wrong mapping, but do not block saving.
- Restoring DOCX with a DOCX mapping produces `.docx`; pasted text produces `.txt`. Cross-format warnings mean text consistency cannot be established.
- PPTX uses path A: PPTX source → masked PPTX → model-edited PPTX → local PPTX restore. A PPTX mapping is version 3 and accepts only a same-format PPTX reply; using a PPTX mapping with text/DOCX, or an older text/DOCX mapping with PPTX, is explicitly rejected without producing a file under a disguised extension. PPTX restore keeps structural edits in the reply, replaces only mapped text, and reports text-reference differences as advisory.
- Restore recovers only mapped text; text restoration does not recover Word layout, and PPTX restore does not recover comments, the original template, original ZIP bytes or cross-run mixed-style distribution. See the [security boundaries](../README.en.md#security-and-usage-boundaries) for DOCX/PPTX review history and unprocessed content.

## Saving and exiting

- **Explicit Save As**: native dialogs remain available on both platforms for saving artifacts to other supported local locations.
- **Automatic saving**: when the Windows/Linux workspace is available, masked copies go to `masked/`, mappings to `mapping/`, and restored results to `restored/`. Automatic saving counts as saved.
- **No overwrite on name collisions**: automatic saving adds `-N` (`-1`, `-2`, …) to the stem; the masked copy and mapping share the suffix. Manual Save As cannot overwrite an existing file; choose another name.
- **File protection**: source files, existing files and symlinks are never overwritten, even if the target is created concurrently. Concurrent publication through multiple entry points returns a retryable busy state rather than mixing pairs or waiting indefinitely.
- **Failure handling**: ordinary processing/write failures publish no partial target and do not overwrite originals. If publication succeeds but temporary cleanup fails, the app reports both the saved result and cleanup warning. It does not promise zero leftovers after power loss or cross-file transactions.
- **Startup degradation**: if any of the five required automatic-saving GUI directories cannot be created or is occupied by a regular file/link, all automatic saving is disabled; the app still starts and supports manual Save As. `pending/` is created independently by the GUI; its failure only disables the corresponding quick-input entry with local feedback and does not change automatic-saving availability. `.agent-state/` never affects the GUI. A single write failure after startup affects only that operation, not overall availability.
- **Exit protection**: exiting requires confirmation when artifacts have been saved neither automatically nor manually; otherwise exit is silent. Processing, writing or cleanup blocks exit while busy. Switching views preserves results; rerunning requires confirmation before replacing unsaved results.

## Workspace directories

The root is fixed to the **executable's directory** (the real directory of `os.Executable()` after resolving symlinks), not the process working directory, and is not configurable. Place the program in a **user-writable directory outside the model project**, such as `C:\Tools\data-masking\` on Windows or `~/opt/data-masking/` on Linux.

**Five GUI directories**: `cards/`, `masked/`, `mapping/`, `original/`, `restored/`; their state decides whether automatic saving is available. The GUI also creates `pending/` independently in the program directory as a quick-input directory for files to restore. Headless requires six directories: `cards/`, `masked/`, `mapping/`, `original/`, `pending/`, `restored/`. Programs placed together share them; separately placed programs have independent workspaces and never read the other entry point's private records.

| Directory | Entry point | Purpose |
| --- | --- | --- |
| `cards/` | GUI + headless | The GUI never scans or auto-loads the word library; only an explicit selection lists direct-child TXT files and imports one word card. Headless reads direct-child TXT files automatically only when custom is enabled |
| `original/` | GUI + headless | Users place originals here; the GUI lists supported direct-child documents only on explicit selection and reads just the chosen file, never importing automatically |
| `masked/` | GUI + headless | Masked copies |
| `mapping/` | GUI + headless | Paired plaintext mappings, **local restore only; never send out**; the restore page can list direct-child JSON files and read the single file the user explicitly selects |
| `pending/` | GUI + headless | Files awaiting restore: the GUI creates it independently and only lists supported direct-child documents, reading one file after explicit selection; it does not recognise jobs or read `.agent-state/`. Headless uses it for project replies and job pairing |
| `restored/` | GUI + headless | Restored results |

- Each entry point idempotently creates missing directories without clearing, deleting or recreating existing content. Existing `pending/` and `.agent-state/` data is **not automatically deleted or cleaned up**.
- Required directories occupied by regular files, symlinks or Windows reparse points are unavailable; links are not followed for writing. The GUI degrades on its five automatic-saving directories (a `pending/` placeholder only disables that quick-input entry), while headless explicitly fails on its six.
- `.agent-state/` contains private headless job records, not business data directories or configuration. The GUI does not create, read or modify it, and does not recognise the jobs or recovery history inside it.
- No-overwrite saving relies on hard links (local NTFS / ext4 and similar). Unsupported filesystems such as FAT/exFAT fail explicitly; choose a supported local directory. There is no fallback to potentially overwriting writes.

## Fixed-directory quick selection

- Mask page: `original/` (DOCX, PPTX and plain-text candidates) and `cards/` (a single TXT word card). Restore page: `pending/` (DOCX, PPTX and plain-text candidates) and `mapping/` (JSON mappings). These four entries sit alongside the existing manual buttons, paste mode and “Use current mapping”.
- Document candidates follow the shared name policy: common/new extensions, extensionless names and dotfiles such as `.env` are listed, while known-unsupported formats such as `pdf`/`rtf`/`xls`/`pptm` are not; an accepted name does not mean the content was validated, and an explicit selection is still checked by the original encoding, size and DOCX/PPTX gates. `cards/` remains TXT-only and `mapping/` remains JSON-only.
- Only **direct-child** regular files are listed (extension matching is case-insensitive). The app does not recurse, does not follow symlinks or Windows reparse points, and does not read file contents while listing. “Refresh” updates only that list; it does not re-read the current file or clear loaded input or results.
- A file is read only after an **explicit selection**, with a size limit, and is then handed to the existing import/parse/multipart/confirmation/automatic-save/Save-As chain. The list is decoupled from the current snapshot: a name disappearing from the list does not invalidate loaded content. Nothing is selected by default, and a placeholder, empty list or read failure never clears loaded input.
- Selections and snapshots live in this session's memory only. Nothing is written to `localStorage` / `sessionStorage` and nothing is restored on restart. While saving or processing is busy, refresh and further selections are blocked, and late responses never overwrite a newer selection.
- Quick-read limits: documents follow the effective configured `max_file_size` (50 MiB by default) and word cards are 1 MiB. The `mapping/` quick entry has an additional **32 MiB transfer-protection limit**; use “Choose mapping” for larger files (the manual entry's accepted range is unchanged and no new 32 MiB business hard limit was added). Oversized, wrong-format or invalid content is still rejected by the original business validation.
- This is not the MCP job manager: the GUI does not recognise jobs/attempts, does not read `.agent-state/`, does not pair mappings automatically, does not merge all `cards/` word cards, and never copies a selected file into `original/` or `pending/`. A mapping chosen from a directory requires the same pairing confirmation as a manual choice and never masquerades as “Use current mapping”.

## Artifact naming

| Artifact | Name |
| --- | --- |
| Masked copy | `<original name>.masked.<ext>`, e.g. `notes.masked.txt`, `合同.masked.docx`, `报告.masked.pptx`; extensionless names and single-segment dotfiles fall back to `.txt`: `README.masked.txt`, `.env.masked.txt` |
| Mapping | `<original name>.masked.mapping.json`, e.g. `README.masked.mapping.json`, `.env.masked.mapping.json`; DOCX uses version 2 and PPTX version 3; `source_file` keeps the real input name |
| Restored DOCX | `<source stem>.restored.docx` |
| Restored PPTX | `<source stem>.restored.pptx`; an empty source name falls back to `restored.pptx` |
| Restored text | `<source stem>.restored.<actual extension>`; pasted text, extensionless names and single-segment dotfiles use `.txt`, e.g. `README.restored.txt`, `.env.restored.txt` |

These are GUI suggested names; headless saves by job, as described in the [MCP guide](mcp.en.md). **Mappings contain plaintext originals: keep them local and never send them to a model or upload them to an online service.**
