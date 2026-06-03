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
				case event := <-sub:
					tt.answerResponse.RequestID = event.Payload.ID
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

func TestQuestion_HasPreview(t *testing.T) {
	tests := []struct {
		name     string
		question Question
		want     bool
	}{
		{
			name: "no preview",
			question: Question{
				Question: "Q?", Header: "H",
				Options: []Option{{Label: "A"}, {Label: "B"}},
			},
			want: false,
		},
		{
			name: "one option has preview",
			question: Question{
				Question: "Q?", Header: "H",
				Options: []Option{
					{Label: "A", Preview: "some code"},
					{Label: "B"},
				},
			},
			want: true,
		},
		{
			name: "all options have preview",
			question: Question{
				Question: "Q?", Header: "H",
				Options: []Option{
					{Label: "A", Preview: "code a"},
					{Label: "B", Preview: "code b"},
				},
			},
			want: true,
		},
		{
			name: "empty preview string is ignored",
			question: Question{
				Question: "Q?", Header: "H",
				Options: []Option{
					{Label: "A", Preview: ""},
					{Label: "B"},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.question.HasPreview())
		})
	}
}
