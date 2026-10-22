package player

import (
	"context"
	"errors"
	"testing"

	redismock "github.com/go-redis/redismock/v8"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testYahooIDSetKey = "test:yahoo-id-set"

func TestLoadYahooIDSet(t *testing.T) {
	t.Parallel()

	t.Run("empty is non-nil", func(t *testing.T) {
		t.Parallel()

		client, redisMock := redismock.NewClientMock()
		redisMock.ExpectSMembers(testYahooIDSetKey).SetVal([]string{})

		ids, err := loadYahooIDSet(context.Background(), client, testYahooIDSetKey)

		require.NoError(t, err)
		assert.NotNil(t, ids)
		assert.Empty(t, ids)
		require.NoError(t, redisMock.ExpectationsWereMet())
	})

	t.Run("skips malformed and deduplicates", func(t *testing.T) {
		t.Parallel()

		client, redisMock := redismock.NewClientMock()
		redisMock.ExpectSMembers(testYahooIDSetKey).SetVal([]string{"42", "bad", "42", "7"})

		ids, err := loadYahooIDSet(context.Background(), client, testYahooIDSetKey)

		require.NoError(t, err)
		assert.Equal(t, map[store.YahooPlayerID]struct{}{7: {}, 42: {}}, ids)
		require.NoError(t, redisMock.ExpectationsWereMet())
	})

	t.Run("returns Redis error", func(t *testing.T) {
		t.Parallel()

		wantErr := errors.New("redis unavailable")
		client, redisMock := redismock.NewClientMock()
		redisMock.ExpectSMembers(testYahooIDSetKey).SetErr(wantErr)

		ids, err := loadYahooIDSet(context.Background(), client, testYahooIDSetKey)

		require.ErrorIs(t, err, wantErr)
		assert.Nil(t, ids)
		require.NoError(t, redisMock.ExpectationsWereMet())
	})
}
