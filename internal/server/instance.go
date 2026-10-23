package server

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type instanceInfo struct {
	PID int    `json:"pid"`
	URL string `json:"url"`
}

func writeLock(path string, info instanceInfo) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// existingInstance 读取锁文件并做存活探针；旧实例已死则视为崩溃残留并清理，返回 alive=false。
func existingInstance(path string) (url string, alive bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var info instanceInfo
	if err := json.Unmarshal(data, &info); err != nil || info.URL == "" {
		_ = os.Remove(path)
		return "", false
	}
	if IsAlive(info.URL) {
		return info.URL, true
	}
	// 崩溃残留：探针不通即判定旧实例已死，清理后允许接管。
	_ = os.Remove(path)
	return "", false
}

// IsAlive 用 HTTP 探针判断实例是否存活（命中页面端点，不新增 API）。
func IsAlive(url string) bool {
	client := &http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Get(url + "/")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func command(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }
