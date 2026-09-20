package workflow

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/sperano/puckdb/internal/worker/asset"
	"github.com/stretchr/testify/mock"
)

const concurrencyProbeActivityDelay = 10 * time.Millisecond

func recordMax(maximum *atomic.Int32, value int32) {
	for {
		observed := maximum.Load()
		if value <= observed {
			return
		}
		if maximum.CompareAndSwap(observed, value) {
			return
		}
	}
}

func (s *FetchAssetsClassWorkflowTestSuite) runWrapperConcurrencyProbe(assetCount, batchSize, concurrency int) int32 {
	s.env.OnActivity("LoadPlayerHeadshotAssets", mock.Anything).
		Return(makeAssets(assetCount), nil)

	var active atomic.Int32
	var maximum atomic.Int32
	var act *asset.Activities
	s.env.OnActivity(act.FetchAssetBatch, mock.Anything, mock.Anything).
		Return(func(_ context.Context, in asset.FetchAssetBatchInput) (asset.FetchAssetBatchResult, error) {
			current := active.Add(1)
			recordMax(&maximum, current)
			time.Sleep(concurrencyProbeActivityDelay)
			active.Add(-1)
			return asset.FetchAssetBatchResult{Results: make([]asset.FetchAssetResult, len(in.Assets))}, nil
		}).
		Times(batchCount(assetCount, batchSize))

	input := FetchAssetsInput{
		AssetBatchSize:   &batchSize,
		ClassConcurrency: &concurrency,
	}
	s.env.ExecuteWorkflow(FetchPlayerHeadshotsWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	return maximum.Load()
}

func (s *FetchAssetsClassWorkflowTestSuite) TestWrapperHonoursClassConcurrencyOverride() {
	const (
		assetCount           = 3
		batchSize            = 1
		requestedConcurrency = 1
	)

	maximum := s.runWrapperConcurrencyProbe(assetCount, batchSize, requestedConcurrency)
	s.Equal(int32(requestedConcurrency), maximum)
}

func (s *FetchAssetsClassWorkflowTestSuite) TestOversizedConcurrencyIsBoundedByBatchCount() {
	const (
		assetCount           = 3
		batchSize            = 1
		requestedConcurrency = 100
	)

	maximum := s.runWrapperConcurrencyProbe(assetCount, batchSize, requestedConcurrency)
	s.Equal(int32(assetCount), maximum)
}
