# Building from source and development

[中文](development.md) | [English home](../README.en.md) | [Usage guide](usage.en.md) | [MCP guide](mcp.en.md) | [Configuration](configuration.en.md) | Building from source

## Development prerequisites

- Go 1.23.0 or newer; Wails SDK/CLI pinned to v2.11.0.
- No Node/npm runtime or build dependency. Static resources in `web/` are embedded with `go:embed`.
- Native Linux GUI builds require a C compiler (gcc), GTK3 and WebKitGTK 4.1 development libraries, with `CGO_ENABLED=1`. Runtime libraries are not development libraries; see the [home page](../README.en.md#platforms-and-formats) for runtime prerequisites.
- Native Windows GUI builds require the pinned Wails CLI. Linux → Windows cross-builds require no MinGW, Node or GTK production linking.
- Headless-only builds do not check for Wails/GTK or generate GUI icons.

Run the following commands from the repository root. They have the **same form in POSIX shells and Windows PowerShell**; no Bash script is required. End users of release builds need no Go or Wails installation.

```sh
# GUI development prerequisite: install the pinned CLI
go install github.com/wailsapp/wails/v2/cmd/wails@v2.11.0

# Headless business regression; does not initialize WebView
go test ./...
go vet ./...

# Desktop-tag type checks; do not link the native GUI frontend
go test -tags desktop,webkit2_41 ./...
go vet -tags desktop,webkit2_41 ./...

# Host-native GUI build
go run ./scripts/build

# Windows GUI target; supports cross-building from Linux
go run ./scripts/build -platform windows/amd64

# Headless only
go run ./scripts/build -headless-only
go run ./scripts/build -headless-only -platform windows/amd64

# Matching GUI + headless; writes this platform's release.json on success
go run ./scripts/build -bundle
go run ./scripts/build -bundle -platform windows/amd64
```

`-headless-only` and `-bundle` are mutually exclusive. Builds are always serial: do not build concurrently in one workspace. The build entry supports only Linux/Windows amd64 hosts and `linux/amd64` / `windows/amd64` targets; a Windows host cannot build a Linux target. Invalid targets are rejected before any file is written.

## Build checks versus actual execution

- `go test ./...` and `go vet ./...` are headless regression checks and do not start WebView.
- `go test -tags desktop ./...`, `go vet -tags desktop ./...` and the `desktop,webkit2_41` commands above only perform desktop-tag type checks. Without `production` / `dev` / `bindings`, Wails selects a Unix stub; this does not prove that the GTK/WebKit frontend was compiled.
- Real Linux GUI production linking is handled by the unified build entry and requires `CGO_ENABLED=1`, the `production,desktop,webkit2_41` tags and native development libraries. Linking fails if any prerequisite is missing.
- Windows cross-compilation and PE resource verification do not replace actual Windows GUI testing. Ordinary business regression tests and historical GUI acceptance do not establish acceptance of a new UI. Report only the environment and scope actually tested.
- Without the `desktop` tag, the root `main` is only a build-hint stub. It opens no browser or TCP service and is not a runnable GUI. Bare `go build` is not a substitute for the delivery build entry that includes icons, manifest and version resources.

## Desktop binding surface

The desktop process binds only the narrow `desktop.Bridge`, never the whole `App`, so injection setters cannot reach JS through reflection. The bound methods are fixed: `SaveFile`, `ReportUnsaved`, `SetExitConfirmed`, `SetUIPreferences`, `AutoSaveMask`, `AutoSaveRestored`, `ListWorkspaceFiles(kind)` and `ReadWorkspaceFile(kind, name)`.

- `ListWorkspaceFiles(kind)` / `ReadWorkspaceFile(kind, name)` accept only the four fixed kinds `original`, `cards`, `pending` and `mapping`, plus a single file name. The target directory is chosen by kind; no root, absolute/relative path or subdirectory is accepted. Listing covers direct-child safe regular files only, and bytes (Base64) are returned only after an explicit read.
- Neither automatic saving nor directory reading adds an HTTP endpoint or accepts arbitrary paths; the business API remains `/api/mask`, `/api/restore` and `/api/exit`.
- Listing/reading reuse the same atomic coordination as saving and closing: while processing, saving or after exit is accepted they return a retryable busy state, always release afterwards, and never advance `unsaved_generation`.

## Delivering acceptance test binaries (end users without a dev environment)

For end users without Go, Node, source or Wails, the default acceptance method is for the developer to compile test binaries for the target platform and hand over the exe files, the exact test filter and the command to run and save logs. End users only run the exe. Cross-compile when possible; otherwise prebuild on the target platform. The Windows junction/link cases in this change are delivered this way.

Generate Windows amd64 test binaries on a Linux dev host (`-c` compiles only and does not run):

```text
mkdir -p dist/windows-amd64/acceptance
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -tags desktop -o dist/windows-amd64/acceptance/workspace.test.exe ./internal/workspace
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -tags desktop -o dist/windows-amd64/acceptance/desktop.test.exe ./internal/desktop
```

PowerShell equivalent on a Windows dev host (restores the previous environment variables afterwards):

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

Run from the delivered directory (PowerShell; `-test.*` are flags of the compiled test binary itself. `--%` stops PowerShell argument parsing so it cannot report `flag provided but not defined: -test`):

```powershell
.\workspace.test.exe --% -test.run=Junction -test.v=true -test.count=1
.\desktop.test.exe --% -test.run=TestWorkspaceInputWindowsRejects -test.v=true -test.count=1
```

- Expected cases: the first command runs `TestEnsureRejectsJunctionOnWindows`, `TestEnsureGUIRejectsRequiredJunctionOnWindows`, `TestEnsureGUIIgnoresPendingJunctionOnWindows`, `TestEnsureDirRejectsJunctionOnWindows` and `TestLockRejectsJunctionOnWindows` (5). The second runs `TestWorkspaceInputWindowsRejectsJunctionDir` and `TestWorkspaceInputWindowsRejectsFileLink` (2).
- Copying the console output is enough. `$LASTEXITCODE` is the test process exit code; **a run passes only when every expected case appears and there is no `--- SKIP`. A SKIP is not a pass**; keep the output and state the reason.
- To save a log, run `Start-Transcript -Path windows-link-tests.log` before the two commands and `Stop-Transcript` after them. Never put `2>&1`, pipes or redirection after `--%`.
- Creating symlinks on Windows may require **Developer Mode** or administrator rights. Address insufficient privileges by changing the environment and rerunning; do not change OS policy just to make tests "pass".
- These tests only write to `t.TempDir` and do not initialise the GUI. Test binaries do not replace GUI inspection, and they do not imply every test can run on a machine without runtime dependencies. Other cases remain covered by the developer's `go test` runs.
- The archived `openspec/changes/archive/2026-10-19-cross-platform-mcp-headless/acceptance.md` used the same `--%` form to validate an older test exe on Windows 10. The Windows test binaries for this change have since been run by the end user on Windows, with a final fully-passing report (intermediate output and final correction are recorded in the change's `acceptance.md`).

## Output layout

All build modes write to `dist/<os>-<arch>/` for the selected target, not to the top level of `dist/`:

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

- The default build writes only the GUI; `-headless-only` writes only the agent; `release.json` is generated only after a successful `-bundle`. Linux → Windows and native Windows builds both output to `dist/windows-amd64/`.
- The Go module path is `github.com/pandatoufu/data-masking`; the headless version injection symbol is `internal/buildinfo.Version` under that path, generated from `info.productVersion` in `wails.json`.
- Both GUI targets (`linux/amd64`, `windows/amd64`) build with the pinned Wails v2.11.0 `-trimpath`, and the headless `go build` keeps `-trimpath` as well, so artifacts do not embed local file system paths. `-trimpath` removes paths only and does not erase source strings or dependency information, so binaries and archive contents still need scanning before delivery.
- Headless builds use `CGO_ENABLED=0`, without desktop/production GUI tags or `-H windowsgui`, and compile `cmd/data-masking-agent`.
- Bundles build GUI first, then headless. Only after both succeed and pass validation are they published to the same platform directory, followed by `release.json` containing the version, target and file SHA-256 hashes. Failures do not present old files or incomplete sets as a new release; single-entry builds do not create or rewrite the bundle manifest.
- Builds only create the current platform directory if missing and update managed files for that build. They do not clear directories or delete, migrate or modify other platform directories, old top-level artifacts or existing runtime data in `cards/`, `mapping/`, `masked/`, `original/`, `restored/`, `pending/`, `.agent-state/`. Old top-level artifacts are neither automatically migrated nor written to both layouts.
- The sole version source is `info.productVersion` in `wails.json`. Builds inject it into headless `internal/buildinfo.Version`; the agent's `--version` matches MCP `serverInfo.version`.

## Icons and resource protection

Windows targets use the pinned Wails CLI to embed a six-size ICO, manifest and version information. The build entry refreshes the managed derived `build/windows/icon.ico`, preserves hand-maintained manifest / metadata files beside it, does not retain an old design merely because an ICO exists, and rejects a pre-existing `data-masking-res.syso`. It cleans up only temporary resources created by that build.

Regenerate the embedded icon (same command in POSIX shells / PowerShell):

```sh
go run ./scripts/genicon
```

Static pages and resources are embedded in a single GUI executable; no adjacent pages or icons are needed. Windows still requires system WebView2; Linux still requires GTK3/WebKitGTK 4.1 and a normal graphical/session bus environment. Do not describe “single-program delivery” as “zero dependencies on every fresh machine.”

## Manual public release

The public branch `github-public` (GitHub's default branch) is an **independent-root public snapshot history**. It shares no ancestry with the development repository `main` and is not bridged by merges. Let `S` be the selected clean development commit (version prepared), `P` the previous public tip before migration, and `R` the cleaned first independent-root public commit:

```text
development: A --> B --> S --> S2
                        :      :
                      export export
                        :      :
new public:             R ---> R2

old public:  P0 --> P1 --> P    (no branch or tag reference after migration)
```

- Export `S`'s tracked files into a brand-new isolated directory. Do not copy `.git`, Git objects, remotes, reflog or user runtime data.
- Create `R` in a brand-new Git repository; its **parent count must be 0**, and `R2` has `R` as its only parent. Never merge, pull-merge or cherry-pick development or old public history, and never merge `main` into the public branch.
- The public tree excludes only the root paths `/.opencode/`, `/openspec/`, `/AGENTS.md` and `/CHANGELOG.md`. Except for those four paths and `.gitignore`, `R`'s tracked tree must match `S` exactly, including content, file types, modes and deletions.
- The public `.gitignore` is `S`'s base rules plus those four exact root paths. `main`'s `.gitignore` does not gain them; development files stay tracked in the development repository and its history is neither deleted nor rewritten.
- Public history carries no development ancestry, but excluding a file tree is **not** a confidentiality mechanism: anything that must stay private cannot be in the export source.
- The author/committer of public commits and the tagger of annotated tags use `pandatoufu` <`8235988+pandatoufu@users.noreply.github.com`>, configured only locally in the candidate repository. The global identity is never modified.
- Private correspondences (`S` SHA, export manifest, `R` and package SHA-256) live only in the development-side acceptance record; they never enter public commits, Release bodies or public files.
- Functional acceptance follows the actual impact of a change: this release preparation changes only the version and release materials, so **a full manual GUI/MCP functional re-acceptance is not required**; when citing existing evidence, state its source and that it was not re-run this time.
- This process is manual. The repository has no automated release, packaging or CHANGELOG workflow; the commands below describe human steps only. Replace angle-bracket placeholders (`<S>`, `<P>`, `<export-dir>`, etc.) with real SHAs or paths before running; do not paste them verbatim.

### 1. Version and CHANGELOG (on `main`)

- The sole version source is `info.productVersion` in `wails.json`; both `build/windows/info.json` and the headless injection reuse it. Do not hand-edit other version files or bulk-replace version literals in test fixtures.
- `CHANGELOG.md` is tracked on `main` only. Record changes under `[Unreleased]` during development and fold them into the version section (Added / Changed / Fixed / Security) before release. Use the real release date; write "未发布" (unreleased) until then.
- The baseline is the previous public tag's tree, not a same-name tag on `main`. Compare across roots, excluding the public exclusions, and verify every entry:

```sh
git diff v1.1 <S> -- . ':(exclude).opencode' ':(exclude)openspec' ':(exclude)AGENTS.md' ':(exclude)CHANGELOG.md'
```

Do not present capabilities that already existed (for example the headless MCP and DOCX handling in `v1.1`) as new, and do not invent dates for older versions.

### 2. Export a clean snapshot and create the independent root `R`

Pre-checks (`S` is committed, the worktree is clean, and the export directory is a brand-new empty directory outside the development worktree):

```sh
git status --porcelain
git rev-parse <S>
```

Export `S`'s tracked files, remove the four public exclusions, and append the public `.gitignore` rules:

```sh
mkdir -p <export-dir>
git archive --format=tar <S> | tar -x -C <export-dir>
rm -rf <export-dir>/.opencode <export-dir>/openspec <export-dir>/AGENTS.md <export-dir>/CHANGELOG.md
printf '%s\n' '/.opencode/' '/openspec/' '/AGENTS.md' '/CHANGELOG.md' >> <export-dir>/.gitignore
```

Windows PowerShell equivalent:

```powershell
New-Item -ItemType Directory -Force <export-dir> | Out-Null
git archive --format=tar --output=<export-dir>\source.tar <S>
tar -xf <export-dir>\source.tar -C <export-dir>
Remove-Item <export-dir>\source.tar
Remove-Item -Recurse -Force <export-dir>\.opencode, <export-dir>\openspec, <export-dir>\AGENTS.md, <export-dir>\CHANGELOG.md
Add-Content -Path <export-dir>\.gitignore -Value @('/.opencode/','/openspec/','/AGENTS.md','/CHANGELOG.md') -Encoding ascii
```

Do not rewrite `.gitignore` with PowerShell `>` / `Out-File`, which can add a BOM and break the first pattern.

Initialise a brand-new repository in the export directory, set the public identity **locally only**, and commit `R`:

```sh
git -C <export-dir> init -b github-public
git -C <export-dir> config user.name "pandatoufu"
git -C <export-dir> config user.email "8235988+pandatoufu@users.noreply.github.com"
git -C <export-dir> config commit.gpgsign false
git -C <export-dir> config tag.gpgsign false
git -C <export-dir> config core.hooksPath .git/empty-hooks
git -C <export-dir> add -A
git -C <export-dir> commit -m "release: v1.2.0 (source <full S SHA>)"
git -C <export-dir> rev-list --parents -n 1 HEAD   # prints one SHA only: 0 parents
git -C <export-dir> cat-file -p HEAD               # author/committer are the noreply identity
```

`core.hooksPath` points at a non-existent empty directory so global hooks cannot inject a private identity or signature into candidate objects; the candidate repository enables no signing configuration. Before committing you can also check the effective identity with `git -C <export-dir> var GIT_AUTHOR_IDENT` / `GIT_COMMITTER_IDENT`.

Whitelist verification (compared across repositories, so development objects never enter the candidate):

```sh
git -C <dev-repo> ls-tree -r --full-tree <S> | awk -F'\t' '$2 !~ /^(\.opencode\/|openspec\/|AGENTS\.md$|CHANGELOG\.md$|\.gitignore$)/' > <work-dir>/dev-tree.txt
git -C <export-dir> ls-tree -r --full-tree HEAD | awk -F'\t' '$2 != ".gitignore"' > <work-dir>/public-tree.txt
diff -u <work-dir>/dev-tree.txt <work-dir>/public-tree.txt    # must print nothing
git -C <dev-repo> show <S>:.gitignore > <work-dir>/dev.gitignore
diff -u <work-dir>/dev.gitignore <export-dir>/.gitignore       # only the four appended lines
git -C <export-dir> ls-files .opencode openspec AGENTS.md CHANGELOG.md   # must be empty
git -C <export-dir> cat-file -e <P>   # expected to fail: old public objects are absent
```

`ls-tree` output includes mode / type / SHA / path, so content, file types and executable modes all participate in the comparison. Any `diff` output means the export is incomplete: stop and investigate. `<dev-repo>` is the development repository root.

### 3. Clean single-parent snapshots for later versions

Always start from a **fresh clone of the clean public remote**; never reuse an old clone or a contaminated directory:

```sh
git clone --branch github-public https://github.com/pandatoufu/data-masking.git <export2-dir>
```

Clear the index, overlay the export of the next selected `S2`, then restore the exclusions and `.gitignore`:

```sh
git -C <export2-dir> rm -r -q --cached .
git archive --format=tar <S2> | tar -x -C <export2-dir>
rm -rf <export2-dir>/.opencode <export2-dir>/openspec <export2-dir>/AGENTS.md <export2-dir>/CHANGELOG.md
printf '%s\n' '/.opencode/' '/openspec/' '/AGENTS.md' '/CHANGELOG.md' >> <export2-dir>/.gitignore
git -C <export2-dir> add -A
git -C <export2-dir> commit -m "release: <version> (source <full S2 SHA>)"
git -C <export2-dir> rev-list --parents -n 1 HEAD   # exactly one parent: the previous public commit
```

- `S2`'s `.gitignore` overwrites the public version in the clone, so the four lines must be appended again every time.
- Whitelist verification is the same as for the first release, with `<S2>` for `<S>` and `<export2-dir>` for `<export-dir>`.
- Do not merge, pull-merge or cherry-pick old public or development commits to “fix different origins”; `R2`'s only parent must be `R`.

### 4. Targeted remote operations for the first migration (separate authorisation required)

Before any write, re-enumerate the remote and the old version objects, and record the expected old `P` full SHA, Release ID and asset IDs/names:

```sh
git fetch origin
git fetch github
git ls-remote --heads --tags github
git rev-parse github/github-public     # must equal the recorded P
gh release view v1.1 --repo pandatoufu/data-masking --json tagName,assets
```

Pause on an unknown head/tag, an unknown Release or asset, or a protection-rule rejection; do not widen the deletion scope. The first replacement uses only a lease against the exact old SHA — never a bare `--force`, `--mirror` or a bulk tag push:

```sh
git -C <export-dir> remote add github https://github.com/pandatoufu/data-masking.git
git -C <export-dir> push --force-with-lease=refs/heads/github-public:<P> github HEAD:refs/heads/github-public
```

Targeted removal of the old version (delete the Release before the tag; confirm there are only the two expected assets beforehand and read back afterwards):

```sh
gh release delete v1.1 --repo pandatoufu/data-masking --yes
git -C <export-dir> push github :refs/tags/v1.1
```

Create the new annotated tag with the same local noreply identity and push it to both remotes (same-name tags must point at the same tag object):

```sh
git -C <export-dir> tag -a v1.2.0 -m "data-masking v1.2.0" <R>
git -C <export-dir> cat-file -p refs/tags/v1.2.0   # tagger is the noreply identity
git -C <export-dir> push github refs/tags/v1.2.0
git -C <export-dir> push <forgejo-remote> refs/tags/v1.2.0
```

If any step fails, stop: do not restore branches/tags/assets containing old information as a default rollback, and never silently move a published tag. When Forgejo authentication is unavailable, state plainly that the sync is incomplete; do not ask for or print credentials.

### 5. Serial builds from `R` and whitelist packaging

Build only from the final `R`'s clean tracked tree (that is, from `R` after committing in the export repository; never build on `S` and then claim `R` as the source). Run both targets serially:

```sh
go run ./scripts/build -bundle
go run ./scripts/build -bundle -platform windows/amd64
git status --porcelain
git diff --stat
```

Builds write ignored `dist/` output; `git status --porcelain` must show no tracked changes, otherwise resolve them, re-determine `R` and rebuild. Both GUI targets build with the pinned Wails v2.11.0 `-trimpath`, and headless also uses `-trimpath`; path-free build arguments do not make source strings clean, so binaries and archive contents must still be scanned before delivery.

Collect only three files per platform (GUI, agent, that platform's `release.json`) from a clean temporary packaging directory. Do not zip the whole `dist/`, include acceptance test exes or any user data directory. Generate the Linux tar.xz on a Linux dev host (preserving the executable bit):

```sh
mkdir -p <pack-dir>/linux-amd64
cp dist/linux-amd64/data-masking dist/linux-amd64/data-masking-agent dist/linux-amd64/release.json <pack-dir>/linux-amd64/
tar -C <pack-dir>/linux-amd64 -cJf data-masking-linux-amd64-v1.2.0.tar.xz data-masking data-masking-agent release.json
```

Generate the Windows zip on a Windows dev host (PowerShell):

```powershell
New-Item -ItemType Directory -Force <pack-dir>\windows-amd64 | Out-Null
Copy-Item dist\windows-amd64\data-masking.exe, dist\windows-amd64\data-masking-agent.exe, dist\windows-amd64\release.json <pack-dir>\windows-amd64\
Compress-Archive -Path <pack-dir>\windows-amd64\data-masking.exe, <pack-dir>\windows-amd64\data-masking-agent.exe, <pack-dir>\windows-amd64\release.json -DestinationPath data-masking-windows-amd64-v1.2.0.zip
```

Verify that each archive holds exactly three members, and cross-check versions and SHA-256 (keep archive names consistent with the delivery record; update these commands if renamed):

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

Cross-check the program `productVersion`, the `CHANGELOG` `1.2.0` section, the `v1.2.0` tag, the Windows PE version resource, the agent `--version` and MCP `serverInfo.version`, both `release.json` files (`version` / `target` / `files[].sha256`), and the SHA-256 of both archives. These version, target-platform, tree/archive-content and hash checks are mandatory for an official release.

**Functional acceptance scope**: this release preparation changes only the version and release materials, not product behaviour, so **a full manual GUI/MCP functional re-acceptance is not required**; not re-running it is neither a defect of this version nor a release blocker. Existing functional evidence lives in the development repository's OpenSpec archive (the public tree excludes that directory); when citing it, state the record path and corresponding version/commit, and make clear that it is historical and was not re-executed this time — never present it as a run that passed in this round. A new build may be smoke-checked for startup and version (GUI launch, agent `--version`, MCP `serverInfo.version`), recording only what was actually done and without turning it into a full business-loop gate; later versions that change product behaviour define their acceptance scope from the actual impact.

### 6. Release and read-back verification

Releases are created on GitHub only, and the body is extracted from the **development commit** `S`'s `CHANGELOG.md` (the public tree lacks it), taking only the `1.2.0` section without `Unreleased` or process notes. POSIX shell:

```sh
git show "<S>:CHANGELOG.md" | awk '/^## \[1\.2\.0\]/{f=1;next} /^## \[/{f=0} f' > release-notes-v1.2.0.md
```

Windows PowerShell equivalent (explicit UTF-8 without BOM):

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

Create the GitHub Release from that body and the two archives, set it as Latest, then read back and verify in layers:

```sh
gh release view v1.2.0 --repo pandatoufu/data-masking --json tagName,body,assets
gh release list --repo pandatoufu/data-masking
git ls-remote --heads --tags https://github.com/pandatoufu/data-masking.git
git clone --branch github-public https://github.com/pandatoufu/data-masking.git <verify-clone-dir>
git -C <verify-clone-dir> rev-list --count --all
git -C <verify-clone-dir> cat-file -e <P>   # expected to fail: old public objects are unreachable
```

- **Controllable references of the original repository**: `github-public` holds only the independent-root lineage, the only public tag is `v1.2.0` pointing at `R`, and the old `v1.1` tag and Release are gone.
- **New deliverables**: the Release body matches this `CHANGELOG` version section, and both asset names and SHA-256 match the local build; implementation test packages are never uploaded.
- **External residue**: the known fork, direct links to old SHAs, caches and other people's clones may still exist. They are not covered by the original repository's cleanup, and no full-network erasure is promised. Contacting the fork owner or GitHub Support requires separate authorisation.
