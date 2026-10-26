# data-masking — local pre-LLM sanitizer

[中文](README.md) | [English](README.en.md)

> Strip sensitive words and confidential information **locally** before sending files to an LLM, then restore the response with the paired mapping when needed.

A Go + Wails v2 desktop app: it runs in its own window, opens no browser, creates no business TCP listener, and uploads nothing. The same core also runs as a **local stdio MCP subprocess** for local model clients and supports headless regression tests.

---

## Features

- **Two-page, one-file workflow**: separate `Mask` and `Restore` pages, one file at a time.
- **Six built-in rule types plus custom words**: phone numbers, ID cards, email, IP, API keys, and literal-exact custom words.
- **Four profiles**: three read-only presets `strict` / `balanced` / `minimal`, plus a `custom` profile with per-rule toggles.
- **Reversible masking**: bracketed placeholders plus a paired plaintext mapping that restores placeholders found in the LLM reply.
- **DOCX with layout preserved**: processes text in the body, tables, headers/footers, footnotes/endnotes and native text boxes, keeps tables and images, deletes comments and accepts supported revisions.
- **PPTX path A with structure preserved**: processes native text in titles, body text, text boxes, grouped shapes, native tables, hidden slides, layouts, masters and notes; notes are kept, comments and people information are removed, and charts/SmartArt/embedded objects/text in images are retained with an unchecked-scope disclosure.
- **Encoding tolerance**: plain text auto-detects UTF-8 / GBK; CRLF / LF line endings are preserved per line and never normalized.
- **Automatic GUI output**: optional; writes artifacts into fixed folders beside the executable and never overwrites existing files.
- **Fixed-directory quick selection**: dropdowns pick direct files from `original/` and `cards/` on the Mask page and from `pending/` and `mapping/` on the Restore page; a file is read only after an explicit selection, and all manual entries and paste mode remain.
- **Native save and file protection**: explicit save dialogs; source files, existing files and symlinks are never overwritten.
- **No half-products on failure**: a failed step produces no partial artifact and never overwrites the original.

---

## Quick start (desktop)

### 1. Get the program

Pick the Windows or Linux package for your platform from this repository's [Releases](https://github.com/pandatoufu/data-masking/releases) page, following that release's notes. If no package for your platform exists yet, build it from source as described in [Building from source](docs/development.en.md); end users of a released build need no Go, Wails or Node installation.

### 2. Place the program

Unpack or copy it to a **user-writable local directory that is not inside your model project**, for example:

- Windows: `C:\Tools\data-masking\`
- Linux: `~/opt/data-masking/` (keep the executable bit)

The program directory is the workspace root (the real directory of `os.Executable()` after resolving symlinks). The launch directory is never used, and there is no setting for it. The filesystem must support **hard links** (local NTFS / ext4 and similar); FAT/exFAT is unsupported and the app fails explicitly, asking you to choose a supported local directory.

### 3. Install runtime prerequisites

- **Windows 10/11 amd64**: system **Evergreen WebView2**. If it is missing or outdated, the app shows a native prompt and opens Microsoft's official install page **only after you confirm**; it never installs silently.
- **Linux (Fedora / Ubuntu) amd64**: **GTK3**, **WebKitGTK 4.1** and a normal graphical / session bus session. Ubuntu packages are `libgtk-3-0` and `libwebkit2gtk-4.1-0`; Fedora packages are `gtk3` and `webkit2gtk4.1`. Missing shared libraries can fail before any window appears.

### 4. Mask

1. Start the app; the `Mask` page opens by default.
2. **Choose a file** (any plain-text extension or none, or `.docx`/`.pptx`), or pick a direct file from `original/` (refresh first; it is read only after selection). Click-to-select and drag-and-drop work on Windows; Linux supports click-to-select only.
3. **Choose a profile**: the default `balanced` masks phone numbers, API keys and custom words only and **leaves email, IP and ID-card numbers untouched**. Pick `strict`, `minimal` or `custom` when you need more.
4. (Optional) **Enter or import custom words**: typed text, a TXT import, or a single TXT word card from `cards/`; matching is literal and case-sensitive.
5. **Run masking** and keep both artifacts:
   - masked copy: `<original name>.masked.<ext>` (extensionless names and dotfiles such as `.env` fall back to `.masked.txt`)
   - paired mapping: `<original name>.masked.mapping.json`
6. When the workspace is usable the app also writes them into `masked/` and `mapping/`; each artifact can still be saved separately. If only one is saved, the page tells you the other is still unsaved.
7. **Send only the masked copy to the LLM.** The `.mapping.json` contains the plaintext lookup table: **keep it local and never send it to a model or upload it to any online service.**

### 5. Restore

1. Open the `Restore` page and pick a direct file from `pending/`, choose one with “choose file to restore”, or paste the reply text.
2. Select the **paired mapping** saved locally (`.mapping.json`, from the `mapping/` dropdown or chosen manually) and make sure you picked the right one.
3. Run restore and save the result.

- Placeholders are matched with case / full-width **best-effort**; placeholders the LLM rewrote or translated **cannot be recovered**.
- Unknown leftover placeholders trigger a prominent warning (likely a wrong mapping) but do not block saving.

See the [detailed usage guide](docs/usage.en.md) for the full format/budget limits, rule details, GUI operations and directory contract.

### Optional: local MCP access

`data-masking-agent` in the same program directory (`.exe` on Windows) runs as a **local stdio MCP subprocess** so MCP-capable clients can mask and restore before sending, exposing `prepare_document`, `confirm_operation` and `restore_document`. The desktop quick start **does not require** an MCP client.

Client JSON, `profile` selection, the cards word list, the two manual confirmations, restore idempotency and the no-read guidance are documented in the [MCP guide](docs/mcp.en.md). That guidance is a **cooperative soft constraint, not a permission boundary**.

---

## Platforms and formats

- **Plain text**: any extension is accepted, including no extension and dotfiles such as `.env` and `.gitignore` (extension comparison is case-insensitive); typical examples are `txt`, `md`, `csv`, `json`, `yaml`, code and logs. Encoding is detected in order: NUL / UTF-16 is rejected, then strict UTF-8, then strict GBK, otherwise rejected. CRLF / LF are preserved per line without normalization.
- **Known unsupported**: office documents such as `pdf`, `doc`/`docm`, `odt`, `rtf`, legacy or macro/template/show formats such as `ppt`/`pptm`/`pot`/`pps`, `xls`/`xlsx`, plus `pages`/`numbers`/`key`, containers such as `zip`/`7z`/`rar`/`gz`, programs such as `exe`/`dll`/`so` and images such as `png`/`jpg`/`gif` are rejected by name; decodable bytes do not make them plain text. Convert them with the original program or save as `.docx`/`.pptx`; renaming alone is not recommended.
- **DOCX**: Transitional and Strict are both accepted; macro-enabled, encrypted and digitally signed packages are rejected.
- **PPTX**: normal `.pptx` Transitional and Strict presentations are accepted, path A only (PPTX source → masked PPTX → model-edited PPTX → local PPTX restore); a PPTX mapping pairs only with a PPTX reply. Macro-enabled, template/show, encrypted or signed packages, unknown comment families and native text that cannot be reliably enumerated are rejected.
- **Size limits**: plain text, DOCX and PPTX share a default 50 MiB (52428800 bytes) per file, adjustable with the existing `max_file_size`; an explicit value already in your configuration wins. Word files remain ≤ 1 MiB, ≤ 256 characters per word, ≤ 1000 participating words per run. DOCX/PPTX packages also have a fixed 200 MiB total-extracted budget that the existing configuration cannot change, and it is not a process memory limit.
- The name policy is a finite known-format set, not general format detection; an accepted name does not mean the content was validated. Binary detection is only the NUL / UTF-16 plus strict-encoding heuristic and **does not guarantee detecting every binary**. Plain text is replaced literally, so code or structured data such as JSON/CSV is **not guaranteed to remain syntactically valid** after replacement.

Full limits and DOCX/PPTX extraction budgets are in the [usage guide](docs/usage.en.md#supported-file-formats-and-limits); platform-specific behavior and build requirements are in [Building from source](docs/development.en.md).

---

## Security and usage boundaries

### Data and rules

- **Never send the mapping**: the paired `.mapping.json` contains the plaintext lookup table and is for local restore only.
- The tool only applies its rules: it **does not guarantee that unknown sensitive information is found, nor semantic-level non-disclosure**. Treat results as "processed by the rules", not "absolutely safe". Residual checks only verify rule coverage; hits produce location and count warnings, and you may still export after confirming.
- `balanced` leaves email, IP and ID-card numbers untouched by default; `strict` only enables the six existing rules and is likewise not a security guarantee. The phone and ID-card rules target **mainland China** numbers.

### DOCX scope

- Processed text: **body, tables, headers/footers, footnotes/endnotes and native text boxes**; images and table structure are kept.
- After comments are deleted and supported revisions accepted, the **review history cannot be recovered**.
- **Out of scope**: text inside images, document properties, field codes, data binding sources, hyperlink targets, charts/SmartArt and embedded attachments; refreshing fields or bindings **may reintroduce unprocessed content**.

### PPTX scope

- Processed text: **slides (including hidden and unreferenced declared slides), titles, body text, text boxes, grouped shapes, native tables, layouts, slide masters, notes slides, notes masters and handout masters**; notes are kept and processed in both directions, while page/shape/table/image/media and animation references are preserved.
- Both directions remove supported classic/modern comments, anchors, people parts and comment-only resources; **comments cannot be recovered**. Unknown comment families are rejected with guidance to delete comments in PowerPoint and save again.
- **Out of scope**: charts and data sources, SmartArt, embedded Excel/OLE objects, text in images, properties, field codes, link targets and review history other than comments; these are retained unchanged and results show a short unchecked summary. **Refreshing data/fields or some animations may reintroduce unprocessed content**.
- Line wrapping, overflow, autofit or cross-run mixed styling after placeholder-width changes are not guaranteed to match the original. No rendering is provided and DOCX/PPTX conversion is not supported.

### Restore boundaries

- Placeholder matching is case / full-width **best-effort**; placeholders rewritten or translated by the model cannot be recovered.
- Restore recovers only the mapped text: it does **not** restore review history, the original ZIP bytes or cross-run mixed styling, and restoring text **does not restore Word layout**; PPTX restore also **does not restore the original template or cleared comments**.
- PPTX restore keeps structural edits in the reply and replaces only mapped text; a text-fingerprint difference is an advisory reference and is not an assertion that the wrong mapping was used.
- Each headless job has exactly one successful return trip; return-trip warnings are reference signals and do not block local results.

### Local operation

- The desktop app uploads nothing and opens no business TCP listener; the MCP entry is a local stdio subprocess that never hands the original or mapping to the model.
- The MCP safety guidance is a **cooperative soft constraint**: clients may ignore it and models may not follow it. It is not a permission boundary or OS sandbox.
- Automatic output relies on hard links and **never overwrites** source files, existing files or symlinks; if a required directory is unusable, the GUI degrades to manual save only.

---

## More documentation

These reference guides are available in both English and Chinese; use the links at the top of each page to switch languages:

| Document | Contents |
| --- | --- |
| [Detailed usage guide](docs/usage.en.md) | Formats and budgets, rules and profiles, custom words, GUI operations, saving and workspace, artifact naming |
| [MCP guide](docs/mcp.en.md) | Client setup, profile and cards rules, job pairing, the two confirmations, return trip and no-read guidance |
| [Configuration and audit](docs/configuration.en.md) | Config paths and fields, GUI/headless differences, profile overrides, audit log |
| [Building from source](docs/development.en.md) | Toolchain, tests and type checks, build modes, artifact layout, icon resources |

---

## License

This project is licensed under the [MIT License](LICENSE).
