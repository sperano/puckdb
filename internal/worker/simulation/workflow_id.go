package simulation

import "strconv"

// workflowIDPrefix starts every pool's workflow ID. The sim_pools.workflow_id
// generated column ('sim-pool-' || id::text) must build the same string.
const workflowIDPrefix = "sim-pool-"

// WorkflowIDForPool returns the Temporal workflow ID of a pool's simulation,
// "sim-pool-<id>": the ID the API starts and cancels, the key of
// its progress report, and the value of sim_pools.workflow_id. poolID is
// sim_pools.id, an int4.
func WorkflowIDForPool(poolID int32) string {
	return workflowIDPrefix + strconv.FormatInt(int64(poolID), 10)
}
