package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/pptx"
	"github.com/pandatoufu/data-masking/internal/pptxtest"
)

func TestPPTXMaskAndRestoreAPI(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	src := pptxtest.Rich()
	body, ct := makeMultipart(t, map[string][]string{"profile": {"strict"}},
		map[string]filePart{"file": {"报告.pptx", src}})
	code, resp := doJSON(t, h, "POST", "/api/mask", body, ct)
	if code != http.StatusOK || resp["ok"] != true {
		t.Fatalf("PPTX 脱敏失败：%d %v", code, resp)
	}
	if resp["format"] != "pptx" {
		t.Fatalf("format 应为 pptx：%v", resp["format"])
	}
	if enc, ok := resp["encoding"].(string); !ok || enc != "" {
		t.Fatalf("PPTX encoding 键应保留且为空串：%v", resp["encoding"])
	}
	if name, _ := resp["masked_name"].(string); !strings.HasSuffix(name, ".masked.pptx") {
		t.Fatalf("PPTX 脱敏名错误：%q", name)
	}
	cleanup, ok := resp["pptx_cleanup"].(map[string]any)
	if !ok {
		t.Fatalf("响应应含独立 pptx_cleanup：%v", resp["pptx_cleanup"])
	}
	if cleanup["comments_deleted"].(float64) != 4 {
		t.Fatalf("pptx 清理计数错误：%v", cleanup)
	}
	if _, exists := cleanup["revisions_accepted"]; exists {
		t.Fatalf("PPTX 不应复用 Word 修订字段：%v", cleanup)
	}
	if _, exists := resp["cleanup"]; exists && resp["cleanup"] != nil {
		t.Fatalf("PPTX 不应填 DOCX cleanup：%v", resp["cleanup"])
	}

	masked, _ := base64.StdEncoding.DecodeString(resp["masked_b64"].(string))
	if _, err := pptx.Open(masked, pptx.DefaultLimits()); err != nil {
		t.Fatalf("masked_b64 应为有效 PPTX：%v", err)
	}
	mappingJSON, _ := base64.StdEncoding.DecodeString(resp["mapping_b64"].(string))
	var mp map[string]any
	if err := json.Unmarshal(mappingJSON, &mp); err != nil {
		t.Fatal(err)
	}
	if mp["version"].(float64) != 3 || mp["source_format"] != "pptx" || mp["source_encoding"] != "" {
		t.Fatalf("PPTX mapping 字段错误：%v", mp)
	}

	rb, rct := makeMultipart(t, nil, map[string]filePart{
		"file":    {"报告.masked.pptx", masked},
		"mapping": {"报告.masked.mapping.json", mappingJSON},
	})
	code, rresp := doJSON(t, h, "POST", "/api/restore", rb, rct)
	if code != http.StatusOK || rresp["format"] != "pptx" {
		t.Fatalf("PPTX 回程失败：%d %v", code, rresp)
	}
	if name, _ := rresp["name"].(string); name != "报告.restored.pptx" {
		t.Fatalf("PPTX 回程命名错误：%q", name)
	}
	rcleanup, ok := rresp["pptx_cleanup"].(map[string]any)
	if !ok || rcleanup["comments_deleted"].(float64) != 0 {
		t.Fatalf("回程清理摘要错误：%v", rresp["pptx_cleanup"])
	}
	restored, _ := base64.StdEncoding.DecodeString(rresp["restored_b64"].(string))
	if _, err := pptx.Open(restored, pptx.DefaultLimits()); err != nil {
		t.Fatalf("restored_b64 应为有效 PPTX：%v", err)
	}
}

func TestPPTXAPIFailureHasNoArtifact(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	body, ct := makeMultipart(t, map[string][]string{"profile": {"strict"}},
		map[string]filePart{"file": {"fake.pptx", []byte("not a zip")}})
	code, resp := doJSON(t, h, "POST", "/api/mask", body, ct)
	if code == http.StatusOK {
		t.Fatalf("假 PPTX 不应成功：%v", resp)
	}
	for _, key := range []string{"masked_b64", "mapping_b64", "restored_b64"} {
		if _, ok := resp[key]; ok {
			t.Fatalf("失败响应不得含产物 %s：%v", key, resp)
		}
	}
}
