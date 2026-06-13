// Package actor 提供统一的Actor身份标识，供 tools、hooks、permission、team runtime 共享。
// 不依赖 internal/team，避免底层工具反向依赖 team domain。
package actor

import "context"

// ActorContext 表示当前操作的执行者身份。
// M1 使用 SessionID/ParentSessionID/MessageID/ToolCallID 四个核心字段。
// M2+ 逐步填充 TeamID/MemberID/TaskID/RunID 等团队相关字段。
type ActorContext struct {
	// --- M1 字段 ---
	SessionID       string `json:"session_id"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
	MessageID       string `json:"message_id,omitempty"`
	ToolCallID      string `json:"tool_call_id,omitempty"`
	WorkspaceID     string `json:"workspace_id,omitempty"`

	// --- M2+ 预留字段（M1 定义，M2+ 填充）---
	TeamID     string `json:"team_id,omitempty"`
	MemberID   string `json:"member_id,omitempty"`
	MemberName string `json:"member_name,omitempty"`
	MemberRole string `json:"member_role,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
	RunID      string `json:"run_id,omitempty"`
}

type actorContextKey struct{}

// WithContext 将 ActorContext 注入 context
func (a ActorContext) WithContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, actorContextKey{}, a)
}

// FromContext 从 context 提取 ActorContext，不存在时返回 false
func FromContext(ctx context.Context) (ActorContext, bool) {
	v, ok := ctx.Value(actorContextKey{}).(ActorContext)
	return v, ok
}

// ShortID 返回截断的 SessionID（前8字符），用于日志
func (a ActorContext) ShortID() string {
	if len(a.SessionID) > 8 {
		return a.SessionID[:8]
	}
	return a.SessionID
}

// IsSubAgent 判断是否为子Agent（存在 ParentSessionID）
func (a ActorContext) IsSubAgent() bool {
	return a.ParentSessionID != ""
}

// IsTeamMember 判断是否为团队成员（M2+ 使用）
func (a ActorContext) IsTeamMember() bool {
	return a.TeamID != "" && a.MemberID != ""
}
