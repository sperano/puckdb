package draftsession

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDatabaseVersionChecksPostgreSQLBigintRange(t *testing.T) {
	version, err := DatabaseVersion(maxDatabaseVersion)
	require.NoError(t, err)
	assert.Equal(t, int64(maxDatabaseVersion), version)

	_, err = DatabaseVersion(maxDatabaseVersion + 1)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrDatabaseVersionOverflow))
}
