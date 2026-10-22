package pgxfixtures

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBatchResultsExec(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		results := &BatchResults{}
		tag, err := results.Exec()

		require.NoError(t, err)
		assert.Equal(t, int64(1), tag.RowsAffected())
		require.NoError(t, results.Close())
	})

	t.Run("repeats configured error", func(t *testing.T) {
		t.Parallel()

		wantErr := errors.New("batch failed")
		results := &BatchResults{ExecErr: wantErr}

		for range 2 {
			_, err := results.Exec()
			require.ErrorIs(t, err, wantErr)
		}
		require.NoError(t, results.Close())
	})
}
