package pptx

import (
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/pptxtest"
)

func TestCleanupRemovesCommentFamiliesAndRelations(t *testing.T) {
	doc := prepareFixture(t, pptxtest.Rich())
	summary := doc.Cleanup()
	if summary.CommentsDeleted != 4 {
		t.Errorf("comments deleted = %d, want 4", summary.CommentsDeleted)
	}
	if summary.PeoplePartsRemoved != 2 {
		t.Errorf("people parts removed = %d, want 2", summary.PeoplePartsRemoved)
	}
	for _, name := range doc.PartNames() {
		body, ok := doc.PartBytes(name)
		if !ok {
			continue
		}
		for _, rels := range doc.rels {
			for _, r := range rels {
				if strings.Contains(strings.ToLower(r.Type), "comment") || strings.Contains(strings.ToLower(r.Type), "authors") {
					t.Errorf("dangling comment relation on %s: %s", name, r.Type)
				}
			}
		}
		_ = body
	}
}

func TestCleanupPreservesSharedImageAndRemovesDedicatedResource(t *testing.T) {
	data := pptxtest.Build(pptxtest.Options{
		Slides:         []pptxtest.Slide{{Title: "共享图片", Picture: true}},
		CommentText:    "批注 13700137000",
		LegacyComments: true,
		ExtraParts: map[string][]byte{
			"ppt/comments/_rels/comment1.xml.rels": []byte(`<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rIdShared" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="/ppt/media/image1.png"/><Relationship Id="rIdOnly" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="/ppt/media/commentOnly.png"/></Relationships>`),
			"ppt/media/commentOnly.png":            []byte("comment-only-resource"),
		},
	})
	doc := prepareFixture(t, data)
	summary := doc.Cleanup()
	if !doc.HasPart("ppt/media/image1.png") {
		t.Error("shared body image was removed")
	}
	if doc.HasPart("ppt/media/commentOnly.png") {
		t.Error("comment-only resource was retained")
	}
	if summary.ResourcesRemoved != 1 {
		t.Errorf("resources removed = %d, want 1", summary.ResourcesRemoved)
	}
}

func TestCleanupKeepsNotesAndOrdinaryProperties(t *testing.T) {
	data := pptxtest.Build(pptxtest.Options{
		Slides:         []pptxtest.Slide{{Title: "正文", Notes: []string{"备注 13800138000"}}},
		LegacyComments: true,
		ModernComments: true,
		CommentText:    "批注 13700137000",
		ExtraParts: map[string][]byte{
			"docProps/core.xml": []byte(`<?xml version="1.0" encoding="UTF-8"?><cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:creator>Alice</dc:creator></cp:coreProperties>`),
		},
	})
	doc := prepareFixture(t, data)
	if !doc.HasPart("ppt/notesSlides/notesSlide1.xml") {
		t.Error("notes were treated as history and deleted")
	}
	body, ok := doc.PartBytes("docProps/core.xml")
	if !ok || !strings.Contains(string(body), "Alice") {
		t.Error("ordinary author property was removed")
	}
}

func TestCleanupCountsZeroWithoutComments(t *testing.T) {
	doc := prepareFixture(t, pptxtest.Minimal("无批注"))
	s := doc.Cleanup()
	if s.CommentsDeleted != 0 || s.PeoplePartsRemoved != 0 || s.PartsRemoved != 0 || s.ResourcesRemoved != 0 {
		t.Fatalf("unexpected non-zero cleanup: %+v", s)
	}
}

func TestModernCommentAnchorRemovedButOtherExtensionsKept(t *testing.T) {
	src := pptxtest.Rich()
	if !strings.Contains(string(partFromZip(t, src, "ppt/slides/slide1.xml")), "commentRel") {
		t.Fatal("fixture should contain modern comment anchor")
	}
	doc := prepareFixture(t, src)
	for _, name := range []string{"ppt/slides/slide1.xml", "ppt/slides/slide2.xml"} {
		body, ok := doc.PartBytes(name)
		if !ok {
			t.Fatalf("missing %s", name)
		}
		if strings.Contains(string(body), "commentRel") {
			t.Fatalf("modern comment anchor not removed from %s", name)
		}
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.ValidateOutput(out); err != nil {
		t.Fatal(err)
	}
}
