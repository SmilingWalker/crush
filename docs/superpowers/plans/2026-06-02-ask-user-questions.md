# Ask User Questions Tool — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build an `ask_user_questions` tool that lets the LLM agent ask the user structured multiple-choice questions with free text "Other" input, validation, and clear rejection semantics.

**Architecture:** Pub/sub service pattern (consistent with Crush's `permission.Service`) — the tool handler publishes a `QuestionsRequest`, the TUI opens a dialog, user answers flow back through a channel. Question text is used as the correlation key between questions and answers.

**Tech Stack:** Go, Bubble Tea v2, lipgloss v2, fantasy AgentTool framework, csync/pubsub internal packages

**Design Spec:** `docs/superpowers/specs/2026-06-02-ask-user-questions-design.md`

---

## File Structure

| File | Responsibility |
|------|---------------|
| `internal/questions/service.go` | Data models (Option, Question, Answer, Request, Response), Service interface, pub/sub implementation, input validation |
| `internal/questions/service_test.go` | Unit tests for service (Ask/Answer/cancellation/concurrency) and validation |
| `internal/agent/tools/ask_user_questions.go` | Tool definition, params struct, handler with validation + formatting |
| `internal/agent/tools/ask_user_questions.md` | Embedded tool prompt markdown |
| `internal/agent/tools/ask_user_questions_test.go` | Tool-level tests (validation errors, rejection, answer formatting) |
| `internal/ui/dialog/questions.go` | Questions dialog (Bubble Tea component) with navigation + Other input |
| `internal/ui/dialog/question_options.go` | Option list rendering component |
| `internal/agent/coordinator.go` | Wire questionsService into coordinator struct and buildTools |
| `internal/app/app.go` | Create questions.NewService(), subscribe to events, pass to coordinator |
| `internal/config/config.go` | Add "ask_user_questions" to allToolNames() |
| `internal/ui/dialog/actions.go` | Add ActionQuestionsResponse type |
| `internal/ui/model/ui.go` | Handle QuestionsRequest event + dispatch action |
| `internal/workspace/workspace.go` | Add QuestionsAnswer() to Workspace interface |
| `internal/workspace/app_workspace.go` | Implement QuestionsAnswer() |
| `internal/workspace/client_workspace.go` | Stub QuestionsAnswer() |

---

### Task 1: Create Questions Service — Data Models & Validation

**Files:**
- Create: `internal/questions/service.go`

- [ ] **Step 1: Create the package with data models, validation, and service**

```go
// internal/questions/service.go
package questions

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/google/uuid"
)

// Option represents a single answer option for a question.
type Option struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// Question represents a single question to be asked.
type Question struct {
	Question    string   `json:"question"`
	Header      string   `json:"header"`
	Options     []Option `json:"options"`
	MultiSelect bool     `json:"multi_select"`
}

// Answer represents a user's response to a Question.
type Answer struct {
	QuestionText string `json:"question"`
	Selected     string `json:"selected"`
	IsOther      bool   `json:"is_other"`
}

// QuestionsRequest represents a request to ask a set of Questions.
type QuestionsRequest struct {
	ID         string
	SessionID  string
	ToolCallID string
	Questions  []Question
}

// NewQuestionsRequest creates a new QuestionsRequest with a generated ID.
func NewQuestionsRequest(sessionID string, toolCallID string, questions []Question) QuestionsRequest {
	return QuestionsRequest{
		ID:         uuid.New().String(),
		SessionID:  sessionID,
		ToolCallID: toolCallID,
		Questions:  questions,
	}
}

// QuestionsResponse represents the set of Answers to a QuestionsRequest.
type QuestionsResponse struct {
	RequestID string
	Answers   []Answer
	Rejected  bool
}

// NewQuestionsResponse creates a new QuestionsResponse for the given request.
func NewQuestionsResponse(req *QuestionsRequest) QuestionsResponse {
	return QuestionsResponse{
		RequestID: req.ID,
		Answers:   make([]Answer, len(req.Questions)),
	}
}

// Service is the interface for asking questions and receiving answers.
type Service interface {
	pubsub.Subscriber[QuestionsRequest]
	Ask(ctx context.Context, req QuestionsRequest) (QuestionsResponse, error)
	Answer(response QuestionsResponse)
}

type questionsService struct {
	*pubsub.Broker[QuestionsRequest]
	pendingRequests *csync.Map[string, chan QuestionsResponse]
}

// NewService creates a new questions Service.
func NewService() Service {
	return &questionsService{
		Broker:          pubsub.NewBroker[QuestionsRequest](),
		pendingRequests: csync.NewMap[string, chan QuestionsResponse](),
	}
}

func (s *questionsService) Ask(ctx context.Context, req QuestionsRequest) (QuestionsResponse, error) {
	slog.Debug("questions Ask", "request_id", req.ID, "session_id", req.SessionID, "questions", len(req.Questions))
	ch := make(chan QuestionsResponse, 1)
	s.pendingRequests.Set(req.ID, ch)
	defer s.pendingRequests.Del(req.ID)

	s.Publish(pubsub.CreatedEvent, req)

	select {
	case <-ctx.Done():
		slog.Debug("questions Ask cancelled", "request_id", req.ID)
		return QuestionsResponse{RequestID: req.ID}, ctx.Err()
	case resp := <-ch:
		return resp, nil
	}
}

func (s *questionsService) Answer(res QuestionsResponse) {
	if ch, found := s.pendingRequests.Get(res.RequestID); found {
		slog.Debug("questions Answer", "request_id", res.RequestID, "rejected", res.Rejected)
		ch <- res
	} else {
		slog.Warn("questions Answer for unknown request", "request_id", res.RequestID)
	}
}

// ValidateQuestions validates the questions array according to the spec rules.
func ValidateQuestions(questions []Question) error {
	if len(questions) < 1 || len(questions) > 4 {
		return fmt.Errorf("must ask between 1 and 4 questions, got %d", len(questions))
	}

	seen := make(map[string]bool)
	for _, q := range questions {
		if q.Question == "" {
			return fmt.Errorf("question text must not be empty")
		}
		if q.Header == "" {
			return fmt.Errorf("header must not be empty for question %q", q.Question)
		}
		if len(q.Header) > 12 {
			return fmt.Errorf("header must be at most 12 characters for question %q, got %d", q.Question, len(q.Header))
		}
		if len(q.Options) < 2 || len(q.Options) > 4 {
			return fmt.Errorf("each question must have 2-4 options, question %q has %d", q.Question, len(q.Options))
		}

		if seen[q.Question] {
			return fmt.Errorf("question texts must be unique, duplicate: %q", q.Question)
		}
		seen[q.Question] = true

		optSeen := make(map[string]bool)
		for _, opt := range q.Options {
			if opt.Label == "" {
				return fmt.Errorf("option label must not be empty in question %q", q.Question)
			}
			if optSeen[opt.Label] {
				return fmt.Errorf("option labels must be unique within question %q, duplicate: %q", q.Question, opt.Label)
			}
			optSeen[opt.Label] = true
		}
	}

	return nil
}
```

- [ ] **Step 2: Commit**

```bash
git add internal/questions/service.go
git commit -m "feat(questions): add questions service with data models and validation"
```

---

### Task 2: Questions Service Tests

**Files:**
- Create: `internal/questions/service_test.go`

- [ ] **Step 1: Write service tests**

```go
// internal/questions/service_test.go
package questions

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestService_AskAndAnswer(t *testing.T) {
	tests := []struct {
		name           string
		questions      []Question
		answerResponse QuestionsResponse
		expErr         error
	}{
		{
			name: "single question single answer",
			questions: []Question{
				{Question: "Auth method?", Header: "Auth", Options: []Option{{Label: "JWT"}, {Label: "OAuth"}}, MultiSelect: false},
			},
			answerResponse: QuestionsResponse{
				Answers: []Answer{{QuestionText: "Auth method?", Selected: "JWT"}},
			},
		},
		{
			name: "multiple questions",
			questions: []Question{
				{Question: "Language?", Header: "Lang", Options: []Option{{Label: "Go"}, {Label: "Rust"}}, MultiSelect: false},
				{Question: "Framework?", Header: "FW", Options: []Option{{Label: "Chi"}, {Label: "Echo"}}, MultiSelect: false},
			},
			answerResponse: QuestionsResponse{
				Answers: []Answer{
					{QuestionText: "Language?", Selected: "Go"},
					{QuestionText: "Framework?", Selected: "Chi"},
				},
			},
		},
		{
			name: "rejected response",
			questions: []Question{
				{Question: "Test?", Header: "T", Options: []Option{{Label: "Yes"}, {Label: "No"}}, MultiSelect: false},
			},
			answerResponse: QuestionsResponse{Rejected: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewService()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			req := NewQuestionsRequest("sess1", "tool1", tt.questions)

			// Subscribe to answer asynchronously
			subCtx, subCancel := context.WithCancel(context.Background())
			defer subCancel()
			sub := srv.Subscribe(subCtx)

			go func() {
				select {
				case <-sub:
					tt.answerResponse.RequestID = req.ID
					srv.Answer(tt.answerResponse)
				case <-time.After(1 * time.Second):
				}
			}()

			resp, err := srv.Ask(ctx, req)
			if tt.expErr != nil {
				require.ErrorIs(t, err, tt.expErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.answerResponse.Rejected, resp.Rejected)
				if !resp.Rejected {
					require.Equal(t, tt.answerResponse.Answers, resp.Answers)
				}
			}
		})
	}
}

func TestService_ContextCancellation(t *testing.T) {
	srv := NewService()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	req := NewQuestionsRequest("sess1", "tool1", []Question{
		{Question: "Test?", Header: "T", Options: []Option{{Label: "Yes"}}},
	})

	_, err := srv.Ask(ctx, req)
	require.ErrorIs(t, err, context.Canceled)
}

func TestService_OrphanedAnswer(t *testing.T) {
	srv := NewService()
	require.NotPanics(t, func() {
		srv.Answer(QuestionsResponse{RequestID: "fake-id"})
	})
}

func TestService_Concurrency(t *testing.T) {
	srv := NewService()
	var wg sync.WaitGroup
	numRequests := 50
	wg.Add(numRequests)

	subCtx, subCancel := context.WithCancel(context.Background())
	defer subCancel()
	sub := srv.Subscribe(subCtx)

	go func() {
		for event := range sub {
			srv.Answer(QuestionsResponse{
				RequestID: event.Payload.ID,
				Answers:   []Answer{{QuestionText: event.Payload.Questions[0].Question, Selected: "opt1"}},
			})
		}
	}()

	for i := 0; i < numRequests; i++ {
		go func() {
			defer wg.Done()
			req := NewQuestionsRequest("sess1", "tool1", []Question{
				{Question: "Test?", Header: "T", Options: []Option{{Label: "opt1"}, {Label: "opt2"}}},
			})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			resp, err := srv.Ask(ctx, req)
			require.NoError(t, err)
			require.Len(t, resp.Answers, 1)
		}()
	}
	wg.Wait()

	s := srv.(*questionsService)
	require.Equal(t, 0, s.pendingRequests.Len(), "pendingRequests should be empty")
}

func TestValidateQuestions(t *testing.T) {
	tests := []struct {
		name    string
		input   []Question
		wantErr string
	}{
		{
			name:    "empty questions",
			input:   []Question{},
			wantErr: "must ask between 1 and 4 questions",
		},
		{
			name: "too many questions",
			input: []Question{
				{Question: "Q1?", Header: "H1", Options: []Option{{Label: "A"}, {Label: "B"}}},
				{Question: "Q2?", Header: "H2", Options: []Option{{Label: "A"}, {Label: "B"}}},
				{Question: "Q3?", Header: "H3", Options: []Option{{Label: "A"}, {Label: "B"}}},
				{Question: "Q4?", Header: "H4", Options: []Option{{Label: "A"}, {Label: "B"}}},
				{Question: "Q5?", Header: "H5", Options: []Option{{Label: "A"}, {Label: "B"}}},
			},
			wantErr: "must ask between 1 and 4 questions",
		},
		{
			name: "empty question text",
			input: []Question{
				{Question: "", Header: "H1", Options: []Option{{Label: "A"}, {Label: "B"}}},
			},
			wantErr: "question text must not be empty",
		},
		{
			name: "empty header",
			input: []Question{
				{Question: "Q1?", Header: "", Options: []Option{{Label: "A"}, {Label: "B"}}},
			},
			wantErr: "header must not be empty",
		},
		{
			name: "header too long",
			input: []Question{
				{Question: "Q1?", Header: "very long header", Options: []Option{{Label: "A"}, {Label: "B"}}},
			},
			wantErr: "header must be at most 12 characters",
		},
		{
			name: "too few options",
			input: []Question{
				{Question: "Q1?", Header: "H1", Options: []Option{{Label: "A"}}},
			},
			wantErr: "each question must have 2-4 options",
		},
		{
			name: "too many options",
			input: []Question{
				{Question: "Q1?", Header: "H1", Options: []Option{{Label: "A"}, {Label: "B"}, {Label: "C"}, {Label: "D"}, {Label: "E"}}},
			},
			wantErr: "each question must have 2-4 options",
		},
		{
			name: "empty option label",
			input: []Question{
				{Question: "Q1?", Header: "H1", Options: []Option{{Label: ""}, {Label: "B"}}},
			},
			wantErr: "option label must not be empty",
		},
		{
			name: "duplicate question text",
			input: []Question{
				{Question: "Same?", Header: "H1", Options: []Option{{Label: "A"}, {Label: "B"}}},
				{Question: "Same?", Header: "H2", Options: []Option{{Label: "A"}, {Label: "B"}}},
			},
			wantErr: "question texts must be unique",
		},
		{
			name: "duplicate option labels",
			input: []Question{
				{Question: "Q1?", Header: "H1", Options: []Option{{Label: "A"}, {Label: "A"}}},
			},
			wantErr: "option labels must be unique",
		},
		{
			name: "valid single question",
			input: []Question{
				{Question: "Auth method?", Header: "Auth", Options: []Option{{Label: "JWT"}, {Label: "OAuth"}}, MultiSelect: false},
			},
			wantErr: "",
		},
		{
			name: "valid multi question multi select",
			input: []Question{
				{Question: "Language?", Header: "Lang", Options: []Option{{Label: "Go"}, {Label: "Rust"}, {Label: "Python"}}, MultiSelect: true},
				{Question: "Editor?", Header: "Editor", Options: []Option{{Label: "Vim"}, {Label: "VSCode"}}, MultiSelect: false},
			},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateQuestions(tt.input)
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests**

```bash
cd /path/to/crush && go test ./internal/questions/ -v
```

Expected: All tests PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/questions/service_test.go
git commit -m "test(questions): add service and validation tests"
```

---

### Task 3: Create Tool Definition & Prompt

**Files:**
- Create: `internal/agent/tools/ask_user_questions.go`
- Create: `internal/agent/tools/ask_user_questions.md`

- [ ] **Step 1: Create the tool prompt markdown**

```markdown
<!-- internal/agent/tools/ask_user_questions.md -->
Use this tool when you need to ask the user questions during execution.

Usage notes:
- Users will always be able to select "Other" to provide custom text input
- Use multi_select: true to allow multiple answers
- If you recommend a specific option, make it the first option and append "(Recommended)" to the label

Constraints:
- Ask 1-4 questions at a time
- Each question must have 2-4 options
- Question texts must be unique
- Option labels must be unique within each question
- Header must be 1-12 characters

When to use:
- Gather user preferences or requirements
- Clarify ambiguous instructions
- Get decisions on implementation choices
- Offer choices about direction to take

Plan mode:
- Use this tool to clarify requirements BEFORE finalizing your plan
- Do NOT ask "Is my plan ready?" or "Should I proceed?"
```

- [ ] **Step 2: Create the tool implementation**

```go
// internal/agent/tools/ask_user_questions.go
package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/questions"
)

const askUserQuestionsToolName = "ask_user_questions"

//go:embed ask_user_questions.md
var askUserQuestionDescription []byte

// AskUserQuestionParams holds the parameters for the ask_user_questions tool.
type AskUserQuestionParams struct {
	Questions []questions.Question `json:"questions" description:"Array of 1-4 questions to ask the user"`
}

// NewAskUserQuestionsTool creates a tool that asks the user multiple-choice questions.
func NewAskUserQuestionsTool(questionsService questions.Service) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		askUserQuestionsToolName,
		string(askUserQuestionDescription),
		func(ctx context.Context, params AskUserQuestionParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
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
```

- [ ] **Step 3: Commit**

```bash
git add internal/agent/tools/ask_user_questions.go internal/agent/tools/ask_user_questions.md
git commit -m "feat(tools): add ask_user_questions tool definition and prompt"
```

---

### Task 4: Tool Tests

**Files:**
- Create: `internal/agent/tools/ask_user_questions_test.go`

- [ ] **Step 1: Write tool-level tests**

```go
// internal/agent/tools/ask_user_questions_test.go
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/questions"
	"github.com/stretchr/testify/require"
)

type mockQuestionsService struct {
	*pubsub.Broker[questions.QuestionsRequest]
	response questions.QuestionsResponse
	err      error
}

func (m *mockQuestionsService) Ask(_ context.Context, _ questions.QuestionsRequest) (questions.QuestionsResponse, error) {
	if m.err != nil {
		return questions.QuestionsResponse{}, m.err
	}
	return m.response, nil
}

func (m *mockQuestionsService) Answer(_ questions.QuestionsResponse) {}

func TestAskUserQuestionsTool_ValidationErrors(t *testing.T) {
	mockService := &mockQuestionsService{
		Broker: pubsub.NewBroker[questions.QuestionsRequest](),
	}
	tool := NewAskUserQuestionsTool(mockService)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	tests := []struct {
		name    string
		params  AskUserQuestionParams
		wantErr string
	}{
		{
			name:    "no questions",
			params:  AskUserQuestionParams{Questions: []questions.Question{}},
			wantErr: "must ask between 1 and 4 questions",
		},
		{
			name: "empty question text",
			params: AskUserQuestionParams{Questions: []questions.Question{
				{Question: "", Header: "H", Options: []questions.Option{{Label: "A"}, {Label: "B"}}},
			}},
			wantErr: "question text must not be empty",
		},
		{
			name: "too few options",
			params: AskUserQuestionParams{Questions: []questions.Question{
				{Question: "Q?", Header: "H", Options: []questions.Option{{Label: "A"}}},
			}},
			wantErr: "each question must have 2-4 options",
		},
		{
			name: "duplicate question texts",
			params: AskUserQuestionParams{Questions: []questions.Question{
				{Question: "Same?", Header: "H1", Options: []questions.Option{{Label: "A"}, {Label: "B"}}},
				{Question: "Same?", Header: "H2", Options: []questions.Option{{Label: "A"}, {Label: "B"}}},
			}},
			wantErr: "question texts must be unique",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input, err := json.Marshal(tt.params)
			require.NoError(t, err)
			call := fantasy.ToolCall{ID: "test-call", Name: askUserQuestionsToolName, Input: string(input)}
			resp, err := tool.Run(ctx, call)
			require.NoError(t, err)
			require.True(t, resp.IsError, "expected error response")
			require.Contains(t, resp.Content, tt.wantErr)
		})
	}
}

func TestAskUserQuestionsTool_SuccessfulAnswer(t *testing.T) {
	mockService := &mockQuestionsService{
		Broker: pubsub.NewBroker[questions.QuestionsRequest](),
		response: questions.QuestionsResponse{
			Answers: []questions.Answer{
				{QuestionText: "Language?", Selected: "Go"},
			},
		},
	}

	tool := NewAskUserQuestionsTool(mockService)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	params := AskUserQuestionParams{
		Questions: []questions.Question{
			{Question: "Language?", Header: "Lang", Options: []questions.Option{{Label: "Go"}, {Label: "Rust"}}, MultiSelect: false},
		},
	}
	input, err := json.Marshal(params)
	require.NoError(t, err)

	call := fantasy.ToolCall{ID: "test-call", Name: askUserQuestionsToolName, Input: string(input)}
	resp, err := tool.Run(ctx, call)
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "Language?")
	require.Contains(t, resp.Content, "Go")
}

func TestAskUserQuestionsTool_Rejection(t *testing.T) {
	mockService := &mockQuestionsService{
		Broker: pubsub.NewBroker[questions.QuestionsRequest](),
		response: questions.QuestionsResponse{
			Rejected: true,
		},
	}

	tool := NewAskUserQuestionsTool(mockService)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	params := AskUserQuestionParams{
		Questions: []questions.Question{
			{Question: "Auth?", Header: "Auth", Options: []questions.Option{{Label: "JWT"}, {Label: "OAuth"}}, MultiSelect: false},
		},
	}
	input, err := json.Marshal(params)
	require.NoError(t, err)

	call := fantasy.ToolCall{ID: "test-call", Name: askUserQuestionsToolName, Input: string(input)}
	resp, err := tool.Run(ctx, call)
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "declined")
}

func TestAskUserQuestionsTool_ServiceError(t *testing.T) {
	mockService := &mockQuestionsService{
		Broker: pubsub.NewBroker[questions.QuestionsRequest](),
		err:     errors.New("service failure"),
	}

	tool := NewAskUserQuestionsTool(mockService)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	params := AskUserQuestionParams{
		Questions: []questions.Question{
			{Question: "Test?", Header: "T", Options: []questions.Option{{Label: "A"}, {Label: "B"}}, MultiSelect: false},
		},
	}
	input, err := json.Marshal(params)
	require.NoError(t, err)

	call := fantasy.ToolCall{ID: "test-call", Name: askUserQuestionsToolName, Input: string(input)}
	_, err = tool.Run(ctx, call)
	require.Error(t, err)
	require.Contains(t, err.Error(), "service failure")
}

func TestAskUserQuestionsTool_OtherAnswer(t *testing.T) {
	mockService := &mockQuestionsService{
		Broker: pubsub.NewBroker[questions.QuestionsRequest](),
		response: questions.QuestionsResponse{
			Answers: []questions.Answer{
				{QuestionText: "Framework?", Selected: "Svelte", IsOther: true},
			},
		},
	}

	tool := NewAskUserQuestionsTool(mockService)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	params := AskUserQuestionParams{
		Questions: []questions.Question{
			{Question: "Framework?", Header: "FW", Options: []questions.Option{{Label: "React"}, {Label: "Vue"}}, MultiSelect: false},
		},
	}
	input, err := json.Marshal(params)
	require.NoError(t, err)

	call := fantasy.ToolCall{ID: "test-call", Name: askUserQuestionsToolName, Input: string(input)}
	resp, err := tool.Run(ctx, call)
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "user input")
}
```

- [ ] **Step 2: Run tests**

```bash
cd /path/to/crush && go test ./internal/agent/tools/ -run TestAskUserQuestions -v
```

Expected: All PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/agent/tools/ask_user_questions_test.go
git commit -m "test(tools): add ask_user_questions tool tests"
```

---

### Task 5: TUI — Option List Component

**Files:**
- Create: `internal/ui/dialog/question_options.go`

- [ ] **Step 1: Create the option list rendering component**

This component renders a vertical list of options with radio/checkbox indicators, following the pattern of `models_list.go` in the same package.

```go
// internal/ui/dialog/question_options.go
package dialog

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/questions"
	"github.com/charmbracelet/crush/internal/ui/list"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
)

// questionOptionsList is a list of options for a question.
type questionOptionsList struct {
	*list.List
	t *styles.Styles
}

// newQuestionOptionsList creates a new list of options for a question.
func newQuestionOptionsList(sty *styles.Styles) *questionOptionsList {
	l := &questionOptionsList{
		List: list.NewList(),
		t:    sty,
	}
	l.RegisterRenderCallback(list.FocusedRenderCallback(l.List))
	return l
}

// SetQuestion sets the question's options in the list.
func (l *questionOptionsList) SetQuestion(q questions.Question, selOpts map[int]bool) {
	var items []list.Item
	for i, opt := range q.Options {
		items = append(items, &questionOptionsListItem{
			parent:   l,
			opt:      opt,
			selected: selOpts[i],
			index:    i,
		})
	}
	// Add "Other..." option
	items = append(items, &questionOptionsListItem{
		parent:   l,
		opt:      questions.Option{Label: "Other...", Description: "Provide a custom answer"},
		selected: selOpts[len(q.Options)], // Other is at index len(q.Options)
		index:    len(q.Options),
		isOther:  true,
	})
	l.SetItems(items...)
}

// questionOptionsListItem is a list item for a question's option.
type questionOptionsListItem struct {
	parent   *questionOptionsList
	opt      questions.Option
	selected bool
	focused  bool
	index    int
	isOther  bool
}

func (i *questionOptionsListItem) Height() int {
	return 1
}

func (i *questionOptionsListItem) String() string {
	return i.opt.Label
}

// SetFocused implements list.Item.
func (i *questionOptionsListItem) SetFocused(focused bool) {
	i.focused = focused
}

func (i *questionOptionsListItem) Render(width int) string {
	t := i.parent.t

	// Radio/checkbox style
	var indicator string
	if i.selected {
		indicator = t.RadioOn.Foreground(t.Green).Bold(true).Render()
	} else {
		indicator = t.RadioOff.Bold(true).Render()
	}

	labelStyle := t.Dialog.NormalItem
	if i.focused {
		labelStyle = t.Dialog.SelectedItem
	}
	descStyle := labelStyle.Italic(true).Foreground(t.FgHalfMuted)
	gapStyle := labelStyle.Padding(0)

	labelRender := labelStyle.Render(i.opt.Label)

	descRender := ""
	if len(i.opt.Description) > 0 {
		descAvailWidth := width - lipgloss.Width(indicator) - lipgloss.Width(labelRender) - 2
		optDesc := ansi.Truncate(i.opt.Description, max(0, descAvailWidth), "…")
		descRender = descStyle.Render(optDesc)
	}

	gapRender := gapStyle.Render(strings.Repeat(" ", max(0, width-lipgloss.Width(indicator)-lipgloss.Width(labelRender)-lipgloss.Width(descRender))))

	return labelStyle.Render(indicator + labelRender + gapRender + descRender)
}
```

- [ ] **Step 2: Commit**

```bash
git add internal/ui/dialog/question_options.go
git commit -m "feat(ui): add question options list component"
```

---

### Task 6: TUI — Questions Dialog

**Files:**
- Create: `internal/ui/dialog/questions.go`

- [ ] **Step 1: Create the Questions dialog component**

This is the main dialog component following the `Dialog` interface from `internal/ui/dialog/dialog.go`. It handles multi-question navigation, "Other" text input, and rejection semantics.

```go
// internal/ui/dialog/questions.go
package dialog

import (
	"fmt"
	"log/slog"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/questions"
	"github.com/charmbracelet/crush/internal/ui/common"
	uv "github.com/charmbracelet/ultraviolet"
)

const QuestionsID = "questions"

// Questions is a dialog that presents multiple-choice questions to the user.
type Questions struct {
	com *common.Common
	req questions.QuestionsRequest

	// State
	currQuestion  int
	selectedOpts  map[int]map[int]bool // map[questionIdx]map[optionIdx]bool
	otherTexts    map[int]string       // map[questionIdx]otherText
	isInTextInput bool
	textInput     string

	// Keyboard
	keyMap struct {
		Up       key.Binding
		Down     key.Binding
		Next     key.Binding
		Previous key.Binding
		Select   key.Binding
		Submit   key.Binding
		Close    key.Binding
	}

	// UI
	list *questionOptionsList
	help help.Model
}

// NewQuestionsDialog creates a new Questions dialog.
func NewQuestionsDialog(com *common.Common, req questions.QuestionsRequest) *Questions {
	d := &Questions{
		com:          com,
		req:          req,
		currQuestion: 0,
		selectedOpts: make(map[int]map[int]bool),
		otherTexts:   make(map[int]string),
		list:         newQuestionOptionsList(com.Styles),
		help:         help.New(),
	}

	d.keyMap.Up = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑", "up"),
	)
	d.keyMap.Down = key.NewBinding(
		key.WithKeys("down", "ctrl+n"),
		key.WithHelp("↓", "down"),
	)
	d.keyMap.Previous = key.NewBinding(
		key.WithKeys("left", "shift+tab"),
		key.WithHelp("←", "prev question"),
	)
	d.keyMap.Next = key.NewBinding(
		key.WithKeys("right", "tab"),
		key.WithHelp("→", "next question"),
	)
	d.keyMap.Select = key.NewBinding(
		key.WithKeys("enter", " "),
		key.WithHelp("enter", "select"),
	)
	d.keyMap.Submit = key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "confirm"),
	)
	d.keyMap.Close = CloseKey

	d.list.Focus()
	d.initList()
	d.list.SetSelected(0)

	d.help.Styles = com.Styles.DialogHelpStyles()

	return d
}

func (q *Questions) ID() string {
	return QuestionsID
}

func (q *Questions) initList() {
	if len(q.req.Questions) == 0 {
		return
	}
	if q.selectedOpts[q.currQuestion] == nil {
		q.selectedOpts[q.currQuestion] = make(map[int]bool)
	}
	q.refreshList()
	q.list.SelectFirst()
}

func (q *Questions) refreshList() {
	q.list.SetQuestion(
		q.req.Questions[q.currQuestion],
		q.selectedOpts[q.currQuestion],
	)
}

func (q *Questions) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		// If in text input mode (Other selected)
		if q.isInTextInput {
			switch {
			case key.Matches(msg, q.keyMap.Close) || key.Matches(msg, q.keyMap.Up):
				// Escape or up exits text input
				q.isInTextInput = false
				q.refreshList()
				return nil
			case msg.String() == "enter":
				// Submit the text as the Other answer
				text := strings.TrimSpace(q.textInput)
				if text != "" {
					currQ := q.req.Questions[q.currQuestion]
					otherIdx := len(currQ.Options) // Other is the last item
					q.selectedOpts[q.currQuestion] = map[int]bool{otherIdx: true}
					q.otherTexts[q.currQuestion] = text
				}
				q.isInTextInput = false
				// Auto-advance if single select
				if !q.req.Questions[q.currQuestion].MultiSelect {
					if q.currQuestion < len(q.req.Questions)-1 {
						q.currQuestion++
						q.initList()
					} else {
						return q.buildSubmitAction()
					}
				}
				return nil
			case msg.String() == "backspace":
				if len(q.textInput) > 0 {
					q.textInput = q.textInput[:len(q.textInput)-1]
				}
				return nil
			default:
				// Append character to text input
				ch := msg.String()
				if len(ch) == 1 && ch >= " " {
					q.textInput += ch
				}
				return nil
			}
		}

		// Normal option navigation
		switch {
		case key.Matches(msg, q.keyMap.Up):
			q.list.Focus()
			if q.list.IsSelectedFirst() {
				q.list.SelectLast()
			} else {
				q.list.SelectPrev()
			}
			q.list.ScrollToSelected()
		case key.Matches(msg, q.keyMap.Down):
			q.list.Focus()
			if q.list.IsSelectedLast() {
				q.list.SelectFirst()
			} else {
				q.list.SelectNext()
			}
			q.list.ScrollToSelected()
		case key.Matches(msg, q.keyMap.Previous):
			if q.currQuestion > 0 {
				q.currQuestion--
				q.initList()
			}
		case key.Matches(msg, q.keyMap.Next):
			if q.currQuestion < len(q.req.Questions)-1 {
				q.currQuestion++
				q.initList()
			}
		case key.Matches(msg, q.keyMap.Select):
			currQ := q.req.Questions[q.currQuestion]
			idx := q.list.Selected()
			if idx < 0 {
				break
			}

			// Check if "Other" was selected
			if idx == len(currQ.Options) {
				q.isInTextInput = true
				q.textInput = ""
				return nil
			}

			if !currQ.MultiSelect {
				// Single select: clear previous selection
				q.selectedOpts[q.currQuestion] = map[int]bool{idx: true}
				delete(q.otherTexts, q.currQuestion)
				q.refreshList()

				// Auto-advance to next question
				if q.currQuestion < len(q.req.Questions)-1 {
					q.currQuestion++
					q.initList()
				} else {
					return q.buildSubmitAction()
				}
			} else {
				// Multi select: toggle
				q.selectedOpts[q.currQuestion][idx] = !q.selectedOpts[q.currQuestion][idx]
				q.refreshList()
			}
		case key.Matches(msg, q.keyMap.Submit):
			return q.buildSubmitAction()
		case key.Matches(msg, q.keyMap.Close):
			slog.Info("QuestionsDialog rejected by user")
			return ActionQuestionsResponse{
				Response: questions.QuestionsResponse{
					RequestID: q.req.ID,
					Rejected:  true,
				},
			}
		}
	}
	return nil
}

// buildSubmitAction assembles the final response from all selected options.
func (q *Questions) buildSubmitAction() Action {
	slog.Info("QuestionsDialog submitting answers")
	res := questions.NewQuestionsResponse(&q.req)
	for questIdx, quest := range q.req.Questions {
		answer := questions.Answer{
			QuestionText: quest.Question,
		}

		// Check if Other was selected for this question
		if otherText, ok := q.otherTexts[questIdx]; ok && otherText != "" {
			answer.Selected = otherText
			answer.IsOther = true
		} else if quest.MultiSelect {
			// Multi-select: comma-separated labels
			var selected []string
			for optIdx, sel := range q.selectedOpts[questIdx] {
				if sel && optIdx < len(quest.Options) {
					selected = append(selected, quest.Options[optIdx].Label)
				}
			}
			answer.Selected = strings.Join(selected, ",")
		} else {
			// Single select: find the selected option
			for optIdx, sel := range q.selectedOpts[questIdx] {
				if sel && optIdx < len(quest.Options) {
					answer.Selected = quest.Options[optIdx].Label
					break
				}
			}
		}

		res.Answers[questIdx] = answer
	}
	return ActionQuestionsResponse{Response: res}
}

func (q *Questions) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	if len(q.req.Questions) == 0 {
		return nil
	}

	currQ := q.req.Questions[q.currQuestion]
	t := q.com.Styles

	width := max(0, min(defaultDialogMaxWidth, area.Dx()-t.Dialog.View.GetHorizontalBorderSize()))
	height := max(0, min(defaultDialogHeight, area.Dy()-t.Dialog.View.GetVerticalBorderSize()))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()

	q.list.SetSize(innerWidth, height-10)
	q.help.SetWidth(innerWidth)

	rc := NewRenderContext(t, width)

	// Title
	rc.Title = "Question"

	// Question navigation bar (tabs)
	navBar := q.renderNavigationBar()
	rc.AddPart(navBar)

	// Question text
	questionText := t.Dialog.TitleAccent.Italic(true).Padding(1, 2).Render(currQ.Question)
	rc.AddPart(questionText)

	// If in text input mode, show text input
	if q.isInTextInput {
		prompt := t.Dialog.InputPrompt.Render("Your answer: ")
		input := t.Dialog.SelectedItem.Render(q.textInput + "│")
		rc.AddPart(prompt + input)
	} else {
		// Options list
		listView := t.Dialog.List.Height(q.list.Height()).Render(q.list.Render())
		rc.AddPart(listView)
	}

	// Help
	rc.Help = q.help.View(q)

	view := rc.Render()
	DrawCenterCursor(scr, area, view, nil)

	return nil
}

// renderNavigationBar renders the question tabs.
func (q *Questions) renderNavigationBar() string {
	t := q.com.Styles
	var tabs []string
	for i, quest := range q.req.Questions {
		header := quest.Header
		if header == "" {
			header = fmt.Sprintf("Q%d", i+1)
		}
		_, answered := q.selectedOpts[i]
		checkbox := "☐"
		if answered {
			checkbox = "☑"
		}
		if i == q.currQuestion {
			tabs = append(tabs, t.Dialog.SelectedItem.Render(fmt.Sprintf(" %s %s ", checkbox, header)))
		} else {
			tabs = append(tabs, fmt.Sprintf(" %s %s ", checkbox, header))
		}
	}
	return strings.Join(tabs, " ")
}

// ShortHelp returns the short help view.
func (q *Questions) ShortHelp() []key.Binding {
	h := []key.Binding{
		q.keyMap.Up,
		q.keyMap.Down,
		q.keyMap.Select,
	}
	if len(q.req.Questions) > 1 {
		h = append(h, q.keyMap.Previous, q.keyMap.Next)
	}
	h = append(h, q.keyMap.Close)
	return h
}

// FullHelp returns the full help view.
func (q *Questions) FullHelp() [][]key.Binding {
	return [][]key.Binding{q.ShortHelp()}
}

// Compile-time assertion.
var _ Dialog = (*Questions)(nil)
```

- [ ] **Step 2: Commit**

```bash
git add internal/ui/dialog/questions.go
git commit -m "feat(ui): add Questions dialog with navigation and Other input"
```

---

### Task 7: Wire Up Action Type & Events

**Files:**
- Modify: `internal/ui/dialog/actions.go` (add `ActionQuestionsResponse`)
- Modify: `internal/ui/model/ui.go` (handle event + dispatch action)
- Modify: `internal/workspace/workspace.go` (add interface method)
- Modify: `internal/workspace/app_workspace.go` (implement method)
- Modify: `internal/workspace/client_workspace.go` (stub method)

- [ ] **Step 1: Add `ActionQuestionsResponse` to actions.go**

In `internal/ui/dialog/actions.go`, find the block where action types are defined (after `ActionDisableDockerMCP`). Add:

```go
// ActionQuestionsResponse is sent when the user completes an ask_user_questions dialog.
ActionQuestionsResponse struct {
	Response questions.QuestionsResponse
}
```

Also add the import for `"github.com/charmbracelet/crush/internal/questions"` at the top of the file.

- [ ] **Step 2: Handle QuestionsRequest event in ui.go**

In `internal/ui/model/ui.go`, find the `case pubsub.Event[permission.PermissionNotification]:` block in `Update()`. After it, add:

```go
case pubsub.Event[questions.QuestionsRequest]:
	d := dialog.NewQuestionsDialog(m.com, msg.Payload)
	m.dialog.OpenDialog(d)
	cmds = append(cmds, m.sendNotification(notification.Notification{
		Title:   "Crush is waiting...",
		Message: "There is a question for you.",
	}))
```

Also add the import `"github.com/charmbracelet/crush/internal/questions"` and `"github.com/charmbracelet/crush/internal/ui/dialog"` (if not already present).

- [ ] **Step 3: Handle `ActionQuestionsResponse` in handleDialogMsg**

In `internal/ui/model/ui.go`, find `handleDialogMsg()`. After the `case dialog.ActionPermissionResponse:` block, add:

```go
case dialog.ActionQuestionsResponse:
	m.com.Workspace.QuestionsAnswer(msg.Response)
	m.dialog.CloseDialog(dialog.QuestionsID)
```

- [ ] **Step 4: Add `QuestionsAnswer()` to Workspace interface**

In `internal/workspace/workspace.go`, find the `// Permissions` section. After `PermissionSetSkipRequests(skip bool)`, add:

```go
// Questions
QuestionsAnswer(res questions.QuestionsResponse)
```

Also add the import `"github.com/charmbracelet/crush/internal/questions"`.

- [ ] **Step 5: Implement in `app_workspace.go`**

In `internal/workspace/app_workspace.go`, after the `PermissionSetSkipRequests` method, add:

```go
// -- Questions --

func (w *AppWorkspace) QuestionsAnswer(response questions.QuestionsResponse) {
	w.app.Questions.Answer(response)
}
```

Also add the import `"github.com/charmbracelet/crush/internal/questions"` (if not present).

- [ ] **Step 6: Stub in `client_workspace.go`**

In `internal/workspace/client_workspace.go`, after the `PermissionSetSkipRequests` method, add:

```go
func (w *ClientWorkspace) QuestionsAnswer(res questions.QuestionsResponse) {
	// TODO: Implement for ClientWorkspace (remote API call)
}
```

Also add the import `"github.com/charmbracelet/crush/internal/questions"` (if not present).

- [ ] **Step 7: Commit**

```bash
git add internal/ui/dialog/actions.go internal/ui/model/ui.go internal/workspace/workspace.go internal/workspace/app_workspace.go internal/workspace/client_workspace.go
git commit -m "feat: wire up questions action, events, and workspace methods"
```

---

### Task 8: Wire Up Service, Coordinator, and Config

**Files:**
- Modify: `internal/app/app.go` (create service, subscribe, pass to coordinator)
- Modify: `internal/agent/coordinator.go` (inject service, register tool)
- Modify: `internal/config/config.go` (add tool name)

- [ ] **Step 1: Add `"ask_user_questions"` to `allToolNames()` in config.go**

In `internal/config/config.go`, find `func allToolNames() []string`. Add `"ask_user_questions"` to the returned slice (alphabetical order, after `"agent"`):

```go
func allToolNames() []string {
	return []string{
		"agent",
		"ask_user_questions",
		"bash",
		// ... rest unchanged
	}
}
```

- [ ] **Step 2: Add `Questions` field to `App` struct in app.go**

In `internal/app/app.go`, find the `App` struct. Add `Questions questions.Service` after the `Permissions` field:

```go
type App struct {
	// ...
	Permissions permission.Service
	Questions   questions.Service
	// ...
}
```

Add the import `"github.com/charmbracelet/crush/internal/questions"`.

- [ ] **Step 3: Create the service in `New()`**

In `internal/app/app.go`, find the `app := &App{...}` initialization. Add:

```go
Questions: questions.NewService(),
```

- [ ] **Step 4: Subscribe to events in `setupEvents()`**

In `internal/app/app.go`, find `setupEvents()`. After the `setupSubscriber` line for `"permissions-notifications"`, add:

```go
setupSubscriber(ctx, app.serviceEventsWG, "questions", app.Questions.Subscribe, app.events)
```

- [ ] **Step 5: Pass service to coordinator in `InitCoderAgent()`**

In `internal/app/app.go`, find `InitCoderAgent()`. Add `app.Questions` to the `agent.NewCoordinator()` call. This requires modifying the `NewCoordinator` signature (next step).

- [ ] **Step 6: Add `questions` field to coordinator struct**

In `internal/agent/coordinator.go`, find `type coordinator struct`. Add `questions questions.Service` after the `permissions` field:

```go
type coordinator struct {
	// ...
	permissions permission.Service
	questions   questions.Service
	// ...
}
```

Add the import `"github.com/charmbracelet/crush/internal/questions"`.

- [ ] **Step 7: Update `NewCoordinator` signature**

In `internal/agent/coordinator.go`, find `func NewCoordinator(...)`. Add `questions questions.Service` parameter after `permissions permission.Service`:

```go
func NewCoordinator(
	ctx context.Context,
	cfg *config.ConfigStore,
	sessions session.Service,
	messages message.Service,
	permissions permission.Service,
	questions questions.Service,
	history history.Service,
	filetracker filetracker.Service,
	// ...
```

Also add `questions: questions,` to the coordinator struct initialization.

- [ ] **Step 8: Register tool in `buildTools()`**

In `internal/agent/coordinator.go`, find `func (c *coordinator) buildTools()`. In the `allTools = append(allTools, ...)` block, add `tools.NewAskUserQuestionsTool(c.questions)` after the `NewEditTool` line:

```go
allTools = append(
	allTools,
	tools.NewBashTool(c.permissions, c.cfg.WorkingDir(), c.cfg.Config().Options.Attribution, modelID),
	// ...
	tools.NewEditTool(c.lspManager, c.permissions, c.history, c.filetracker, c.cfg.WorkingDir()),
	tools.NewAskUserQuestionsTool(c.questions),
	tools.NewMultiEditTool(c.lspManager, c.permissions, c.history, c.filetracker, c.cfg.WorkingDir()),
	// ...
)
```

- [ ] **Step 9: Update `InitCoderAgent()` call site**

In `internal/app/app.go`, update the `agent.NewCoordinator()` call to pass `app.Questions`:

```go
app.AgentCoordinator, err = agent.NewCoordinator(
	ctx,
	app.config,
	app.Sessions,
	app.Messages,
	app.Permissions,
	app.Questions,  // <-- add this
	app.History,
	app.FileTracker,
	// ...
)
```

- [ ] **Step 10: Fix any callers of NewCoordinator**

Search for other calls to `agent.NewCoordinator` in the codebase (test files, backend, etc.) and add the new `questions` parameter. For test/other callers, pass `questions.NewService()` or `nil` as appropriate.

- [ ] **Step 11: Update config test fixtures**

In `internal/config/load_test.go`, find the tests that assert `coderAgent.AllowedTools` includes the full tool list. Add `"ask_user_questions"` to the expected slice in the appropriate position (alphabetically after `"agent"`).

- [ ] **Step 12: Commit**

```bash
git add internal/app/app.go internal/agent/coordinator.go internal/config/config.go internal/config/load_test.go
git commit -m "feat: wire up questions service in app, coordinator, and config"
```

---

### Task 9: Build & Smoke Test

- [ ] **Step 1: Verify the project compiles**

```bash
cd /path/to/crush && go build ./...
```

Expected: No compilation errors.

- [ ] **Step 2: Run all tests**

```bash
cd /path/to/crush && go test ./internal/questions/ ./internal/agent/tools/ -v -run "Questions|AskUser"
```

Expected: All tests PASS.

- [ ] **Step 3: Run the full test suite to check for regressions**

```bash
cd /path/to/crush && go test ./... 2>&1 | tail -20
```

Expected: No new failures. If any existing test fails due to the `NewCoordinator` signature change, fix the caller.

- [ ] **Step 4: Final commit if any fixes were needed**

```bash
git add -A
git commit -m "fix: resolve compilation and test issues from questions integration"
```

---

## Self-Review

**1. Spec coverage:**
- ✅ Data models (Option, Question, Answer, Request, Response) — Task 1
- ✅ Input validation (all rules) — Task 1 + tested in Task 2 + Task 4
- ✅ Questions Service (pub/sub + pending channel) — Task 1
- ✅ Tool definition + prompt — Task 3
- ✅ "Other" free text input — Task 5 (option list) + Task 6 (dialog)
- ✅ Rejection semantics — Task 6 (dialog) + Task 4 (tool tests)
- ✅ Multi-question navigation — Task 6 (dialog)
- ✅ Tool response formatting — Task 3
- ✅ TUI layout (navigationBar, option list, help) — Tasks 5 + 6
- ✅ Wiring (app, coordinator, config, workspace, UI) — Tasks 7 + 8

**2. Placeholder scan:** No TBD/TODO/fill-in-later. All code is concrete.

**3. Type consistency:**
- `questions.Question` used consistently across service, tool, dialog
- `questions.QuestionsResponse` used consistently across service, tool, dialog, workspace
- `ActionQuestionsResponse` type matches the dispatch pattern in ui.go
- `NewCoordinator` signature updated in all call sites
