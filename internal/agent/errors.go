// Package agent 承载 headless MCP 入口的应用层：任务用例、文件门禁、持久任务 store、
// 两类确认与独立公开 DTO。它只依赖 core/config/workspace/audit，不加载 desktop、server 或 Wails。
package agent

import "errors"

// Code 是失败代码全集中的稳定取值。interrupted 仅作为失败代码，不新增状态。
type Code string

const (
	CodeInvalidRequest   Code = "invalid_request"
	CodeInvalidSource    Code = "invalid_source"
	CodeInvalidProject   Code = "invalid_project"
	CodeInvalidProfile   Code = "invalid_profile"
	CodeInvalidConfig    Code = "invalid_config"
	CodeUnsupportedInput Code = "unsupported_input"
	CodeInputTooLarge    Code = "input_too_large"
	CodeProcessingFailed Code = "processing_failed"
	CodeWorkspaceUnavail Code = "workspace_unavailable"
	CodePublishFailed    Code = "publish_failed"
	CodeUnknownJob       Code = "unknown_job"
	CodeInvalidConfirm   Code = "invalid_confirmation"
	CodeConfirmStale     Code = "confirmation_stale"
	CodeInvalidState     Code = "invalid_state"
	CodeBusy             Code = "busy"
	CodeInterrupted      Code = "interrupted"
	CodeInternalError    Code = "internal_error"
)

// Error 是应用层的稳定失败：只携带 code 与固定消息模板 id，不携带任何自由错误字符串、
// 源名、私有路径或核心 DTO，便于 MCP 适配层按白名单重建输出。
type Error struct {
	Code    Code
	Message string
}

func (e *Error) Error() string {
	if e == nil {
		return string(CodeInternalError)
	}
	return string(e.Code)
}

// NewError 构造固定消息的失败。
func NewError(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// MessageMissingProfile 是缺档位时的专属固定提示（仍属 invalid_request）。
// 用于严格解码成功、其他必填项齐全但 profile 缺失/空/null 的立即拒绝，
// 不进入业务处理、不创建任务、不读取原件或词库。
const MessageMissingProfile = "缺少脱敏档位 profile。请先询问用户选择本机有效档位，明确选择后再调用；不得自行选择档位或原样重复调用。"

// fixedMessages 是各失败代码的固定可读指引（不含任何调用方输入）。
var fixedMessages = map[Code]string{
	CodeInvalidRequest:   "请求参数无效，请按工具说明提供必填字段。",
	CodeInvalidSource:    "原件名无效：只允许 original/ 下的单个文件名，不能包含路径、分隔符或特殊设备名。",
	CodeInvalidProject:   "项目路径无效：必须是现有的绝对目录，且不能包含软件工作区。",
	CodeInvalidProfile:   "profile 无效。请先询问用户确认本机配置中有效的档位名，明确选择后再调用；不得自行选择档位或原样重复调用。",
	CodeInvalidConfig:    "本地配置存在但无效或不可读，已拒绝本次操作以避免改变策略。",
	CodeUnsupportedInput: "不支持的文件格式或内容；本工具只处理纯文本（UTF-8/GBK）与 DOCX。",
	CodeInputTooLarge:    "输入超过既定大小上限，已拒绝。",
	CodeProcessingFailed: "处理失败，未生成交付产物。",
	CodeWorkspaceUnavail: "工作区不可用：程序目录的固定目录缺失、被占位或不可写。",
	CodePublishFailed:    "发布失败：目标不支持安全的无覆盖发布，未交付。",
	CodeUnknownJob:       "未知任务标识。",
	CodeInvalidConfirm:   "确认无效：凭据、类型或任务不匹配，未产生副作用。",
	CodeConfirmStale:     "确认已过期：原件、规则、词库或项目发生变化，请重新 prepare。",
	CodeInvalidState:     "当前任务状态不允许该操作。",
	CodeBusy:             "资源正忙，请稍后重试；本次调用未改变任务状态。",
	CodeInterrupted:      "任务因中断未完成。",
	CodeInternalError:    "内部错误，请重试或查看本地诊断。",
}

// MessageFor 返回失败代码的固定消息模板。
func MessageFor(code Code) string {
	if msg, ok := fixedMessages[code]; ok {
		return msg
	}
	return fixedMessages[CodeInternalError]
}

// AsError 提取 *Error；未知错误映射 internal_error，绝不回显原异常。
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var ae *Error
	if errors.As(err, &ae) {
		return ae
	}
	return NewError(CodeInternalError, MessageFor(CodeInternalError))
}
