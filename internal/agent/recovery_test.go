package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pandatoufu/data-masking/internal/workspace"
)

func writeStateFile(t *testing.T, root, rel string, data []byte) string {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return abs
}

func newJob(t *testing.T, store *Store) string {
	t.Helper()
	job, err := NewJobID()
	if err != nil {
		t.Fatal(err)
	}
	session, _ := NewSessionID()
	if err := store.Append(job, Record{Type: RecCreated, SessionID: session, SourceName: "report.txt", Profile: "balanced"}); err != nil {
		t.Fatal(err)
	}
	return job
}

// 项目副本完整发布后崩溃：恢复补记 delivered。
func TestRecoverCompletesProjectDelivery(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	job := newJob(t, store)
	projectRel := filepath.ToSlash(filepath.Join(".masked-input", job, "input.masked.txt"))
	data := []byte("masked-bytes")
	writeStateFile(t, root, projectRel, data)
	sha := SHA256Hex(data)
	if err := store.Append(job, Record{Type: RecProjectIntent, ProjectFileRel: projectRel, ProjectFileSHA256: sha}); err != nil {
		t.Fatal(err)
	}

	res, err := store.RecoverJob(root, job)
	if err != nil {
		t.Fatalf("恢复失败：%v", err)
	}
	if res.Action != RecoveryCompletedProject || res.State != StateDelivered {
		t.Fatalf("应补记 delivered：%+v", res)
	}
}

// 还原文件完整发布后崩溃：恢复补记 restored。
func TestRecoverCompletesRestore(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	job := newJob(t, store)
	if err := store.Append(job, Record{Type: RecDelivered, ProjectFileRel: ".masked-input/" + job + "/input.masked.txt"}); err != nil {
		t.Fatal(err)
	}
	attempt, _ := NewAttemptID()
	restoredRel := filepath.ToSlash(filepath.Join(workspace.DirRestored, job, attempt, "report.restored.txt"))
	data := []byte("restored")
	writeStateFile(t, root, restoredRel, data)
	if err := store.Append(job, Record{Type: RecRestoreIntent, AttemptID: attempt, RestoredRel: restoredRel, RestoredSHA256: SHA256Hex(data)}); err != nil {
		t.Fatal(err)
	}
	res, err := store.RecoverJob(root, job)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != RecoveryCompletedRestore || res.State != StateRestored {
		t.Fatalf("应补记 restored：%+v", res)
	}
}

// 私有半配对：可确定归属的文件被删除并记中断。
func TestRecoverPartialPrivatePairRollsBack(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	job := newJob(t, store)
	maskedRel := filepath.ToSlash(filepath.Join(workspace.DirMasked, job, "report.masked.txt"))
	data := []byte("masked")
	abs := writeStateFile(t, root, maskedRel, data)
	if err := store.Append(job, Record{
		Type:          RecPublishIntent,
		MaskedRel:     maskedRel,
		MaskedSHA256:  SHA256Hex(data),
		MappingRel:    filepath.ToSlash(filepath.Join(workspace.DirMapping, job, "report.masked.mapping.json")),
		MappingSHA256: SHA256Hex([]byte("mapping")),
	}); err != nil {
		t.Fatal(err)
	}
	res, err := store.RecoverJob(root, job)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != RecoveryInterrupted {
		t.Fatalf("半配对应记中断：%+v", res)
	}
	if _, err := os.Stat(abs); !os.IsNotExist(err) {
		t.Fatal("可归属的半配对文件应被回滚")
	}
}

// 归属不明（SHA 不符）的文件不得删除，任务被拒绝。
func TestRecoverUnknownOwnershipPreserved(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	job := newJob(t, store)
	maskedRel := filepath.ToSlash(filepath.Join(workspace.DirMasked, job, "report.masked.txt"))
	abs := writeStateFile(t, root, maskedRel, []byte("someone-else"))
	if err := store.Append(job, Record{
		Type:          RecPublishIntent,
		MaskedRel:     maskedRel,
		MaskedSHA256:  SHA256Hex([]byte("expected")),
		MappingRel:    filepath.ToSlash(filepath.Join(workspace.DirMapping, job, "m.map.json")),
		MappingSHA256: SHA256Hex([]byte("mapping")),
	}); err != nil {
		t.Fatal(err)
	}
	res, err := store.RecoverJob(root, job)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != RecoveryUnknownOwner {
		t.Fatalf("应拒绝归属不明的任务：%+v", res)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("归属不明文件不得删除：%v", err)
	}
}

// prepare 未发布（只有 created）记 interrupted。
func TestRecoverUnpublishedPrepareInterrupted(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	job := newJob(t, store)
	res, err := store.RecoverJob(root, job)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != RecoveryInterrupted || res.State != StateFailed {
		t.Fatalf("未发布 prepare 应记中断：%+v", res)
	}
}

// 未完成的 restore attempt 保持 delivered 可重试。
func TestRecoverIncompleteRestoreKeepsDelivered(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	job := newJob(t, store)
	if err := store.Append(job, Record{Type: RecDelivered, ProjectFileRel: "x"}); err != nil {
		t.Fatal(err)
	}
	attempt, _ := NewAttemptID()
	if err := store.Append(job, Record{Type: RecRestoreIntent, AttemptID: attempt, RestoredRel: "restored/missing.txt", RestoredSHA256: "deadbeef"}); err != nil {
		t.Fatal(err)
	}
	res, err := store.RecoverJob(root, job)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != RecoveryInterrupted || res.State != StateDelivered {
		t.Fatalf("应保持 delivered：%+v", res)
	}
	records, err := store.Load(job)
	if err != nil {
		t.Fatal(err)
	}
	if DeriveState(records) != StateDelivered {
		t.Fatalf("稳定状态应为 delivered：%v", DeriveState(records))
	}
}

// 终态任务恢复不产生新事件。
func TestRecoverTerminalNoop(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	job := newJob(t, store)
	if err := store.Append(job, Record{Type: RecDelivered, ProjectFileRel: "x"}); err != nil {
		t.Fatal(err)
	}
	before, _ := store.Load(job)
	res, err := store.RecoverJob(root, job)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != RecoveryNone {
		t.Fatalf("终态不应有新动作：%+v", res)
	}
	after, _ := store.Load(job)
	if len(before) != len(after) {
		t.Fatal("终态不得追加事件")
	}
}
