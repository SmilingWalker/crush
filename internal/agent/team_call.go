package agent

import (
	"context"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/actor"
)

// ToolSettableRunner is an OPTIONAL interface that TurnRunner implementations
// may support. It lets callers inject extra tools into the runner before turns
// begin. TurnRunnerAdapter implements it; mock TurnRunners in tests do not
// (and don't need to).
//
// Introduced in M4-06 for MemberRunner to inject team_report_status and
// team_send_message tools into the member's TurnRunner without breaking the
// TurnRunner interface or any existing mock implementations.
type ToolSettableRunner interface {
	AppendTools(tools []fantasy.AgentTool)
}

// TurnStatus 表示一轮 agent turn 的终态
type TurnStatus string

const (
	TurnCompleted TurnStatus = "completed" // 正常完成
	TurnQueued    TurnStatus = "queued"    // 已入队，尚未执行
	TurnCanceled  TurnStatus = "canceled"  // 被取消
	TurnFailed    TurnStatus = "failed"    // 执行失败

	// TurnRunning 不是终态，仅用于状态查询。
	// 仅由未来的 status-query API 产生，Run() 永远不会返回它（Run 返回即意味 turn 结束）。
	TurnRunning TurnStatus = "running"
)

// IsTerminal 判断是否为终态
func (s TurnStatus) IsTerminal() bool {
	return s == TurnCompleted || s == TurnCanceled || s == TurnFailed
}

// TeamAgentCall 是 team/delegate/member runtime 调用 agent 的描述符。
// 它与 SessionAgentCall 是独立类型——SessionAgentCall 被 SessionAgent.Run 直接消费，
// TeamAgentCall 用于 TurnRunner 接口，两个不能混淆。
type TeamAgentCall struct {
	SessionID       string
	ParentSessionID string
	PromptEnvelope  string             // 组装后的完整提示词
	Actor           actor.ActorContext // 执行者身份
	ToolPolicy      ToolPolicyProfile  // 工具策略
}

// ToolPolicyProfile 定义工具使用策略
type ToolPolicyProfile struct {
	AllowedTools    []string // 白名单（nil = 全部允许）
	DisallowedTools []string // 黑名单（额外禁止）
	PermissionMode  string   // default | acceptEdits | plan | bypassPermissions
}

// ReadOnlyDelegatePolicy 返回M2 delegate的只读工具策略。
// 仅允许 view/grep/glob/ls/sourcegraph 五个只读工具；破坏性工具
// (bash/write/edit/agent 等) 被禁止。
//
// 白名单语义为"仅允许这些"：coordinator 的工具过滤器
// (!slices.Contains(AllowedTools, name) → skip) 会让任何不在 AllowedTools
// 中的工具被跳过。DisallowedTools 是破坏性子集的 defense-in-depth——两层
// 都必须满足后 delegate 才会到达真实 LLM。生产 AgentFactory（尚未落地）
// 负责把该 ToolPolicyProfile 映射到构建 runner 的 config；在它落地前，本
// 函数仅定义策略字面量。
//
// 5 个 allowlist 条目和所有 disallow 条目均已对照
// internal/agent/tools/*.go 的真实 tool 注册验证（见 team_call_policy_test.go）。
func ReadOnlyDelegatePolicy() ToolPolicyProfile {
	return ToolPolicyProfile{
		AllowedTools: []string{"view", "grep", "glob", "ls", "sourcegraph"},
		DisallowedTools: []string{
			"agent", "ask_user_questions", "job_output", "job_kill",
			"todos", "crush_info", "crush_logs",
			"bash", "write", "edit", "multiedit", "download",
			"fetch", "agentic_fetch",
		},
		PermissionMode: "default",
	}
}

// TurnRunResult 是一轮 agent turn 的结果
// Status 为 queued 时 Result 为 nil（表示已入队但尚未执行）
type TurnRunResult struct {
	Status TurnStatus
	Result *fantasy.AgentResult
}

// TurnRunner 是 agent turn 的执行接口。
// M1 用于单个子Agent，M2 DelegateRunner 用于并行搜索，
// M4 MemberRunner 用于长期队员。三层共用同一接口。
type TurnRunner interface {
	// Run 执行一轮 agent turn
	Run(ctx context.Context, call TeamAgentCall) (TurnRunResult, error)

	// Cancel 取消指定 session 的当前 turn
	Cancel(sessionID string)

	// IsSessionBusy 检查 session 是否有活跃的 turn
	IsSessionBusy(sessionID string) bool
}

// AgentSpec 描述创建一个 TurnRunner 所需的参数
type AgentSpec struct {
	AgentType      string            // agent 类型标识 (如 "general-purpose")
	SystemPrompt   string            // 系统提示词
	ModelType      string            // 模型选择 ("large" | "small" | "inherit")
	PermissionMode string            // 权限模式
	ToolPolicy     ToolPolicyProfile // 工具策略

	// MaxTurns 是该 runner 的 turn 预算上限。零值 = 无限制（无 turn 预算），
	// 与 config.Agent.MaxTurns 语义一致。AgentSpec 是 BuildRunner 暴露的唯一 seam，
	// 在 M2 DelegateRunner 需要 turn 预算前预先承载该字段。
	MaxTurns int

	// MaxPromptBytes 是 prompt envelope 的字节上限。零值 = 默认 200KB。
	// M4-05 PromptBuilder 超过上限时按优先级截断低优先级 section。
	MaxPromptBytes int
}

// AgentFactory 创建独立 SessionAgent 实例的工厂。
// 每个 runner 持有独立实例——不复用 Coordinator.currentAgent，
// 确保 coder 的 SetTools/SetModels 不影响 delegate/member runner。
type AgentFactory interface {
	BuildRunner(ctx context.Context, spec AgentSpec) (TurnRunner, error)
}
