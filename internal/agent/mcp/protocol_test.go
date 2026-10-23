package mcp_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pandatoufu/data-masking/internal/agent"
	agentmcp "github.com/pandatoufu/data-masking/internal/agent/mcp"
	"github.com/pandatoufu/data-masking/internal/audit"
	"github.com/pandatoufu/data-masking/internal/config"
	"github.com/pandatoufu/data-masking/internal/core"
	"github.com/pandatoufu/data-masking/internal/docxtest"
	"github.com/pandatoufu/data-masking/internal/workspace"
)

// newPair 返回已连接的内存传输 client/server 与已打开的应用层。
func newPair(t *testing.T) (*sdk.ClientSession, *agent.App, string) {
	t.Helper()
	root := t.TempDir()
	if err := workspace.EnsureHeadless(root); err != nil {
		t.Fatal(err)
	}
	// custom 启用时词库来自 cards/；预置中性词文件满足默认 profile 前提。
	if err := os.WriteFile(filepath.Join(root, workspace.DirCards, "fixture.txt"), []byte("fixture-card-word\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := agent.NewApp(root, filepath.Join(t.TempDir(), "absent.json"))
	if err := app.Open(); err != nil {
		t.Fatalf("打开会话失败：%v", err)
	}
	t.Cleanup(func() { _ = app.Close() })

	server := agentmcp.NewServer(app)
	serverT, clientT := sdk.NewInMemoryTransports()
	ctx := context.Background()
	go func() { _ = server.SDKServer().Run(ctx, serverT) }()

	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	session, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("连接失败：%v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, app, root
}

// 工具发现只暴露三个工具，且描述要求转问用户、不引导读取原文。
func TestToolDiscoveryExactlyThree(t *testing.T) {
	session, _, _ := newPair(t)
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("列举工具失败：%v", err)
	}
	if len(res.Tools) != 3 {
		t.Fatalf("应只发现三个工具：%d", len(res.Tools))
	}
	names := map[string]*sdk.Tool{}
	for _, tool := range res.Tools {
		names[tool.Name] = tool
	}
	for _, want := range []string{agentmcp.ToolPrepare, agentmcp.ToolConfirm, agentmcp.ToolRestore} {
		tool, ok := names[want]
		if !ok {
			t.Fatalf("缺少工具 %s", want)
		}
		if tool.InputSchema == nil {
			t.Fatalf("%s 应有入参 schema", want)
		}
	}
	prepare := names[agentmcp.ToolPrepare]
	if !strings.Contains(prepare.Description, "必须询问用户") {
		t.Error("prepare 描述应要求转问用户")
	}
	if !strings.Contains(prepare.Description, "balanced") {
		t.Error("prepare 描述应披露 balanced 边界")
	}
	if !strings.Contains(prepare.Description, "cards/") || !strings.Contains(prepare.Description, "每行一词") {
		t.Error("prepare 描述应说明 cards 直属 TXT 词库来源")
	}
	if !strings.Contains(prepare.Description, "关闭时不读取词库") {
		t.Error("prepare 描述应说明 custom 关闭不读词库")
	}
	if !strings.Contains(names[agentmcp.ToolConfirm].Description, "cards 词库") {
		t.Error("confirm 描述应说明 cards 变化使确认失效")
	}
	if !strings.Contains(names[agentmcp.ToolRestore].Description, "不依赖当前 cards 词库") {
		t.Error("restore 描述应说明回程不依赖 cards")
	}
	for _, tool := range res.Tools {
		if strings.Contains(tool.Description, "force") || strings.Contains(tool.Description, "词数组") {
			t.Errorf("%s 描述不得提供 force/词数组入口", tool.Name)
		}
	}
}

// 入参 schema 只允许规定字段，拒绝 force/原文/词数组等。
func TestToolInputSchemaStrict(t *testing.T) {
	session, _, _ := newPair(t)
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string][]string{
		agentmcp.ToolPrepare: {"source_name", "project_path", "profile"},
		agentmcp.ToolConfirm: {"job_id", "confirmation_id", "decision"},
		agentmcp.ToolRestore: {"job_id", "response_path"},
	}
	for _, tool := range res.Tools {
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		props, _ := schema["properties"].(map[string]any)
		if len(props) != len(allowed[tool.Name]) {
			t.Fatalf("%s 入参数量不符：%v", tool.Name, props)
		}
		for _, name := range allowed[tool.Name] {
			if _, ok := props[name]; !ok {
				t.Fatalf("%s 缺少入参 %s", tool.Name, name)
			}
		}
		for _, banned := range []string{"force", "confirmed", "text", "content", "words", "mapping", "rules"} {
			if _, ok := props[banned]; ok {
				t.Fatalf("%s 不得接受 %s", tool.Name, banned)
			}
		}
		// 三个字段都必须必填（prepare/restore/confirm 全部 required）。
		required, _ := schema["required"].([]any)
		if len(required) != len(allowed[tool.Name]) {
			t.Fatalf("%s 应全部必填：%v", tool.Name, required)
		}
	}
}

// 输出 schema/枚举必须公开审定白名单全集（含独立 text_mismatch）。
func TestToolOutputSchemaWarningEnum(t *testing.T) {
	session, _, _ := newPair(t)
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	catalog := agent.WarningCatalog()
	wantKinds := make([]string, 0, len(catalog))
	wantMessages := make([]string, 0, len(catalog))
	for k, m := range catalog {
		wantKinds = append(wantKinds, k)
		wantMessages = append(wantMessages, m)
	}
	sort.Strings(wantKinds)
	sort.Strings(wantMessages)
	if !slices.Contains(wantKinds, "text_mismatch") {
		t.Fatalf("公开白名单应含 text_mismatch：%v", wantKinds)
	}
	for _, tool := range res.Tools {
		if tool.OutputSchema == nil {
			t.Fatalf("%s 应声明输出 schema", tool.Name)
		}
		raw, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		props, _ := schema["properties"].(map[string]any)
		warnings, _ := props["warnings"].(map[string]any)
		items, _ := warnings["items"].(map[string]any)
		wprops, _ := items["properties"].(map[string]any)
		kinds := enumStrings(t, wprops["kind"])
		messages := enumStrings(t, wprops["message"])
		if !equalStrings(kinds, wantKinds) {
			t.Fatalf("%s warnings.kind 枚举应与白名单一致：%v vs %v", tool.Name, kinds, wantKinds)
		}
		if !slices.Contains(kinds, "text_mismatch") {
			t.Fatalf("%s warnings.kind 应含 text_mismatch：%v", tool.Name, kinds)
		}
		if !equalStrings(messages, wantMessages) {
			t.Fatalf("%s warnings.message 枚举应与固定文案一致", tool.Name)
		}
	}
}

// enumStrings 从 JSON schema 节点取 enum 字符串切片。
func enumStrings(t *testing.T, node any) []string {
	t.Helper()
	m, ok := node.(map[string]any)
	if !ok {
		t.Fatalf("schema 节点不是对象：%T", node)
	}
	raw, _ := m["enum"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("enum 成员应为字符串：%T", v)
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// 还原一致性告警：text/structured content 同源，且不泄露原文/指纹/部件名。
func TestRestoreWarningContentAgreeAndNoLeak(t *testing.T) {
	session, _, root := newPair(t)
	project := t.TempDir()

	orig, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{Body: []string{"电话 13812345678"}})
	if err != nil {
		t.Fatal(err)
	}
	origDir := filepath.Join(root, workspace.DirOriginal)
	if err := os.MkdirAll(origDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(origDir, "合同.docx"), orig, 0o600); err != nil {
		t.Fatal(err)
	}

	prep := callTool(t, session, agentmcp.ToolPrepare, map[string]any{
		"source_name":  "合同.docx",
		"project_path": project,
		"profile":      "strict",
	})
	if prep["status"] != "delivered" {
		t.Fatalf("应交付：%v", prep)
	}

	rewritten, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{Body: []string{"完全不同的回复内容"}})
	if err != nil {
		t.Fatal(err)
	}
	reply := filepath.Join(project, "reply.docx")
	if err := os.WriteFile(reply, rewritten, 0o600); err != nil {
		t.Fatal(err)
	}

	call, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name: agentmcp.ToolRestore,
		Arguments: map[string]any{
			"job_id":        prep["job_id"],
			"response_path": reply,
		},
	})
	if err != nil {
		t.Fatalf("调用失败：%v", err)
	}
	if call.IsError {
		t.Fatalf("还原不应为工具错误：%+v", call.Content)
	}
	structuredRaw, err := json.Marshal(call.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var structured map[string]any
	if err := json.Unmarshal(structuredRaw, &structured); err != nil {
		t.Fatal(err)
	}
	if structured["status"] != "restored" {
		t.Fatalf("应返回 restored：%v", structured)
	}
	if len(call.Content) != 1 {
		t.Fatalf("应有一个 text content：%+v", call.Content)
	}
	text, ok := call.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("content 应为 text：%T", call.Content[0])
	}
	var fromText map[string]any
	if err := json.Unmarshal([]byte(text.Text), &fromText); err != nil {
		t.Fatalf("text 应为 JSON：%v", err)
	}
	if !reflect.DeepEqual(fromText, structured) {
		t.Fatalf("text/structured content 必须同源：text=%v structured=%v", fromText, structured)
	}

	warnings, _ := structured["warnings"].([]any)
	found := false
	for _, item := range warnings {
		w, _ := item.(map[string]any)
		if w["kind"] != "text_mismatch" {
			continue
		}
		found = true
		if w["message"] != "还原后的 DOCX 文字指纹与原件不一致；仅供参考，不代表还原失败或 mapping 配错。" {
			t.Fatalf("text_mismatch 文案不符：%v", w)
		}
	}
	if !found {
		t.Fatalf("应返回 text_mismatch 告警：%v", warnings)
	}

	sum := sha256.Sum256(orig)
	fingerprint := hex.EncodeToString(sum[:])
	for _, channel := range []string{string(text.Text), string(structuredRaw)} {
		for _, banned := range []string{"13812345678", fingerprint, "word/document.xml", "part", "params"} {
			if strings.Contains(channel, banned) {
				t.Fatalf("MCP 输出泄露 %q：%s", banned, channel)
			}
		}
	}
}

// guidanceTexts 收集 initialize instructions 与三个工具说明，供固定安全段的一致性断言使用。
func guidanceTexts(t *testing.T, session *sdk.ClientSession) map[string]string {
	t.Helper()
	init := session.InitializeResult()
	if init == nil || strings.TrimSpace(init.Instructions) == "" {
		t.Fatal("initialize 应返回固定 instructions")
	}
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("列举工具失败：%v", err)
	}
	out := map[string]string{"initialize": init.Instructions}
	for _, tool := range res.Tools {
		out[tool.Name] = tool.Description
	}
	if len(out) != 4 {
		t.Fatalf("应包含初始化说明与三个工具说明，got %d", len(out))
	}
	return out
}

// prepareInputSchema 返回 prepare_document 的公开入参 schema。
func prepareInputSchema(t *testing.T, session *sdk.ClientSession) map[string]any {
	t.Helper()
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("列举工具失败：%v", err)
	}
	for _, tool := range res.Tools {
		if tool.Name != agentmcp.ToolPrepare {
			continue
		}
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		return schema
	}
	t.Fatalf("缺少工具 %s", agentmcp.ToolPrepare)
	return nil
}

// 初始化 instructions、三工具说明与 prepare.profile 属性说明都带固定安全指引：
// 五类禁读对象、其他工具绕读、「先询问用户」档位指引与合作式非沙箱定位。
func TestGuidanceOnInitializeAndToolDiscovery(t *testing.T) {
	session, _, _ := newPair(t)
	texts := guidanceTexts(t, session)

	forbidden := []string{"original/", "cards/", "mapping/", "restored/", ".agent-state/"}
	shared := []string{"文件读取", "搜索", "终端", "config.json", "先询问用户", "沙箱"}
	for name, text := range texts {
		for _, want := range forbidden {
			if !strings.Contains(text, want) {
				t.Errorf("%s 说明应含禁读对象 %q", name, want)
			}
		}
		for _, want := range shared {
			if !strings.Contains(text, want) {
				t.Errorf("%s 说明应含固定指引 %q", name, want)
			}
		}
	}

	// prepare 说明另需披露内置档位名、custom 别名与本机配置优先。
	prep := texts[agentmcp.ToolPrepare]
	for _, want := range []string{"strict", "balanced", "minimal", "custom", "显示别名", "自建", "不是完整有效列表", "实际本机配置优先"} {
		if !strings.Contains(prep, want) {
			t.Errorf("prepare 说明应含档位边界 %q", want)
		}
	}

	// profile 属性说明同样要求先询问用户，且不得引导读取 config.json 发现档位。
	schema := prepareInputSchema(t, session)
	props, _ := schema["properties"].(map[string]any)
	profile, _ := props["profile"].(map[string]any)
	desc, _ := profile["description"].(string)
	if !strings.Contains(desc, "先询问用户") || !strings.Contains(desc, "不重复询问") {
		t.Errorf("profile 属性说明应要求先询问且不重复询问：%q", desc)
	}
	if !strings.Contains(desc, "strict") || !strings.Contains(desc, "custom") {
		t.Errorf("profile 属性说明应列出内置档位：%q", desc)
	}

	// 严格结构不变：三字段全必填、类型为 string、additionalProperties=false、不新增字段。
	required, _ := schema["required"].([]any)
	if len(required) != 3 {
		t.Errorf("prepare 应保持三个必填字段：%v", required)
	}
	if schema["additionalProperties"] != false {
		t.Errorf("prepare 应保持 additionalProperties=false：%v", schema["additionalProperties"])
	}
	if len(props) != 3 {
		t.Errorf("prepare 入参不应新增字段：%v", props)
	}
	for _, name := range []string{"source_name", "project_path", "profile"} {
		entry, _ := props[name].(map[string]any)
		if entry["type"] != "string" {
			t.Errorf("%s 应保持 string 类型：%v", name, entry["type"])
		}
	}
}

// 静态输出边界：不同工作区/配置/源名/候选名/词值/正文/mapping/指纹不得进入 instructions 或工具说明；
// 同时指引保留正常项目闭环（delivered 后可读 project_file、项目内写回复并传 response_path）。
func TestGuidanceStaticOutputBoundary(t *testing.T) {
	first, _, firstRoot := newPair(t)
	second, secondRoot := newPairWithFixture(t, "zq-guidance-marker")

	firstTexts := guidanceTexts(t, first)
	secondTexts := guidanceTexts(t, second)
	if !reflect.DeepEqual(firstTexts, secondTexts) {
		t.Fatalf("两套 fixture 的说明文本应完全一致：\n%v\n%v", firstTexts, secondTexts)
	}

	banned := []string{
		firstRoot, secondRoot,
		"zq-guidance-marker", "custom_words", "fixture-card-word",
	}
	for name, text := range firstTexts {
		for _, want := range banned {
			if strings.Contains(text, want) {
				t.Errorf("%s 说明不应含动态标记 %q", name, want)
			}
		}
	}

	// 正常闭环允许项必须在同一固定段内可见。
	loop := []string{
		"只有在 delivered 之后可以读取返回的 project_file 脱敏副本",
		"可以在项目内正常读写回复并把 response_path 交给 restore_document 本地还原",
		"不要读取 restored/ 正文",
		"不得自行批准风险确认",
		"也不代表整包或语义无泄露",
	}
	for name, text := range firstTexts {
		for _, want := range loop {
			if !strings.Contains(text, want) {
				t.Errorf("%s 说明应含正常闭环允许项 %q", name, want)
			}
		}
	}
}

// newPairWithFixture 构造带测试标记的第二组 fixture：标记化工作区、自建档位配置与候选词。
// 仅用于静态输出边界断言，不产生或读取任何真实敏感内容。
func newPairWithFixture(t *testing.T, marker string) (*sdk.ClientSession, string) {
	t.Helper()
	root := t.TempDir()
	if err := workspace.EnsureHeadless(root); err != nil {
		t.Fatal(err)
	}
	cardName := marker + "-cards.txt"
	if err := os.WriteFile(filepath.Join(root, workspace.DirCards, cardName), []byte(marker+"-word\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg := `{"custom_words":["` + marker + `-word"],"profiles":{"` + marker + `-profile":{"phone":true}}}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	app := agent.NewApp(root, cfgPath)
	if err := app.Open(); err != nil {
		t.Fatalf("打开会话失败：%v", err)
	}
	t.Cleanup(func() { _ = app.Close() })

	server := agentmcp.NewServer(app)
	serverT, clientT := sdk.NewInMemoryTransports()
	ctx := context.Background()
	go func() { _ = server.SDKServer().Run(ctx, serverT) }()

	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	session, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("连接失败：%v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, root
}

// 独立文本 content 与 structured content 同一失败信封，并断言 IsError 语义未改变。
func callToolEnvelope(t *testing.T, session *sdk.ClientSession, name string, args any) (map[string]any, map[string]any) {
	t.Helper()
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s 调用失败：%v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s 返回工具错误：%+v", name, res.Content)
	}
	if len(res.Content) != 1 {
		t.Fatalf("%s 应有一个 text content：%+v", name, res.Content)
	}
	text, ok := res.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("content 应为 text：%T", res.Content[0])
	}
	var fromText map[string]any
	if err := json.Unmarshal([]byte(text.Text), &fromText); err != nil {
		t.Fatalf("text 应为 JSON：%v", err)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var fromStructured map[string]any
	if err := json.Unmarshal(raw, &fromStructured); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromText, fromStructured) {
		t.Fatalf("%s text/structured content 必须同源：text=%v structured=%v", name, fromText, fromStructured)
	}
	return fromText, fromStructured
}

// assertFailedEnvelope 断言失败信封的 code/message 与未新增字段/状态。
func assertFailedEnvelope(t *testing.T, body map[string]any, code, message string) {
	t.Helper()
	if body["status"] != "failed" {
		t.Fatalf("应为 failed：%v", body)
	}
	if body["code"] != code {
		t.Fatalf("code 应为 %s：%v", code, body)
	}
	if body["message"] != message {
		t.Fatalf("message 应逐字匹配：got %q want %q", body["message"], message)
	}
	if body["schema_version"] != float64(agent.SchemaVersion) {
		t.Fatalf("应保持 schema_version=%d：%v", agent.SchemaVersion, body["schema_version"])
	}
	for _, banned := range []string{"job_id", "confirmation_id", "confirmation_kind", "allowed_decisions", "project_file"} {
		if _, ok := body[banned]; ok {
			t.Fatalf("失败信封不应新增/返回 %q：%v", banned, body)
		}
	}
}

// 档位反馈固定文本（逐字断言，不回显输入）。
const (
	msgMissingProfile = "缺少脱敏档位 profile。请先询问用户选择本机有效档位，明确选择后再调用；不得自行选择档位或原样重复调用。"
	msgGenericInvalid = "请求参数无效，请按工具说明提供必填字段。"
	msgInvalidProfile = "profile 无效。请先询问用户确认本机配置中有效的档位名，明确选择后再调用；不得自行选择档位或原样重复调用。"
)

// 缺失、空字符串或 null 档位在严格解码与其他必填项校验后立即返回专属 invalid_request；
// 非空未知档位保持 invalid_profile 并使用固定纠错指引。
func TestProfileFeedbackExactMessages(t *testing.T) {
	session, _, _ := newPair(t)
	project := t.TempDir()

	missing := []map[string]any{
		{"source_name": "fixture.txt", "project_path": project},
		{"source_name": "fixture.txt", "project_path": project, "profile": ""},
		{"source_name": "fixture.txt", "project_path": project, "profile": nil},
	}
	for _, args := range missing {
		text, structured := callToolEnvelope(t, session, agentmcp.ToolPrepare, args)
		assertFailedEnvelope(t, text, "invalid_request", msgMissingProfile)
		assertFailedEnvelope(t, structured, "invalid_request", msgMissingProfile)
	}

	text, structured := callToolEnvelope(t, session, agentmcp.ToolPrepare, map[string]any{
		"source_name":  "fixture.txt",
		"project_path": project,
		"profile":      "no-such-profile-xyz",
	})
	assertFailedEnvelope(t, text, "invalid_profile", msgInvalidProfile)
	assertFailedEnvelope(t, structured, "invalid_profile", msgInvalidProfile)
}

// 严格校验优先级矩阵：解码/类型/未知字段与其他必填项错误保持通用提示，
// 纯空白未知档位按未知档位处理，不 trim 成缺档位，也不默认换档。
func TestProfileFeedbackPriorityMatrix(t *testing.T) {
	session, _, root := newPair(t)
	project := t.TempDir()

	genericCases := []struct {
		name string
		args any
	}{
		// 传输层会拒绝语法错误的帧；handler 侧的非 JSON 对象（顶层字符串/数组/数字/null）
		// 与类型错误一样走严格解码失败 → 通用 invalid_request。
		{"顶层字符串参数", json.RawMessage(`"just-a-string"`)},
		{"顶层数组参数", json.RawMessage(`[1,2]`)},
		{"顶层数字参数", json.RawMessage(`7`)},
		{"顶层 null 参数", json.RawMessage(`null`)},
		{"数字档位", map[string]any{"source_name": "fixture.txt", "project_path": project, "profile": 1}},
		{"布尔档位", map[string]any{"source_name": "fixture.txt", "project_path": project, "profile": true}},
		{"对象档位", map[string]any{"source_name": "fixture.txt", "project_path": project, "profile": map[string]any{"name": "strict"}}},
		{"数组档位", map[string]any{"source_name": "fixture.txt", "project_path": project, "profile": []string{"strict"}}},
		{"未知字段", map[string]any{"source_name": "fixture.txt", "project_path": project, "profile": "strict", "extra": true}},
		{"缺 source_name 且缺档位", map[string]any{"project_path": project}},
		{"空 source_name 且空档位", map[string]any{"source_name": "", "project_path": project, "profile": ""}},
		{"缺 project_path 且缺档位", map[string]any{"source_name": "fixture.txt"}},
		{"空 project_path 且 null 档位", map[string]any{"source_name": "fixture.txt", "project_path": "", "profile": nil}},
	}
	for _, c := range genericCases {
		_, structured := callToolEnvelope(t, session, agentmcp.ToolPrepare, c.args)
		assertFailedEnvelope(t, structured, "invalid_request", msgGenericInvalid)
	}

	// 纯空白档位：不 trim 成缺档位，按非空未知档位处理。
	_, structured := callToolEnvelope(t, session, agentmcp.ToolPrepare, map[string]any{
		"source_name":  "fixture.txt",
		"project_path": project,
		"profile":      "   ",
	})
	assertFailedEnvelope(t, structured, "invalid_profile", msgInvalidProfile)

	// 路径/配置前置校验优先于未知档位：既有分类与顺序不变。
	_, structured = callToolEnvelope(t, session, agentmcp.ToolPrepare, map[string]any{
		"source_name":  "fixture.txt",
		"project_path": "relative-project",
		"profile":      "no-such-profile-xyz",
	})
	assertFailedEnvelope(t, structured, "invalid_project", agent.MessageFor(agent.CodeInvalidProject))

	_, structured = callToolEnvelope(t, session, agentmcp.ToolPrepare, map[string]any{
		"source_name":  "../fixture.txt",
		"project_path": project,
		"profile":      "no-such-profile-xyz",
	})
	assertFailedEnvelope(t, structured, "invalid_source", agent.MessageFor(agent.CodeInvalidSource))

	corruptSession, corruptRoot := newPairWithConfig(t, `{not-json`)
	seedOriginalFile(t, corruptRoot, "fixture.txt", []byte("hello team"))
	_, structured = callToolEnvelope(t, corruptSession, agentmcp.ToolPrepare, map[string]any{
		"source_name":  "fixture.txt",
		"project_path": t.TempDir(),
		"profile":      "no-such-profile-xyz",
	})
	assertFailedEnvelope(t, structured, "invalid_config", agent.MessageFor(agent.CodeInvalidConfig))

	// 有效内置档位与自建档位继续既有处理，不换档。
	seedOriginalFile(t, root, "fixture.txt", []byte("hello team"))
	for _, profile := range []string{"strict", "balanced", "minimal", "custom"} {
		body := callTool(t, session, agentmcp.ToolPrepare, map[string]any{
			"source_name":  "fixture.txt",
			"project_path": t.TempDir(),
			"profile":      profile,
		})
		if body["status"] != "delivered" {
			t.Fatalf("有效档位 %s 应继续既有处理：%v", profile, body)
		}
	}

	builtSession, builtRoot := newPairWithConfig(t, `{"profiles":{"team-built":{"phone":true}}}`)
	seedOriginalFile(t, builtRoot, "fixture.txt", []byte("hello team"))
	body := callTool(t, builtSession, agentmcp.ToolPrepare, map[string]any{
		"source_name":  "fixture.txt",
		"project_path": t.TempDir(),
		"profile":      "team-built",
	})
	if body["status"] != "delivered" {
		t.Fatalf("自建档位应继续既有处理：%v", body)
	}
}

// spyMasker 记录核心调用；缺档位/未知档位都不应触达核心。
type spyMasker struct {
	maskCalls    int
	restoreCalls int
}

func (s *spyMasker) Mask(string, []byte, core.MaskOptions, *config.Config, *audit.Log) (*core.MaskOutput, error) {
	s.maskCalls++
	return nil, errors.New("spy: 核心不应被调用")
}

func (s *spyMasker) Restore(string, []byte, []byte, *config.Config, *audit.Log) (*core.RestoreOutput, error) {
	s.restoreCalls++
	return nil, errors.New("spy: 核心不应被调用")
}

// 缺档位立即拒绝：不进入业务、不创建任务、不读原件/词库/配置，也没有等待态或后台重试。
func TestMissingProfileHasNoSideEffects(t *testing.T) {
	// A. 损坏配置 + 缺档位：若进入业务会先报 invalid_config，因此能证明未读取配置/词库/原件。
	root := t.TempDir()
	if err := workspace.EnsureHeadless(root); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := agent.NewApp(root, cfgPath)
	spy := &spyMasker{}
	app.Core = spy
	if err := app.Open(); err != nil {
		t.Fatalf("打开会话失败：%v", err)
	}
	t.Cleanup(func() { _ = app.Close() })
	session := pairFor(t, app)

	before := snapshotTree(t, root)
	project := t.TempDir()
	text, structured := callToolEnvelope(t, session, agentmcp.ToolPrepare, map[string]any{
		"source_name":  "absent-fixture.txt",
		"project_path": project,
		"profile":      "",
	})
	assertFailedEnvelope(t, text, "invalid_request", msgMissingProfile)
	assertFailedEnvelope(t, structured, "invalid_request", msgMissingProfile)

	if spy.maskCalls != 0 || spy.restoreCalls != 0 {
		t.Fatalf("缺档位不得调用核心：mask=%d restore=%d", spy.maskCalls, spy.restoreCalls)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(before, after) {
		t.Fatalf("缺档位不得新增任务记录或业务产物：\n before=%v\n after=%v", before, after)
	}
	if _, err := os.Stat(filepath.Join(project, ".masked-input")); !os.IsNotExist(err) {
		t.Fatal("缺档位不得向项目交付任何内容")
	}

	// B. 默认配置 + 无 cards 目录 + 无原件：未知档位在业务读取前拒绝，不创建任务。
	unknownRoot := t.TempDir()
	if err := workspace.EnsureHeadless(unknownRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(unknownRoot, workspace.DirCards)); err != nil {
		t.Fatal(err)
	}
	unknownApp := agent.NewApp(unknownRoot, filepath.Join(t.TempDir(), "absent-config.json"))
	unknownSpy := &spyMasker{}
	unknownApp.Core = unknownSpy
	if err := unknownApp.Open(); err != nil {
		t.Fatalf("打开会话失败：%v", err)
	}
	t.Cleanup(func() { _ = unknownApp.Close() })
	unknownSession := pairFor(t, unknownApp)

	beforeUnknown := snapshotTree(t, unknownRoot)
	unknownProject := t.TempDir()
	_, structured = callToolEnvelope(t, unknownSession, agentmcp.ToolPrepare, map[string]any{
		"source_name":  "absent-fixture.txt",
		"project_path": unknownProject,
		"profile":      "no-such-profile-xyz",
	})
	assertFailedEnvelope(t, structured, "invalid_profile", msgInvalidProfile)
	if unknownSpy.maskCalls != 0 || unknownSpy.restoreCalls != 0 {
		t.Fatalf("未知档位不得调用核心：mask=%d restore=%d", unknownSpy.maskCalls, unknownSpy.restoreCalls)
	}
	if after := snapshotTree(t, unknownRoot); !reflect.DeepEqual(beforeUnknown, after) {
		t.Fatalf("未知档位不得新增任务记录：\n before=%v\n after=%v", beforeUnknown, after)
	}
	if _, err := os.Stat(filepath.Join(unknownProject, ".masked-input")); !os.IsNotExist(err) {
		t.Fatal("未知档位不得向项目交付任何内容")
	}
}

// pairFor 用给定 app 建立内存 MCP 会话。
func pairFor(t *testing.T, app *agent.App) *sdk.ClientSession {
	t.Helper()
	server := agentmcp.NewServer(app)
	serverT, clientT := sdk.NewInMemoryTransports()
	ctx := context.Background()
	go func() { _ = server.SDKServer().Run(ctx, serverT) }()
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	session, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("连接失败：%v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// newPairWithConfig 用给定配置内容构造已连接的内存 client/server；词库使用中性 fixture。
func newPairWithConfig(t *testing.T, cfgRaw string) (*sdk.ClientSession, string) {
	t.Helper()
	root := t.TempDir()
	if err := workspace.EnsureHeadless(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, workspace.DirCards, "fixture.txt"), []byte("fixture-card-word\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(cfgRaw), 0o600); err != nil {
		t.Fatal(err)
	}
	app := agent.NewApp(root, cfgPath)
	if err := app.Open(); err != nil {
		t.Fatalf("打开会话失败：%v", err)
	}
	t.Cleanup(func() { _ = app.Close() })
	return pairFor(t, app), root
}

// seedOriginalFile 写入中性测试原件。
func seedOriginalFile(t *testing.T, root, name string, data []byte) {
	t.Helper()
	dir := filepath.Join(root, workspace.DirOriginal)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// snapshotTree 返回工作区内的相对路径快照，用于断言没有新增任务记录或产物。
func snapshotTree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("遍历工作区失败：%v", err)
	}
	sort.Strings(out)
	return out
}

// 额外字段被 schema 拒绝。
func TestUnknownFieldRejected(t *testing.T) {
	session, _, _ := newPair(t)
	cases := []struct {
		tool string
		args map[string]any
	}{
		{agentmcp.ToolPrepare, map[string]any{"source_name": "a.txt", "project_path": "/tmp", "profile": "balanced", "force": true}},
		{agentmcp.ToolConfirm, map[string]any{"job_id": "x", "confirmation_id": "y", "decision": "cancel", "force": true}},
		{agentmcp.ToolRestore, map[string]any{"job_id": "x", "response_path": "/tmp/a.txt", "mapping": "raw"}},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: c.tool, Arguments: c.args})
			if err != nil {
				t.Fatalf("未知字段应由固定信封拒绝而非协议错误：%v", err)
			}
			raw, err := json.Marshal(res.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			if body["status"] != "failed" || body["code"] != "invalid_request" {
				t.Fatalf("未知字段应返回 failed/invalid_request：%v", body)
			}
			if strings.Contains(string(raw), "force") || strings.Contains(string(raw), "mapping") {
				t.Fatalf("错误输出不得回显额外字段名：%s", raw)
			}
		})
	}
}

// text content 与 structured content 语义一致。
func TestTextAndStructuredContentAgree(t *testing.T) {
	session, app, root := newPair(t)
	dir := filepath.Join(root, workspace.DirOriginal)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("13800138000"), 0o600); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name: agentmcp.ToolPrepare,
		Arguments: map[string]any{
			"source_name":  "notes.txt",
			"project_path": project,
			"profile":      "strict",
		},
	})
	if err != nil {
		t.Fatalf("调用失败：%v", err)
	}
	if len(res.Content) != 1 {
		t.Fatalf("应有一个 text content：%+v", res.Content)
	}
	text, ok := res.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("content 应为 text：%T", res.Content[0])
	}
	var fromText map[string]any
	if err := json.Unmarshal([]byte(text.Text), &fromText); err != nil {
		t.Fatalf("text 应为 JSON：%v", err)
	}
	var fromStructured map[string]any
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fromStructured); err != nil {
		t.Fatal(err)
	}
	if fromStructured["status"] != "delivered" || fromText["status"] != "delivered" {
		t.Fatalf("两者应语义一致：text=%v structured=%v", fromText["status"], fromStructured["status"])
	}
	if fromStructured["schema_version"] != float64(agent.SchemaVersion) {
		t.Fatalf("应带 schema_version=1：%v", fromStructured["schema_version"])
	}
	_ = app
}
