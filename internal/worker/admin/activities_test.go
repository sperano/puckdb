package admin

import (
	"errors"
	"fmt"
	"testing"

	"github.com/sperano/puckdb/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

const dirtyVersion = 2

func TestFailFastIfDirty(t *testing.T) {
	t.Parallel()
	dirty := &database.DirtyMigrationError{Version: dirtyVersion}
	errOther := errors.New("connection refused")

	t.Run("nil stays nil", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, failFastIfDirty(nil))
	})

	t.Run("other errors stay retryable", func(t *testing.T) {
		t.Parallel()
		err := failFastIfDirty(errOther)
		var appErr *temporal.ApplicationError
		assert.False(t, errors.As(err, &appErr))
		require.ErrorIs(t, err, errOther)
	})

	for name, err := range map[string]error{
		"dirty":         dirty,
		"wrapped dirty": fmt.Errorf("migration failed: %w", dirty),
		"joined dirty":  errors.Join(errOther, dirty),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := failFastIfDirty(err)

			var appErr *temporal.ApplicationError
			require.ErrorAs(t, got, &appErr)
			assert.True(t, appErr.NonRetryable())
			assert.Equal(t, dirtyMigrationErrorType, appErr.Type())
			assert.Contains(t, appErr.Error(), "force-version")
		})
	}
}
