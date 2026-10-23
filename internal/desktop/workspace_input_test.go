package desktop

import (
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/core"
)

// inputCounters 记录测试期间的打开次数与实际读取字节数，用于断言“列举不读正文”
// 与“超限不整读”。
type inputCounters struct {
	opens     int
	bytes     int64
	afterOpen func(path string)
}

func newTestInput(root string, maxDoc int64) (*WorkspaceInput, *inputCounters) {
	c := &inputCounters{}
	in := NewWorkspaceInput(func() string { return root }, nil, maxDoc)
	in.openFile = func(path string) (*os.File, error) {
		f, err := os.Open(path)
		if err == nil {
			c.opens++
			if c.afterOpen != nil {
				c.afterOpen(path)
			}
		}
		return f, err
	}
	in.readUpTo = func(r io.Reader, limit int64) ([]byte, error) {
		data, err := readUpToLimit(r, limit)
		c.bytes += int64(len(data))
		return data, err
	}
	return in, c
}

// seedInputDir 在 root/name 下创建目录与给定文件（值为原始字节）。
func seedInputDir(t *testing.T, root, name string, files map[string][]byte) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for fileName, data := range files {
		if err := os.WriteFile(filepath.Join(dir, fileName), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func decodedContent(t *testing.T, res WorkspaceFileContent) []byte {
	t.Helper()
	if res.Status != inputStatusOK {
		t.Fatalf("读取应成功：%+v", res)
	}
	data, err := base64.StdEncoding.DecodeString(res.DataB64)
	if err != nil {
		t.Fatalf("返回数据应为有效 base64：%v", err)
	}
	return data
}

// --- 2.1 直属列举、固定 kind/格式与只读名称 ---

func TestWorkspaceInputListFormatsAndOrder(t *testing.T) {
	root := t.TempDir()
	seedInputDir(t, root, workspaceDirOriginal, map[string][]byte{
		"notes.txt":     []byte("a"),
		"README.MD":     []byte("b"),
		"app.JS":        []byte("c"),
		"image.png":     []byte("d"),
		"合同 副本.docx":    []byte("e"),
		"notes.txt.bak": []byte("f"),
	})
	seedInputDir(t, root, workspaceDirCards, map[string][]byte{
		"A.txt":     []byte("x"),
		"B.TXT":     []byte("y"),
		"word.md":   []byte("z"),
		"note.json": []byte("j"),
	})
	seedInputDir(t, root, workspaceDirMapping, map[string][]byte{
		"pair.JSON": []byte("{}"),
		"pair.txt":  []byte("no"),
	})
	seedInputDir(t, root, workspaceDirPending, map[string][]byte{
		"reply.md":  []byte("r"),
		"reply.py":  []byte("p"),
		"reply.png": []byte("n"),
	})

	in, counters := newTestInput(root, 0)
	cases := []struct {
		kind string
		want []string
	}{
		{WorkspaceInputOriginal, []string{"README.MD", "app.JS", "notes.txt", "notes.txt.bak", "合同 副本.docx"}},
		{WorkspaceInputCards, []string{"A.txt", "B.TXT"}},
		{WorkspaceInputMapping, []string{"pair.JSON"}},
		{WorkspaceInputPending, []string{"reply.md", "reply.py"}},
	}
	for _, c := range cases {
		res := in.List(c.kind)
		if res.Status != inputStatusOK {
			t.Fatalf("%s 列举应成功：%+v", c.kind, res)
		}
		if strings.Join(res.Files, ",") != strings.Join(c.want, ",") {
			t.Fatalf("%s 列举/排序不符：got %v want %v", c.kind, res.Files, c.want)
		}
	}
	if counters.opens != 0 || counters.bytes != 0 {
		t.Fatalf("列举不得打开或读取文件正文：opens=%d bytes=%d", counters.opens, counters.bytes)
	}
}

func TestWorkspaceInputListSkipsDirsLinksSpecialAndKeepsOthers(t *testing.T) {
	root := t.TempDir()
	dir := seedInputDir(t, root, workspaceDirOriginal, map[string][]byte{
		"good.txt": []byte("ok"),
	})
	if err := os.Mkdir(filepath.Join(dir, "nested.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkCreated := os.Symlink(outside, filepath.Join(dir, "link.txt")) == nil
	if !linkCreated && runtime.GOOS != "windows" {
		t.Fatal("创建符号链接失败")
	}
	if runtime.GOOS != "windows" {
		fifo := filepath.Join(dir, "pipe.txt")
		if err := mkfifo(fifo); err != nil {
			t.Fatalf("创建 FIFO 失败：%v", err)
		}
	}
	in, counters := newTestInput(root, 0)
	res := in.List(WorkspaceInputOriginal)
	if res.Status != inputStatusOK {
		t.Fatalf("列举应成功：%+v", res)
	}
	if strings.Join(res.Files, ",") != "good.txt" {
		t.Fatalf("只应返回安全常规文件：%v", res.Files)
	}
	if counters.opens != 0 {
		t.Fatalf("列举不得打开任何条目：%d", counters.opens)
	}
}

func TestWorkspaceInputListEmptyAndFailures(t *testing.T) {
	root := t.TempDir()
	seedInputDir(t, root, workspaceDirCards, nil)
	in, _ := newTestInput(root, 0)
	if res := in.List(WorkspaceInputCards); res.Status != inputStatusEmpty || len(res.Files) != 0 {
		t.Fatalf("空目录应为 empty：%+v", res)
	}
	// 目录不存在：不可用，而不是空。
	if res := in.List(WorkspaceInputOriginal); res.Status != inputStatusUnavailable || res.MessageID != "input.dir_unavailable" {
		t.Fatalf("缺失目录应不可用：%+v", res)
	}
	// 未知用途拒绝，且不访问文件系统。
	if res := in.List("../original"); res.Status != inputStatusFailed || res.MessageID != "input.bad_kind" {
		t.Fatalf("未知 kind 应拒绝：%+v", res)
	}
	// root 未解析：不可用。
	noRoot, _ := newTestInput("", 0)
	if res := noRoot.List(WorkspaceInputMapping); res.Status != inputStatusUnavailable || res.MessageID != "input.root_unavailable" {
		t.Fatalf("无 root 应不可用：%+v", res)
	}
}

func TestWorkspaceInputListRejectsPlaceholderDir(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, workspaceDirPending), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	in, _ := newTestInput(root, 0)
	if res := in.List(WorkspaceInputPending); res.Status != inputStatusUnavailable || res.MessageID != "input.dir_unavailable" {
		t.Fatalf("pending 占位应不可用：%+v", res)
	}
	// 其他目录仍可用（单个入口失败隔离）。
	seedInputDir(t, root, workspaceDirMapping, map[string][]byte{"a.json": []byte("{}")})
	if res := in.List(WorkspaceInputMapping); res.Status != inputStatusOK {
		t.Fatalf("其他可用目录不应受影响：%+v", res)
	}
}

func TestWorkspaceInputPendingEnsureRetryOnRefresh(t *testing.T) {
	root := t.TempDir()
	placeholder := filepath.Join(root, workspaceDirPending)
	if err := os.WriteFile(placeholder, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := NewWorkspaceInput(func() string { return root }, func() error {
		return ensurePendingDir(root)
	}, 0)
	if res := in.List(WorkspaceInputPending); res.Status != inputStatusUnavailable {
		t.Fatalf("占位时应不可用：%+v", res)
	}
	if err := os.Remove(placeholder); err != nil {
		t.Fatal(err)
	}
	if res := in.List(WorkspaceInputPending); res.Status != inputStatusEmpty {
		t.Fatalf("修复后刷新应恢复为空目录：%+v", res)
	}
}

func ensurePendingDir(root string) error {
	path := filepath.Join(root, workspaceDirPending)
	if info, err := os.Lstat(path); err == nil && !info.IsDir() {
		return errors.New("pending 不是目录")
	}
	return os.MkdirAll(path, 0o755)
}

// 3.1 original/pending 复用统一候选策略：普通/新后缀、无后缀与点文件可列举与读取；
// 已知拒绝格式不列举且在读取前拒绝；cards/mapping 专用用途不变；列举仍不读正文。
func TestWorkspaceInputNewDocumentNamePolicy(t *testing.T) {
	root := t.TempDir()
	seedInputDir(t, root, workspaceDirOriginal, map[string][]byte{
		"data.json":   []byte("{}"),
		"README":      []byte("readme"),
		".env":        []byte("KEY=v"),
		"notes.xyz":   []byte("xyz"),
		"合同.docx":     []byte("docx"),
		"slides.pptx": []byte("pptx"),
		"notes.rtf":   []byte("rtf"),
		"image.png":   []byte("png"),
	})
	seedInputDir(t, root, workspaceDirPending, map[string][]byte{
		"reply.json": []byte("r"),
		"README":     []byte("r2"),
		"reply.zip":  []byte("z"),
	})
	seedInputDir(t, root, workspaceDirCards, map[string][]byte{
		"ok.txt": []byte("x"),
		"noext":  []byte("y"),
		".env":   []byte("z"),
	})
	seedInputDir(t, root, workspaceDirMapping, map[string][]byte{
		"pair.json": []byte("{}"),
		"README":    []byte("no"),
	})

	in, counters := newTestInput(root, 0)
	cases := []struct {
		kind string
		want []string
	}{
		{WorkspaceInputOriginal, []string{".env", "README", "data.json", "notes.xyz", "合同.docx"}},
		{WorkspaceInputPending, []string{"README", "reply.json"}},
		{WorkspaceInputCards, []string{"ok.txt"}},
		{WorkspaceInputMapping, []string{"pair.json"}},
	}
	for _, c := range cases {
		res := in.List(c.kind)
		if res.Status != inputStatusOK || strings.Join(res.Files, ",") != strings.Join(c.want, ",") {
			t.Fatalf("%s 列举不符：%+v want %v", c.kind, res, c.want)
		}
	}
	if counters.opens != 0 || counters.bytes != 0 {
		t.Fatalf("列举不得读取正文：opens=%d bytes=%d", counters.opens, counters.bytes)
	}

	if got := decodedContent(t, in.Read(WorkspaceInputOriginal, ".env")); string(got) != "KEY=v" {
		t.Fatalf("点文件应可读取原始字节：%q", got)
	}
	res := in.Read(WorkspaceInputOriginal, "README")
	if res.Status != inputStatusOK || res.Name != "README" {
		t.Fatalf("无后缀文件应可显式读取：%+v", res)
	}
	for _, name := range []string{"slides.pptx", "notes.rtf", "image.png"} {
		bad := in.Read(WorkspaceInputOriginal, name)
		if bad.Status != inputStatusFailed || bad.MessageID != "input.unsupported_format" || bad.DataB64 != "" {
			t.Fatalf("%q 应因已知不支持格式在打开前拒绝：%+v", name, bad)
		}
	}
	if counters.opens != 2 || counters.bytes != int64(len("KEY=v")+len("readme")) {
		t.Fatalf("只应打开并读取两次显式读取：opens=%d bytes=%d", counters.opens, counters.bytes)
	}
}

// --- 2.2 单文件名校验、安全打开、身份复核与限量读取 ---

func TestWorkspaceInputReadValidBytesAndName(t *testing.T) {
	root := t.TempDir()
	gbk := []byte{0xD6, 0xD0, 0xCE, 0xC4} // “中文”的 GBK 原始字节
	seedInputDir(t, root, workspaceDirOriginal, map[string][]byte{
		"notes 副本 <b>.txt": append([]byte("前缀"), gbk...),
	})
	in, counters := newTestInput(root, 0)
	res := in.Read(WorkspaceInputOriginal, "notes 副本 <b>.txt")
	got := decodedContent(t, res)
	if res.Name != "notes 副本 <b>.txt" {
		t.Fatalf("应返回真实文件名：%q", res.Name)
	}
	want := append([]byte("前缀"), gbk...)
	if string(got) != string(want) {
		t.Fatalf("应返回原始字节（不转码）：%v", got)
	}
	if counters.opens != 1 || counters.bytes != int64(len(want)) {
		t.Fatalf("应只打开并读取一次：opens=%d bytes=%d", counters.opens, counters.bytes)
	}
}

func TestWorkspaceInputReadRejectsInvalidNamesAndKinds(t *testing.T) {
	root := t.TempDir()
	seedInputDir(t, root, workspaceDirOriginal, map[string][]byte{"ok.txt": []byte("ok")})
	seedInputDir(t, root, workspaceDirCards, map[string][]byte{"ok.json": []byte("{}")})
	outside := filepath.Join(filepath.Dir(root), "secret.txt")
	_ = os.WriteFile(outside, []byte("secret"), 0o644)
	in, counters := newTestInput(root, 0)
	cases := []struct{ kind, name, id string }{
		{"../original", "ok.txt", "input.bad_kind"},
		{WorkspaceInputOriginal, "../secret.txt", "input.bad_name"},
		{WorkspaceInputOriginal, "nested/ok.txt", "input.bad_name"},
		{WorkspaceInputOriginal, `nested\ok.txt`, "input.bad_name"},
		{WorkspaceInputOriginal, outside, "input.bad_name"},
		{WorkspaceInputOriginal, `C:\Temp\ok.txt`, "input.bad_name"},
		{WorkspaceInputOriginal, "ok.txt:ads", "input.bad_name"},
		{WorkspaceInputOriginal, "NUL.txt", "input.bad_name"},
		{WorkspaceInputOriginal, "con", "input.bad_name"},
		{WorkspaceInputOriginal, "bad\x01.txt", "input.bad_name"},
		{WorkspaceInputOriginal, "trailing.txt ", "input.bad_name"},
		{WorkspaceInputOriginal, "trailing.txt.", "input.bad_name"},
		{WorkspaceInputOriginal, "..", "input.bad_name"},
		{WorkspaceInputCards, "ok.json", "input.unsupported_format"},
		{WorkspaceInputOriginal, "missing.txt", "input.file_missing"},
	}
	for _, c := range cases {
		res := in.Read(c.kind, c.name)
		if res.Status != inputStatusFailed || res.MessageID != c.id || res.DataB64 != "" {
			t.Fatalf("Read(%q,%q) 应拒绝为 %s：%+v", c.kind, c.name, c.id, res)
		}
	}
	if counters.opens != 0 {
		t.Fatalf("拒绝路径不得打开文件：%d", counters.opens)
	}
}

func TestWorkspaceInputReadDocxBytesUnchanged(t *testing.T) {
	root := t.TempDir()
	docx := []byte{0x50, 0x4b, 0x03, 0x04, 0x14, 0x00, 0x00, 0x00}
	seedInputDir(t, root, workspaceDirOriginal, map[string][]byte{"合同.docx": docx})
	in, _ := newTestInput(root, 0)
	res := in.Read(WorkspaceInputOriginal, "合同.docx")
	if got := decodedContent(t, res); string(got) != string(docx) {
		t.Fatalf("DOCX 原始字节应原样返回：%v", got)
	}
	if res.Name != "合同.docx" {
		t.Fatalf("应返回真实文件名：%q", res.Name)
	}
}

func TestWorkspaceInputReadRejectsDirAndLink(t *testing.T) {
	root := t.TempDir()
	dir := seedInputDir(t, root, workspaceDirOriginal, nil)
	if err := os.Mkdir(filepath.Join(dir, "nested.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	in, _ := newTestInput(root, 0)
	if res := in.Read(WorkspaceInputOriginal, "nested.txt"); res.MessageID != "input.not_regular" {
		t.Fatalf("目录应拒绝：%+v", res)
	}
	outside := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link.txt")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("Windows 无创建符号链接权限：平台特有 reparse 用例按平台运行")
		}
		t.Fatal(err)
	}
	if res := in.Read(WorkspaceInputOriginal, "link.txt"); res.MessageID != "input.not_regular" {
		t.Fatalf("链接应拒绝：%+v", res)
	}
}

func TestWorkspaceInputReadDetectsFileReplacementAfterOpen(t *testing.T) {
	root := t.TempDir()
	dir := seedInputDir(t, root, workspaceDirOriginal, map[string][]byte{"notes.txt": []byte("old")})
	in, counters := newTestInput(root, 0)
	counters.afterOpen = func(string) {
		// 打开后、复核前把同名文件替换为新对象。
		_ = os.Rename(filepath.Join(dir, "notes.txt"), filepath.Join(dir, "notes.txt.bak"))
		_ = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("new"), 0o644)
	}
	res := in.Read(WorkspaceInputOriginal, "notes.txt")
	if res.MessageID != "input.changed" || res.DataB64 != "" {
		t.Fatalf("打开后替换应拒绝：%+v", res)
	}
}

func TestWorkspaceInputReadDetectsDirReplacementAfterOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("目录替换竞态用例在 POSIX 环境执行；Windows reparse 用例另行记录")
	}
	root := t.TempDir()
	dir := seedInputDir(t, root, workspaceDirOriginal, map[string][]byte{"notes.txt": []byte("old")})
	elsewhere := t.TempDir()
	in, counters := newTestInput(root, 0)
	counters.afterOpen = func(string) {
		moved := dir + "-moved"
		_ = os.Rename(dir, moved)
		_ = os.Symlink(elsewhere, dir)
	}
	res := in.Read(WorkspaceInputOriginal, "notes.txt")
	if res.MessageID != "input.changed" || res.DataB64 != "" {
		t.Fatalf("目录替换应拒绝：%+v", res)
	}
}

func TestWorkspaceInputReadLimits(t *testing.T) {
	root := t.TempDir()
	// 文档类：沿用有效 MaxFileSize（此处注入 8 字节）。
	seedInputDir(t, root, workspaceDirOriginal, map[string][]byte{
		"tiny.txt": []byte("12345678"),
		"big.txt":  []byte("123456789"),
	})
	docIn, docCounters := newTestInput(root, 8)
	if res := docIn.Read(WorkspaceInputOriginal, "tiny.txt"); res.Status != inputStatusOK {
		t.Fatalf("等于上限应可读：%+v", res)
	}
	before := docCounters.bytes
	res := docIn.Read(WorkspaceInputOriginal, "big.txt")
	if res.MessageID != "input.too_large" || res.DataB64 != "" {
		t.Fatalf("超过文档上限应拒绝：%+v", res)
	}
	if docCounters.bytes != before {
		t.Fatalf("已知超限不应整读文件：before=%d after=%d", before, docCounters.bytes)
	}
	if res.Params["limit"] == nil || res.Params["size"] == nil {
		t.Fatalf("超限应给出上限与大小：%+v", res.Params)
	}

	// 词卡固定复用 core 1 MiB。
	wordDir := filepath.Join(root, workspaceDirCards)
	if err := os.MkdirAll(wordDir, 0o755); err != nil {
		t.Fatal(err)
	}
	atLimit := filepath.Join(wordDir, "words.txt")
	f, err := os.Create(atLimit)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(core.MaxWordFileSize); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	over := filepath.Join(wordDir, "over.txt")
	f, err = os.Create(over)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(core.MaxWordFileSize + 1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	wordIn, wordCounters := newTestInput(root, 0)
	if res := wordIn.Read(WorkspaceInputCards, "words.txt"); res.Status != inputStatusOK || int64(len(decodedContent(t, res))) != core.MaxWordFileSize {
		t.Fatalf("1 MiB 词卡应可读：%+v", res)
	}
	wordCounters.bytes = 0
	if res := wordIn.Read(WorkspaceInputCards, "over.txt"); res.MessageID != "input.too_large" {
		t.Fatalf("超过 1 MiB 词卡应拒绝：%+v", res)
	}
	if wordCounters.bytes != 0 {
		t.Fatalf("超限词卡不应整读：%d", wordCounters.bytes)
	}

	// mapping 快捷读取的 32 MiB 传输保护上限（不改变既有业务/手动入口）。
	if workspaceMappingInputLimit != 32<<20 {
		t.Fatalf("mapping 快捷读取上限应为 32 MiB，实际 %d", workspaceMappingInputLimit)
	}
	mapDir := filepath.Join(root, workspaceDirMapping)
	if err := os.MkdirAll(mapDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bigMap := filepath.Join(mapDir, "big.json")
	f, err = os.Create(bigMap)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(workspaceMappingInputLimit + 1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	mapIn, mapCounters := newTestInput(root, 0)
	if res := mapIn.Read(WorkspaceInputMapping, "big.json"); res.MessageID != "input.too_large" {
		t.Fatalf("超过 32 MiB 的 mapping 应拒绝：%+v", res)
	}
	if mapCounters.bytes != 0 {
		t.Fatalf("超限 mapping 不应整读：%d", mapCounters.bytes)
	}
}

func TestWorkspaceInputReadErrorsReleaseAndNoLeak(t *testing.T) {
	root := t.TempDir()
	seedInputDir(t, root, workspaceDirOriginal, map[string][]byte{"notes.txt": []byte("ok")})
	in, _ := newTestInput(root, 0)
	in.readUpTo = func(io.Reader, int64) ([]byte, error) {
		return nil, errors.New("boom")
	}
	if res := in.Read(WorkspaceInputOriginal, "notes.txt"); res.MessageID != "input.read_failed" || res.DataB64 != "" {
		t.Fatalf("读取错误应受控失败：%+v", res)
	}
}

func TestWorkspaceInputReadPermissionFailure(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("目录/文件权限语义在 Windows 或 root 下不适用：按环境如实记录")
	}
	root := t.TempDir()
	dir := seedInputDir(t, root, workspaceDirOriginal, map[string][]byte{"notes.txt": []byte("ok")})
	if err := os.Chmod(filepath.Join(dir, "notes.txt"), 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(filepath.Join(dir, "notes.txt"), 0o644) }()
	in, _ := newTestInput(root, 0)
	res := in.Read(WorkspaceInputOriginal, "notes.txt")
	if res.Status != inputStatusFailed || res.DataB64 != "" {
		t.Fatalf("不可读文件应受控失败：%+v", res)
	}
	if res.MessageID != "input.read_failed" {
		t.Fatalf("不可读文件应提示读取失败：%+v", res)
	}
}

// mkfifo 在 POSIX 平台创建命名管道，用于验证特殊文件被跳过。
func mkfifo(path string) error { return mkfifoPlatform(path) }
