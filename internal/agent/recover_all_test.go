package agent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pandatoufu/data-masking/internal/workspace"
)

// 旧会话未确认任务在重启恢复后作废并记 interrupted；已交付任务不动。
func TestRecoverAllInvalidatesOldSessionConfirmations(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)

	oldSession, _ := NewSessionID()
	awaiting, _ := NewJobID()
	if err := store.Append(awaiting, Record{Type: RecCreated, SessionID: oldSession, SourceName: "a.txt"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(awaiting, Record{Type: RecFrequencyRequired, ConfirmationID: "cid", ConfirmationKind: "frequency"}); err != nil {
		t.Fatal(err)
	}

	delivered, _ := NewJobID()
	if err := store.Append(delivered, Record{Type: RecCreated, SessionID: oldSession, SourceName: "b.txt"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(delivered, Record{Type: RecDelivered, ProjectFileRel: ".masked-input/" + delivered + "/input.masked.txt"}); err != nil {
		t.Fatal(err)
	}

	newSession, _ := NewSessionID()
	results, err := store.RecoverAll(root, newSession)
	if err != nil {
		t.Fatalf("恢复失败：%v", err)
	}
	byJob := map[string]RecoveryResult{}
	for _, r := range results {
		byJob[r.JobID] = r
	}
	if got := byJob[awaiting]; got.State != StateFailed {
		t.Fatalf("旧会话未确认任务应失败：%+v", got)
	}
	if got := byJob[delivered]; got.State != StateDelivered || got.Action != RecoveryNone {
		t.Fatalf("已交付任务应保持可跨重启恢复：%+v", got)
	}

	recs, err := store.Load(awaiting)
	if err != nil {
		t.Fatal(err)
	}
	if DeriveState(recs) != StateFailed {
		t.Fatalf("稳定状态应为 failed，got %v", DeriveState(recs))
	}
	if !PreviousSessionsInvalidated(recs[:len(recs)-1], newSession) {
		t.Fatal("未确认任务应被识别为旧会话")
	}
}

// 跨重启：新 store 实例可读取已交付任务的配对记录，不扫描业务目录猜配对。
func TestRecoverAllCrossRestartDeliveredPair(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	job, _ := NewJobID()
	session, _ := NewSessionID()
	if err := store.Append(job, Record{Type: RecCreated, SessionID: session, SourceName: "report.docx"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(job, Record{Type: RecPublishIntent, MaskedRel: "masked/" + job + "/report.masked.docx", MappingRel: "mapping/" + job + "/report.masked.mapping.json"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(job, Record{Type: RecProjectIntent, ProjectFileRel: ".masked-input/" + job + "/input.masked.docx"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(job, Record{Type: RecDelivered, ProjectFileRel: ".masked-input/" + job + "/input.masked.docx"}); err != nil {
		t.Fatal(err)
	}

	// 业务目录中的无关文件不得被当作配对使用。
	if err := os.MkdirAll(filepath.Join(root, workspace.DirMapping), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, workspace.DirMapping, "other.mapping.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	restarted := NewStore(root)
	newSession, _ := NewSessionID()
	results, err := restarted.RecoverAll(root, newSession)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].JobID != job || results[0].Action != RecoveryNone {
		t.Fatalf("已交付任务跨重启应可恢复且不重算：%+v", results)
	}
	latest, err := restarted.Latest(job)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Type != RecDelivered {
		t.Fatalf("应保留交付记录：%+v", latest)
	}
}

// 未完成的 prepare 在恢复后记 failed/interrupted，且不自动重算。
func TestRecoverAllUnfinishedPrepareFailed(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	job, _ := NewJobID()
	session, _ := NewSessionID()
	if err := store.Append(job, Record{Type: RecCreated, SessionID: session, SourceName: "a.txt"}); err != nil {
		t.Fatal(err)
	}
	results, err := store.RecoverAll(root, session)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Action != RecoveryInterrupted || results[0].State != StateFailed {
		t.Fatalf("未完成 prepare 应记中断：%+v", results)
	}
	recs, err := store.Load(job)
	if err != nil {
		t.Fatal(err)
	}
	last := recs[len(recs)-1]
	if last.Type != RecFailed || last.FailCode != string(CodeInterrupted) {
		t.Fatalf("应追加 interrupted 失败事件：%+v", last)
	}
}

// 未知版本任务不猜测迁移，恢复直接拒绝。
func TestRecoverAllUnknownVersionRejected(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	job, _ := NewJobID()
	dir := store.JobDir(job)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "000001.json"), []byte(`{"version":99,"seq":1,"job_id":"`+job+`","type":"created"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	session, _ := NewSessionID()
	results, err := store.RecoverAll(root, session)
	if err != nil {
		t.Fatalf("未知版本不应让启动失败，而是拒绝该任务：%v", err)
	}
	if len(results) != 1 || results[0].State != StateFailed {
		t.Fatalf("未知版本应拒绝该任务：%+v", results)
	}
	if _, lerr := store.Load(job); !errors.Is(lerr, ErrUnknownStateVersion) {
		t.Fatalf("仍应报告未知版本：%v", lerr)
	}
}
