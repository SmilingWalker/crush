package tools

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/questions"
)

const askUserQuestionsToolName = "ask_user_questions"

//go:embed ask_user_questions.md
var askUserQuestionsDescription string

// AskUserQuestionsParams holds the parameters for the ask_user_questions tool.
type AskUserQuestionsParams struct {
	Questions []questions.Question `json:"questions" description:"Array of 1-4 questions to ask the user"`
}

// NewAskUserQuestionsTool creates a tool that asks the user multiple-choice questions.
func NewAskUserQuestionsTool(questionsService questions.Service) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		askUserQuestionsToolName,
		askUserQuestionsDescription,
		func(ctx context.Context, params AskUserQuestionsParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			// Validate input
			if err := questions.ValidateQuestions(params.Questions); err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			sessionID := GetSessionFromContext(ctx)
			req := questions.NewQuestionsRequest(sessionID, call.ID, params.Questions)

			resp, err := questionsService.Ask(ctx, req)
			if err != nil {
				return fantasy.ToolResponse{}, err
			}

			if resp.Rejected {
				return fantasy.NewTextResponse(
					"User declined to answer your questions. Continue without this information.",
				), nil
			}

			return formatAnswersResponse(resp), nil
		},
	)
}

// formatAnswersResponse formats the answers into a text response for the LLM.
func formatAnswersResponse(resp questions.QuestionsResponse) fantasy.ToolResponse {
	parts := make([]string, 0, len(resp.Answers))
	for _, ans := range resp.Answers {
		selected := ans.Selected
		if ans.IsOther && selected != "" {
			selected = fmt.Sprintf("%s (user input)", selected)
		}
		parts = append(parts, fmt.Sprintf("%q=%q", ans.QuestionText, selected))
	}
	msg := fmt.Sprintf("User answered your questions: %s. You can continue with the user's answers in mind.",
		strings.Join(parts, ", "))
	return fantasy.NewTextResponse(msg)
}
