package cmd

import (
	"testing"

	"github.com/sperano/puckdb/internal/temporal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkerQueueName(t *testing.T) {
	t.Parallel()
	name, err := workerQueueName(workerQueueTypeTasks)
	require.NoError(t, err)
	assert.Equal(t, temporal.QueueTasks, name)

	name, err = workerQueueName(workerQueueTypeAdmin)
	require.NoError(t, err)
	assert.Equal(t, temporal.QueueAdmin, name)

	_, err = workerQueueName("assets")
	require.EqualError(t, err, `invalid queue type "assets": must be "tasks" or "admin"`)
}

// The error channel must hold one error per goroutine runWorker starts, or a
// goroutine that fails after the command returned blocks forever.
func TestWorkerErrChCap(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 3, workerErrChCap(workerQueueTypeTasks), "metrics + main worker + asset worker")
	assert.Equal(t, 2, workerErrChCap(workerQueueTypeAdmin), "metrics + main worker")
}
