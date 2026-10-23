package agent

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/pandatoufu/data-masking/internal/workspace"
)

var fingerprintPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func cardsRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, workspace.DirCards), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeCard(t *testing.T, root, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(root, workspace.DirCards, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertCardsError(t *testing.T, err error, want string) {
	t.Helper()
	var ae *Error
	if !errors.As(err, &ae) {
		t.Fatalf("应返回固定配置错误，got %v", err)
	}
	if ae.Code != CodeInvalidConfig || ae.Message != want {
		t.Fatalf("固定错误不符：%+v", ae)
	}
}

// 候选按原始文件名字节升序、文件内行序解析；trim/空行忽略、跨文件精确去重保留首次出现。
func TestCardSnapshotMergesOrderedTXTAndIgnoresNonTXT(t *testing.T) {
	root := cardsRoot(t)
	writeCard(t, root, "b.txt", []byte("gamma\n测试词\nbeta\n"))
	gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("delta\n重复\n"))
	if err != nil {
		t.Fatal(err)
	}
	writeCard(t, root, "C.TXT", gbk)
	writeCard(t, root, "a.txt", []byte("  alpha  \r\n\r\nbeta\nalpha\n"))
	writeCard(t, root, "ignored.log", []byte("ignored-word\n"))
	if err := os.MkdirAll(filepath.Join(root, workspace.DirCards, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCard(t, root, filepath.Join("nested", "nested.txt"), []byte("nested-word\n"))

	snap, err := LoadCardSnapshot(root, true)
	if err != nil {
		t.Fatalf("应成功加载：%v", err)
	}
	// 文件名按原始字节升序：C.TXT(0x43) → a.txt(0x61) → b.txt(0x62)。
	want := []string{"delta", "重复", "alpha", "beta", "gamma", "测试词"}
	got := snap.Words()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("合并顺序/去重不符：%v want %v", got, want)
	}
	for _, banned := range []string{"ignored-word", "nested-word"} {
		for _, w := range got {
			if w == banned {
				t.Fatalf("不得读取非 TXT/子目录：%v", got)
			}
		}
	}
}

// custom 关闭时完全不列举/读取 cards，即使目录缺失或词文件损坏。
func TestCardSnapshotDisabledDoesNotRead(t *testing.T) {
	root := t.TempDir() // 没有 cards 目录
	snap, err := LoadCardSnapshot(root, false)
	if err != nil {
		t.Fatalf("关闭 custom 不应读取/要求词库：%v", err)
	}
	if len(snap.Words()) != 0 || snap.Fingerprint() != "" {
		t.Fatalf("关闭 custom 应为空词集/无指纹：%v %q", snap.Words(), snap.Fingerprint())
	}

	root = cardsRoot(t)
	writeCard(t, root, "bad.txt", []byte{0xff, 0xfe, 0x00, 0x00})
	snap, err = LoadCardSnapshot(root, false)
	if err != nil || len(snap.Words()) != 0 {
		t.Fatalf("关闭 custom 不得读取损坏 TXT：%v %v", snap, err)
	}
}

// 无直属 TXT 使用固定“先放词库”文案；空/非法文件使用固定“无效或无法读取”文案。
func TestCardSnapshotFixedErrors(t *testing.T) {
	root := cardsRoot(t)
	_, err := LoadCardSnapshot(root, true)
	assertCardsError(t, err, msgCardsMissing)

	writeCard(t, root, "empty.txt", []byte("\n\r\n  \n"))
	writeCard(t, root, "good.txt", []byte("alpha\n"))
	_, err = LoadCardSnapshot(root, true)
	assertCardsError(t, err, msgCardsInvalid)

	root = cardsRoot(t)
	writeCard(t, root, "utf16.txt", []byte{0xff, 0xfe, 'a', 0x00, 'b', 0x00})
	_, err = LoadCardSnapshot(root, true)
	assertCardsError(t, err, msgCardsInvalid)
}

// .txt 命名的目录、特殊文件与链接候选整批拒绝；链接外目标不被读取。
func TestCardSnapshotRejectsUnsafeCandidates(t *testing.T) {
	root := cardsRoot(t)
	if err := os.Mkdir(filepath.Join(root, workspace.DirCards, "dir.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := LoadCardSnapshot(root, true)
	assertCardsError(t, err, msgCardsInvalid)

	root = cardsRoot(t)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	secret := "SECRET-OUTSIDE-CARD"
	if err := os.WriteFile(outside, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, workspace.DirCards, "linked.txt")
	if err := os.Symlink(outside, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("Windows 无创建符号链接权限：原生 reparse 用例按平台运行")
		}
		t.Fatal(err)
	}
	_, err = LoadCardSnapshot(root, true)
	assertCardsError(t, err, msgCardsInvalid)
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("错误不得回显链接外内容：%v", err)
	}
	// 链接外目标未读取：内容保持不变。
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != secret+"\n" {
		t.Fatalf("不得改写/读取链接外目标：%q %v", got, err)
	}
}

// 三限：单文件 1MB、单词 256 字符、合并后 1000 去重词；任何候选失败整批拒绝。
func TestCardSnapshotLimitsRejectWholeBatch(t *testing.T) {
	root := cardsRoot(t)
	writeCard(t, root, "a.txt", []byte("good\n"))
	writeCard(t, root, "big.txt", bytes.Repeat([]byte("a"), int(1<<20)+1))
	_, err := LoadCardSnapshot(root, true)
	assertCardsError(t, err, msgCardsInvalid)

	root = cardsRoot(t)
	writeCard(t, root, "a.txt", []byte("good\n"))
	writeCard(t, root, "long.txt", []byte(strings.Repeat("长", 257)+"\n"))
	_, err = LoadCardSnapshot(root, true)
	assertCardsError(t, err, msgCardsInvalid)

	root = cardsRoot(t)
	var all []string
	for i := 0; i < 1001; i++ {
		all = append(all, "word-"+string(rune(0x4e00+i)))
	}
	writeCard(t, root, "many1.txt", []byte(strings.Join(all[:600], "\n")+"\n"))
	writeCard(t, root, "many2.txt", []byte(strings.Join(all[600:], "\n")+"\n"))
	_, err = LoadCardSnapshot(root, true)
	assertCardsError(t, err, msgCardsInvalid)
}

// 指纹固定为 64 字符小写 hex；文件名、原始字节或顺序变化都会改变指纹。
func TestCardFingerprintDeterministicAndSensitive(t *testing.T) {
	root := cardsRoot(t)
	writeCard(t, root, "a.txt", []byte("alpha\n"))
	first, err := LoadCardSnapshot(root, true)
	if err != nil {
		t.Fatal(err)
	}
	fp := first.Fingerprint()
	if !fingerprintPattern.MatchString(fp) {
		t.Fatalf("指纹应为 64 字符小写 hex：%q", fp)
	}
	again, err := LoadCardSnapshot(root, true)
	if err != nil || again.Fingerprint() != fp {
		t.Fatalf("同样输入应得到同一指纹：%v %v", again, err)
	}

	// 词集合相同但原始字节变化（追加空行）。
	writeCard(t, root, "a.txt", []byte("alpha\n\n"))
	changed, err := LoadCardSnapshot(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Fingerprint() == fp {
		t.Fatal("原始字节变化应改变指纹")
	}

	// 改名（内容不变）。
	root = cardsRoot(t)
	writeCard(t, root, "a.txt", []byte("alpha\n"))
	base, _ := LoadCardSnapshot(root, true)
	if err := os.Rename(filepath.Join(root, workspace.DirCards, "a.txt"), filepath.Join(root, workspace.DirCards, "b.txt")); err != nil {
		t.Fatal(err)
	}
	renamed, err := LoadCardSnapshot(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Fingerprint() == base.Fingerprint() {
		t.Fatal("候选名变化应改变指纹")
	}
}
