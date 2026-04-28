package workflow

import (
	"context"
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/worker/asset"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

// ─── Loader activity stubs ────────────────────────────────────────────────────
//
// The generic FetchAssetsClassWorkflow invokes the loader by string name
// (input.Class.LoaderName). The test environment requires the activity to be
// registered before it can be mocked by name, so we register thin stubs that
// match the expected signatures.

// stubLoaderActivities provides no-op implementations of every loader activity.
// They are never actually called in these tests; Temporal's test env intercepts
// all calls via OnActivity mocks before they reach the real implementation.
type stubLoaderActivities struct{}

func (s *stubLoaderActivities) LoadPlayerHeadshotAssets(_ context.Context) ([]asset.Asset, error) {
	return nil, nil
}
func (s *stubLoaderActivities) LoadPlayerHeroImageAssets(_ context.Context) ([]asset.Asset, error) {
	return nil, nil
}
func (s *stubLoaderActivities) LoadPlayerYahooImageSmallAssets(_ context.Context) ([]asset.Asset, error) {
	return nil, nil
}
func (s *stubLoaderActivities) LoadPlayerYahooImageMediumAssets(_ context.Context) ([]asset.Asset, error) {
	return nil, nil
}
func (s *stubLoaderActivities) LoadPlayerYahooImageLargeAssets(_ context.Context) ([]asset.Asset, error) {
	return nil, nil
}
func (s *stubLoaderActivities) LoadTeamLogoAssets(_ context.Context) ([]asset.Asset, error) {
	return nil, nil
}
func (s *stubLoaderActivities) LoadYahooTeamLogoAssets(_ context.Context) ([]asset.Asset, error) {
	return nil, nil
}
func (s *stubLoaderActivities) LoadYahooLeagueLogoAssets(_ context.Context) ([]asset.Asset, error) {
	return nil, nil
}
func (s *stubLoaderActivities) LoadYahooManagerImageAssets(_ context.Context) ([]asset.Asset, error) {
	return nil, nil
}

// ─── Test suite ───────────────────────────────────────────────────────────────

type FetchAssetsClassWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env  *testsuite.TestWorkflowEnvironment
	stub *stubLoaderActivities
}

func (s *FetchAssetsClassWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.stub = &stubLoaderActivities{}

	s.env.RegisterWorkflow(FetchAssetsClassWorkflow)
	s.env.RegisterWorkflow(FetchPlayerHeadshotsWorkflow)

	// Register all stub loaders so OnActivity-by-name works.
	s.env.RegisterActivity(s.stub.LoadPlayerHeadshotAssets)
	s.env.RegisterActivity(s.stub.LoadPlayerHeroImageAssets)
	s.env.RegisterActivity(s.stub.LoadPlayerYahooImageSmallAssets)
	s.env.RegisterActivity(s.stub.LoadPlayerYahooImageMediumAssets)
	s.env.RegisterActivity(s.stub.LoadPlayerYahooImageLargeAssets)
	s.env.RegisterActivity(s.stub.LoadTeamLogoAssets)
	s.env.RegisterActivity(s.stub.LoadYahooTeamLogoAssets)
	s.env.RegisterActivity(s.stub.LoadYahooLeagueLogoAssets)
	s.env.RegisterActivity(s.stub.LoadYahooManagerImageAssets)

	// Register FetchAssetBatch via a nil-receiver pointer (Temporal resolves the
	// method name for registration purposes, matching how cmd/worker.go does it).
	var act *asset.Activities
	s.env.RegisterActivity(act.FetchAssetBatch)
}

func (s *FetchAssetsClassWorkflowTestSuite) AfterTest(_, _ string) {
	s.env.AssertExpectations(s.T())
}

func TestFetchAssetsClassWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(FetchAssetsClassWorkflowTestSuite))
}

// ─── Helper ───────────────────────────────────────────────────────────────────

// makeAssets builds a slice of len n player headshot assets with distinct URLs.
func makeAssets(n int) []asset.Asset {
	assets := make([]asset.Asset, n)
	for i := range assets {
		assets[i] = asset.NewPlayerHeadshot(
			nhl.PlayerID(8470000+i),
			"https://assets.nhle.com/mugs/nhl/20242025/test.png",
		)
	}
	return assets
}

// batchCount returns the number of batches for n assets at the given batchSize.
func batchCount(n, batchSize int) int {
	if n == 0 {
		return 0
	}
	return (n + batchSize - 1) / batchSize
}

// ─── Tests ────────────────────────────────────────────────────────────────────

// TestFetchAssetsClassWorkflow_Success exercises the happy path with 250 assets
// (3 batches at default batch size 100). Mixed origins verify aggregation.
func (s *FetchAssetsClassWorkflowTestSuite) TestFetchAssetsClassWorkflow_Success() {
	const numAssets = 250
	assets := makeAssets(numAssets)

	// The loader returns 250 assets.
	s.env.OnActivity("LoadPlayerHeadshotAssets", mock.Anything).
		Return(assets, nil)

	// Each FetchAssetBatch call returns one OriginRemoteNHLCDN per input asset.
	// The mock captures the actual input batch size so the result length matches.
	var act *asset.Activities
	s.env.OnActivity(act.FetchAssetBatch, mock.Anything, mock.MatchedBy(func(_ asset.FetchAssetBatchInput) bool { return true })).
		Return(func(_ context.Context, in asset.FetchAssetBatchInput) (asset.FetchAssetBatchResult, error) {
			r := make([]asset.FetchAssetResult, len(in.Assets))
			for i := range r {
				r[i] = asset.FetchAssetResult{Origin: core.OriginRemoteNHLCDN}
			}
			return asset.FetchAssetBatchResult{Results: r}, nil
		}).
		Times(batchCount(numAssets, asset.DefaultAssetBatchSize))

	input := FetchAssetsClassInput{
		Class:          classPlayerHeadshots,
		RefreshCurrent: false,
	}
	s.env.ExecuteWorkflow(FetchAssetsClassWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var counts core.OriginCounts
	s.NoError(s.env.GetWorkflowResult(&counts))
	// 2 full batches of 100 + 1 batch of 50; each result reports one OriginRemoteNHLCDN
	// per asset (100+100+50 = 250).
	s.Equal(250, counts[core.OriginRemoteNHLCDN])
}

// TestFetchAssetsClassWorkflow_EmptyAssets verifies that an empty loader result
// completes cleanly without touching FetchAssetBatch.
func (s *FetchAssetsClassWorkflowTestSuite) TestFetchAssetsClassWorkflow_EmptyAssets() {
	s.env.OnActivity("LoadPlayerHeadshotAssets", mock.Anything).
		Return([]asset.Asset{}, nil)

	input := FetchAssetsClassInput{
		Class:          classPlayerHeadshots,
		RefreshCurrent: false,
	}
	s.env.ExecuteWorkflow(FetchAssetsClassWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var counts core.OriginCounts
	s.NoError(s.env.GetWorkflowResult(&counts))
	s.Empty(counts)
}

// TestFetchAssetsClassWorkflow_PerRowErrors verifies that per-row errors are
// logged but do not fail the workflow or appear in OriginCounts.
func (s *FetchAssetsClassWorkflowTestSuite) TestFetchAssetsClassWorkflow_PerRowErrors() {
	assets := makeAssets(10)
	s.env.OnActivity("LoadPlayerHeadshotAssets", mock.Anything).
		Return(assets, nil)

	var act *asset.Activities
	// Return a batch with some errors and some successes.
	s.env.OnActivity(act.FetchAssetBatch, mock.Anything, mock.Anything).
		Return(asset.FetchAssetBatchResult{
			Results: []asset.FetchAssetResult{
				{Err: "404 not found"},
				{Origin: core.OriginRemoteNHLCDN},
				{Err: "invalid MIME type"},
				{Origin: core.OriginFileSystem},
				{Origin: core.OriginRemoteNHLCDN},
				{Origin: core.OriginRemoteNHLCDN},
				{Err: "parse error"},
				{Origin: core.OriginRemoteNHLCDN},
				{Origin: core.OriginFileSystem},
				{Origin: core.OriginRemoteNHLCDN},
			},
		}, nil)

	input := FetchAssetsClassInput{
		Class:          classPlayerHeadshots,
		RefreshCurrent: false,
	}
	s.env.ExecuteWorkflow(FetchAssetsClassWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var counts core.OriginCounts
	s.NoError(s.env.GetWorkflowResult(&counts))
	// 5 OriginRemoteNHLCDN, 2 OriginFileSystem; 3 errors and 0 OriginUnknown omitted.
	s.Equal(5, counts[core.OriginRemoteNHLCDN])
	s.Equal(2, counts[core.OriginFileSystem])
}

// TestFetchAssetsClassWorkflow_RefreshCurrentPropagates verifies that the
// RefreshCurrent flag is forwarded to each FetchAssetBatch invocation.
func (s *FetchAssetsClassWorkflowTestSuite) TestFetchAssetsClassWorkflow_RefreshCurrentPropagates() {
	assets := makeAssets(5)
	s.env.OnActivity("LoadPlayerHeadshotAssets", mock.Anything).
		Return(assets, nil)

	var act *asset.Activities
	// The mock captures the batch input so we can assert RefreshCurrent.
	var capturedInput asset.FetchAssetBatchInput
	s.env.OnActivity(act.FetchAssetBatch, mock.Anything, mock.MatchedBy(func(in asset.FetchAssetBatchInput) bool {
		capturedInput = in
		return true
	})).Return(asset.FetchAssetBatchResult{
		Results: make([]asset.FetchAssetResult, len(assets)),
	}, nil)

	input := FetchAssetsClassInput{
		Class:          classPlayerHeadshots,
		RefreshCurrent: true,
	}
	s.env.ExecuteWorkflow(FetchAssetsClassWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.True(capturedInput.RefreshCurrent, "RefreshCurrent should propagate to batch input")
}

// TestFetchAssetsClassWorkflow_LoaderError verifies that a loader activity error
// causes the workflow to fail.
func (s *FetchAssetsClassWorkflowTestSuite) TestFetchAssetsClassWorkflow_LoaderError() {
	s.env.OnActivity("LoadPlayerHeadshotAssets", mock.Anything).
		Return(([]asset.Asset)(nil), errTest("database unavailable"))

	input := FetchAssetsClassInput{
		Class:          classPlayerHeadshots,
		RefreshCurrent: false,
	}
	s.env.ExecuteWorkflow(FetchAssetsClassWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// TestPerClassWrappers verifies every entry-point wrapper dispatches to
// FetchAssetsClassWorkflow with the correct assetClass — proven by checking
// which loader activity gets invoked. The wrappers are mechanical (one per
// class) and exist for Temporal-UI clarity, not behavior, so the right way
// to lock them in place is "wrapper N → loader N", not output assertions.
//
// If anyone reorders the assetClass constants or copy-pastes a wrapper with
// the wrong Class field, exactly one subtest will fail with a clear name.
func TestPerClassWrappers(t *testing.T) {
	cases := []struct {
		name           string
		wrapper        any
		expectedLoader string
	}{
		{"PlayerHeadshots", FetchPlayerHeadshotsWorkflow, "LoadPlayerHeadshotAssets"},
		{"PlayerHeroImages", FetchPlayerHeroImagesWorkflow, "LoadPlayerHeroImageAssets"},
		{"PlayerYahooImagesSmall", FetchPlayerYahooImagesSmallWorkflow, "LoadPlayerYahooImageSmallAssets"},
		{"PlayerYahooImagesMedium", FetchPlayerYahooImagesMediumWorkflow, "LoadPlayerYahooImageMediumAssets"},
		{"PlayerYahooImagesLarge", FetchPlayerYahooImagesLargeWorkflow, "LoadPlayerYahooImageLargeAssets"},
		{"TeamLogos", FetchTeamLogosWorkflow, "LoadTeamLogoAssets"},
		{"YahooTeamLogos", FetchYahooTeamLogosWorkflow, "LoadYahooTeamLogoAssets"},
		{"YahooLeagueLogos", FetchYahooLeagueLogosWorkflow, "LoadYahooLeagueLogoAssets"},
		{"YahooManagerImages", FetchYahooManagerImagesWorkflow, "LoadYahooManagerImageAssets"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ts testsuite.WorkflowTestSuite
			env := ts.NewTestWorkflowEnvironment()

			stub := &stubLoaderActivities{}
			// Both the wrapper and the generic workflow it delegates to must
			// be registered for ExecuteWorkflow to resolve names.
			env.RegisterWorkflow(FetchAssetsClassWorkflow)
			env.RegisterWorkflow(tc.wrapper)
			env.RegisterActivity(stub.LoadPlayerHeadshotAssets)
			env.RegisterActivity(stub.LoadPlayerHeroImageAssets)
			env.RegisterActivity(stub.LoadPlayerYahooImageSmallAssets)
			env.RegisterActivity(stub.LoadPlayerYahooImageMediumAssets)
			env.RegisterActivity(stub.LoadPlayerYahooImageLargeAssets)
			env.RegisterActivity(stub.LoadTeamLogoAssets)
			env.RegisterActivity(stub.LoadYahooTeamLogoAssets)
			env.RegisterActivity(stub.LoadYahooLeagueLogoAssets)
			env.RegisterActivity(stub.LoadYahooManagerImageAssets)

			// The expected loader gets a one-shot mock returning empty (so the
			// workflow completes immediately without dispatching FetchAssetBatch).
			// If the wrapper dispatches to the wrong class, this loader's
			// expectation goes unmet and AssertExpectations fails.
			env.OnActivity(tc.expectedLoader, mock.Anything).
				Return([]asset.Asset{}, nil).Once()

			env.ExecuteWorkflow(tc.wrapper, FetchAssetsInput{})

			require.True(t, env.IsWorkflowCompleted())
			require.NoError(t, env.GetWorkflowError())
			env.AssertExpectations(t)
		})
	}
}

// TestSplitAssetBatches verifies the batching helper directly (pure function).
func TestSplitAssetBatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		n         int
		batchSize int
		wantLen   int
		wantLast  int
	}{
		{"exact multiple", 300, 100, 3, 100},
		{"remainder", 250, 100, 3, 50},
		{"single", 50, 100, 1, 50},
		{"empty", 0, 100, 0, 0},
		{"one item", 1, 100, 1, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assets := makeAssets(tc.n)
			batches := splitAssetBatches(assets, tc.batchSize)
			require.Len(t, batches, tc.wantLen)
			if tc.wantLen > 0 {
				require.Len(t, batches[tc.wantLen-1], tc.wantLast)
			}
		})
	}
}

// ─── errTest helper ───────────────────────────────────────────────────────────

type testError string

func errTest(msg string) testError { return testError(msg) }
func (e testError) Error() string  { return string(e) }
