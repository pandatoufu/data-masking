package desktop

import (
	"encoding/base64"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeDialog 记录调用并按脚本返回（路径, 取消, 错误）。
type fakeDialog struct {
	mu        sync.Mutex
	calls     int
	path      string
	cancelled bool
	err       error
}

func (d *fakeDialog) Choose(title, suggestedName string) (string, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	if d.err != nil {
		return "", false, d.err
	}
	return d.path, d.cancelled, nil
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestSaveCancelledCreatesNothing(t *testing.T) {
	dir := t.TempDir()
	d := &fakeDialog{cancelled: true}
	c := NewSaveCoordinator(d, nil)
	res := c.Save("a.txt", b64("hello"))
	if res.Status != SaveStatusCancelled {
		t.Fatalf("取消应返回 cancelled，got %q", res.Status)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("取消不应创建文件：%v", entries)
	}
	if c.IsSaving() {
		t.Fatal("取消后不应仍标记保存中")
	}
}

func TestSaveInvalidArgsRejected(t *testing.T) {
	d := &fakeDialog{path: filepath.Join(t.TempDir(), "x.txt")}
	c := NewSaveCoordinator(d, nil)
	if res := c.Save("../escape.txt", b64("x")); res.Status != SaveStatusError {
		t.Fatalf("含路径分隔符的建议名应拒绝：%+v", res)
	}
	if res := c.Save("ok.txt", "!!!not-base64!!!"); res.Status != SaveStatusError {
		t.Fatalf("非法 base64 应拒绝：%+v", res)
	}
	if d.calls != 0 {
		t.Fatalf("参数校验失败不得打开对话框，calls=%d", d.calls)
	}
}

func TestSaveSuccessWritesExactBytes(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "中文 结果.bin")
	d := &fakeDialog{path: dest}
	c := NewSaveCoordinator(d, nil)
	content := []byte("完整字节 \r\n line2")
	res := c.Save("result.bin", base64.StdEncoding.EncodeToString(content))
	if res.Status != SaveStatusSaved {
		t.Fatalf("应保存成功：%+v", res)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("字节应一致：%q", got)
	}
	// 无残留临时文件。
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("成功后只应有一个目标文件：%v", entries)
	}
}

func TestSaveRefusesExistingTarget(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "exists.txt")
	if err := os.WriteFile(dest, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &fakeDialog{path: dest}
	c := NewSaveCoordinator(d, nil)
	res := c.Save("exists.txt", b64("new-data"))
	if res.Status != SaveStatusError {
		t.Fatalf("已存在目标应拒绝：%+v", res)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "original" {
		t.Fatalf("已有目标字节不得改变：%q", got)
	}
}

func TestSaveRefusesSymlinkTarget(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(real, []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("平台不支持符号链接：%v", err)
	}
	d := &fakeDialog{path: link}
	c := NewSaveCoordinator(d, nil)
	res := c.Save("link.txt", b64("new"))
	if res.Status != SaveStatusError {
		t.Fatalf("符号链接目标应拒绝：%+v", res)
	}
	got, _ := os.ReadFile(real)
	if string(got) != "real" {
		t.Fatalf("符号链接指向的实体不得改变：%q", got)
	}
}

func TestSaveRefusesMissingParentDir(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "no-such-dir", "x.txt")
	d := &fakeDialog{path: dest}
	c := NewSaveCoordinator(d, nil)
	res := c.Save("x.txt", b64("x"))
	if res.Status != SaveStatusError {
		t.Fatalf("不存在的父目录应拒绝且不创建：%+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "no-such-dir")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("不得自动创建父目录")
	}
}

func TestSaveConcurrentOnlyOneDialog(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "race.txt")
	// 阻塞对话框，让第二个请求在第一个持锁期间进入。
	gate := make(chan struct{})
	d := &blockingDialog{path: dest, release: gate}
	c := NewSaveCoordinator(d, nil)

	done := make(chan SaveResult, 1)
	go func() { done <- c.Save("race.txt", b64("one")) }()
	// 等待第一次进入保存中。
	for !c.IsSaving() {
	}
	second := c.Save("race.txt", b64("two"))
	if second.Status != SaveStatusError || !strings.Contains(second.Message, "保存进行中") {
		t.Fatalf("重复请求应返回保存进行中：%+v", second)
	}
	close(gate)
	first := <-done
	if first.Status != SaveStatusSaved {
		t.Fatalf("首个保存应成功：%+v", first)
	}
	if d.calls != 1 {
		t.Fatalf("只应打开一次对话框，calls=%d", d.calls)
	}
}

type blockingDialog struct {
	path    string
	release chan struct{}
	calls   int
}

func (d *blockingDialog) Choose(string, string) (string, bool, error) {
	d.calls++
	<-d.release
	return d.path, false, nil
}

// --- 故障注入：写入/Sync/Close/Link/清理失败 ---

type injectOps struct {
	OSFileOps
	lstatErr  error
	linkErr   error
	removeErr error
}

func (o *injectOps) Lstat(p string) (os.FileInfo, error) {
	if o.lstatErr != nil {
		return nil, o.lstatErr
	}
	return o.OSFileOps.Lstat(p)
}

func (o *injectOps) Link(a, b string) error {
	if o.linkErr != nil {
		return o.linkErr
	}
	return o.OSFileOps.Link(a, b)
}

func (o *injectOps) Remove(p string) error {
	if o.removeErr != nil {
		return o.removeErr
	}
	return o.OSFileOps.Remove(p)
}

// failingTempFile 包装真实临时文件，按注入项让 Write/Sync/Close 失败。
type failingTempFile struct {
	f        TempFile
	writeErr error
	syncErr  error
	closeErr error
}

func (t *failingTempFile) Name() string { return t.f.Name() }
func (t *failingTempFile) Write(p []byte) (int, error) {
	if t.writeErr != nil {
		return 0, t.writeErr
	}
	return t.f.Write(p)
}
func (t *failingTempFile) Sync() error {
	if t.syncErr != nil {
		return t.syncErr
	}
	return t.f.Sync()
}
func (t *failingTempFile) Close() error {
	err := t.f.Close()
	if t.closeErr != nil {
		return t.closeErr
	}
	return err
}

// failTempOps 用包装临时文件注入指定阶段的失败。
type failTempOps struct {
	OSFileOps
	writeErr error
	syncErr  error
	closeErr error
}

func (o *failTempOps) CreateTemp(dir, pattern string) (TempFile, error) {
	f, err := o.OSFileOps.CreateTemp(dir, pattern)
	if err != nil {
		return nil, err
	}
	return &failingTempFile{f: f, writeErr: o.writeErr, syncErr: o.syncErr, closeErr: o.closeErr}, nil
}

func testTempFailure(t *testing.T, writeErr, syncErr, closeErr error, label string) {
	t.Helper()
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.bin")
	ops := &failTempOps{writeErr: writeErr, syncErr: syncErr, closeErr: closeErr}
	c := NewSaveCoordinator(&fakeDialog{path: dest}, ops)
	res := c.Save("out.bin", b64("data"))
	if res.Status != SaveStatusError {
		t.Fatalf("%s 应报错：%+v", label, res)
	}
	if _, err := os.Stat(dest); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s 不得留下目标", label)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("%s 应清理本次临时文件：%v", label, entries)
	}
}

func TestSaveWriteFailureCleansTempAndNoTarget(t *testing.T) {
	testTempFailure(t, errors.New("disk full"), nil, nil, "写入失败")
}

func TestSaveSyncFailureCleansTempAndNoTarget(t *testing.T) {
	testTempFailure(t, nil, errors.New("fsync failed"), nil, "Sync 失败")
}

func TestSaveCloseFailureCleansTempAndNoTarget(t *testing.T) {
	testTempFailure(t, nil, nil, errors.New("close failed"), "Close 失败")
}

func TestSaveLinkFailureCleansTempAndNoTarget(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.bin")
	ops := &injectOps{linkErr: errors.New("operation not permitted")}
	c := NewSaveCoordinator(&fakeDialog{path: dest}, ops)
	res := c.Save("out.bin", b64("data"))
	if res.Status != SaveStatusError {
		t.Fatalf("发布失败应报错：%+v", res)
	}
	if _, err := os.Stat(dest); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("发布失败不得留下目标")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("发布失败应清理本次临时文件：%v", entries)
	}
}

func TestSaveLinkRaceDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "race.txt")
	// 模拟：检查目标不存在后，另一进程创建了目标；Link 因目标已存在失败。
	racing := &raceOps{OSFileOps: OSFileOps{}, dest: dest, content: "other"}
	c := NewSaveCoordinator(&fakeDialog{path: dest}, racing)
	res := c.Save("race.txt", b64("mine"))
	if res.Status != SaveStatusError {
		t.Fatalf("目标竞争应拒绝覆盖：%+v", res)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "other" {
		t.Fatalf("其他进程创建的目标字节应保持：%q", got)
	}
}

type raceOps struct {
	OSFileOps
	dest    string
	content string
	created bool
}

func (o *raceOps) Link(a, b string) error {
	if !o.created {
		o.created = true
		_ = os.WriteFile(o.dest, []byte(o.content), 0o644)
	}
	return o.OSFileOps.Link(a, b)
}

func TestSaveCleanupFailureReportsSavedPlusWarning(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.bin")
	ops := &injectOps{removeErr: errors.New("busy")}
	c := NewSaveCoordinator(&fakeDialog{path: dest}, ops)
	res := c.Save("out.bin", b64("data"))
	if res.Status != SaveStatusSaved {
		t.Fatalf("发布成功但清理失败应报告已保存：%+v", res)
	}
	if res.CleanupWarning == "" {
		t.Fatal("应给出清理警告")
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != "data" {
		t.Fatalf("目标应完整保存：%q err=%v", got, err)
	}
}
