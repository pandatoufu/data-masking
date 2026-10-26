package server

import (
	"errors"
	"io"
	"math"
	"net/http"

	"github.com/pandatoufu/data-masking/internal/core"
	"github.com/pandatoufu/data-masking/internal/i18n"
	"github.com/pandatoufu/data-masking/internal/loader"
)

// multipart 协议保护参数：与旧 ParseMultipartForm(32MiB) 的有限保护等价或更严。
// 这些是协议元数据/字段数量预算，不是文档业务上限；文档、词文件与 mapping 各自限量。
const (
	// formMaxParts 与旧 ReadForm 的 maxParts 一致，防止无限 part 拖垮请求。
	formMaxParts = 1000
	// formMaxValueBytes 对应旧解析器为非文件字段预留的内存预算（32MiB + 10MiB）。
	formMaxValueBytes int64 = 42 << 20
)

// errFormTooLarge 表示 multipart 协议元数据超出有限预算（旧解析器同样会拒绝）。
var errFormTooLarge = errors.New("server: multipart 表单超过协议预算")

// partReadError 标记读取某个 part 内容失败，并保留字段名以映射既有错误语义。
type partReadError struct {
	field string
	err   error
}

func (e *partReadError) Error() string { return "server: 读取表单字段 " + e.field + " 失败" }
func (e *partReadError) Unwrap() error { return e.err }

// formFile 是限量读取后的单个文件部件快照。
// over 为 true 表示实际内容超过对应上限，data 至多保留上限 + 1 字节。
type formFile struct {
	present  bool
	filename string
	data     []byte
	over     bool
}

// formValue 是受文档上限约束的文本值（粘贴还原）。
type formValue struct {
	present bool
	value   string
	over    bool
}

// formKind 决定本次请求真正需要的文件/文本字段；未使用字段只做丢弃式读取，
// 不进入内存快照（mapping 仅在还原时读取，词文件仅在脱敏时读取）。
type formKind int

const (
	formForMask formKind = iota
	formForRestore
)

// formData 是一次请求的限量表单快照：字段顺序不限，重复值按出现顺序保留。
type formData struct {
	values  map[string][]string
	doc     formFile
	word    formFile
	mapping formFile
	text    formValue
}

func newFormData() *formData {
	return &formData{values: map[string][]string{}}
}

// first 返回同名值中的首个（与旧 FormValue 语义一致）。
func (f *formData) first(name string) string {
	if vs := f.values[name]; len(vs) > 0 {
		return vs[0]
	}
	return ""
}

// all 返回同名值的全部出现顺序（保留重复 confirmed/custom）。
func (f *formData) all(name string) []string { return f.values[name] }

// docLimit 返回本次请求的有效文档上限；<=0 沿用既有 fallback。
func (s *Server) docLimit() int64 {
	if s.cfg != nil && s.cfg.MaxFileSize > 0 {
		return s.cfg.MaxFileSize
	}
	return loader.DefaultMaxSize
}

// readBounded 读取至多 limit+1 字节并报告是否超限；limit 接近 MaxInt64 时不发生溢出。
func readBounded(r io.Reader, limit int64) ([]byte, bool, error) {
	if limit < 0 {
		limit = 0
	}
	capBytes := limit
	if limit < math.MaxInt64 {
		capBytes = limit + 1
	}
	data, err := io.ReadAll(io.LimitReader(r, capBytes))
	if err != nil {
		return nil, false, err
	}
	return data, int64(len(data)) > limit, nil
}

// parseForm 逐 part 读取两种业务表单，不调用会自动把大文件 spool 到磁盘的 ReadForm。
// 若调用方已经预解析 MultipartForm（兼容层/测试），则从该结构读取；临时资源由最外层
// 响应路径统一清理。
func parseForm(r *http.Request, docLimit int64, kind formKind) (*formData, error) {
	if docLimit <= 0 {
		docLimit = loader.DefaultMaxSize
	}
	if r.MultipartForm != nil {
		return parsePreparsedForm(r, docLimit, kind)
	}
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, err
	}
	fd := newFormData()
	valueBudget := formMaxValueBytes
	for parts := 0; ; {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		parts++
		if parts > formMaxParts {
			return nil, errFormTooLarge
		}
		name := p.FormName()
		if name == "" {
			continue // 旧解析器同样跳过无 form-data 名称的部件
		}
		filename := p.FileName()
		if filename == "" {
			// 非文件字段：text 属于文档输入，其余受协议字段预算约束。
			if name == "text" {
				if kind != formForRestore {
					_, _ = io.Copy(io.Discard, p) // 脱敏不使用粘贴文本
					continue
				}
				if fd.text.present {
					continue // 旧 FormValue 只取首个 text
				}
				data, over, rerr := readBounded(p, docLimit)
				if rerr != nil {
					return nil, &partReadError{field: name, err: rerr}
				}
				fd.text = formValue{present: true, value: string(data), over: over}
				continue
			}
			data, over, rerr := readBounded(p, valueBudget)
			if rerr != nil {
				return nil, &partReadError{field: name, err: rerr}
			}
			if over {
				return nil, errFormTooLarge
			}
			valueBudget -= int64(len(data))
			fd.values[name] = append(fd.values[name], string(data))
			continue
		}
		switch name {
		case "file":
			if fd.doc.present {
				continue // 首个同名文件生效，其余丢弃
			}
			data, over, rerr := readBounded(p, docLimit)
			if rerr != nil {
				return nil, &partReadError{field: name, err: rerr}
			}
			fd.doc = formFile{present: true, filename: filename, data: data, over: over}
		case "custom_file":
			if kind != formForMask {
				_, _ = io.Copy(io.Discard, p) // 还原不使用词文件
				continue
			}
			if fd.word.present {
				continue
			}
			data, over, rerr := readBounded(p, core.MaxWordFileSize)
			if rerr != nil {
				return nil, &partReadError{field: name, err: rerr}
			}
			fd.word = formFile{present: true, filename: filename, data: data, over: over}
		case "mapping":
			if kind != formForRestore {
				_, _ = io.Copy(io.Discard, p) // 脱敏不使用 mapping
				continue
			}
			if fd.mapping.present {
				continue
			}
			data, rerr := io.ReadAll(p)
			if rerr != nil {
				return nil, &partReadError{field: name, err: rerr}
			}
			fd.mapping = formFile{present: true, filename: filename, data: data}
		default:
			// 未知文件字段既不进内存也不落盘。
			_, _ = io.Copy(io.Discard, p)
		}
	}
	return fd, nil
}

// parsePreparsedForm 兼容已调用 ParseMultipartForm 的调用方；仍按新上限构建快照，
// 由 respondForm 在最外层清理其临时资源。
func parsePreparsedForm(r *http.Request, docLimit int64, kind formKind) (*formData, error) {
	fd := newFormData()
	for name, vals := range r.MultipartForm.Value {
		if name == "text" {
			if kind != formForRestore || len(vals) == 0 {
				continue
			}
			fd.text = formValue{present: true, value: vals[0], over: int64(len(vals[0])) > docLimit}
			continue
		}
		fd.values[name] = append([]string(nil), vals...)
	}
	readHeader := func(name string, limit int64) (formFile, error) {
		fhs := r.MultipartForm.File[name]
		if len(fhs) == 0 {
			return formFile{}, nil
		}
		fh := fhs[0]
		f, err := fh.Open()
		if err != nil {
			return formFile{}, &partReadError{field: name, err: err}
		}
		defer func() { _ = f.Close() }()
		data, over, err := readBounded(f, limit)
		if err != nil {
			return formFile{}, &partReadError{field: name, err: err}
		}
		return formFile{present: true, filename: fh.Filename, data: data, over: over}, nil
	}
	var err error
	if fd.doc, err = readHeader("file", docLimit); err != nil {
		return nil, err
	}
	if kind == formForMask {
		if fd.word, err = readHeader("custom_file", core.MaxWordFileSize); err != nil {
			return nil, err
		}
	}
	fhs := r.MultipartForm.File["mapping"]
	if kind == formForRestore && len(fhs) > 0 {
		fh := fhs[0]
		f, oerr := fh.Open()
		if oerr != nil {
			return nil, &partReadError{field: "mapping", err: oerr}
		}
		data, rerr := io.ReadAll(f)
		_ = f.Close()
		if rerr != nil {
			return nil, &partReadError{field: "mapping", err: rerr}
		}
		fd.mapping = formFile{present: true, filename: fh.Filename, data: data}
	}
	return fd, nil
}

// cleanupMultipart 关闭并清理调用方预解析持有的 multipart 临时资源；
// 正常逐 part 路径不创建任何临时文件。调用方须在该资源的最外层响应路径调用。
func cleanupMultipart(r *http.Request) error {
	if r.MultipartForm == nil {
		return nil
	}
	return r.MultipartForm.RemoveAll()
}

// respondForm 在写出任何响应前清理预解析临时资源；清理失败以本地化反馈替代本次结果，
// 不静默宣称资源已清除，也不回显原文/词值/mapping。
func (s *Server) respondForm(w http.ResponseWriter, r *http.Request, status int, payload map[string]any) {
	cleanup := cleanupMultipart
	if s.formCleanup != nil {
		cleanup = s.formCleanup
	}
	if err := cleanup(r); err != nil {
		writeErrPres(w, http.StatusInternalServerError, "表单临时资源清理失败",
			"请重试；如问题持续，请重启应用后再试。", i18n.P("http.form_cleanup_failed", nil))
		return
	}
	writeJSON(w, status, payload)
}

// errPayload 构造与旧字段兼容的错误响应体。
func errPayload(reason, advice string, pres *i18n.Presentation) map[string]any {
	body := map[string]any{
		"ok":      false,
		"error":   reason,
		"message": reason + "。" + advice,
	}
	if pres != nil {
		body["presentation"] = pres
	}
	return body
}

// coreErrResult 把 core/loader 错误映射为 400 + 兼容响应体。
func coreErrResult(err error) (int, map[string]any) {
	var pres *i18n.Presentation
	if p, ok := err.(presenter); ok {
		pres = p.Presentation()
	}
	reason, advice := err.Error(), "请检查输入后重试。"
	if he, ok := err.(*loader.HumanError); ok {
		reason, advice = he.Reason, he.Advice
	}
	return http.StatusBadRequest, errPayload(reason, advice, pres)
}

// formParseResult 把解析失败映射为 400，并保留既有字段级错误文案。
func formParseResult(err error, restore bool) (int, map[string]any) {
	var pre *partReadError
	if errors.As(err, &pre) {
		switch {
		case pre.field == "file":
			return http.StatusBadRequest, errPayload("读取文件失败", "请重试。", i18n.P("http.file_read_failed", nil))
		case restore && pre.field == "mapping":
			return http.StatusBadRequest, errPayload("读取 mapping 失败", "请重试。", i18n.P("http.mapping_read_failed", nil))
		}
	}
	advice := "请重新选择文件后再试。"
	if restore {
		advice = "请重新提交后再试。"
	}
	return http.StatusBadRequest, errPayload("表单解析失败", advice, i18n.P("http.form_parse_failed", nil))
}
