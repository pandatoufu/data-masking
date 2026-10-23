package agent

import (
	"crypto/rand"
	"encoding/hex"
)

// NewJobID 生成 128 bit 加密随机任务标识（32 位小写 hex），不可由调用者指定。
func NewJobID() (string, error) {
	return randomHex(16)
}

// NewSessionID 生成 headless 会话标识，用于确认凭据绑定。
func NewSessionID() (string, error) {
	return randomHex(16)
}

// NewConfirmationID 生成与 job_id 同级的确认凭据（不可由调用者指定）。
func NewConfirmationID() (string, error) {
	return randomHex(16)
}

// NewAttemptID 生成回程 attempt 标识，避免失败重试与旧快照重名。
func NewAttemptID() (string, error) {
	return randomHex(16)
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
