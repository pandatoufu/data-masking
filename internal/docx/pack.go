package docx

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sort"
	"strings"
	"time"
)

// patch 是对某部件原始字节的一次替换；start==end 表示插入。
type patch struct {
	start int
	end   int
	text  string
}

// addPatch 累积对某部件的源码补丁。
func (d *Document) addPatch(partName string, p patch) {
	d.patches[partName] = append(d.patches[partName], p)
}

var zipFixedTime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// Bytes 在清理与文字回写后执行确定性打包，并再次执行包预算门禁。
func (d *Document) Bytes() ([]byte, error) {
	out, err := d.buildBytes()
	if err != nil {
		return nil, err
	}
	if _, err := openZip(out, d.limits); err != nil {
		return nil, err
	}
	return out, nil
}

func (d *Document) buildBytes() ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range d.order {
		p := d.parts[name]
		if p.removed {
			continue
		}
		data := p.data
		if ps := d.patches[name]; len(ps) > 0 {
			data = applyPatches(data, ps)
		}
		changed := p.dirty || p.orig == nil || len(d.patches[name]) > 0
		if changed {
			fh := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: zipFixedTime}
			w, err := zw.CreateHeader(fh)
			if err != nil {
				return nil, err
			}
			if _, err := w.Write(data); err != nil {
				return nil, err
			}
			continue
		}
		raw, err := p.orig.OpenRaw()
		if err != nil {
			return nil, err
		}
		rawBytes, err := io.ReadAll(raw)
		if err != nil {
			return nil, err
		}
		fh := p.orig.FileHeader
		fh.Modified = zipFixedTime
		fh.Extra = nil
		fh.Comment = ""
		fh.Flags &^= 0x08 // 清除 data descriptor 标志
		w, err := zw.CreateRaw(&fh)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(rawBytes); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// applyPatches 按源码偏移升序应用补丁，补丁基于同一份原始字节，故不产生偏移漂移。
func applyPatches(data []byte, ps []patch) []byte {
	sorted := make([]patch, len(ps))
	copy(sorted, ps)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].start != sorted[j].start {
			return sorted[i].start < sorted[j].start
		}
		return sorted[i].end < sorted[j].end
	})
	var b bytes.Buffer
	prev := 0
	for _, p := range sorted {
		if p.start < prev || p.start < 0 || p.end > len(data) || p.start > p.end {
			continue
		}
		b.Write(data[prev:p.start])
		b.WriteString(p.text)
		prev = p.end
	}
	b.Write(data[prev:])
	return b.Bytes()
}

// escapeText 对文字内容做最小必要 XML 转义（保留换行/制表，转义 CR）。
func escapeText(s string) string {
	if !strings.ContainsAny(s, "&<>\r") {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '\r':
			b.WriteString("&#xD;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// needsPreserve 报告文字是否含首尾空白，需要 xml:space="preserve"。
func needsPreserve(s string) bool {
	if s == "" {
		return false
	}
	ws := func(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }
	return ws(s[0]) || ws(s[len(s)-1])
}

// insertionPoint 返回在起始标签中插入属性的位置（闭括号或 "/>" 之前）。
func insertionPoint(data []byte, tagStart, tagEnd int) int {
	if tagStart < 0 || tagEnd > len(data) || tagStart >= tagEnd {
		return tagStart
	}
	tag := data[tagStart:tagEnd]
	i := bytes.LastIndexByte(tag, '>')
	if i < 0 {
		return tagEnd
	}
	if i >= 1 && tag[i-1] == '/' {
		return tagStart + i - 1
	}
	return tagStart + i
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
