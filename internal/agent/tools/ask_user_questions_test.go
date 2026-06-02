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
		params  AskUserQuestionsParams
		wantErr string
	}{
		{
			name:    "no questions",
			params:  AskUserQuestionsParams{Questions: []questions.Question{}},
			wantErr: "must ask between 1 and 4 questions",
		},
		{
			name: "empty question text",
			params: AskUserQuestionsParams{Questions: []questions.Question{
				{Question: "", Header: "H", Options: []questions.Option{{Label: "A"}, {Label: "B"}}},
			}},
			wantErr: "question text must not be empty",
		},
		{
			name: "too few options",
			params: AskUserQuestionsParams{Questions: []questions.Question{
				{Question: "Q?", Header: "H", Options: []questions.Option{{Label: "A"}}},
			}},
			wantErr: "each question must have 2-4 options",
		},
		{
			name: "duplicate question texts",
			params: AskUserQuestionsParams{Questions: []questions.Question{
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

	params := AskUserQuestionsParams{
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

	params := AskUserQuestionsParams{
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

	params := AskUserQuestionsParams{
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

	params := AskUserQuestionsParams{
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
