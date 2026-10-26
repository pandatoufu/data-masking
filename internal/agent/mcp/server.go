// Package mcp 是 headless 的 stdio MCP 协议适配层：只注册三个工具、拒绝未知字段、
// 用同一净化 DTO 生成 text 与 structured content，并把所有错误与 stderr 收敛为固定白名单。
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pandatoufu/data-masking/internal/agent"
	"github.com/pandatoufu/data-masking/internal/buildinfo"
)

// 工具名全集。
const (
	ToolPrepare = "prepare_document"
	ToolConfirm = "confirm_operation"
	ToolRestore = "restore_document"
)

// ServerName 是 MCP initialize 的 serverInfo.name。
const ServerName = "data-masking-agent"

// securityGuidance 是审定固定的安全段：initialize instructions 与三个工具 description 共用，
// 只含公开固定文本与字面目录名，不拼入工作区/项目路径、源名、配置内容、候选名、词值、
// 正文、mapping 或私有指纹，也不放宽既有业务响应白名单。
const securityGuidance = "安全边界（合作式软约束，不是 OS 沙箱，也不限制客户端其他工具的权限）：" +
	"不得用文件读取、搜索、终端或其他工具读取、预览或上传 original/ 原件、cards/ 词库、mapping/ 配对、restored/ 还原正文或 .agent-state/ 私有任务记录；这些内容只由本地程序处理。" +
	"不要为发现档位或排错打开本地 config.json、词库或其他配置内容，也不要提交词值或正文；词库或配置出错时请用户在本机检查。" +
	"未交付前不要读取私有脱敏产物或猜测其路径；准备中、等待确认或失败时不得自行批准风险确认。" +
	"只有在 delivered 之后可以读取返回的 project_file 脱敏副本，它不等于原件或 mapping，也不代表整包或语义无泄露；可以在项目内正常读写回复并把 response_path 交给 restore_document 本地还原，还原后只使用返回的匿名状态，不要读取 restored/ 正文。" +
	"用户没有明确档位时先询问用户再调用，已明确时不重复询问，不要猜测档位，也不要默认 balanced。"

// instructions 是 initialize 返回的固定说明：工具集合与安全指引均为审定静态文本。
const instructions = "本服务只提供 prepare_document、confirm_operation、restore_document 三个工具：" +
	"在本地脱敏 original/ 中的单个文件，并把获准交付的脱敏副本送入用户项目。" +
	"支持纯文本、DOCX 与普通 PPTX；PPTX 采用路径 A（PPTX 原件 → 脱敏 PPTX → 项目内模型编辑 → 本地还原 PPTX），" +
	"只处理原生标题/正文/文本框/组合形状/表格/隐藏页/版式/母版/备注文字，批注及相关人员信息按支持范围清除，备注保留。" +
	"图表、SmartArt、嵌入对象、图片文字、属性与链接目标不检查，刷新可能重新引入未处理值。" +
	"客户端自身必须具备编辑 PPTX 并保留占位符的能力；不支持时保持未验证，不要用纯文本回复冒充 PPTX 回程。" +
	securityGuidance

// 固定工具描述：要求 agent 转问用户、披露规则边界，且不引导读取原文或词库。
const (
	descPrepare = "在本地脱敏 original/ 中的一个文件，并把获准交付的脱敏副本送入项目。" +
		"source_name 只能是 original/ 下的单个文件名；project_path 必须是用户明确指定的现有绝对目录；profile 必须是本机配置中存在的档位。" +
		"内置档位为 strict/balanced/minimal/custom，其中 custom 是现有显示别名；本机配置可覆盖内置项或自建档位，因此内置名称不是完整有效列表，实际本机配置优先。" +
		"自定义词只来自程序目录 cards/ 直属 TXT（每行一词）；custom 启用时必须已有有效 TXT，关闭时不读取词库；不接受调用方提交敏感词或原文。" +
		"balanced 不处理 email/IP/身份证号；规则匹配不构成语义级无泄露保证。" +
		"PPTX 仅支持普通 .pptx 与同格式 PPTX 回复；宏、模板/放映格式、加密或签名文稿会被拒绝。" +
		"若返回确认态，必须询问用户并在得到明确答复后调用 confirm_operation；不得自行批准。" +
		securityGuidance
	descConfirm = "提交用户对当前任务的确认决定。" +
		"decision 仅接受 continue_masking / deliver_with_residual_risk / cancel，且必须与返回的 confirmation_kind 匹配；" +
		"两类确认授权不互通。文件、规则、cards 词库或项目变化会使旧确认失效。" +
		"必须转问用户后调用，不得自行批准或复用其他任务的凭据。" +
		securityGuidance
	descRestore = "把项目内的一份回复文件在本地用该任务配对的 mapping 还原。" +
		"response_path 必须是该任务 project_path 子树内的常规文件；结果只保存在本机，不返回还原正文。" +
		"回程只使用任务记录的 mapping，不依赖当前 cards 词库；每个任务只接受一次成功回程。" +
		"DOCX 任务按既有跨格式边界处理；PPTX 任务只接受同格式 PPTX 回复，PPTX mapping 不能还原文本/DOCX，文本/DOCX mapping 也不能还原 PPTX。" +
		securityGuidance
)

// PrepareInput 是 prepare_document 的严格入参。
type PrepareInput struct {
	SourceName  string `json:"source_name"`
	ProjectPath string `json:"project_path"`
	Profile     string `json:"profile"`
}

// ConfirmInput 是 confirm_operation 的严格入参。
type ConfirmInput struct {
	JobID          string `json:"job_id"`
	ConfirmationID string `json:"confirmation_id"`
	Decision       string `json:"decision"`
}

// RestoreInput 是 restore_document 的严格入参。
type RestoreInput struct {
	JobID        string `json:"job_id"`
	ResponsePath string `json:"response_path"`
}

// 各工具公开入参 schema：字段与必填项固定，additionalProperties=false。
var (
	schemaPrepare = json.RawMessage(`{
  "type": "object",
  "properties": {
    "source_name": {"type": "string", "description": "original 目录下的单个文件名，例如 report.docx 或 slides.pptx"},
    "project_path": {"type": "string", "description": "用户指定的现有绝对项目目录"},
    "profile": {"type": "string", "description": "本机配置中有效的档位名，例如内置 strict/balanced/minimal/custom（custom 可能映射到本机自建项）。用户未明确档位时必须先询问用户并按用户的明确选择填写；已明确时不重复询问；不要猜测档位，也不要默认 balanced；不要为确认档位而读取 config.json。"}
  },
  "required": ["source_name", "project_path", "profile"],
  "additionalProperties": false
}`)
	schemaConfirm = json.RawMessage(`{
  "type": "object",
  "properties": {
    "job_id": {"type": "string", "description": "prepare_document 返回的任务标识"},
    "confirmation_id": {"type": "string", "description": "确认态返回的凭据"},
    "decision": {"type": "string", "enum": ["continue_masking", "deliver_with_residual_risk", "cancel"], "description": "仅与当前确认类型匹配的动作"}
  },
  "required": ["job_id", "confirmation_id", "decision"],
  "additionalProperties": false
}`)
	schemaRestore = json.RawMessage(`{
  "type": "object",
  "properties": {
    "job_id": {"type": "string", "description": "prepare_document 返回的任务标识"},
    "response_path": {"type": "string", "description": "该项目子树内的回复文件绝对路径"}
  },
  "required": ["job_id", "response_path"],
  "additionalProperties": false
}`)
)

// schemaOutput 是三个工具共用的公开响应 schema：字段来自净化 DTO 白名单，
// warnings.kind/message 明确枚举审定全集，使客户端可见的 schema/枚举含独立 text_mismatch。
// SDK 的 AddTool 只声明 schema（不校验结果），输出仍由 toolResult 用同一净化 DTO 生成。
var schemaOutput = buildOutputSchema()

// buildOutputSchema 以 agent 公开枚举构建输出 schema，避免与白名单漂移。
func buildOutputSchema() json.RawMessage {
	catalog := agent.WarningCatalog()
	kinds := make([]string, 0, len(catalog))
	messages := make([]string, 0, len(catalog))
	for kind, msg := range catalog {
		kinds = append(kinds, kind)
		messages = append(messages, msg)
	}
	sort.Strings(kinds)
	sort.Strings(messages)
	doc := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"schema_version": map[string]any{"type": "integer"},
			"status": map[string]any{
				"type": "string",
				"enum": []string{
					agent.StatusAwaitingFrequency, agent.StatusAwaitingResidual,
					agent.StatusDelivered, agent.StatusRestored, agent.StatusCancelled, agent.StatusFailed,
				},
			},
			"job_id":            map[string]any{"type": "string"},
			"code":              map[string]any{"type": "string"},
			"message":           map[string]any{"type": "string"},
			"profile":           map[string]any{"type": "string"},
			"project_file":      map[string]any{"type": "string"},
			"confirmation_id":   map[string]any{"type": "string"},
			"confirmation_kind": map[string]any{"type": "string", "enum": []string{agent.KindFrequency, agent.KindResidual}},
			"allowed_decisions": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
					"enum": []string{agent.DecisionContinueMasking, agent.DecisionDeliverResidual, agent.DecisionCancel},
				},
			},
			"word_count":     map[string]any{"type": "integer"},
			"hit_counts":     map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
			"threshold":      map[string]any{"type": "integer"},
			"residual_count": map[string]any{"type": "integer"},
			"restored_count": map[string]any{"type": "integer"},
			"warnings": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"kind":    map[string]any{"type": "string", "enum": kinds},
						"message": map[string]any{"type": "string", "enum": messages},
						"count":   map[string]any{"type": "integer"},
					},
					"required":             []string{"kind", "count"},
					"additionalProperties": false,
				},
			},
			"audit_failed": map[string]any{"type": "boolean"},
		},
		"additionalProperties": false,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		panic("mcp: 无法构建输出 schema: " + err.Error())
	}
	return raw
}

// Server 组装 MCP server 与应用层。
type Server struct {
	app *agent.App
	sdk *sdk.Server
}

// NewServer 注册三个工具并返回适配层。
func NewServer(app *agent.App) *Server {
	s := &Server{app: app}
	sdkServer := sdk.NewServer(&sdk.Implementation{
		Name:    ServerName,
		Version: buildinfo.Effective(),
	}, &sdk.ServerOptions{Instructions: instructions})

	sdkServer.AddTool(&sdk.Tool{Name: ToolPrepare, Description: descPrepare, InputSchema: schemaPrepare, OutputSchema: schemaOutput},
		s.rawHandler(ToolPrepare, func(raw json.RawMessage) agent.Response {
			var in PrepareInput
			if err := strictDecode(raw, &in); err != nil {
				return invalidRequest()
			}
			// 顺序固定：先严格解码/类型/未知字段，再其他必填项，最后缺档位专属提示。
			// profile 缺失、空字符串或 null（string 解码为零值）都不进入业务处理。
			if in.SourceName == "" || in.ProjectPath == "" {
				return invalidRequest()
			}
			if in.Profile == "" {
				return missingProfile()
			}
			return app.Prepare(agent.PrepareRequest{
				SourceName:  in.SourceName,
				ProjectPath: in.ProjectPath,
				Profile:     in.Profile,
			})
		}))

	sdkServer.AddTool(&sdk.Tool{Name: ToolConfirm, Description: descConfirm, InputSchema: schemaConfirm, OutputSchema: schemaOutput},
		s.rawHandler(ToolConfirm, func(raw json.RawMessage) agent.Response {
			var in ConfirmInput
			if err := strictDecode(raw, &in); err != nil {
				return invalidRequest()
			}
			if in.JobID == "" || in.ConfirmationID == "" || in.Decision == "" {
				return invalidRequest()
			}
			switch in.Decision {
			case agent.DecisionContinueMasking, agent.DecisionDeliverResidual, agent.DecisionCancel:
			default:
				return invalidRequest()
			}
			return app.Confirm(agent.ConfirmRequest{
				JobID:          in.JobID,
				ConfirmationID: in.ConfirmationID,
				Decision:       in.Decision,
			})
		}))

	sdkServer.AddTool(&sdk.Tool{Name: ToolRestore, Description: descRestore, InputSchema: schemaRestore, OutputSchema: schemaOutput},
		s.rawHandler(ToolRestore, func(raw json.RawMessage) agent.Response {
			var in RestoreInput
			if err := strictDecode(raw, &in); err != nil {
				return invalidRequest()
			}
			if in.JobID == "" || in.ResponsePath == "" {
				return invalidRequest()
			}
			return app.Restore(agent.RestoreRequest{
				JobID:        in.JobID,
				ResponsePath: in.ResponsePath,
			})
		}))

	s.sdk = sdkServer
	return s
}

// rawHandler 统一处理参数解码与输出净化：任何解码/协议错误都映射为固定失败信封，
// 不把 SDK/JSON 原始错误交给客户端。
func (s *Server) rawHandler(name string, run func(json.RawMessage) agent.Response) sdk.ToolHandler {
	return func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		_ = name
		var raw json.RawMessage
		if req != nil && req.Params != nil && len(req.Params.Arguments) > 0 {
			raw = req.Params.Arguments
		}
		resp := safeRun(func() agent.Response { return run(raw) })
		return toolResult(resp), nil
	}
}

// safeRun 兜底 panic 与未知错误：只返回固定 internal_error 信封。
func safeRun(fn func() agent.Response) (resp agent.Response) {
	defer func() {
		if r := recover(); r != nil {
			resp = agent.FailureResponse(agent.NewError(agent.CodeInternalError, agent.MessageFor(agent.CodeInternalError)))
		}
	}()
	return fn()
}

// strictDecode 以 DisallowUnknownFields 解码；未知字段、类型错误与多余数据都拒绝。
func strictDecode(raw json.RawMessage, v any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return errors.New("mcp: empty arguments")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("mcp: trailing data")
	}
	return nil
}

// invalidRequest 返回固定 invalid_request 失败信封。
func invalidRequest() agent.Response {
	return agent.FailureResponse(agent.NewError(agent.CodeInvalidRequest, agent.MessageFor(agent.CodeInvalidRequest)))
}

// missingProfile 返回缺档位专属固定提示：仍是 invalid_request，经同一 FailureResponse
// 白名单重建路径发布，不改 code、status 或 IsError 语义，也不回显输入。
func missingProfile() agent.Response {
	return agent.FailureResponse(agent.NewError(agent.CodeInvalidRequest, agent.MessageMissingProfile))
}

// SDKServer 返回底层 SDK server（供 Run/测试）。
func (s *Server) SDKServer() *sdk.Server { return s.sdk }

// Run 以 stdio 运行；stdout 仅承载 MCP 消息。
func (s *Server) Run(ctx context.Context) error {
	return s.sdk.Run(ctx, &sdk.StdioTransport{})
}

// toolResult 用同一净化 DTO 生成 text 与 structured content，并显式设置 schema 版本。
func toolResult(resp agent.Response) *sdk.CallToolResult {
	if resp.SchemaVersion == 0 {
		resp.SchemaVersion = agent.SchemaVersion
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		fail := agent.FailureResponse(agent.NewError(agent.CodeInternalError, agent.MessageFor(agent.CodeInternalError)))
		raw, _ = json.Marshal(fail)
	}
	// 防御：结构化输出必须来自白名单 DTO；发现越界字段时退化为固定失败信封。
	if leaked := containsSensitiveShape(raw); leaked {
		fail := agent.FailureResponse(agent.NewError(agent.CodeInternalError, agent.MessageFor(agent.CodeInternalError)))
		raw, _ = json.Marshal(fail)
	}
	return &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: string(raw)}},
		StructuredContent: json.RawMessage(raw),
	}
}

// containsSensitiveShape 检查序列化结果是否含非白名单顶层字段（防御性断言）。
func containsSensitiveShape(raw []byte) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return true
	}
	allowed := map[string]bool{
		"schema_version": true, "status": true, "job_id": true, "code": true, "message": true,
		"profile": true, "project_file": true, "confirmation_id": true, "confirmation_kind": true,
		"allowed_decisions": true, "word_count": true, "hit_counts": true, "threshold": true,
		"residual_count": true, "restored_count": true, "warnings": true, "audit_failed": true,
	}
	for key := range probe {
		if !allowed[key] {
			return true
		}
	}
	// 告警条目只允许 kind/count/message。
	if warnRaw, ok := probe["warnings"]; ok {
		var list []map[string]json.RawMessage
		if err := json.Unmarshal(warnRaw, &list); err != nil {
			return true
		}
		for _, item := range list {
			for key := range item {
				if key != "kind" && key != "count" && key != "message" {
					return true
				}
			}
		}
	}
	return strings.Contains(string(raw), "\u0000")
}
