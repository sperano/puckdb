package admin

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWorkflowIDConstants(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "drop-database", WorkflowIDDropDatabase)
	assert.Equal(t, "migrate-database", WorkflowIDMigrateDatabase)
	assert.Equal(t, "reset-database", WorkflowIDResetDatabase)
	assert.Equal(t, "flush-redis", WorkflowIDFlushRedis)
}
