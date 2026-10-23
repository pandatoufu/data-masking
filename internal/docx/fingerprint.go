package docx

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strconv"
)

// textFingerprintDomain 是 docx-text-v1 指纹的域分隔字符串。
const textFingerprintDomain = "docx-text-v1"

// TextFingerprint 按冻结的 `docx-text-v1` 编码生成文字参考字节流：
// 先写长度前缀域字符串与 unit 数量，再逐 unit 按 Part、Paragraph、Segment、Text
// 各写一个长度前缀 UTF-8 字符串；所有长度与数量为 8-byte big-endian。
//
// 该编码只覆盖有序逻辑文字单元，不依赖 run 划分、ZIP 时间戳或样式。
func TextFingerprint(units []TextUnit) []byte {
	var out []byte
	out = appendLP(out, []byte(textFingerprintDomain))
	var cnt [8]byte
	binary.BigEndian.PutUint64(cnt[:], uint64(len(units)))
	out = append(out, cnt[:]...)
	for _, u := range units {
		out = appendLP(out, []byte(u.Part))
		out = appendLP(out, []byte(strconv.Itoa(u.Paragraph)))
		out = appendLP(out, []byte(strconv.Itoa(u.Segment)))
		out = appendLP(out, []byte(u.Text))
	}
	return out
}

// TextSHA256 返回 docx-text-v1 编码的 SHA-256 十六进制小写串。
func TextSHA256(units []TextUnit) string {
	sum := sha256.Sum256(TextFingerprint(units))
	return hex.EncodeToString(sum[:])
}

func appendLP(dst, s []byte) []byte {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(s)))
	dst = append(dst, n[:]...)
	return append(dst, s...)
}
