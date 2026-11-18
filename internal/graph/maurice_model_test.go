package graph

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sperano/puckdb/internal/appuser"
	"github.com/sperano/puckdb/internal/maurice"
)

// fakeModel answers a fixed configured model.
type fakeModel struct {
	maurice.Service
	model maurice.ModelInfo
}

func (f *fakeModel) Model() maurice.ModelInfo { return f.model }

// mauriceModelQuery is the query maurice-web sends: its field names and
// shape are the contract.
const mauriceModelQuery = `query MauriceModel { mauriceModel { provider model } }`

func TestMauriceModel_AnswersTheConfiguredModel(t *testing.T) {
	svc := &fakeModel{model: maurice.ModelInfo{Provider: "Anthropic", Model: "claude-sonnet-5-5"}}
	server := newTeamPickerServer(t, &Resolver{MauriceService: svc})

	resp := postGraphQL(t, server, http.Header{"X-Test-User": {"user-1"}}, mauriceModelQuery, nil)
	require.Empty(t, resp.Errors)
	assert.JSONEq(t, `{"mauriceModel": {"provider": "Anthropic", "model": "claude-sonnet-5-5"}}`, string(resp.Data))
}

func TestMauriceModel_RequiresAUser(t *testing.T) {
	svc := &fakeModel{model: maurice.ModelInfo{Provider: "Anthropic", Model: "claude-sonnet-5-5"}}
	server := newTeamPickerServer(t, &Resolver{MauriceService: svc})

	resp := postGraphQL(t, server, nil, mauriceModelQuery, nil)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, appuser.ErrUnauthenticated.Error(), resp.Errors[0].Message)
}

func TestMauriceModel_NotConfigured(t *testing.T) {
	ctx := appuser.WithUser(context.Background(), appuser.User{ID: "user-1"})
	_, err := (&Resolver{}).mauriceModel(ctx)
	require.ErrorIs(t, err, errMauriceNotConfigured)
}
