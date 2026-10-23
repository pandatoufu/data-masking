package server

import (
	"net/http"
	"testing"

	"github.com/pandatoufu/data-masking/internal/config"
)

// WithOperationGuard 端到端：忙时拒绝且不产出产物；成功请求在完整响应后释放保留。
func TestOperationGuardEndToEnd(t *testing.T) {
	// 忙：begin 返回 false → 409 http.operation_busy，且无产物字段。
	busySrv := New(config.Defaults(), nil,
		WithDesktopMode(),
		WithOperationGuard(func() bool { return false }, func() {}),
		WithIdleTimeout(0),
	)
	body, ct := makeMultipart(t, map[string][]string{"profile": {"balanced"}},
		map[string]filePart{"file": {"a.txt", []byte("电话 13812345678")}})
	code, resp := doJSON(t, busySrv.Handler(), "POST", "/api/mask", body, ct)
	if code != http.StatusConflict {
		t.Fatalf("忙时应 409，实际 %d %v", code, resp)
	}
	pres, _ := resp["presentation"].(map[string]any)
	if pres["message_id"] != "http.operation_busy" {
		t.Fatalf("应携带 http.operation_busy：%v", resp["presentation"])
	}
	if _, has := resp["masked_b64"]; has {
		t.Fatal("忙拒绝不得产出产物")
	}

	// 正常：begin 成功、end 在完整响应后被调用一次。
	ends := 0
	okSrv := New(config.Defaults(), nil,
		WithDesktopMode(),
		WithOperationGuard(func() bool { return true }, func() { ends++ }),
		WithIdleTimeout(0),
	)
	body2, ct2 := makeMultipart(t, map[string][]string{"profile": {"balanced"}},
		map[string]filePart{"file": {"a.txt", []byte("电话 13812345678")}})
	code2, resp2 := doJSON(t, okSrv.Handler(), "POST", "/api/mask", body2, ct2)
	if code2 != http.StatusOK || resp2["ok"] != true {
		t.Fatalf("正常处理应 200：%d %v", code2, resp2)
	}
	if ends != 1 {
		t.Fatalf("完整响应后应释放一次操作保留，实际 %d", ends)
	}
}

// 处理中 /api/exit 拒绝：guard begin 忙无法获取关闭保留（端到端）。
func TestOperationGuardBlocksExitWhileBusy(t *testing.T) {
	saverState := false
	s := New(config.Defaults(), nil,
		WithDesktopMode(),
		WithOperationGuard(func() bool {
			if saverState {
				return false
			}
			saverState = true
			return true
		}, func() { saverState = false }),
		WithExitGuard(func() error { return errString("busy") }),
		WithIdleTimeout(0),
	)
	// 先占用处理保留。
	if !s.beginOperation() {
		t.Fatal("应可开始处理")
	}
	defer s.endOperation()
	body, ct := makeMultipart(t, nil, nil)
	code, resp := doJSON(t, s.Handler(), "POST", "/api/exit", body, ct)
	if code != http.StatusConflict {
		t.Fatalf("处理中退出应被拒绝，实际 %d %v", code, resp)
	}
	pres, _ := resp["presentation"].(map[string]any)
	if pres["message_id"] != "exit.blocked" {
		t.Fatalf("应携带 exit.blocked：%v", resp["presentation"])
	}
}
