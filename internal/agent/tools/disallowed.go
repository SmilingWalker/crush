package tools

// SubAgentDisallowedTools is the list of tools NO sub-agent may ever use.
// buildTools enforces it as the final filter layer whenever isSubAgent is
// true. The main agent (coder, isSubAgent=false) is unaffected.
//
// Why each entry is here:
//   - agent:               prevents a sub-agent from recursively spawning more sub-agents
//   - ask_user_questions:  a sub-agent must not pop UI prompts at the user
//   - job_output:          a sub-agent must not read other parallel jobs' output
//   - job_kill:            a sub-agent must not terminate other jobs
//   - todos:               a sub-agent does not manage the task list
//   - crush_info:          a sub-agent does not need crush self-info
//   - crush_logs:          a sub-agent does not need access to logs
var SubAgentDisallowedTools = []string{
	"agent",
	"ask_user_questions",
	"job_output",
	"job_kill",
	"todos",
	"crush_info",
	"crush_logs",
}

// IsSubAgentDisallowed reports whether toolName is on the global sub-agent
// denylist.
func IsSubAgentDisallowed(toolName string) bool {
	for _, disallowed := range SubAgentDisallowedTools {
		if toolName == disallowed {
			return true
		}
	}
	return false
}
