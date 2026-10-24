package workflow

import (
	"github.com/sperano/puckdb/internal/config"
	"go.temporal.io/sdk/workflow"
)

// dayLoopBatchedProgressChangeID gates batched progress saves in the NHL
// season fetch and import day loops. Histories recorded before it save after
// every completion and carry no marker, so they must retain SaveEvery 0.
const (
	dayLoopBatchedProgressChangeID = "nhl-season-day-loop-batched-progress"
	dayLoopBatchedProgressVersion  = 1
)

func dayLoopProgressSaveEvery(ctx workflow.Context) int {
	version := workflow.GetVersion(ctx, dayLoopBatchedProgressChangeID, workflow.DefaultVersion,
		dayLoopBatchedProgressVersion)
	if version == workflow.DefaultVersion {
		return 0
	}
	return config.DefaultDayLoopProgressSaveEvery
}
