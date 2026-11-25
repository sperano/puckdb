package player

import (
	"context"
	"testing"

	"github.com/go-redis/redismock/v9"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A Yahoo ID loaded without details (its pool entry was missing or
// undecodable) has no name to search: it is left unverified, never searched
// and never persisted as verified non-NHL.
func TestVerifyUnmatchedBatch_NamelessPlayerStaysUnverified(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.ExpectSMembers(VerifiedNonNHLKey).SetVal([]string{})

	a := &Activities{Storage: store.NewMemStorage(), NHLClient: client, RedisClient: redisClient}
	result, err := a.verifyUnmatchedBatch(ctx, []UnmatchedYahooPlayer{{YahooID: 4242}}, noopHeartbeat)

	require.NoError(t, err)
	assert.Equal(t, []VerifiedPlayer{{YahooID: 4242}}, result.Unverified)
	assert.Empty(t, result.VerifiedNonNHL)
	assert.Empty(t, result.NotFoundInNHL)
	assert.Empty(t, result.TrulyUnmatched)
	client.AssertNotCalled(t, "SearchPlayer")
	require.NoError(t, mockRedis.ExpectationsWereMet(), "no SADD of the verified non-NHL set")
}

// A name missing its first or last part is not searched either: the single
// token matches no NHL full name, which would file the player as not found.
func TestVerifyPlayer_PartialNameIsAnError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		player UnmatchedYahooPlayer
	}{
		{"blank", UnmatchedYahooPlayer{YahooID: 4242, FirstName: " "}},
		{"first name only", UnmatchedYahooPlayer{YahooID: 4242, FirstName: "Connor", LastName: " "}},
		{"last name only", UnmatchedYahooPlayer{YahooID: 4242, LastName: "McDavid"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := &MockNHLClient{}
			a := &Activities{NHLClient: client}
			_, err := a.verifyPlayer(context.Background(), tt.player)

			require.ErrorIs(t, err, errNamelessPlayer)
			assert.EqualError(t, err, "yahoo id 4242: yahoo player has no full name to search")
			client.AssertNotCalled(t, "SearchPlayer")
		})
	}
}
