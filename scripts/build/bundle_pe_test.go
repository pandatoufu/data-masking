package main

import (
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const windowsBundleTarget = "windows/amd64"

// bundleManifestEvidence 表示平台目录 release.json 对 Windows 发布核验的可采信程度。
// 平台目录只保存单一目标清单；非 windows/amd64 的清单仍必须跳过，
// 不能当作本次 Windows 发布证据。
type bundleManifestEvidence int

const (
	// evidenceMissing 表示清单不存在（尚未 bundle 或已被清理）。
	evidenceMissing bundleManifestEvidence = iota
	// evidenceOtherTarget 表示清单存在但目标不是 windows/amd64。
	evidenceOtherTarget
	// evidenceWindows 表示清单声明 windows/amd64，须严格核验。
	evidenceWindows
)

// verifyWindowsBundleManifest 读取给定平台发布目录的 release.json；仅当目标为 windows/amd64 时
// 严格核验版本、产物集合（两个 exe）与 SHA。清单缺失或为其他目标时返回对应
// evidence 且不读取产物，由调用方 Skip 并提示重建。清单损坏一律返回错误，不跳过。
func verifyWindowsBundleManifest(dist string) (releaseManifest, bundleManifestEvidence, error) {
	manifest := releaseManifest{}
	raw, err := os.ReadFile(filepath.Join(dist, "release.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return manifest, evidenceMissing, nil
		}
		return manifest, evidenceMissing, fmt.Errorf("读取 release.json 失败：%w", err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return manifest, evidenceMissing, fmt.Errorf("解析 release.json 失败：%w", err)
	}
	if manifest.Target != windowsBundleTarget {
		// windows/ 但非 amd64 仍属 Windows 清单错误，不得静默跳过。
		if strings.HasPrefix(manifest.Target, "windows/") {
			return manifest, evidenceWindows, fmt.Errorf("清单目标应为 %s，got %q", windowsBundleTarget, manifest.Target)
		}
		return manifest, evidenceOtherTarget, nil
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return manifest, evidenceWindows, errors.New("清单缺少 version")
	}
	if len(manifest.Files) != 2 {
		return manifest, evidenceWindows, fmt.Errorf("清单应含两个产物，got %d", len(manifest.Files))
	}

	want := map[string]bool{
		outputBase + ".exe":       false,
		outputBase + "-agent.exe": false,
	}
	for _, f := range manifest.Files {
		seen, ok := want[f.Name]
		if !ok {
			return manifest, evidenceWindows, fmt.Errorf("清单产物名不在 Windows bundle 集合内：%q", f.Name)
		}
		if seen {
			return manifest, evidenceWindows, fmt.Errorf("清单产物重复：%q", f.Name)
		}
		want[f.Name] = true

		sum, err := hex.DecodeString(f.SHA256)
		if err != nil || len(sum) != sha256.Size || strings.ToLower(f.SHA256) != f.SHA256 {
			return manifest, evidenceWindows, fmt.Errorf("%s 的 SHA-256 格式非法：%q", f.Name, f.SHA256)
		}
		data, err := os.ReadFile(filepath.Join(dist, f.Name))
		if err != nil {
			return manifest, evidenceWindows, fmt.Errorf("清单产物缺失 %s：%w", f.Name, err)
		}
		if len(data) == 0 {
			return manifest, evidenceWindows, fmt.Errorf("清单产物为空 %s", f.Name)
		}
		actual := sha256.Sum256(data)
		if hex.EncodeToString(actual[:]) != f.SHA256 {
			return manifest, evidenceWindows, fmt.Errorf("%s SHA 不一致", f.Name)
		}
	}
	return manifest, evidenceWindows, nil
}

// windowsBundleDir 返回 Windows 平台发布目录；实物检查只读取该目录，
// 不搜索 dist 顶层或 Linux 目录作为替代证据。
func windowsBundleDir() string {
	return filepath.Join("..", "..", "dist", "windows-amd64")
}

// 本次 bundle 交叉构建的 Windows PE 资源核验（编译+资源检查，不代表 Windows 实际运行）。
// 该检查独立于 release.json：即使平台清单缺失或损坏，现有 exe 仍可做 PE 资源检查，
// 但日志须明确其不是 Windows 发布证据。
func TestBundleWindowsPEResources(t *testing.T) {
	dist := windowsBundleDir()
	gui := filepath.Join(dist, "data-masking.exe")
	agent := filepath.Join(dist, "data-masking-agent.exe")
	for _, p := range []string{gui, agent} {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("%s 不存在；先串行执行 go run ./scripts/build -bundle -platform windows/amd64", p)
		}
	}

	manifest, evidence, err := verifyWindowsBundleManifest(dist)
	switch {
	case err != nil:
		t.Logf("提示：平台 release.json 无法核验（%v）；仍对现有 exe 做 PE 资源检查，但这不是 Windows 发布证据，需重建：go run ./scripts/build -bundle -platform windows/amd64", err)
	case evidence != evidenceWindows:
		t.Logf("提示：平台 release.json 不是 windows/amd64 清单（target=%q）；仍对现有 exe 做 PE 资源检查，但这不是 Windows 发布证据，需重建：go run ./scripts/build -bundle -platform windows/amd64", manifest.Target)
	}

	guiRes, err := readWindowsResources(gui)
	if err != nil {
		t.Fatalf("读取 GUI PE 失败：%v", err)
	}
	if guiRes.machine != 0x8664 {
		t.Fatalf("GUI 架构应为 amd64(0x8664)，got 0x%x", guiRes.machine)
	}
	if guiRes.subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI {
		t.Fatalf("GUI subsystem 应保留 Windows GUI，got %d", guiRes.subsystem)
	}
	if !guiRes.hasRsrc || !guiRes.hasManifest || !guiRes.hasVersion {
		t.Fatalf("GUI 应保留资源/清单/版本：rsrc=%v manifest=%v version=%v", guiRes.hasRsrc, guiRes.hasManifest, guiRes.hasVersion)
	}
	if len(guiRes.images) != 6 {
		t.Fatalf("GUI 图标应有 6 档：%d", len(guiRes.images))
	}

	agentRes, err := readWindowsResources(agent)
	if err != nil {
		t.Fatalf("读取 headless PE 失败：%v", err)
	}
	if agentRes.machine != 0x8664 {
		t.Fatalf("headless 架构应为 amd64(0x8664)，got 0x%x", agentRes.machine)
	}
	// IMAGE_SUBSYSTEM_WINDOWS_CUI = 3；headless 不得使用 windowsgui。
	if agentRes.subsystem != 3 {
		t.Fatalf("headless 应保留 console subsystem(3)，got %d", agentRes.subsystem)
	}
}

// Windows 清单核验独立成测：只有平台目录 release.json 明确声明 windows/amd64 时才是
// Windows 发布证据；其他目标 Skip 并提示重建，损坏/版本/SHA/产物集合错误一律失败。
func TestBundleWindowsManifest(t *testing.T) {
	dist := windowsBundleDir()
	manifest, evidence, err := verifyWindowsBundleManifest(dist)
	if err != nil {
		t.Fatalf("Windows 清单核验失败：%v", err)
	}
	switch evidence {
	case evidenceMissing:
		t.Skip("dist/windows-amd64/release.json 缺失；这不是 Windows 发布证据，需重建：go run ./scripts/build -bundle -platform windows/amd64")
	case evidenceOtherTarget:
		t.Skipf("dist/windows-amd64/release.json 目标为 %q（非 windows/amd64）；这不是 Windows 发布证据，需重建：go run ./scripts/build -bundle -platform windows/amd64", manifest.Target)
	}

	// 版本注入在 headless 二进制内可见（与清单版本一致）。
	agentBytes, err := os.ReadFile(filepath.Join(dist, "data-masking-agent.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agentBytes), manifest.Version) {
		t.Fatalf("headless 二进制应含注入版本 %q", manifest.Version)
	}
	t.Logf("Windows bundle version=%s files=%d", manifest.Version, len(manifest.Files))
}

// 旧顶层与 Linux 平台产物不作为 Windows 证据：检查固定读 dist/windows-amd64。
func TestWindowsEvidenceUsesOnlyPlatformDir(t *testing.T) {
	root := t.TempDir()
	b := &builder{root: root}
	dist := filepath.Join(root, "dist")

	// 旧顶层放置看似完整的 Windows bundle，Linux 平台目录放置完整 Linux bundle。
	topFiles := map[string]string{
		"data-masking.exe":       "top-gui",
		"data-masking-agent.exe": "top-agent",
	}
	for n, c := range topFiles {
		mustWrite(t, filepath.Join(dist, n), []byte(c))
	}
	topManifest, err := json.MarshalIndent(releaseManifest{
		Version: "1.0.0",
		Target:  windowsBundleTarget,
		Files: []releaseFile{
			{Name: "data-masking.exe", SHA256: sha256Hex([]byte(topFiles["data-masking.exe"]))},
			{Name: "data-masking-agent.exe", SHA256: sha256Hex([]byte(topFiles["data-masking-agent.exe"]))},
		},
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dist, "release.json"), topManifest)

	linuxDir := filepath.Join(dist, "linux-amd64")
	linuxFiles := map[string]string{"data-masking": "linux-gui", "data-masking-agent": "linux-agent"}
	for n, c := range linuxFiles {
		mustWrite(t, filepath.Join(linuxDir, n), []byte(c))
	}
	linuxManifest, err := json.MarshalIndent(releaseManifest{
		Version: "1.0.0",
		Target:  "linux/amd64",
		Files: []releaseFile{
			{Name: "data-masking", SHA256: sha256Hex([]byte(linuxFiles["data-masking"]))},
			{Name: "data-masking-agent", SHA256: sha256Hex([]byte(linuxFiles["data-masking-agent"]))},
		},
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(linuxDir, "release.json"), linuxManifest)

	winDir := b.distDir(buildTarget{OS: "windows", Arch: "amd64"})
	if winDir != filepath.Join(dist, "windows-amd64") {
		t.Fatalf("Windows 证据目录应为 dist/windows-amd64，实际 %s", winDir)
	}
	m, evidence, err := verifyWindowsBundleManifest(winDir)
	if err != nil {
		t.Fatalf("缺失 Windows 平台目录应返回证据不足而非失败：%v", err)
	}
	if evidence != evidenceMissing || m.Target != "" {
		t.Fatalf("旧顶层/Linux 清单不得作为 Windows 证据：evidence=%v manifest=%+v", evidence, m)
	}
}

// 隔离夹具回归：全部在临时目录构造，不读写 dist，不改生产发布行为。
func TestVerifyWindowsBundleManifestFixtures(t *testing.T) {
	guiName := outputBase + ".exe"
	agentName := outputBase + "-agent.exe"
	guiData := []byte("gui-binary")
	agentData := []byte("agent-binary")

	newValid := func() releaseManifest {
		return releaseManifest{
			Version: "1.0.0",
			Target:  windowsBundleTarget,
			Files: []releaseFile{
				{Name: guiName, SHA256: sha256Hex(guiData)},
				{Name: agentName, SHA256: sha256Hex(agentData)},
			},
		}
	}
	writeValidFiles := func(t *testing.T, dir string) {
		t.Helper()
		mustWrite(t, filepath.Join(dir, guiName), guiData)
		mustWrite(t, filepath.Join(dir, agentName), agentData)
	}
	writeManifest := func(t *testing.T, dir string, m releaseManifest) {
		t.Helper()
		raw, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(dir, "release.json"), raw)
	}

	t.Run("合法 Windows 清单", func(t *testing.T) {
		dir := t.TempDir()
		writeValidFiles(t, dir)
		writeManifest(t, dir, newValid())

		m, evidence, err := verifyWindowsBundleManifest(dir)
		if err != nil {
			t.Fatalf("合法清单不应失败：%v", err)
		}
		if evidence != evidenceWindows || m.Target != windowsBundleTarget || m.Version != "1.0.0" || len(m.Files) != 2 {
			t.Fatalf("合法清单解析不符：evidence=%v manifest=%+v", evidence, m)
		}
	})

	t.Run("Linux 清单不作为 Windows 证据", func(t *testing.T) {
		dir := t.TempDir()
		// 故意不写任何产物：非 Windows 目标不应读取或校验产物。
		writeManifest(t, dir, releaseManifest{
			Version: "1.0.0",
			Target:  "linux/amd64",
			Files: []releaseFile{
				{Name: outputBase, SHA256: sha256Hex([]byte("linux-gui"))},
				{Name: outputBase + "-agent", SHA256: sha256Hex([]byte("linux-agent"))},
			},
		})

		m, evidence, err := verifyWindowsBundleManifest(dir)
		if err != nil {
			t.Fatalf("Linux 清单应被判定为非 Windows 证据而非失败：%v", err)
		}
		if evidence != evidenceOtherTarget || m.Target != "linux/amd64" {
			t.Fatalf("Linux 清单应返回证据不足：evidence=%v manifest=%+v", evidence, m)
		}
	})

	t.Run("损坏清单", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "release.json"), []byte(`{"version":`))
		if _, _, err := verifyWindowsBundleManifest(dir); err == nil {
			t.Fatal("损坏清单必须失败")
		}
	})

	t.Run("缺失清单", func(t *testing.T) {
		dir := t.TempDir()
		_, evidence, err := verifyWindowsBundleManifest(dir)
		if err != nil {
			t.Fatalf("缺失清单应返回证据不足：%v", err)
		}
		if evidence != evidenceMissing {
			t.Fatalf("缺失清单 evidence=%v", evidence)
		}
	})

	t.Run("错误 SHA", func(t *testing.T) {
		dir := t.TempDir()
		writeValidFiles(t, dir)
		m := newValid()
		m.Files[0].SHA256 = sha256Hex([]byte("other"))
		writeManifest(t, dir, m)
		if _, _, err := verifyWindowsBundleManifest(dir); err == nil {
			t.Fatal("SHA 不一致必须失败")
		}
	})

	t.Run("非法 SHA 格式", func(t *testing.T) {
		dir := t.TempDir()
		writeValidFiles(t, dir)
		m := newValid()
		m.Files[1].SHA256 = "not-a-sha"
		writeManifest(t, dir, m)
		if _, _, err := verifyWindowsBundleManifest(dir); err == nil {
			t.Fatal("非法 SHA 格式必须失败")
		}
	})

	t.Run("空版本", func(t *testing.T) {
		dir := t.TempDir()
		writeValidFiles(t, dir)
		m := newValid()
		m.Version = "  "
		writeManifest(t, dir, m)
		if _, _, err := verifyWindowsBundleManifest(dir); err == nil {
			t.Fatal("空版本必须失败")
		}
	})

	t.Run("产物集合错误", func(t *testing.T) {
		dir := t.TempDir()
		writeValidFiles(t, dir)
		m := newValid()
		m.Files = append(m.Files, releaseFile{Name: "extra.exe", SHA256: sha256Hex([]byte("extra"))})
		writeManifest(t, dir, m)
		if _, _, err := verifyWindowsBundleManifest(dir); err == nil {
			t.Fatal("多余产物必须失败")
		}
	})

	t.Run("产物名错误", func(t *testing.T) {
		dir := t.TempDir()
		writeValidFiles(t, dir)
		m := newValid()
		m.Files[1].Name = outputBase
		writeManifest(t, dir, m)
		if _, _, err := verifyWindowsBundleManifest(dir); err == nil {
			t.Fatal("非 Windows 产物名必须失败")
		}
	})

	t.Run("产物缺失", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, guiName), guiData)
		writeManifest(t, dir, newValid())
		if _, _, err := verifyWindowsBundleManifest(dir); err == nil {
			t.Fatal("清单产物缺失必须失败")
		}
	})

	t.Run("Windows 错误架构", func(t *testing.T) {
		dir := t.TempDir()
		m := newValid()
		m.Target = "windows/arm64"
		writeManifest(t, dir, m)
		if _, _, err := verifyWindowsBundleManifest(dir); err == nil {
			t.Fatal("非 amd64 的 Windows 目标必须失败")
		}
	})
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
