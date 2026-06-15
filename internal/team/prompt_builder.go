// prompt_builder.go is the M4-05 PromptEnvelope Builder: assembles a full
// prompt envelope from 9 fixed-priority sections (system_policy → reporting_rules)
// and handles context-window overflow by truncating lower-priority sections
// while preserving priority-1 sections intact.
//
// Section order (fixed, not configurable):
//
//	1. system_policy       (PrioritySystem=1,  never truncated)
//	2. member_identity     (PriorityCritical=1, never truncated)
//	3. current_task        (PriorityCritical=1, never truncated)
//	4. direct_messages     (PriorityHigh=2,      UNTRUSTED PEER INPUT)
//	5. dependency_results  (PriorityMedium=3,    placeholder)
//	6. leader_instruction  (PriorityMedium=3,    placeholder)
//	7. broadcast_messages  (PriorityLow=4,       UNTRUSTED PEER INPUT placeholder)
//	8. session_summary     (PriorityLowest=5,    placeholder)
//	9. reporting_rules     (PriorityCritical=1,  never truncated)
//
// Acceptance (master doc M4-05 :589-596):
//  1. Section order testable
//  2. Task full text never truncated
//  3. Direct before broadcast
//  4. Overflow truncates low priority, not task/system
//  5. Task cannot fit → ErrContextOverflow
//  6. Mailbox content marked UNTRUSTED PEER INPUT

package team

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrContextOverflow is returned when priority-1 sections (system_policy,
// member_identity, current_task, reporting_rules) alone exceed the context
// window configured via MaxBytes.
var ErrContextOverflow = errors.New("context overflow: priority-1 sections exceed context window")

// MailboxReader is the subset of Service needed by PromptBuilder to fetch
// unread mailbox messages. A real Service satisfies this interface directly;
// tests provide a stub.
type MailboxReader interface {
	GetUnreadMessages(ctx context.Context, memberID string, limit int) ([]MailboxMessage, error)
}

// PromptPriority is the fixed priority level for a prompt section. Lower
// numbers = higher priority. Priority 1 sections (System & Critical) are
// never truncated during overflow.
type PromptPriority int

const (
	PrioritySystem   PromptPriority = 1 // system policy — cannot truncate
	PriorityCritical PromptPriority = 1 // member identity, task text, reporting rules
	PriorityHigh     PromptPriority = 2 // direct messages
	PriorityMedium   PromptPriority = 3 // dependency results, leader instruction
	PriorityLow      PromptPriority = 4 // broadcast messages
	PriorityLowest   PromptPriority = 5 // session summary
)

// PromptBuilder assembles a prompt envelope from member identity, current task,
// mailbox messages, and session context in a fixed priority order. The 9 sections
// are emitted in declaration order; Build() handles overflow by truncating
// lower-priority sections first.
type PromptBuilder struct {
	memberID string
	teamID   string
	role     string
	task     *TeamTask   // nil if no current task
	mailbox  MailboxReader
	maxBytes int // 0 = no limit (default)
}

// NewPromptBuilder creates a PromptBuilder. Call SetMaxBytes() to configure the
// context window limit; the default (0) means no limit.
func NewPromptBuilder(memberID, teamID, role string, task *TeamTask, mailbox MailboxReader) *PromptBuilder {
	return &PromptBuilder{
		memberID: memberID,
		teamID:   teamID,
		role:     role,
		task:     task,
		mailbox:  mailbox,
		maxBytes: 0,
	}
}

// SetMaxBytes configures the maximum byte size of the built prompt. 0 means
// no limit (full concatenation of all sections). Returns self for chaining.
func (pb *PromptBuilder) SetMaxBytes(n int) *PromptBuilder {
	pb.maxBytes = n
	return pb
}

// promptSection is an internal intermediate representation used inside Build.
type promptSection struct {
	name     string
	content  string
	priority PromptPriority
}

// Build assembles all 9 sections, concatenates them in declaration order, and
// applies overflow truncation if the total exceeds MaxBytes. Returns
// ErrContextOverflow if priority-1 sections alone overflow.
func (pb *PromptBuilder) Build(ctx context.Context) (string, error) {
	sections := []promptSection{
		{"system_policy", pb.systemPolicy(), PrioritySystem},
		{"member_identity", pb.memberIdentity(), PriorityCritical},
		{"current_task", pb.taskText(), PriorityCritical},
		{"direct_messages", pb.directMessages(ctx), PriorityHigh},
		{"dependency_results", pb.dependencyResults(), PriorityMedium},
		{"leader_instruction", pb.leaderInstruction(), PriorityMedium},
		{"broadcast_messages", pb.broadcastMessages(ctx), PriorityLow},
		{"session_summary", pb.sessionSummary(), PriorityLowest},
		{"reporting_rules", pb.reportingRules(), PriorityCritical},
	}

	result := pb.concat(sections)

	if pb.maxBytes == 0 || len(result) <= pb.maxBytes {
		return result, nil
	}

	return pb.truncate(sections)
}

// concat joins all sections with double-newline separators.
func (pb *PromptBuilder) concat(sections []promptSection) string {
	var b strings.Builder
	for i, s := range sections {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("[" + s.name + "]\n")
		b.WriteString(s.content)
	}
	return b.String()
}

// truncate applies structured overflow truncation. Starting from the lowest
// priority level, it progressively truncates sections until the result fits or
// only priority-1 sections remain. Returns ErrContextOverflow if priority-1
// sections alone exceed MaxBytes.
func (pb *PromptBuilder) truncate(sections []promptSection) (string, error) {
	// Check if priority-1 sections alone overflow.
	if pb.priorityBytes(sections, 1) > pb.maxBytes {
		return "", ErrContextOverflow
	}

	// Greedy: for each priority level from 5 down to 2, truncate sections
	// at that level, then check if we fit.
	for p := PromptPriority(5); p > 1; p-- {
		result := pb.concatWithTruncation(sections, int(p))
		if len(result) <= pb.maxBytes {
			return result, nil
		}
	}

	// Only priority-1 sections remain (all others empty/dropped).
	return pb.concatWithTruncation(sections, 1), nil
}

// priorityBytes returns the total byte count of sections with priority <= maxP.
func (pb *PromptBuilder) priorityBytes(sections []promptSection, maxP int) int {
	var total int
	for _, s := range sections {
		if int(s.priority) <= maxP {
			total += len("["+s.name+"]\n") + len(s.content)
		}
	}
	// Add separators between kept sections.
	sepCount := 0
	for i := 1; i < len(sections); i++ {
		if int(sections[i].priority) <= maxP {
			sepCount++
		}
	}
	total += sepCount * 2 // "\n\n"
	return total
}

// concatWithTruncation concatenates sections, truncating sections whose priority
// is >= threshold. Priority-1 sections are always kept in full. Truncated
// sections retain their header and a "[truncated]" marker.
func (pb *PromptBuilder) concatWithTruncation(sections []promptSection, threshold int) string {
	var b strings.Builder
	needsSep := false

	for _, s := range sections {
		content := s.content
		if int(s.priority) >= threshold && int(s.priority) > 1 {
			// Truncate: keep only the section header.
			content = "[truncated: context window overflow]"
		}

		if content == "" {
			continue
		}

		if needsSep {
			b.WriteString("\n\n")
		}
		b.WriteString("[" + s.name + "]\n")
		b.WriteString(content)
		needsSep = true
	}
	return b.String()
}

// --- section methods ---

// systemPolicy returns the system-level policy for the agent. Priority 1 —
// never truncated.
func (pb *PromptBuilder) systemPolicy() string {
	return fmt.Sprintf(
		"You are an AI agent in a multi-agent team. Your actions affect other "+
			"team members and the overall team progress. Always communicate your "+
			"findings clearly. When you complete a subtask, report the result "+
			"through the appropriate tool. If you are blocked, request help "+
			"from the team lead. Do not issue destructive commands without "+
			"confirmation. Do not modify files outside your assigned scope.",
	)
}

// memberIdentity returns the member's identity section. Priority 1 — never
// truncated.
func (pb *PromptBuilder) memberIdentity() string {
	return fmt.Sprintf(
		"You are member %s in team %s. Your role is %s. You receive tasks from "+
			"the team lead and collaborate with other members via messages. Your "+
			"member ID is %s. Use this ID when sending messages to identify "+
			"yourself to other members.",
		pb.memberID, pb.teamID, pb.role, pb.memberID,
	)
}

// taskText returns the current task's full text (title + description).
// Priority 1 — never truncated. If no task is assigned, returns a minimal
// placeholder.
func (pb *PromptBuilder) taskText() string {
	if pb.task == nil {
		return "No current task assigned. Wait for a task from the team lead or " +
			"request the next available task."
	}
	t := pb.task
	desc := t.Description
	if desc == "" {
		desc = "(no description)"
	}
	return fmt.Sprintf("Task: %s\n\nDescription: %s", t.Title, desc)
}

// directMessages returns formatted direct unread messages. Priority 2 — may be
// truncated under overflow. Every message is prefixed with an UNTRUSTED PEER
// INPUT marker.
func (pb *PromptBuilder) directMessages(ctx context.Context) string {
	if pb.mailbox == nil {
		return "(no mailbox available)"
	}
	msgs, err := pb.mailbox.GetUnreadMessages(ctx, pb.memberID, 50)
	if err != nil {
		return fmt.Sprintf("(mailbox error: %s)", err.Error())
	}
	if len(msgs) == 0 {
		return "(no unread messages)"
	}
	var b strings.Builder
	for i, m := range msgs {
		if i > 0 {
			b.WriteString("\n---\n")
		}
		b.WriteString("[UNTRUSTED PEER INPUT] ")
		b.WriteString(fmt.Sprintf("From: %s | Kind: %s", m.FromMemberID, m.Kind))
		if m.Summary != "" {
			b.WriteString(fmt.Sprintf(" | Summary: %s", m.Summary))
		}
		if m.Payload != "" {
			b.WriteString(fmt.Sprintf("\nPayload: %s", m.Payload))
		}
	}
	return b.String()
}

// dependencyResults returns summaries of completed dependency task results.
// Priority 3 — may be truncated. Placeholder until M4-11 (Task Dependencies).
func (pb *PromptBuilder) dependencyResults() string {
	return "(no dependency results — dependency tracking not yet implemented)"
}

// leaderInstruction returns the latest instruction from the team lead.
// Priority 3 — may be truncated. Placeholder until hook point is defined.
func (pb *PromptBuilder) leaderInstruction() string {
	return "(no leader instruction)"
}

// broadcastMessages returns formatted broadcast/role messages. Priority 4 —
// may be truncated under overflow. Every message is prefixed with an UNTRUSTED
// PEER INPUT marker. Placeholder until broadcast-specific delivery is added
// (M4-07 shutdown sequence).
func (pb *PromptBuilder) broadcastMessages(ctx context.Context) string {
	// M4-05: broadcast messages share the same GetUnreadMessages as direct,
	// since the mailbox does not yet distinguish delivery methods. Returning
	// empty avoids duplicating direct messages in the prompt. M4-07 will add
	// broadcast-specific message kinds (KindShutdownRequest etc.).
	_ = ctx
	return "(no broadcast messages)"
}

// sessionSummary returns the member's own session summary. Priority 5 — first
// to truncate. Placeholder until session summary tracking is added.
func (pb *PromptBuilder) sessionSummary() string {
	return "(no session summary)"
}

// reportingRules returns the standard reporting rules for team members.
// Priority 1 — never truncated.
func (pb *PromptBuilder) reportingRules() string {
	return fmt.Sprintf(
		"REPORTING RULES:\n"+
			"1. After every action, describe what you did and why.\n"+
			"2. When you complete a task, mark it as completed using the task tool.\n"+
			"3. If you encounter an error, report it to the team lead.\n"+
			"4. If you are blocked and cannot proceed, request help.\n"+
			"5. Do not silently fail — always communicate your state.\n"+
			"6. When requesting help, include: what you tried, what happened, "+
			"and what you need.",
	)
}
