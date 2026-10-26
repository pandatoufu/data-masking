package pptx

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"path"
	"strings"
)

// Limits 是 PPTX 独立资源预算。零值由 normalize 填入默认值。
type Limits struct {
	MaxEntries           int   // 最多条目数（默认 2048）
	MaxTotalUncompressed int64 // 所有条目实际解压总量（默认 200MiB）
	MaxXMLPart           int64 // 单个 XML 部件大小（默认 20MiB）
	MaxDepth             int   // XML 嵌套深度（默认 128）
}

// DefaultLimits 返回规范预算。
func DefaultLimits() Limits {
	return Limits{
		MaxEntries:           2048,
		MaxTotalUncompressed: 200 * 1024 * 1024,
		MaxXMLPart:           20 * 1024 * 1024,
		MaxDepth:             128,
	}
}

func (l Limits) normalize() Limits {
	d := DefaultLimits()
	if l.MaxEntries <= 0 {
		l.MaxEntries = d.MaxEntries
	}
	if l.MaxTotalUncompressed <= 0 {
		l.MaxTotalUncompressed = d.MaxTotalUncompressed
	}
	if l.MaxXMLPart <= 0 {
		l.MaxXMLPart = d.MaxXMLPart
	}
	if l.MaxDepth <= 0 {
		l.MaxDepth = d.MaxDepth
	}
	return l
}

// oleSignature 是 OLE 复合文件（加密演示文稿）头。
var oleSignature = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

var zipLocalSig = []byte{'P', 'K', 0x03, 0x04}

// zipEntry 是读取后的一个包条目。
type zipEntry struct {
	name string
	file *zip.File
	data []byte
}

// openZip 有界读取 ZIP 包：校验条目数、路径、压缩方法、加密旗标、声明大小、CRC 与总解压预算。
func openZip(data []byte, limits Limits) ([]zipEntry, error) {
	if len(data) >= 8 && bytes.Equal(data[:8], oleSignature) {
		return nil, herrf("检测到加密的演示文稿（OLE 复合文件）",
			"本工具不能解密，请先在 PowerPoint 中打开并输入密码，另存为普通、未加密的 .pptx 后重试", "pptx.encrypted_ole")
	}
	if len(data) < 4 || !bytes.Equal(data[:4], zipLocalSig) {
		if looksLikeText(data) {
			return nil, herrf("文件不是有效的 PPTX 包（疑似纯文本改名为 .pptx）",
				"请确认文件确为 PowerPoint 另存的 .pptx，不要用改名方式伪装格式", "pptx.not_pptx")
		}
		return nil, herrf("文件不是有效的 PPTX 包（ZIP 结构损坏或格式不符）",
			"请用 PowerPoint 打开并另存为普通、未加密的 .pptx 后重试", "pptx.not_pptx")
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, herrf("无法解析 PPTX 的 ZIP 结构："+err.Error(),
			"文件可能已损坏；请在 PowerPoint 中打开并另存为普通、未加密的 .pptx 后重试", "pptx.not_pptx")
	}

	var out []zipEntry
	seen := map[string]bool{}
	var total int64
	for _, f := range zr.File {
		name := f.Name
		if strings.HasSuffix(name, "/") {
			continue
		}
		if len(out) >= limits.MaxEntries {
			return nil, herrf("PPTX 条目数超过上限",
				"演示文稿包含过多内部条目；请在 PowerPoint 中另存精简后再试", "pptx.zip_budget")
		}
		if err := validateEntryName(name); err != nil {
			return nil, err
		}
		key := lowerASCII(name)
		if seen[key] {
			return nil, herrf("PPTX 含重复或别名部件名 "+name,
				"包内部部件名必须唯一；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.part_name_invalid")
		}
		seen[key] = true

		if f.Flags&0x1 != 0 {
			return nil, herrf("PPTX 含加密 ZIP 条目",
				"本工具不能处理加密条目；请先在 PowerPoint 中解密并另存为普通 .pptx", "pptx.encrypted_ole")
		}
		if f.Method != zip.Store && f.Method != zip.Deflate {
			return nil, herrf("PPTX 含不支持的 ZIP 压缩方法",
				"请用 PowerPoint 重新另存为普通 .pptx 以使用受支持的压缩方式", "pptx.zip_budget")
		}

		isXML := isXMLPart(name)
		partCap := limits.MaxTotalUncompressed - total
		if isXML && limits.MaxXMLPart < partCap {
			partCap = limits.MaxXMLPart
		}
		if partCap < 0 {
			partCap = 0
		}
		buf, err := readBounded(f, partCap)
		if err != nil {
			return nil, err
		}
		if int64(len(buf)) > partCap {
			if isXML {
				return nil, herrf("PPTX 单个 XML 部件超过大小上限",
					"请精简演示文稿内容后另存为新的 .pptx 再试", "pptx.zip_budget")
			}
			return nil, herrf("PPTX 实际解压总量超过预算上限",
				"演示文稿解压后过大；请精简内容或拆分为多个文件再试", "pptx.zip_budget")
		}
		if uint64(len(buf)) != f.UncompressedSize64 {
			return nil, herrf("PPTX 条目声明大小与实际不符（疑似伪造大小）",
				"请用 PowerPoint 重新另存为正规 .pptx 后重试", "pptx.zip_budget")
		}
		total += int64(len(buf))
		out = append(out, zipEntry{name: name, file: f, data: buf})
	}
	return out, nil
}

// readBounded 读取条目至多 cap 字节；命中 EOF 触发 CRC 校验。
func readBounded(f *zip.File, cap int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, herrf("无法读取 PPTX 条目 "+f.Name+"："+err.Error(),
			"文件可能已损坏；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.not_pptx")
	}
	defer rc.Close()
	buf, err := io.ReadAll(io.LimitReader(rc, cap+1))
	if err != nil {
		if errors.Is(err, zip.ErrChecksum) {
			return nil, herrf("PPTX 条目 CRC 校验失败",
				"文件内容已损坏；请在 PowerPoint 中打开并另存为新的 .pptx 后重试", "pptx.zip_budget")
		}
		return nil, herrf("读取 PPTX 条目失败："+err.Error(),
			"文件可能已损坏；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.not_pptx")
	}
	return buf, nil
}

// validateEntryName 校验 ZIP 条目名：拒绝绝对路径、反斜杠、`..`、`.` 别名与盘符。
func validateEntryName(name string) error {
	if name == "" {
		return herrf("PPTX 含空部件名", "文件包结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.part_name_invalid")
	}
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") {
		return herrf("PPTX 含绝对路径部件名 "+name, "ZIP 部件名必须为包内相对路径；请重新另存 .pptx", "pptx.part_name_invalid")
	}
	if strings.Contains(name, "\\") {
		return herrf("PPTX 含反斜杠部件名 "+name, "ZIP 部件名必须使用正斜杠；请重新另存 .pptx", "pptx.part_name_invalid")
	}
	if strings.Contains(name, ":") {
		return herrf("PPTX 含非法部件名 "+name, "ZIP 部件名不得包含冒号；请重新另存 .pptx", "pptx.part_name_invalid")
	}
	if path.Clean(name) != name {
		return herrf("PPTX 含别名或越界部件名 "+name, "ZIP 部件名不得包含 . / .. 或重复分隔符；请重新另存 .pptx", "pptx.part_name_invalid")
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return herrf("PPTX 含非法路径部件名 "+name, "ZIP 部件名不得包含空段、. 或 ..；请重新另存 .pptx", "pptx.part_name_invalid")
		}
	}
	return nil
}

// isXMLPart 报告部件是否为 XML/XML 关系部件（受单 XML 大小预算约束）。
func isXMLPart(name string) bool {
	return strings.HasSuffix(lowerASCII(name), ".xml") || strings.HasSuffix(lowerASCII(name), ".rels")
}

// looksLikeText 粗略判断字节是否像纯文本（用于给出“改名”提示）。
func looksLikeText(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	limit := len(data)
	if limit > 512 {
		limit = 512
	}
	printable := 0
	for _, b := range data[:limit] {
		if b == 0x00 {
			return false
		}
		if b == '\t' || b == '\n' || b == '\r' || (b >= 0x20 && b < 0x7f) || b >= 0x80 {
			printable++
		}
	}
	return printable*100/limit >= 90
}
