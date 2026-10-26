package agent

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/pandatoufu/data-masking/internal/workspace"
)

// headless 文档读取与 core/server 使用同一有效 max_file_size：三种格式精确等于通过、
// 加 1 拒绝；prepare 超限在读取阶段失败且不产生任何交付/任务产物。
func TestReadSourceDocumentLimitAcrossFormats(t *testing.T) {
	root := t.TempDir()
	files := map[string][]byte{
		"notes.txt": []byte("phone 13800138000\n"),
		"合同.docx":   append([]byte("PK\x03\x04"), []byte("docx-payload")...),
		"报告.pptx":   append([]byte("PK\x03\x04"), []byte("pptx-payload")...),
	}
	for name, data := range files {
		seedOriginal(t, root, name, data)
		limit := int64(len(data))
		got, err := ReadSource(root, name, limit)
		if err != nil {
			t.Fatalf("%s 等于上限应可读：%v", name, err)
		}
		if int64(len(got)) != limit {
			t.Fatalf("%s 应完整读取 %d 字节，got %d", name, limit, len(got))
		}
		_, err = ReadSource(root, name, limit-1)
		var ae *Error
		if !errors.As(err, &ae) || ae.Code != CodeInputTooLarge {
			t.Fatalf("%s 加 1 字节应返回 input_too_large，got %v", name, err)
		}
	}
}

func TestPrepareOverLimitProducesNoArtifact(t *testing.T) {
	root := t.TempDir()
	if err := workspace.EnsureHeadless(root); err != nil {
		t.Fatal(err)
	}
	seedCards(t, root, "fixture.txt", []byte("fixture-card-word\n"))
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"max_file_size": 16, "high_freq_threshold": 20}`), 0o644); err != nil {
		t.Fatal(err)
	}
	app := NewApp(root, cfgPath)
	if err := app.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })

	project := t.TempDir()
	data := append([]byte("PK\x03\x04"), []byte("0123456789abc")...) // 17 字节，超过 16 字节上限
	names := []string{"big.txt", "big.docx", "big.pptx"}
	for _, name := range names {
		seedOriginal(t, root, name, data)
	}
	before := treeSnapshot(t, root)
	for _, name := range names {
		resp := app.Prepare(PrepareRequest{SourceName: name, ProjectPath: project, Profile: "strict"})
		if resp.Status != StatusFailed || resp.Code != string(CodeInputTooLarge) {
			t.Fatalf("%s 超限应在读取阶段失败：%+v", name, resp)
		}
		// 原件保留且不被改写。
		got, err := os.ReadFile(filepath.Join(root, workspace.DirOriginal, name))
		if err != nil || string(got) != string(data) {
			t.Fatalf("%s 原件应保持不变：err=%v", name, err)
		}
		if diff := diffSnapshots(before, treeSnapshot(t, root)); len(diff) != 0 {
			t.Fatalf("%s 超限不得产生任务/交付产物：%v", name, diff)
		}
	}
	if entries, err := os.ReadDir(project); err != nil || len(entries) != 0 {
		t.Fatalf("项目目录不应有产物：%v %v", entries, err)
	}
}

// treeSnapshot 记录 root 下所有相对路径（含文件大小），用于断言失败路径不新增产物。
func treeSnapshot(t *testing.T, root string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[rel] = info.Size()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func diffSnapshots(before, after map[string]int64) []string {
	var diff []string
	for path, size := range after {
		if old, ok := before[path]; !ok {
			diff = append(diff, "+"+path)
		} else if old != size {
			diff = append(diff, "~"+path)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			diff = append(diff, "-"+path)
		}
	}
	sort.Strings(diff)
	return diff
}
