package docx

import (
	"fmt"
	"strings"
)

// ValidateOutput 重开输出包并验证文字计划、关系、清理、命名空间、结构与图片后置条件。
// 任一失败都返回阻断错误，调用方不得交付产物。
func (d *Document) ValidateOutput(out []byte) error {
	_, err := d.ReopenValidated(out)
	return err
}

// ReopenValidated 与 ValidateOutput 做同样的校验，并返回重开后的 Document，
// 供调用方在“实际输出原生文字”上做残留扫描与文字参考指纹计算。
func (d *Document) ReopenValidated(out []byte) (*Document, error) {
	outDoc, err := Open(out, d.limits)
	if err != nil {
		return nil, err
	}
	if err := d.checkCleanupPostconditions(outDoc); err != nil {
		return nil, err
	}
	if err := d.checkUnits(outDoc); err != nil {
		return nil, err
	}
	if err := d.checkImages(outDoc); err != nil {
		return nil, err
	}
	if err := d.checkStructure(outDoc); err != nil {
		return nil, err
	}
	return outDoc, nil
}

func (d *Document) checkCleanupPostconditions(outDoc *Document) error {
	for _, name := range outDoc.order {
		p := outDoc.parts[name]
		if p.removed {
			continue
		}
		if isCommentFamilyContentType(p.ct) || looksLikeCommentContentType(p.ct) {
			return herrf("输出包仍残留批注家族部件："+name, "内部校验失败，请报告此问题", "docx.output_validation_failed")
		}
	}
	for _, rels := range outDoc.rels {
		for _, r := range rels {
			if isKnownCommentRel(r.Type) || looksLikeCommentRelationship(r.Type) {
				return herrf("输出包仍残留批注关系："+r.Type, "内部校验失败，请报告此问题", "docx.output_validation_failed")
			}
		}
	}
	for _, name := range outDoc.order {
		p := outDoc.parts[name]
		if p.removed || !wmlContentTypes[p.ct] {
			continue
		}
		err := outDoc.walkPart(p, func(ev *event) error {
			if ev.kind != evStart {
				return nil
			}
			if !isWMLName(ev.name) {
				return nil
			}
			local := ev.name.Local
			switch local {
			case "commentRangeStart", "commentRangeEnd", "commentReference":
				return herrf("输出包仍残留批注锚点："+name, "内部校验失败，请报告此问题", "docx.output_validation_failed")
			case "delText", "delInstrText":
				return herrf("输出包仍残留删除文字节点："+name, "内部校验失败，请报告此问题", "docx.output_validation_failed")
			case "trackRevisions":
				return herrf("输出包仍开启修订跟踪："+name, "内部校验失败，请报告此问题", "docx.output_validation_failed")
			}
			if isRevisionLocal(local) {
				return herrf("输出包仍残留修订节点 "+local+"："+name, "内部校验失败，请报告此问题", "docx.output_validation_failed")
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (d *Document) checkUnits(outDoc *Document) error {
	if err := outDoc.rebuildUnits(); err != nil {
		return err
	}
	if len(outDoc.units) != len(d.units) {
		return herrf(fmt.Sprintf("输出文字单元数量不符：期望 %d，实际 %d", len(d.units), len(outDoc.units)),
			"输出验证失败；未交付文件", "docx.output_validation_failed")
	}
	for i := range d.units {
		want := d.units[i]
		got := outDoc.units[i]
		if want.Part != got.Part || want.Paragraph != got.Paragraph || want.Segment != got.Segment {
			return herrf(fmt.Sprintf("输出文字定位不符：期望 %s P%d S%d，实际 %s P%d S%d",
				want.Part, want.Paragraph, want.Segment, got.Part, got.Paragraph, got.Segment),
				"输出验证失败；未交付文件", "docx.output_validation_failed")
		}
		expectedText, ok := d.expected[i]
		if !ok {
			expectedText = want.Text
		}
		if got.Text != expectedText {
			return herrf("输出文字与计划不符："+want.Part,
				"输出验证失败；未交付文件", "docx.output_validation_failed")
		}
	}
	return nil
}

func (d *Document) checkImages(outDoc *Document) error {
	for _, name := range d.imageNames {
		want, ok := d.parts[name]
		if !ok {
			continue
		}
		got, ok := outDoc.parts[name]
		if !ok || got.removed {
			return herrf("输出包缺失最终稿图片："+name, "输出验证失败；未交付文件", "docx.output_validation_failed")
		}
		if !bytesEqual(want.data, got.data) {
			return herrf("输出包最终稿图片字节被改变："+name, "输出验证失败；未交付文件", "docx.output_validation_failed")
		}
	}
	return nil
}

func (d *Document) checkStructure(outDoc *Document) error {
	if d.cleanedSnapshot == nil {
		return nil
	}
	outSnap, err := snapshotOf(outDoc)
	if err != nil {
		return err
	}
	diffs := d.cleanedSnapshot.Diff(outSnap)
	if len(diffs) > 0 {
		parts := make([]string, 0, len(diffs))
		for _, df := range diffs {
			parts = append(parts, df.Part+"("+df.Kind+")")
		}
		return herrf("输出包结构/资源与允许差异不符："+strings.Join(parts, ", "),
			"输出验证失败；未交付文件", "docx.output_validation_failed")
	}
	return nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
