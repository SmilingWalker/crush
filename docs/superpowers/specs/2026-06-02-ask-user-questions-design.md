# Ask User Questions Tool — Design Spec

## Overview

An `ask_user_questions` tool for Crush that lets the LLM agent ask the user structured multiple-choice questions during execution. Modeled after Claude Code's `AskUserQuestion` tool, adapted to Crush's Go + Bubble Tea architecture.

Built on PR #2579's pub/sub architecture, enhanced with input validation, "Other" free text input, rejection semantics, multi-question navigation, and improved tool prompts.

## Phasing

**P1 (this spec): Core functionality**
- 1-4 questions, 2-4 options each
- Single-select and multi-select
- "Other" free text input
- Input validation with clear error messages
- Rejection vs empty-submission distinction
- Multi-question tab navigation
- Tool prompt with usage guidance and plan mode notes

**P2 (future): Rich interaction**
- Preview panel (side-by-side option list + preview content)
- Annotations (user notes on selections)
- Markdown rendering for preview content

**P3 (future): Advanced features**
- Image paste support
- Plan mode interview phase
- Metadata/analytics tracking
- HTML preview sanitization

## Data Model

### Option

```go
type Option struct {
    Label       string `json:"label"`
    Description string `json:"description"`
    Preview     string `json:"preview,omitempty"` // P2: reserved
}
```

### Question

```go
type Question struct {
    Question    string   `json:"question"`      // Full question text; used as answer key
    Header      string   `json:"header"`        // Tab label, max 12 chars
    Options     []Option `json:"options"`        // 2-4 options
    MultiSelect bool     `json:"multi_select"`   // Allow multiple selections
}
```

### Answer

```go
type Answer struct {
    QuestionText string `json:"question"`   // Maps back to Question.Question
    Selected     string `json:"selected"`   // Single: label or custom text; Multi: comma-separated
    IsOther      bool   `json:"is_other"`   // True when user typed custom text
}
```

### Request / Response

```go
type QuestionsRequest struct {
    ID         string
    SessionID  string
    ToolCallID string
    Questions  []Question
}

type QuestionsResponse struct {
    RequestID string
    Answers   []Answer
    Rejected  bool // true = user declined entirely
}
```

Question text is the key linking questions to answers (not UUIDs). This matches Claude Code's design and is more readable for the LLM.

## Input Validation

Validated in the tool handler before dispatching to the service:

| Rule | Error message |
|------|--------------|
| 1-4 questions | "Must ask between 1 and 4 questions" |
| Question text non-empty | "Question text must not be empty" |
| Header non-empty, ≤12 chars | "Header must be 1-12 characters" |
| 2-4 options per question | "Each question must have 2-4 options" |
| Option label non-empty | "Option label must not be empty" |
| Unique question texts | "Question texts must be unique" |
| Unique option labels per question | "Option labels must be unique within each question" |

Validation failures return `fantasy.NewTextErrorResponse()` with the specific message so the LLM can self-correct.

## Tool Prompt

```markdown
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

When to use:
- Gather user preferences or requirements
- Clarify ambiguous instructions
- Get decisions on implementation choices
- Offer choices about direction to take

Plan mode:
- Use this tool to clarify requirements BEFORE finalizing your plan
- Do NOT ask "Is my plan ready?" or "Should I proceed?"
```

## Questions Service

### Architecture

Pub/sub + pending channel pattern, consistent with Crush's `permission.Service`:

```go
type Service interface {
    pubsub.Subscriber[QuestionsRequest]
    Ask(ctx context.Context, req QuestionsRequest) (QuestionsResponse, error)
    Answer(response QuestionsResponse)
}
```

### Flow

1. `Ask()` creates a buffered channel, stores it in `pendingRequests` map, publishes the request
2. UI model receives the event via subscription, opens the dialog
3. User completes the dialog, UI calls `Answer(response)`
4. `Answer()` sends the response through the channel
5. `Ask()` returns the response to the tool handler

### Cancellation

- Context cancellation (e.g., session ends) → `Ask()` returns context error
- Pending request channel is cleaned up via `defer`

## TUI Dialog

### Layout

```
┌──────────────────────────────────────────────────┐
│ ← [☑ Auth method] [☐ Library] [✓ Submit] →       │  NavigationBar
│                                                    │
│ Which authentication method should we use?         │  Question text
│                                                    │
│   ◉ JWT tokens (Recommended)                      │  Selected option
│   ○ Session cookies                               │
│   ○ OAuth2                                        │
│   ○ Other...                                      │  Always-present
│                                                    │
│ Help: ↑/↓ choose · enter confirm · esc close       │  Help bar
└──────────────────────────────────────────────────┘
```

### Multi-question Navigation

- `QuestionNavigationBar` component renders tabs with checkbox states
- Tab / Shift+Tab or ←/→ to switch between questions
- Single question + non-multi-select: hide navigation and submit tab
- Current question gets highlighted background; answered questions show ☑

### "Other" Free Text Input

- "Other..." option always appended to the option list
- Selecting it switches the list to a text input field
- Enter submits the text; Escape returns to option list
- Custom text is stored as `Selected` with `IsOther = true`

### Rejection Semantics

- **Escape** → `ActionQuestionsResponse{Rejected: true}` → LLM gets "User declined to answer your questions"
- **Submit with empty answers** → normal submission, unanswered questions have empty `Selected`
- The distinction matters: rejection means "stop asking", empty means "saw it, no preference"

### Keyboard Bindings

| Key | Action |
|-----|--------|
| ↑/↓ or j/k | Navigate options |
| Enter | Confirm selection (single-select: auto-advance to next question) |
| Space | Toggle selection (multi-select only) |
| Tab / ←/→ | Navigate between questions |
| Esc | Reject / cancel |
| 1-9 | Quick-select option by number |

### Rendering

- Uses `RenderContext` from `internal/ui/dialog/common.go`
- `DrawCenterCursor()` for centered positioning
- Max width 70, height dynamically computed from option count
- Styles from `Styles.Dialog` constants
- Help bar via `help.Model` with `ShortHelp()` / `FullHelp()`

## Tool Layer

### Parameters

```go
type AskUserQuestionParams struct {
    Questions []questions.Question `json:"questions" description:"Array of 1-4 questions to ask the user"`
}
```

### Implementation

```go
func NewAskUserQuestionsTool(questionsService questions.Service) fantasy.AgentTool {
    return fantasy.NewAgentTool(
        "ask_user_questions",
        string(askUserQuestionDescription),
        func(ctx context.Context, params AskUserQuestionParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
            if err := validateQuestions(params.Questions); err != nil {
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
```

### Response Formatting

```
User answered your questions: "Which auth method?"="JWT tokens", "Which library?"="React". You can continue with the user's answers in mind.
```

For "Other" answers: `"Which framework?"="Svelte (user input)"`.

For multi-select: `"Which features?"="auth,dashboard,settings"`.

## File Manifest

### New Files

| File | Purpose |
|------|---------|
| `internal/questions/service.go` | Service + data models + validation |
| `internal/questions/service_test.go` | Service unit tests |
| `internal/agent/tools/ask_user_questions.go` | Tool definition + validation + formatting |
| `internal/agent/tools/ask_user_questions.md` | Tool prompt |
| `internal/agent/tools/ask_user_questions_test.go` | Tool tests |
| `internal/ui/dialog/questions.go` | Questions dialog (navigation + Other input) |
| `internal/ui/dialog/question_options.go` | Option list rendering component |

### Modified Files

| File | Change |
|------|--------|
| `internal/agent/coordinator.go` | Inject questionsService, register tool |
| `internal/app/app.go` | Create service, subscribe to events |
| `internal/config/config.go` | Add to `allToolNames()` |
| `internal/ui/dialog/actions.go` | Add `ActionQuestionsResponse` |
| `internal/ui/model/ui.go` | Handle QuestionsRequest event + action |
| `internal/workspace/workspace.go` | Add `QuestionsAnswer()` to interface |
| `internal/workspace/app_workspace.go` | Implement `QuestionsAnswer()` |
| `internal/workspace/client_workspace.go` | Stub `QuestionsAnswer()` |

## P2/P3 Reserved Fields

The data model includes fields that are unused in P1 but reserved for future phases:

- `Option.Preview` — P2: side-by-side preview content
- `QuestionsResponse.Annotations` — P2: user notes (not yet in struct, will add)
- `QuestionsRequest.Metadata` — P3: analytics tracking

These fields are ignored in P1 code but present in JSON schemas so the LLM doesn't include them.
