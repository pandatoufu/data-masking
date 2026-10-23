package agent

// 公开状态全集（响应 status，与持久稳定状态区分）。
const (
	StatusAwaitingFrequency = "awaiting_frequency_confirmation"
	StatusAwaitingResidual  = "awaiting_residual_confirmation"
	StatusDelivered         = "delivered"
	StatusRestored          = "restored"
	StatusCancelled         = "cancelled"
	StatusFailed            = "failed"
)

// 确认种类。
const (
	KindFrequency = "frequency"
	KindResidual  = "residual"
)

// 允许动作。
const (
	DecisionContinueMasking = "continue_masking"
	DecisionDeliverResidual = "deliver_with_residual_risk"
	DecisionCancel          = "cancel"
)

// SchemaVersion 是公开响应 schema 版本。
const SchemaVersion = 1

// Warning 是公开告警：只含 kind、count 与固定 message。
type Warning struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Count   int    `json:"count"`
}

// Response 是公开响应白名单字段全集；字段按状态选用。
type Response struct {
	SchemaVersion    int       `json:"schema_version"`
	Status           string    `json:"status,omitempty"`
	JobID            string    `json:"job_id,omitempty"`
	Code             string    `json:"code,omitempty"`
	Message          string    `json:"message,omitempty"`
	Profile          string    `json:"profile,omitempty"`
	ProjectFile      string    `json:"project_file,omitempty"`
	ConfirmationID   string    `json:"confirmation_id,omitempty"`
	ConfirmationKind string    `json:"confirmation_kind,omitempty"`
	AllowedDecisions []string  `json:"allowed_decisions,omitempty"`
	WordCount        int       `json:"word_count,omitempty"`
	HitCounts        []int     `json:"hit_counts,omitempty"`
	Threshold        int       `json:"threshold,omitempty"`
	ResidualCount    int       `json:"residual_count,omitempty"`
	RestoredCount    int       `json:"restored_count,omitempty"`
	Warnings         []Warning `json:"warnings,omitempty"`
	AuditFailed      bool      `json:"audit_failed,omitempty"`
}

// 固定文案模板。
const (
	msgAwaitingFrequency = "有 {word_count} 个自定义词超过阈值 {threshold}，命中次数为 {hit_counts}，可能过度替换；尚未执行脱敏。请用户选择继续脱敏或取消调整本地词库。"
	msgAwaitingResidual  = "脱敏后本地复核仍有 {residual_count} 处命中启用规则，可能包含敏感内容；尚未复制到项目。请用户选择接受风险并交付或取消交付。该检测仅验证规则覆盖率，不构成语义无泄露保证。"
	msgDelivered         = "已交付脱敏副本到项目；本地保留与之配对的 mapping。"
	msgRestored          = "已在本地生成还原结果；不返回还原正文。"
	msgCancelled         = "已取消本次任务，未交付，也未删除原件或历史文件。"
)

// FailureResponse 把应用层失败映射为固定失败信封。
func FailureResponse(err error) Response {
	ae := AsError(err)
	return Response{
		SchemaVersion: SchemaVersion,
		Status:        StatusFailed,
		Code:          string(ae.Code),
		Message:       ae.Message,
	}
}
