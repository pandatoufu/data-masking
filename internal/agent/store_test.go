package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"

	"github.com/pandatoufu/data-masking/internal/workspace"
)

func TestRandomIDsAreUniqueLowerHex(t *testing.T) {
	pattern := regexp.MustCompile(`^[0-9a-f]{32}$`)
	seen := map[string]bool{}
	for i := 0; i < 8; i++ {
		for _, gen := range []func() (string, error){
			NewJobID, NewSessionID, NewConfirmationID, NewAttemptID,
		} {
			id, err := gen()
			if err != nil {
				t.Fatal(err)
			}
			if !pattern.MatchString(id) {
				t.Fatalf("标识应为 32 位小写 hex：%q", id)
			}
			if seen[id] {
				t.Fatalf("标识重复：%q", id)
			}
			seen[id] = true
		}
	}
}

func TestStoreAppendLoadContiguous(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	job, _ := NewJobID()
	session, _ := NewSessionID()
	if err := store.Append(job, Record{Type: RecCreated, SessionID: session, SourceName: "report.docx", Profile: "balanced"}); err != nil {
		t.Fatalf("首条事件失败：%v", err)
	}
	if err := store.Append(job, Record{Type: RecMaskedSaved, MaskedRel: "masked/" + job + "/report.masked.docx"}); err != nil {
		t.Fatalf("第二条事件失败：%v", err)
	}
	records, err := store.Load(job)
	if err != nil {
		t.Fatalf("Load 失败：%v", err)
	}
	if len(records) != 2 || records[0].Seq != 1 || records[1].Seq != 2 {
		t.Fatalf("序号应连续：%+v", records)
	}
	if records[0].Version != stateVersion || records[1].JobID != job {
		t.Fatalf("应写入版本与 job_id：%+v", records)
	}
	if SessionOf(records) != session {
		t.Fatalf("会话应可恢复：%q", SessionOf(records))
	}
	if SeqOf(records) != 2 {
		t.Fatalf("SeqOf=%d", SeqOf(records))
	}
}

func TestStoreRejectsUnknownVersionAndGaps(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	job, _ := NewJobID()
	if err := store.Append(job, Record{Type: RecCreated}); err != nil {
		t.Fatal(err)
	}
	dir := store.JobDir(job)
	// 未知版本。
	unknown := Record{Version: 99, Seq: 2, JobID: job, Type: RecDelivered}
	raw, _ := json.Marshal(unknown)
	if err := os.WriteFile(filepath.Join(dir, "000002.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(job); !errors.Is(err, ErrUnknownStateVersion) {
		t.Fatalf("未知版本应拒绝，got %v", err)
	}
	// 序号缺口。
	if err := os.Remove(filepath.Join(dir, "000002.json")); err != nil {
		t.Fatal(err)
	}
	gap := Record{Version: stateVersion, Seq: 3, JobID: job, Type: RecDelivered}
	raw, _ = json.Marshal(gap)
	if err := os.WriteFile(filepath.Join(dir, "000003.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(job); !errors.Is(err, ErrStateIntegrity) {
		t.Fatalf("序号缺口应拒绝，got %v", err)
	}
}

// 事件记录只含白名单字段，不落原文/词值/正文。
func TestStoreRecordHasNoSensitiveFields(t *testing.T) {
	rec := Record{
		Version:      stateVersion,
		Seq:          1,
		JobID:        "0123456789abcdef0123456789abcdef",
		Type:         RecCreated,
		SourceName:   "report.txt",
		Profile:      "balanced",
		MaskedRel:    "masked/x/report.masked.txt",
		MappingRel:   "mapping/x/report.masked.mapping.json",
		InputSHA256:  "abc",
		MaskedSHA256: "def",
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"version": true, "seq": true, "time": true, "job_id": true, "session_id": true, "type": true,
		"source_name": true, "profile": true, "project_path": true, "config_fingerprint": true,
		"input_sha256": true, "masked_rel": true, "mapping_rel": true, "masked_sha256": true,
		"mapping_sha256": true, "confirmation_id": true, "confirmation_kind": true, "residual_count": true,
		"attempt_id":       true,
		"project_file_rel": true, "project_file_sha256": true, "restored_rel": true, "restored_sha256": true, "response_sha256": true,
		"restored_count": true,
		"warnings":       true,
		"fail_code":      true,
		"audit_failed":   true,
	}
	for key := range decoded {
		if !allowed[key] {
			t.Fatalf("未审定字段 %q 不得进入任务记录", key)
		}
	}
	for _, banned := range []string{"words", "mapping_bytes", "restored_text", "masked_bytes", "content", "secret_word"} {
		if _, ok := decoded[banned]; ok {
			t.Fatalf("不得存 %q", banned)
		}
	}
}

func TestStorePrivatePermissionsNewFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 继承用户目录 ACL，不适用 POSIX 权限断言")
	}
	root := t.TempDir()
	store := NewStore(root)
	job, _ := NewJobID()
	if err := store.Append(job, Record{Type: RecCreated}); err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Stat(filepath.Join(root, workspace.AgentStateDir))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("私有目录应为 0700，got %o", perm)
	}
	jobInfo, err := os.Stat(store.JobDir(job))
	if err != nil {
		t.Fatal(err)
	}
	if perm := jobInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("任务目录应为 0700，got %o", perm)
	}
	fileInfo, err := os.Stat(filepath.Join(store.JobDir(job), "000001.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Fatalf("事件文件应为 0600，got %o", perm)
	}
}

func TestStoreRejectsInvalidJobID(t *testing.T) {
	store := NewStore(t.TempDir())
	for _, bad := range []string{"../escape", "", "xyz", "0123456789ABCDEF0123456789abcdef"} {
		if err := store.Append(bad, Record{Type: RecCreated}); err == nil {
			t.Fatalf("非法 job_id 应拒绝：%q", bad)
		}
	}
}
