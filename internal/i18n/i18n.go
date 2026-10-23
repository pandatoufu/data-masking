// Package i18n 提供嵌入双语的稳定 message_id/params 呈现元数据与本地化渲染。
//
// 设计约束：
//   - 只做展示层本地化，不改变错误分类、处理算法、产物或旧 API 的中文字段。
//   - 所有资源随应用嵌入，绝不依赖网络、CDN 或在线翻译。
//   - 未知 message_id 按当前语言给出通用失败/下一步，绝不误报成功或吞掉定位。
//   - 参数值始终原样插值（前端负责按 textContent/转义渲染），不翻译用户内容。
package i18n

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Lang 是展示语言。
type Lang string

const (
	// ZH 中文。
	ZH Lang = "zh"
	// EN English。
	EN Lang = "en"
)

// Valid 报告是否为受支持的语言值（用于偏好/绑定校验，拒绝未知值）。
func (l Lang) Valid() bool { return l == ZH || l == EN }

// NormalizeLang 用于系统语言探测结果的归一：英文归 EN，其余归 ZH。
// 偏好持久化不接受未知值，此函数不用于偏好校验。
func NormalizeLang(s string) Lang {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", "-")
	if s == "en" || strings.HasPrefix(s, "en-") {
		return EN
	}
	return ZH
}

// LanguageName 返回语言在自身语言下的显示名（用于偏好入口）。
func LanguageName(l Lang) string {
	if l == EN {
		return "English"
	}
	return "中文"
}

// Presentation 是服务器/绑定反馈追加的展示元数据，字段固定为 message_id 与 params。
type Presentation struct {
	MessageID string         `json:"message_id"`
	Params    map[string]any `json:"params,omitempty"`
}

// P 构造一个 Presentation。
func P(id string, params map[string]any) *Presentation {
	return &Presentation{MessageID: id, Params: params}
}

// Nested 表示参数值本身也是一个需要本地化的子消息（参数一致的一层嵌套）。
// Go 渲染时递归解析；前端按 {message_id, params} 对象同样递归处理。
type Nested struct {
	MessageID string         `json:"message_id"`
	Params    map[string]any `json:"params,omitempty"`
}

// N 构造嵌套子消息。
func N(id string, params map[string]any) Nested { return Nested{MessageID: id, Params: params} }

// 通用回退标识。
const (
	// IDErrorGeneric 是失败类通用回退。
	IDErrorGeneric = "error.generic"
	// IDErrorUnknownID 是未知 message_id 的通用回退（渲染时附原始 id）。
	IDErrorUnknownID = "error.unknown_id"
	// IDWarningGeneric 是警告类通用回退。
	IDWarningGeneric = "warning.generic"
)

// Fallback 返回当前语言的通用失败/下一步文案；kind 为 "warning" 时使用警告回退。
func Fallback(lang Lang, kind string) string {
	id := IDErrorGeneric
	if kind == "warning" {
		id = IDWarningGeneric
	}
	return T(lang, id, nil)
}

// T 按语言解析 message_id 并插值 params。未知 id 使用当前语言的通用回退。
func T(lang Lang, id string, params map[string]any) string {
	if !lang.Valid() {
		lang = ZH
	}
	if s, ok := lookup(id, lang); ok {
		return interpolate(s, params, lang)
	}
	if s, ok := lookup(id, EN); ok {
		return interpolate(s, params, lang)
	}
	if id == "" {
		return T(lang, IDErrorGeneric, nil)
	}
	return interpolate(lookup2(IDErrorUnknownID, lang), map[string]any{"id": id}, lang)
}

var paramRe = regexp.MustCompile(`\{([a-zA-Z0-9_.]+)\}`)

func interpolate(tmpl string, params map[string]any, lang Lang) string {
	if !strings.Contains(tmpl, "{") || params == nil {
		// 即使没有参数也应展开嵌套占位（调用方可能传 nil）。
		if !strings.Contains(tmpl, "{") {
			return tmpl
		}
	}
	return paramRe.ReplaceAllStringFunc(tmpl, func(tok string) string {
		key := tok[1 : len(tok)-1]
		v, ok := params[key]
		if !ok {
			return tok
		}
		return stringify(v, lang)
	})
}

func stringify(v any, lang Lang) string {
	switch t := v.(type) {
	case nil:
		return ""
	case Nested:
		return T(lang, t.MessageID, t.Params)
	case *Nested:
		if t == nil {
			return ""
		}
		return T(lang, t.MessageID, t.Params)
	case Presentation:
		return T(lang, t.MessageID, t.Params)
	case *Presentation:
		if t == nil {
			return ""
		}
		return T(lang, t.MessageID, t.Params)
	case []Nested:
		parts := make([]string, 0, len(t))
		for _, n := range t {
			parts = append(parts, T(lang, n.MessageID, n.Params))
		}
		return strings.Join(parts, "；")
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			parts = append(parts, stringify(e, lang))
		}
		return strings.Join(parts, "；")
	default:
		return fmt.Sprint(v)
	}
}

// Messages 返回指定语言的完整静态文案表（供页面首帧注入与测试校验）。
func Messages(lang Lang) map[string]string {
	if !lang.Valid() {
		lang = ZH
	}
	out := make(map[string]string, len(catalog))
	for id, m := range catalog {
		if s, ok := m[lang]; ok {
			out[id] = s
		} else if s, ok := m[EN]; ok {
			out[id] = s
		}
	}
	return out
}

// AllMessages 返回 zh/en 两份文案表，供页面注入。
func AllMessages() map[string]map[string]string {
	return map[string]map[string]string{
		string(ZH): Messages(ZH),
		string(EN): Messages(EN),
	}
}

// Manifest 返回全部已注册 message_id（排序），供完整性回归枚举。
func Manifest() []string {
	out := make([]string, 0, len(catalog))
	for id := range catalog {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Has 报告某 id 是否有该语言（或 EN 回退）文案。
func Has(id string, lang Lang) bool {
	_, ok := lookup(id, lang)
	return ok
}

func lookup(id string, lang Lang) (string, bool) {
	m, ok := catalog[id]
	if !ok {
		return "", false
	}
	s, ok := m[lang]
	return s, ok
}

func lookup2(id string, lang Lang) string {
	if s, ok := lookup(id, lang); ok {
		return s
	}
	if s, ok := lookup(id, EN); ok {
		return s
	}
	// 最后兜底（catalog 必然包含两个通用回退，不会走到这里）。
	if lang == EN {
		return "The operation did not complete. Check your input and try again."
	}
	return "操作未完成，请检查输入后重试。"
}
