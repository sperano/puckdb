package yahoo

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/sperano/puckdb/internal/httpx"
	"github.com/stretchr/testify/assert"
)

func TestIsPreseasonResourceUnavailable(t *testing.T) {
	t.Parallel()
	wrapped := func(status int) error {
		return fmt.Errorf("%w: %w", ErrDownload, &httpx.HTTPError{StatusCode: status})
	}

	assert.True(t, isPreseasonResourceUnavailable(wrapped(http.StatusBadRequest)))
	assert.False(t, isPreseasonResourceUnavailable(wrapped(http.StatusNotFound)))
	assert.False(t, isPreseasonResourceUnavailable(wrapped(http.StatusUnauthorized)))
	assert.False(t, isPreseasonResourceUnavailable(wrapped(http.StatusTooManyRequests)))
	assert.False(t, isPreseasonResourceUnavailable(errors.New("connection reset")))
}
