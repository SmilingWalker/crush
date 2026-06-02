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
