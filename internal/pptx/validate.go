package pptx

import (
	"fmt"
	"strings"
)

// ValidateOutput 重开输出包并验证文字计划、关系、清理、命名空间、结构与资源后置条件。
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
		if isKnownCommentContentType(p.ct) || looksLikeUnknownCommentContentType(p.ct) {
			return herrf("输出包仍残留批注家族部件："+name,
				"输出验证失败；未交付文件", "pptx.output_validation_failed")
		}
	}
	for _, rels := range outDoc.rels {
		for _, r := range rels {
			if isKnownCommentRel(r.Type) || looksLikeUnknownCommentRel(r.Type) {
				return herrf("输出包仍残留批注关系："+r.Type,
					"输出验证失败；未交付文件", "pptx.output_validation_failed")
			}
		}
	}
	for _, name := range outDoc.order {
		p := outDoc.parts[name]
		if p.removed || !isSlideTextPart(p.ct) {
			continue
		}
		var anchor string
		err := outDoc.walkPart(p, func(ev *event) error {
			if anchor == "" && ev.kind == evStart && ev.name.Space == nsP188 && ev.name.Local == "commentRel" {
				anchor = ev.name.Local
			}
			return nil
		})
		if err != nil {
			return err
		}
		if anchor != "" {
			return herrf("输出包仍残留批注锚点 "+anchor+"："+name,
				"输出验证失败；未交付文件", "pptx.output_validation_failed")
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
			"输出验证失败；未交付文件", "pptx.output_validation_failed")
	}
	for i := range d.units {
		want := d.units[i]
		got := outDoc.units[i]
		if want.Part != got.Part || want.Paragraph != got.Paragraph || want.Segment != got.Segment {
			return herrf(fmt.Sprintf("输出文字定位不符：期望 %s P%d S%d，实际 %s P%d S%d",
				want.Part, want.Paragraph, want.Segment, got.Part, got.Paragraph, got.Segment),
				"输出验证失败；未交付文件", "pptx.output_validation_failed")
		}
		expectedText, ok := d.expected[i]
		if !ok {
			expectedText = want.Text
		}
		if got.Text != expectedText {
			return herrf("输出文字与计划不符："+want.Part,
				"输出验证失败；未交付文件", "pptx.output_validation_failed")
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
			"输出验证失败；未交付文件", "pptx.output_validation_failed")
	}
	return nil
}
