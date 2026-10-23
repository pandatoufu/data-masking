package core

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/config"
	"github.com/pandatoufu/data-masking/internal/detector"
	"github.com/pandatoufu/data-masking/internal/docx"
	"github.com/pandatoufu/data-masking/internal/docxtest"
	"github.com/pandatoufu/data-masking/internal/loader"
	"github.com/pandatoufu/data-masking/internal/mapping"
)

func docxFixture(t *testing.T, opts docxtest.FixtureOptions) []byte {
	t.Helper()
	data, err := docxtest.BuildFullFixture(opts)
	if err != nil {
		t.Fatalf("BuildFullFixture: %v", err)
	}
	return data
}

func reopenedText(t *testing.T, data []byte) string {
	t.Helper()
	doc, err := docx.Open(data, docx.DefaultLimits())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := doc.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	var parts []string
	for _, u := range doc.Units() {
		parts = append(parts, u.Text)
	}
	return strings.Join(parts, "|")
}

// 7.1 名称策略分流、大小门禁、词文件仅纯文本。
func TestDocxDispatchAndGates(t *testing.T) {
	cfg := config.Defaults()
	valid := docxFixture(t, docxtest.FixtureOptions{Body: []string{"正文 13812345678"}})

	// 大小写 DOCX 进入文档处理。
	out, err := MaskFile("REPORT.DOCX", valid, MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatalf("大写 DOCX 应处理：%v", err)
	}
	if out.Format != "docx" || out.Encoding != "" {
		t.Fatalf("DOCX 响应格式/编码错误：%q %q", out.Format, out.Encoding)
	}
	if _, err := docx.Open(out.MaskedBytes, docx.DefaultLimits()); err != nil {
		t.Fatalf("产物应是有效 DOCX：%v", err)
	}

	// 原五格式仍走纯文本。
	for _, name := range []string{"a.txt", "b.md", "c.log", "d.py", "e.js"} {
		if _, err := MaskFile(name, []byte("电话 13812345678"), MaskOptions{Profile: "strict"}, cfg, nil); err != nil {
			t.Errorf("%s 应处理：%v", name, err)
		}
	}
	// .doc/.docm 拒绝。
	for _, name := range []string{"a.doc", "a.docm"} {
		if _, err := MaskFile(name, []byte("x"), MaskOptions{}, cfg, nil); err == nil {
			t.Errorf("%s 应拒绝", name)
		}
	}
	// 伪造 DOCX（纯文本改名）。
	if _, err := MaskFile("fake.docx", []byte("这不是 DOCX"), MaskOptions{}, cfg, nil); err == nil {
		t.Fatal("伪造 DOCX 应拒绝")
	}
	// DOCX 改名 txt：仍按纯文本二进制门禁拒绝。
	if _, err := MaskFile("fake.txt", valid, MaskOptions{}, cfg, nil); err == nil {
		t.Fatal("DOCX 改名 txt 应被纯文本门禁拒绝")
	}
	// 超大小上限在解压前拒绝。
	if _, err := MaskFile("big.docx", valid, MaskOptions{MaxSize: int64(len(valid)) - 1}, cfg, nil); err == nil {
		t.Fatal("超限 DOCX 应拒绝")
	}
	// 词文件仍仅纯文本。
	if _, err := ParseCustomWords(nil, valid, true); err == nil {
		t.Fatal("DOCX 词文件应被拒绝")
	}

	// 回程门禁：DOCX 改名 txt 按纯文本二进制拒绝；伪造 docx 按包校验拒绝。
	txtMask, err := MaskFile("notes.txt", []byte("电话 13812345678"), MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreFile("fake.txt", valid, txtMask.MappingBytes, cfg, nil); err == nil {
		t.Fatal("DOCX 改名 txt 回程应被纯文本门禁拒绝")
	}
	if _, err := RestoreFile("fake.docx", []byte("这不是 DOCX"), txtMask.MappingBytes, cfg, nil); err == nil {
		t.Fatal("伪造 DOCX 回程应被包校验拒绝")
	}
}

// 7.2 逐 unit 碰撞/高频、跨 run 与硬边界、custom 关闭忽略词。
func TestDocxCollisionAndHighFreq(t *testing.T) {
	cfg := config.Defaults()

	// 跨 run 文法拒绝。
	crossRun := docxFixture(t, docxtest.FixtureOptions{BodyRuns: []string{"[PHO", "NE_1]"}})
	if _, err := MaskFile("a.docx", crossRun, MaskOptions{Profile: "balanced"}, cfg, nil); err == nil {
		t.Fatal("跨 run 文法应拒绝")
	}
	// 硬边界分隔不拼接。
	split := docxFixture(t, docxtest.FixtureOptions{Table: [][]string{{"[PHO", "NE_1]"}}})
	if _, err := MaskFile("a.docx", split, MaskOptions{Profile: "balanced"}, cfg, nil); err != nil {
		t.Fatalf("硬边界分隔不应拼接成冲突：%v", err)
	}
	// 已删除历史不算冲突。
	hist := docxFixture(t, docxtest.FixtureOptions{Body: []string{"正常"}, InlineDelete: "[PHONE_1]"})
	if _, err := MaskFile("a.docx", hist, MaskOptions{Profile: "balanced"}, cfg, nil); err != nil {
		t.Fatalf("已删除历史不应算冲突：%v", err)
	}

	// 高频 25/30 分组。
	cfgHF := config.Defaults()
	cfgHF.CustomWords = []string{"常备词"}
	var body []string
	for i := 0; i < 25; i++ {
		body = append(body, "本次词")
	}
	for i := 0; i < 30; i++ {
		body = append(body, "常备词")
	}
	hf := docxFixture(t, docxtest.FixtureOptions{Body: body})
	_, err := MaskFile("a.docx", hf, MaskOptions{Profile: "balanced", ExtraCustom: []string{"本次词"}}, cfgHF, nil)
	pre, ok := err.(*PreviewRequiredError)
	if !ok {
		t.Fatalf("应要求预览确认，got %v", err)
	}
	if pre.Extra["本次词"] != 25 || pre.Builtin["常备词"] != 30 {
		t.Fatalf("高频分组计数错误：%+v", pre)
	}
	// 确认后重试成功。
	if _, err := MaskFile("a.docx", hf, MaskOptions{
		Profile: "balanced", ExtraCustom: []string{"本次词"},
		ConfirmedCustom: []string{"本次词", "常备词"},
	}, cfgHF, nil); err != nil {
		t.Fatalf("确认后应成功：%v", err)
	}
	// custom 关时词提交静默忽略。
	if _, err := MaskFile("a.docx", hf, MaskOptions{Profile: "minimal", ExtraCustom: []string{"本次词"}}, cfgHF, nil); err != nil {
		t.Fatalf("custom 关时词提交应忽略：%v", err)
	}
}

// 7.3 共享 Masker 去程：全区域同号、首次顺序、profile/覆盖、输出文字符合计划。
func TestDocxForwardSharedMasker(t *testing.T) {
	cfg := config.Defaults()
	data := docxFixture(t, docxtest.FixtureOptions{
		Body:           []string{"正文 13812345678"},
		Table:          [][]string{{"单元格 13812345678"}},
		Headers:        []string{"页眉 13812345678"},
		Footers:        []string{"页脚 13812345678"},
		Footnotes:      []string{"脚注 13812345678"},
		DrawingTextbox: "文本框 13812345678",
	})
	out, err := MaskFile("合同.docx", data, MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatalf("DOCX 去程失败：%v", err)
	}
	text := reopenedText(t, out.MaskedBytes)
	if strings.Contains(text, "13812345678") {
		t.Fatalf("输出仍含原文：%s", text)
	}
	if n := strings.Count(text, "[PHONE_1]"); n != 6 {
		t.Fatalf("全部区域应同号 [PHONE_1]，got %d：%s", n, text)
	}
	// mapping v2 字段。
	mp, err := mapping.Unmarshal(out.MappingBytes)
	if err != nil {
		t.Fatal(err)
	}
	if mp.Version != 2 || mp.SourceFormat != "docx" || mp.SourceEncoding != "" || mp.SourceTextSHA256 == "" {
		t.Fatalf("mapping v2 字段错误：%+v", mp)
	}
	if len(mp.Placeholders) != 1 || mp.Placeholders[0].Value != "13812345678" {
		t.Fatalf("mapping 表错误：%+v", mp.Placeholders)
	}
	// 首次顺序：正文先于页眉。
	order := docxFixture(t, docxtest.FixtureOptions{Body: []string{"13900000000"}, Headers: []string{"13812345678"}})
	out2, err := MaskFile("n.docx", order, MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	mp2, _ := mapping.Unmarshal(out2.MappingBytes)
	if mp2.Placeholders[0].Value != "13900000000" || mp2.Placeholders[1].Value != "13812345678" {
		t.Fatalf("首次出现顺序错误：%+v", mp2.Placeholders)
	}

	// 已删除历史值不进入 mapping。
	hist := docxFixture(t, docxtest.FixtureOptions{Body: []string{"新号码 13812345678"}, InlineDelete: "旧号码 13700000000"})
	outH, err := MaskFile("h.docx", hist, MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	mpH, _ := mapping.Unmarshal(outH.MappingBytes)
	for _, e := range mpH.Placeholders {
		if e.Value == "13700000000" {
			t.Fatalf("已删除历史值不应进入 mapping：%+v", mpH.Placeholders)
		}
	}

	// 跨 run 的 fixed 与 custom 命中在真实包结果上验收。
	crossFixed := docxFixture(t, docxtest.FixtureOptions{BodyRuns: []string{"138", "1234", "5678"}})
	outCF, err := MaskFile("cf.docx", crossFixed, MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopenedText(t, outCF.MaskedBytes); got != "[PHONE_1]" {
		t.Fatalf("跨 run 固定规则命中错误：%q", got)
	}
	crossCustom := docxFixture(t, docxtest.FixtureOptions{BodyRuns: []string{"内部", "词"}})
	outCC, err := MaskFile("cc.docx", crossCustom, MaskOptions{Profile: "balanced", ExtraCustom: []string{"内部词"}}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopenedText(t, outCC.MaskedBytes); got != "[CUSTOM_1]" {
		t.Fatalf("跨 run 自定义词命中错误：%q", got)
	}

	// profile：balanced 保留 idcard；override 开启后处理。
	idc := docxFixture(t, docxtest.FixtureOptions{Body: []string{"身份证 110101199003078515"}})
	outB, err := MaskFile("n.docx", idc, MaskOptions{Profile: "balanced"}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(reopenedText(t, outB.MaskedBytes), "[IDCARD_1]") {
		t.Fatal("balanced 下 idcard 应保留")
	}
	outO, err := MaskFile("n.docx", idc, MaskOptions{Profile: "balanced", RuleOverrides: map[detector.RuleID]bool{detector.RuleIDCard: true}}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reopenedText(t, outO.MaskedBytes), "[IDCARD_1]") {
		t.Fatal("override 开启后 idcard 应处理")
	}
}

// 7.4 DOCX 回程：跨 run/全角、未知占位符、输入新增批注/修订清理、硬边界不拼接。
func TestDocxRestore(t *testing.T) {
	cfg := config.Defaults()
	orig := docxFixture(t, docxtest.FixtureOptions{Body: []string{"电话 13812345678"}})
	fwd, err := MaskFile("合同.docx", orig, MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 大小写/全角被拆到多个 runs。
	reply := docxFixture(t, docxtest.FixtureOptions{BodyRuns: []string{"[ｐｈｏ", "ｎｅ_１]"}})
	res, err := RestoreFile("reply.docx", reply, fwd.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatalf("回程失败：%v", err)
	}
	if res.Format != "docx" || res.Encoding != "" {
		t.Fatalf("回程格式错误：%q %q", res.Format, res.Encoding)
	}
	text := reopenedText(t, res.Bytes)
	if !strings.Contains(text, "13812345678") || strings.Contains(text, "[PHONE_1]") {
		t.Fatalf("跨 run/全角还原失败：%s", text)
	}
	if res.RestoredCount != 1 {
		t.Fatalf("还原计数应为 1，got %d", res.RestoredCount)
	}

	// 重复占位符：全部还原并计数。
	dup := docxFixture(t, docxtest.FixtureOptions{Body: []string{"电话 [PHONE_1] 备用 [phone_1]"}})
	resDup, err := RestoreFile("reply.docx", dup, fwd.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resDup.RestoredCount != 2 {
		t.Fatalf("重复占位符还原计数应为 2，got %d", resDup.RestoredCount)
	}
	if strings.Count(reopenedText(t, resDup.Bytes), "13812345678") != 2 {
		t.Fatal("重复占位符应全部还原")
	}

	// 未知占位符：保留 + 反向残留非阻断。
	unknown := docxFixture(t, docxtest.FixtureOptions{Body: []string{"回复 [PHONE_9]"}})
	resU, err := RestoreFile("reply.docx", unknown, fwd.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatalf("未知占位符不应阻断：%v", err)
	}
	if !strings.Contains(reopenedText(t, resU.Bytes), "[PHONE_9]") {
		t.Fatal("未知占位符应保留")
	}
	if !hasWarning(resU.Warnings, "residual_reverse") {
		t.Fatalf("未知占位符应给残留警告：%+v", resU.Warnings)
	}

	// 输入新增批注/修订：回程先清理，输出无历史。
	dirty := docxFixture(t, docxtest.FixtureOptions{
		Body:         []string{"电话 [PHONE_1]"},
		InlineInsert: "插入 13900000000",
		Comments:     &docxtest.CommentsFixture{Text: "批注值", Extended: true, People: true},
	})
	resD, err := RestoreFile("reply.docx", dirty, fwd.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatalf("脏回程失败：%v", err)
	}
	if resD.Cleanup == nil || resD.Cleanup.CommentsDeleted == 0 {
		t.Fatalf("回程应产出清理摘要：%+v", resD.Cleanup)
	}
	reopened, err := docx.Open(resD.Bytes, docx.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"word/comments.xml", "word/commentsExtended.xml", "word/people.xml"} {
		if reopened.HasPart(name) {
			t.Errorf("回程输出不应残留批注部件 %s", name)
		}
	}
	if strings.Contains(reopenedText(t, resD.Bytes), "批注值") {
		t.Fatal("批注值不应出现在输出")
	}

	// 硬边界切开的碎片不拼接还原，也不误报完整文法残留。
	split := docxFixture(t, docxtest.FixtureOptions{Table: [][]string{{"[PHO", "NE_1]"}}})
	resS, err := RestoreFile("reply.docx", split, fwd.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatalf("碎片不应阻断：%v", err)
	}
	if hasWarning(resS.Warnings, "residual_reverse") {
		t.Fatalf("碎片不应触发完整文法残留：%+v", resS.Warnings)
	}
}

// 7.5 四格一致性参考与跨格式提示。
func TestDocxConsistencyGrid(t *testing.T) {
	cfg := config.Defaults()
	orig := docxFixture(t, docxtest.FixtureOptions{Body: []string{"电话 13812345678"}})
	fwd, err := MaskFile("合同.docx", orig, MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	// v2 + DOCX 配对：无 text_mismatch（同文字、仅 run 划分不同）。
	paired := docxFixture(t, docxtest.FixtureOptions{BodyRuns: []string{"电话 [PHO", "NE_1]"}})
	res, err := RestoreFile("reply.docx", paired, fwd.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if hasWarning(res.Warnings, "text_mismatch") {
		t.Fatalf("配对且仅 run 改变不应误报：%+v", res.Warnings)
	}
	// v2 + DOCX 正文改写：非阻断 text_mismatch。
	rewritten := docxFixture(t, docxtest.FixtureOptions{Body: []string{"完全不同的回复内容"}})
	resR, err := RestoreFile("reply.docx", rewritten, fwd.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatalf("正文改写不应阻断：%v", err)
	}
	if !hasWarning(resR.Warnings, "text_mismatch") {
		t.Fatalf("正文改写应给文字参考提示：%+v", resR.Warnings)
	}

	// v1 + DOCX：无同格式基线，非阻断。
	v1out, err := MaskFile("notes.txt", []byte("电话 13812345678"), MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	v1docx := docxFixture(t, docxtest.FixtureOptions{Body: []string{"回复 [PHONE_1]"}})
	resV1, err := RestoreFile("notes.docx", v1docx, v1out.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatalf("v1 还原 DOCX 不应阻断：%v", err)
	}
	if resV1.Format != "docx" || !strings.HasSuffix(resV1.Name, ".restored.docx") {
		t.Fatalf("v1+DOCX 命名/格式错误：%q %q", resV1.Name, resV1.Format)
	}
	if !hasWarning(resV1.Warnings, "sha_mismatch") {
		t.Fatalf("v1+DOCX 应给跨格式提示：%+v", resV1.Warnings)
	}

	// v2 + 文本/粘贴：UTF-8 文本命名，跨格式提示，不命名 docx。
	resT, err := RestoreFile("reply.txt", []byte("回复 [PHONE_1]"), fwd.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatalf("v2+文本不应阻断：%v", err)
	}
	if resT.Format != "text" || resT.Encoding != loader.EncodingUTF8 {
		t.Fatalf("v2+文本格式/编码错误：%q %q", resT.Format, resT.Encoding)
	}
	if resT.Name != "合同.restored.txt" {
		t.Fatalf("v2+文本命名错误：%q", resT.Name)
	}
	if !hasWarning(resT.Warnings, "sha_mismatch") || !hasWarning(resT.Warnings, "docx_history") {
		t.Fatalf("v2+文本应给跨格式与历史提示：%+v", resT.Warnings)
	}
}

// 2.1 DOCX 来源 mapping 接受新文本回复：普通后缀沿用实际小写规则；
// 无后缀、单段点文件与粘贴回退 .txt，不冒充 DOCX。
func TestDocxMappingAcceptsNewTextReplyNames(t *testing.T) {
	cfg := config.Defaults()
	docxData := docxFixture(t, docxtest.FixtureOptions{Body: []string{"手机号 13800138000"}})
	fwd, err := MaskFile("合同.docx", docxData, MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatalf("DOCX 脱敏失败：%v", err)
	}
	cases := []struct{ reply, want string }{
		{"reply.json", "合同.restored.json"},
		{"reply.YAML", "合同.restored.yaml"},
		{"README", "合同.restored.txt"},
		{".env", "合同.restored.txt"},
		{"", "合同.restored.txt"},
	}
	for _, c := range cases {
		res, err := RestoreFile(c.reply, []byte("回复 [PHONE_1]"), fwd.MappingBytes, cfg, nil)
		if err != nil {
			t.Fatalf("%q 回程不应阻断：%v", c.reply, err)
		}
		if res.Format != "text" || res.Encoding != loader.EncodingUTF8 {
			t.Fatalf("%q 应以 UTF-8 文本回程：%q %q", c.reply, res.Format, res.Encoding)
		}
		if res.Name != c.want {
			t.Fatalf("%q 命名=%q want %q", c.reply, res.Name, c.want)
		}
		if strings.HasSuffix(strings.ToLower(res.Name), ".docx") {
			t.Fatalf("%q 文本回程不得命名为 DOCX：%q", c.reply, res.Name)
		}
		if !hasWarning(res.Warnings, "sha_mismatch") || !hasWarning(res.Warnings, "docx_history") {
			t.Fatalf("%q 应给跨格式与历史提示：%+v", c.reply, res.Warnings)
		}
	}
}

// 7.6 DOCX 定位、审计按重开文字统计、无原文。
func TestDocxPositionsAndAudit(t *testing.T) {
	cfg := config.Defaults()
	// 正向残留：custom 词吃掉 api_key 尾部，剩余前缀仍命中规则。
	data := docxFixture(t, docxtest.FixtureOptions{Body: []string{"token sk-abcdefghijklmnop 结束"}})
	log, path := newAudit(t)
	out, err := MaskFile("a.docx", data, MaskOptions{Profile: "balanced", ExtraCustom: []string{"mnop"}}, cfg, log)
	if err != nil {
		t.Fatalf("残留不应阻断：%v", err)
	}
	w := findWarning(out.Warnings, "residual_forward")
	if w == nil {
		t.Fatalf("应报告正向残留：%+v", out.Warnings)
	}
	if w.Part == "" || w.Paragraph < 1 || w.Segment < 1 || w.Column < 1 {
		t.Fatalf("DOCX 定位字段缺失：%+v", w)
	}
	if w.Line != 0 {
		t.Fatalf("DOCX 不应使用 Line：%+v", w)
	}
	// 审计：无原文/XML/mapping，计数来自重开文字。
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("13812345678")) || bytes.Contains(raw, []byte("<w:")) || bytes.Contains(raw, []byte("placeholders")) {
		t.Fatalf("审计不得含原文/XML/mapping：%s", raw)
	}
	if !bytes.Contains(raw, []byte("CUSTOM_1")) {
		t.Fatalf("审计应含占位符：%s", raw)
	}
	var rec map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("审计行非法：%s", line)
		}
		if _, ok := rec["xml"]; ok {
			t.Fatalf("审计不应新增字段：%s", line)
		}
	}
}

// 7.7 双向确定性与格式保持；残留可导出；结构失败阻断。
func TestDocxDeterminismAndPreservation(t *testing.T) {
	cfg := config.Defaults()
	data := docxFixture(t, docxtest.FixtureOptions{
		Body:  []string{"正文 13812345678"},
		Table: [][]string{{"单元格 13900000000", "普通"}},
		Image: []byte{0x89, 'P', 'N', 'G', 7, 7, 7},
	})
	// 清理后基线快照。
	base, err := docx.Open(data, docx.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := base.Prepare(); err != nil {
		t.Fatal(err)
	}
	baseBytes, err := base.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	baseSnap, err := docx.Snapshot(baseBytes)
	if err != nil {
		t.Fatal(err)
	}

	o1, err := MaskFile("合同.docx", data, MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	o2, err := MaskFile("合同.docx", data, MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(o1.MaskedBytes, o2.MaskedBytes) || !bytes.Equal(o1.MappingBytes, o2.MappingBytes) {
		t.Fatal("重复去程 DOCX/mapping 应字节一致")
	}
	// 结构/图片保持（文字差异允许）。
	snap, err := docx.Snapshot(o1.MaskedBytes)
	if err != nil {
		t.Fatal(err)
	}
	if diffs := baseSnap.Diff(snap); len(diffs) != 0 {
		t.Fatalf("表格/样式/图片不应因脱敏改变：%+v", diffs)
	}
	// 回程确定性。
	r1, err := RestoreFile("reply.docx", o1.MaskedBytes, o1.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := RestoreFile("reply.docx", o1.MaskedBytes, o1.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(r1.Bytes, r2.Bytes) {
		t.Fatal("重复回程 DOCX 应字节一致")
	}
	if !strings.Contains(reopenedText(t, r1.Bytes), "13812345678") {
		t.Fatal("回程应恢复原文")
	}

	// 结构失败阻断：段落标记删除。
	bad := docxFixture(t, docxtest.FixtureOptions{Body: []string{"正文"}, ParagraphMarkDelete: true})
	if _, err := MaskFile("bad.docx", bad, MaskOptions{Profile: "strict"}, cfg, nil); err == nil {
		t.Fatal("不支持的修订应阻断")
	}
}

func hasWarning(ws []Warning, kind string) bool { return findWarning(ws, kind) != nil }

func findWarning(ws []Warning, kind string) *Warning {
	for i := range ws {
		if ws[i].Kind == kind {
			return &ws[i]
		}
	}
	return nil
}

const mainCT = "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"

func mcDocx(t *testing.T, inner string) []byte {
	t.Helper()
	b := docxtest.NewBuilder(false)
	xml := b.WMLDocument(inner, map[string]string{
		"mc":  "http://schemas.openxmlformats.org/markup-compatibility/2006",
		"wps": "http://schemas.microsoft.com/office/word/2010/wordprocessingShape",
	})
	b.AddPart("word/document.xml", mainCT, xml)
	b.SetMain("word/document.xml")
	return b.Bytes()
}

// W1(a) 跨分支碎片不得拼成号码、不得跨宿主边界、不产生 mapping。
func TestDocxMCCrossBranchNoFalsePositive(t *testing.T) {
	cfg := config.Defaults()
	inner := `<w:p><w:r><w:t>1</w:t></w:r>` +
		`<mc:AlternateContent>` +
		`<mc:Choice Requires="wps"><w:r><w:t>38</w:t></w:r></mc:Choice>` +
		`<mc:Fallback><w:r><w:t>12345678</w:t></w:r></mc:Fallback>` +
		`</mc:AlternateContent>` +
		`<w:r><w:t>9</w:t></w:r></w:p>`
	out, err := MaskFile("a.docx", mcDocx(t, inner), MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatalf("不应报错：%v", err)
	}
	mp, err := mapping.Unmarshal(out.MappingBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(mp.Placeholders) != 0 {
		t.Fatalf("跨分支碎片不应产生 mapping：%+v", mp.Placeholders)
	}
	text := reopenedText(t, out.MaskedBytes)
	if strings.Contains(text, "[PHONE_1]") {
		t.Fatalf("不应误脱敏：%s", text)
	}
}

// W1(b) 两分支各存完整同号码：各自替换、计数 2、同号一条 mapping。
func TestDocxMCBothBranchesFullNumber(t *testing.T) {
	cfg := config.Defaults()
	inner := `<w:p><mc:AlternateContent>` +
		`<mc:Choice Requires="wps"><w:r><w:t>13812345678</w:t></w:r></mc:Choice>` +
		`<mc:Fallback><w:r><w:t>13812345678</w:t></w:r></mc:Fallback>` +
		`</mc:AlternateContent></w:p>`
	out, err := MaskFile("a.docx", mcDocx(t, inner), MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := reopenedText(t, out.MaskedBytes)
	if strings.Count(text, "[PHONE_1]") != 2 {
		t.Fatalf("两分支应各自替换且同号：%s", text)
	}
	if out.Coverage[detector.RulePhone] != 2 {
		t.Fatalf("命中计数应为 2：%+v", out.Coverage)
	}
	mp, _ := mapping.Unmarshal(out.MappingBytes)
	if len(mp.Placeholders) != 1 || mp.Placeholders[0].Value != "13812345678" {
		t.Fatalf("同值应只有一条 mapping：%+v", mp.Placeholders)
	}
}

// W1(c) 分支内部跨 runs 的占位符可 restore；跨分支碎片不构成 collision。
func TestDocxMCBranchCrossRunRestore(t *testing.T) {
	cfg := config.Defaults()
	// 去程：正常文档得到 mapping。
	orig := docxFixture(t, docxtest.FixtureOptions{Body: []string{"电话 13812345678"}})
	fwd, err := MaskFile("合同.docx", orig, MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 回程：两个分支各自把占位符拆到两个 runs。
	reply := mcDocx(t, `<w:p><mc:AlternateContent>`+
		`<mc:Choice Requires="wps"><w:r><w:t>[PHO</w:t></w:r><w:r><w:t>NE_1]</w:t></w:r></mc:Choice>`+
		`<mc:Fallback><w:r><w:t>[PHONE_1]</w:t></w:r></mc:Fallback>`+
		`</mc:AlternateContent></w:p>`)
	res, err := RestoreFile("reply.docx", reply, fwd.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatalf("分支内跨 run 回程不应阻断：%v", err)
	}
	text := reopenedText(t, res.Bytes)
	if strings.Contains(text, "[PHONE_1]") {
		t.Fatalf("占位符应全部还原：%s", text)
	}
	if strings.Count(text, "13812345678") != 2 {
		t.Fatalf("两分支应各自还原：%s", text)
	}

	// 跨分支碎片不构成 collision：Choice "[PHO" + Fallback "NE_1]"。
	collision := mcDocx(t, `<w:p><mc:AlternateContent>`+
		`<mc:Choice Requires="wps"><w:r><w:t>[PHO</w:t></w:r></mc:Choice>`+
		`<mc:Fallback><w:r><w:t>NE_1]</w:t></w:r></mc:Fallback>`+
		`</mc:AlternateContent></w:p>`)
	if _, err := MaskFile("a.docx", collision, MaskOptions{Profile: "balanced"}, cfg, nil); err != nil {
		t.Fatalf("跨分支碎片不应触发 collision：%v", err)
	}
	// 同一分支内跨 run 的完整文法应拒绝。
	collisionSame := mcDocx(t, `<w:p><mc:AlternateContent>`+
		`<mc:Choice Requires="wps"><w:r><w:t>[PHO</w:t></w:r><w:r><w:t>NE_1]</w:t></w:r></mc:Choice>`+
		`</mc:AlternateContent></w:p>`)
	if _, err := MaskFile("a.docx", collisionSame, MaskOptions{Profile: "balanced"}, cfg, nil); err == nil {
		t.Fatal("同一分支内跨 run 的完整文法应拒绝")
	}
}

// W2: 人工样本（mixed font / 兼容分支 / 表格合并 / 书签 / 真实 PNG）与 Strict 样本可被本工具读取与处理。
func TestDocxRichManualSamples(t *testing.T) {
	cfg := config.Defaults()
	pngBytes := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 1, 2, 3}
	rich := docxFixture(t, docxtest.FixtureOptions{
		Body:            []string{"合同 13812345678"},
		TableMerge:      true,
		MixedFontNumber: "13812345678",
		CompatText:      "13812345678",
		Bookmark:        "书签 13812345678",
		Headers:         []string{"页眉 13812345678"},
		Footnotes:       []string{"脚注 13812345678"},
		DrawingTextbox:  "文本框 13812345678",
		Image:           pngBytes,
	})
	out, err := MaskFile("rich.docx", rich, MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatalf("rich 样本处理失败：%v", err)
	}
	text := reopenedText(t, out.MaskedBytes)
	if strings.Contains(text, "13812345678") {
		t.Fatalf("rich 样本仍有原文：%s", text)
	}
	if !strings.Contains(text, "[PHONE_1]") {
		t.Fatalf("rich 样本应脱敏：%s", text)
	}
	// 图片字节保持。
	rd, _ := docx.Open(out.MaskedBytes, docx.DefaultLimits())
	img, ok := rd.PartBytes("word/media/image1.png")
	if !ok || !bytes.Equal(img, pngBytes) {
		t.Fatal("rich 样本图片字节应保持")
	}

	// Strict 样本。
	strict := docxFixture(t, docxtest.FixtureOptions{
		Strict:          true,
		Body:            []string{"严格 13812345678"},
		Table:           [][]string{{"甲 13812345678", "乙"}},
		Headers:         []string{"严格页眉 13812345678"},
		Footnotes:       []string{"严格脚注 13812345678"},
		MixedFontNumber: "13812345678",
	})
	if _, err := MaskFile("strict.docx", strict, MaskOptions{Profile: "strict"}, cfg, nil); err != nil {
		t.Fatalf("Strict 样本处理失败：%v", err)
	}
}
