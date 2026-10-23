package workspace

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type failTempOps struct {
	OSFileOps
	writeErr error
}

func (o *failTempOps) CreateTemp(dir, pattern string) (TempFile, error) {
	f, err := o.OSFileOps.CreateTemp(dir, pattern)
	if err != nil {
		return nil, err
	}
	return &failingTempFile{f: f, writeErr: o.writeErr}, nil
}

type failingTempFile struct {
	f        TempFile
	writeErr error
}

func (f *failingTempFile) Name() string { return f.f.Name() }
func (f *failingTempFile) Sync() error  { return f.f.Sync() }
func (f *failingTempFile) Close() error { return f.f.Close() }
func (f *failingTempFile) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return f.f.Write(p)
}

type linkOps struct {
	OSFileOps
	failOn    func(dest string) error
	removeErr error
}

func (o *linkOps) Link(a, b string) error {
	if o.failOn != nil {
		if err := o.failOn(b); err != nil {
			return err
		}
	}
	return o.OSFileOps.Link(a, b)
}

func (o *linkOps) Remove(path string) error {
	if o.removeErr != nil && strings.Contains(filepath.Base(path), ".data-masking-") {
		return o.removeErr
	}
	return o.OSFileOps.Remove(path)
}

func TestPublishSingleNoOverwrite(t *testing.T) {
	dir := t.TempDir()
	pub := NewPublisher(OSFileOps{})
	path, err := pub.Publish(dir, "out.txt", []byte("hello"))
	if err != nil {
		t.Fatalf("发布失败：%v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "hello" {
		t.Fatalf("字节应完整：%q", got)
	}
	names, _ := os.ReadDir(dir)
	if len(names) != 1 {
		t.Fatalf("不应留下临时文件：%v", names)
	}
}

func TestPublishTargetRacePreservesExisting(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "race.txt")
	if err := os.WriteFile(dest, []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	pub := NewPublisher(OSFileOps{})
	_, err := pub.Publish(dir, "race.txt", []byte("mine"))
	if err == nil || !errors.Is(err, fs.ErrExist) {
		t.Fatalf("应返回 fs.ErrExist：%v", err)
	}
	if got, _ := os.ReadFile(dest); string(got) != "other" {
		t.Fatalf("不得覆盖已有目标：%q", got)
	}
	names, _ := os.ReadDir(dir)
	if len(names) != 1 {
		t.Fatalf("失败应清理临时文件：%v", names)
	}
}

func TestPublishPairSecondFailureRollsBackFirst(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	ops := &linkOps{failOn: func(dest string) error {
		if strings.HasPrefix(dest, second) {
			return errors.New("operation not permitted")
		}
		return nil
	}}
	pub := NewPublisher(ops)
	_, err := pub.PublishPair(
		Artifact{Dir: first, Name: "a.txt", Data: []byte("a")},
		Artifact{Dir: second, Name: "b.txt", Data: []byte("b")},
	)
	if err == nil {
		t.Fatal("第二件失败应返回错误")
	}
	var pe *PublishError
	if !errors.As(err, &pe) || pe.Kind != PublishKindLink {
		t.Fatalf("应报告 Link 失败：%#v", pe)
	}
	if names, _ := os.ReadDir(first); len(names) != 0 {
		t.Fatalf("第一件应回滚：%v", names)
	}
	if names, _ := os.ReadDir(second); len(names) != 0 {
		t.Fatalf("第二件不应留下半成品：%v", names)
	}
}

func TestPublishWriteFailureCleansTemp(t *testing.T) {
	dir := t.TempDir()
	ops := &failTempOps{writeErr: errors.New("disk full")}
	pub := NewPublisher(ops)
	_, err := pub.Publish(dir, "out.bin", []byte("data"))
	var pe *PublishError
	if !errors.As(err, &pe) || pe.Kind != PublishKindWrite {
		t.Fatalf("应报告写入失败：%v", err)
	}
	if names, _ := os.ReadDir(dir); len(names) != 0 {
		t.Fatalf("应清理临时文件：%v", names)
	}
}

func TestPublishCleanupFailureReportsPublished(t *testing.T) {
	dir := t.TempDir()
	ops := &linkOps{removeErr: errors.New("busy")}
	pub := NewPublisher(ops)
	path, err := pub.Publish(dir, "out.bin", []byte("data"))
	var pe *PublishError
	if !errors.As(err, &pe) || pe.Kind != PublishKindCleanup {
		t.Fatalf("应报告清理失败：%v", err)
	}
	if got, rerr := os.ReadFile(path); rerr != nil || string(got) != "data" {
		t.Fatalf("目标应完整：%q err=%v", got, rerr)
	}
}
