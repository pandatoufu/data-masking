package docx

import "strings"

func lowerASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

func containsASCII(s, sub string) bool { return strings.Contains(s, sub) }

// prefixOf 返回限定名的前缀；无冒号返回空串。
func prefixOf(name string) string {
	if i := strings.IndexByte(name, ':'); i >= 0 {
		return name[:i]
	}
	return ""
}
