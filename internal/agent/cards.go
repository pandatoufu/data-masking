package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pandatoufu/data-masking/internal/core"
	"github.com/pandatoufu/data-masking/internal/workspace"
)

// 固定词库错误文案：不暴露候选名、词、行内容、完整路径或底层错误。
const (
	msgCardsMissing = "自定义词规则已启用，请先在程序目录的 cards 文件夹放入有效 TXT 词库文件。"
	msgCardsInvalid = "cards 词库无效或无法读取，请在本机检查 TXT 文件的编码、内容及输入限制后重试。"
)

// CardSnapshot 是本次用例的 cards 词库内存快照：
// words 供 core/高频复核使用；fingerprint/文件身份只用于私有任务记录与会话内确认重验。
type CardSnapshot struct {
	enabled     bool
	words       []string
	fingerprint string
	dirInfo     os.FileInfo
	files       []cardFile
}

// cardFile 保留候选名、原始字节摘要与打开后的 FileInfo。
// FileInfo 只保存在当前会话内存，不序列化到任务事件。
type cardFile struct {
	name   string
	digest [sha256.Size]byte
	info   os.FileInfo
}

// Words 返回词表副本（保留文件顺序与首次出现顺序）。
func (c *CardSnapshot) Words() []string {
	if c == nil {
		return []string{}
	}
	return append([]string(nil), c.words...)
}

// Fingerprint 返回私有 cards 指纹；custom 关闭时为固定空串。
func (c *CardSnapshot) Fingerprint() string {
	if c == nil || !c.enabled {
		return ""
	}
	return c.fingerprint
}

// LoadCardSnapshot 从 root/cards 直属加载扩展名大小写不敏感的 .txt 候选。
// custom 关闭时直接返回空快照，不列举、不读取任何词文件。
// 启用时目录/候选拒绝链接/reparse，候选必须是常规文件且打开后复核身份；
// 逐文件限量读取并复用 core.ParseCustomWords 的编码与三限校验，任一候选失败整批拒绝。
func LoadCardSnapshot(root string, enabled bool) (*CardSnapshot, error) {
	if !enabled {
		return &CardSnapshot{enabled: false, words: []string{}}, nil
	}
	dir := filepath.Join(root, workspace.DirCards)
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, NewError(CodeInvalidConfig, msgCardsMissing)
		}
		return nil, NewError(CodeInvalidConfig, msgCardsInvalid)
	}
	if !dirInfo.IsDir() || workspace.IsLinkOrReparse(dir, dirInfo) {
		return nil, NewError(CodeInvalidConfig, msgCardsInvalid)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, NewError(CodeInvalidConfig, msgCardsInvalid)
	}

	candidates := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.EqualFold(filepath.Ext(e.Name()), ".txt") {
			candidates = append(candidates, e.Name())
		}
	}
	if len(candidates) == 0 {
		return nil, NewError(CodeInvalidConfig, msgCardsMissing)
	}

	snap := &CardSnapshot{enabled: true, dirInfo: dirInfo, files: make([]cardFile, 0, len(candidates))}
	seen := map[string]bool{}
	for _, name := range candidates {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil {
			return nil, NewError(CodeInvalidConfig, msgCardsInvalid)
		}
		if !info.Mode().IsRegular() || workspace.IsLinkOrReparse(path, info) {
			return nil, NewError(CodeInvalidConfig, msgCardsInvalid)
		}
		data, opened, err := readCardFile(path, info)
		if err != nil {
			return nil, NewError(CodeInvalidConfig, msgCardsInvalid)
		}
		words, err := core.ParseCustomWords(nil, data, true)
		if err != nil {
			return nil, NewError(CodeInvalidConfig, msgCardsInvalid)
		}
		for _, w := range words {
			if seen[w] {
				continue
			}
			seen[w] = true
			snap.words = append(snap.words, w)
			if len(snap.words) > core.MaxWordCount {
				return nil, NewError(CodeInvalidConfig, msgCardsInvalid)
			}
		}
		snap.files = append(snap.files, cardFile{name: name, digest: sha256.Sum256(data), info: opened})
	}
	snap.fingerprint = cardFingerprint(snap.files)
	return snap, nil
}

// readCardFile 限量读取单个候选（上限+1 探测），并在打开后复核常规文件与身份。
func readCardFile(path string, lstat os.FileInfo) ([]byte, os.FileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()

	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(lstat, opened) {
		return nil, nil, errors.New("agent: cards 候选身份不一致")
	}
	data, err := io.ReadAll(io.LimitReader(f, core.MaxWordFileSize+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(data)) > core.MaxWordFileSize {
		return nil, nil, errors.New("agent: cards 词文件超过大小上限")
	}
	return data, opened, nil
}

// cardFingerprint 按 design D3a 计算私有指纹：
// 候选数量、每个有序候选名的字节长度与名字节、每个文件原始字节的 32 字节 SHA-256
// 均按长度前缀（8 字节小端 uint64）编码后整体取 SHA-256，仅返回 64 字符小写 hex。
func cardFingerprint(files []cardFile) string {
	var buf bytes.Buffer
	var width [8]byte
	writeLen := func(n uint64) {
		binary.LittleEndian.PutUint64(width[:], n)
		buf.Write(width[:])
	}
	writeLen(uint64(len(files)))
	for _, f := range files {
		writeLen(uint64(len(f.name)))
		buf.WriteString(f.name)
		writeLen(uint64(len(f.digest)))
		buf.Write(f.digest[:])
	}
	sum := sha256.Sum256(buf.Bytes())
	return SHA256Hex(sum[:])
}

// sameCards 比较当前会话内保存的 cards 身份与本次重读快照。
// custom 关闭时 cards 内容/身份不参与重验；启用时目录或任一候选被替换（即使字节相同）都视为变化。
func (a *App) sameCards(jobID string, current *CardSnapshot) bool {
	previous, ok := a.storedCards(jobID)
	if !ok || previous == nil || current == nil {
		return false
	}
	if !previous.enabled && !current.enabled {
		return true
	}
	if previous.enabled != current.enabled || previous.fingerprint != current.fingerprint {
		return false
	}
	if previous.dirInfo == nil || current.dirInfo == nil || !os.SameFile(previous.dirInfo, current.dirInfo) {
		return false
	}
	if len(previous.files) != len(current.files) {
		return false
	}
	for i := range previous.files {
		if previous.files[i].name != current.files[i].name {
			return false
		}
		if previous.files[i].info == nil || current.files[i].info == nil {
			return false
		}
		if !os.SameFile(previous.files[i].info, current.files[i].info) {
			return false
		}
	}
	return true
}
