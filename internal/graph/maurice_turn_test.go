package graph

import (
	"context"
	"errors"
	"testing"

	"github.com/sperano/puckdb/internal/appuser"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/maurice"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTurns records the StartChat and GetTurn calls and answers with the
// configured values.
type fakeTurns struct {
	maurice.Service
	starts   []maurice.ChatRequest
	startErr error
	getUser  string
	getID    string
	turn     *maurice.Turn
	getErr   error
}

func (f *fakeTurns) StartChat(_ context.Context, req maurice.ChatRequest) (*maurice.TurnRef, error) {
	f.starts = append(f.starts, req)
	if f.startErr != nil {
		return nil, f.startErr
	}
	return &maurice.TurnRef{TurnID: "t1", ConversationID: "c1"}, nil
}

func (f *fakeTurns) GetTurn(_ context.Context, userID, turnID string) (*maurice.Turn, error) {
	f.getUser, f.getID = userID, turnID
	return f.turn, f.getErr
}

func userContext() context.Context {
	return appuser.WithUser(context.Background(), appuser.User{ID: "user-1", SessionID: "s"})
}

func TestMauriceChatStart_StartsTheUsersTurn(t *testing.T) {
	svc := &fakeTurns{}
	r := &Resolver{MauriceService: svc}
	conv := "c1"

	got, err := r.mauriceChatStart(userContext(), &conv, "who leads?", "key-1")
	require.NoError(t, err)
	assert.Equal(t, &model.MauriceTurnStart{TurnID: "t1", ConversationID: "c1"}, got)
	assert.Equal(t, []maurice.ChatRequest{{
		UserID: "user-1", ConversationID: &conv, Message: "who leads?", IdempotencyKey: "key-1",
	}}, svc.starts)
}

func TestMauriceChatStart_Rejections(t *testing.T) {
	svc := &fakeTurns{}
	r := &Resolver{MauriceService: svc}

	_, err := r.mauriceChatStart(context.Background(), nil, "hi", "key")
	require.ErrorIs(t, err, appuser.ErrUnauthenticated)
	_, err = (&Resolver{}).mauriceChatStart(userContext(), nil, "hi", "key")
	require.ErrorIs(t, err, errMauriceNotConfigured)
	_, err = r.mauriceChatStart(userContext(), nil, "hi", "")
	require.ErrorIs(t, err, errMissingIdempotencyKey)
	assert.Empty(t, svc.starts, "no turn starts without a user, a service or a key")

	svc.startErr = maurice.ErrTurnInProgress
	_, err = r.mauriceChatStart(userContext(), nil, "hi", "key")
	require.ErrorIs(t, err, maurice.ErrTurnInProgress)
}

func TestMauriceTurn_ReadsTheUsersTurn(t *testing.T) {
	svc := &fakeTurns{turn: &maurice.Turn{ID: "t1", ConversationID: "c1",
		TurnReplay: maurice.TurnReplay{Status: maurice.TurnRunning}}}
	r := &Resolver{MauriceService: svc}

	got, err := r.mauriceTurn(userContext(), "t1")
	require.NoError(t, err)
	assert.Equal(t, &model.MauriceTurn{ID: "t1", ConversationID: "c1", Status: model.MauriceTurnStatusRunning,
		ToolsUsed: []string{}, Warnings: []string{}}, got)
	assert.Equal(t, "user-1", svc.getUser)
	assert.Equal(t, "t1", svc.getID)
}

// Another user's turn, an unknown one and one of a deleted conversation are
// all ErrTurnNotFound in the service, null in the API.
func TestMauriceTurn_UnreadableTurnIsNull(t *testing.T) {
	r := &Resolver{MauriceService: &fakeTurns{getErr: maurice.ErrTurnNotFound}}
	got, err := r.mauriceTurn(userContext(), "t1")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestMauriceTurn_Rejections(t *testing.T) {
	failure := errors.New("database down")
	r := &Resolver{MauriceService: &fakeTurns{getErr: failure}}
	_, err := r.mauriceTurn(userContext(), "t1")
	require.ErrorIs(t, err, failure)
	_, err = r.mauriceTurn(context.Background(), "t1")
	require.ErrorIs(t, err, appuser.ErrUnauthenticated)
	_, err = (&Resolver{}).mauriceTurn(userContext(), "t1")
	require.ErrorIs(t, err, errMauriceNotConfigured)
}

func TestMauriceTurnModel(t *testing.T) {
	str := func(s string) *string { return &s }
	tests := []struct {
		name string
		turn maurice.TurnReplay
		want model.MauriceTurn
	}{
		{"running", maurice.TurnReplay{Status: maurice.TurnRunning},
			model.MauriceTurn{Status: model.MauriceTurnStatusRunning, ToolsUsed: []string{}, Warnings: []string{}}},
		{"succeeded", maurice.TurnReplay{Status: maurice.TurnSucceeded, MessageID: "m1", Content: "42 goals",
			ToolsUsed: []string{"goals"}, Warnings: []string{"partial data"}},
			model.MauriceTurn{Status: model.MauriceTurnStatusSucceeded, MessageID: str("m1"), Content: str("42 goals"),
				ToolsUsed: []string{"goals"}, Warnings: []string{"partial data"}}},
		{"succeeded without text", maurice.TurnReplay{Status: maurice.TurnSucceeded, MessageID: "m1"},
			model.MauriceTurn{Status: model.MauriceTurnStatusSucceeded, MessageID: str("m1"), Content: str(""),
				ToolsUsed: []string{}, Warnings: []string{}}},
		{"failed", maurice.TurnReplay{Status: maurice.TurnFailed, ErrorClass: maurice.ErrorClassProvider},
			model.MauriceTurn{Status: model.MauriceTurnStatusFailed, Error: str("provider_error"),
				ToolsUsed: []string{}, Warnings: []string{}}},
		{"abandoned", maurice.TurnReplay{Status: maurice.TurnFailed, ErrorClass: maurice.ErrorClassAbandoned},
			model.MauriceTurn{Status: model.MauriceTurnStatusFailed, Error: str("abandoned"),
				ToolsUsed: []string{}, Warnings: []string{}}},
		{"cancelled is failed", maurice.TurnReplay{Status: maurice.TurnCancelled, ErrorClass: maurice.ErrorClassCancelled},
			model.MauriceTurn{Status: model.MauriceTurnStatusFailed, Error: str("cancelled"),
				ToolsUsed: []string{}, Warnings: []string{}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.want
			want.ID, want.ConversationID = "t1", "c1"
			got := mauriceTurnModel(&maurice.Turn{ID: "t1", ConversationID: "c1", TurnReplay: tc.turn})
			assert.Equal(t, &want, got)
		})
	}
}
