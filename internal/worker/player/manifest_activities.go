package player

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/store"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

// CreateManifestInput names the manifest of a ProcessPlayers chain.
type CreateManifestInput struct {
	ManifestID string // the chain's FirstRunID
	WorkflowID string
}

// CreatedManifest is the manifest CreateProcessPlayersManifest created or
// found, with the start years of the seasons its list was extracted from
// (nil: unknown). ManifestRef is embedded so a result recorded before Seasons
// existed decodes unchanged.
type CreatedManifest struct {
	ManifestRef
	Seasons []int
}

// CreateProcessPlayersManifest copies the consolidated boxscore player list
// and its seasons from Redis into a durable manifest ordered by player ID.
// When the manifest already exists (a retry after a commit, or an attempt
// overlapping a timed out one) it returns it without reading Redis again, so
// the chain keeps the list it started with even if the Redis key changed in
// between. A missing or empty Redis list returns Count 0 and stores nothing.
func (a *Activities) CreateProcessPlayersManifest(ctx context.Context, input CreateManifestInput) (CreatedManifest, error) {
	logger := activity.GetLogger(ctx)
	ref, err := a.Manifests.GetManifest(ctx, input.ManifestID)
	if err == nil {
		logger.Info("Process players manifest already exists", "manifest", ref.ID, "players", ref.Count)
		return a.withManifestSeasons(ctx, ref)
	}
	if !errors.Is(err, ErrManifestNotFound) {
		return CreatedManifest{}, err
	}

	players, seasons, err := cache.LoadConsolidatedBoxscorePlayers(ctx, a.RedisClient)
	if err != nil {
		return CreatedManifest{}, err
	}
	if len(players) == 0 {
		return CreatedManifest{ManifestRef: ManifestRef{ID: input.ManifestID}}, nil
	}
	if seasons == nil {
		logger.Warn("Boxscore player list has no saved seasons; phase 3 will verify every unmatched Yahoo ID")
	}
	sorted := sortManifestPlayers(players)
	ref, err = a.Manifests.CreateManifest(ctx, input.ManifestID, input.WorkflowID, sorted, hashManifestPlayers(sorted), seasons)
	if err != nil {
		return CreatedManifest{}, err
	}
	logger.Info("Created process players manifest", "manifest", ref.ID, "players", ref.Count, "hash", ref.Hash,
		"seasons", seasons)
	return a.withManifestSeasons(ctx, ref)
}

// withManifestSeasons adds the stored seasons to ref: an existing manifest
// keeps the seasons it was created with.
func (a *Activities) withManifestSeasons(ctx context.Context, ref ManifestRef) (CreatedManifest, error) {
	seasons, err := a.Manifests.ManifestSeasons(ctx, ref.ID)
	if err != nil {
		return CreatedManifest{}, err
	}
	return CreatedManifest{ManifestRef: ref, Seasons: seasons}, nil
}

// ManifestBatchInput is one batch of a ProcessPlayers chain: the manifest
// ordinals [From, To), the Yahoo ID pool size the chain saved (a pool
// holding fewer entries is rebuilt first), and the game list seasons it read.
type ManifestBatchInput struct {
	Manifest         ManifestRef
	From             int
	To               int
	YahooPoolSize    int
	YahooListSeasons []int
}

// yahooPool is the pool check the batch carries.
func (input ManifestBatchInput) yahooPool() EnsureYahooIDPoolInput {
	return EnsureYahooIDPoolInput{
		PoolSize: input.YahooPoolSize, ListSeasons: input.YahooListSeasons,
	}
}

// ManifestBatchResult is ProcessPlayerBatchResult (without Errors) plus the
// batch's per-player errors with their stage.
type ManifestBatchResult struct {
	ProcessPlayerBatchResult
	PlayerErrors []PlayerError
}

// ProcessPlayerManifestBatch processes the players at manifest ordinals
// [From, To) like ProcessPlayerBatch. It does not record errors: the
// workflow records them from the completed result, so a timed-out attempt
// that kept running cannot add errors the workflow never counted.
func (a *Activities) ProcessPlayerManifestBatch(ctx context.Context, input ManifestBatchInput) (ManifestBatchResult, error) {
	players, err := a.loadManifestWindow(ctx, input)
	if err != nil {
		return ManifestBatchResult{}, err
	}
	out, err := a.processPlayerBatch(ctx, players, input.yahooPool())
	return ManifestBatchResult{ProcessPlayerBatchResult: out.result, PlayerErrors: out.errors}, err
}

// loadManifestWindow returns the batch's players after checking that the
// stored manifest is still the one the chain carries.
func (a *Activities) loadManifestWindow(ctx context.Context, input ManifestBatchInput) ([]store.BoxscorePlayer, error) {
	ref := input.Manifest
	if input.From < 0 || input.From >= input.To || input.To > ref.Count {
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("manifest %s: window [%d, %d) outside [0, %d)", ref.ID, input.From, input.To, ref.Count),
			InvalidManifestWindowErrorType, nil)
	}
	stored, err := a.Manifests.GetManifest(ctx, ref.ID)
	if errors.Is(err, ErrManifestNotFound) {
		return nil, manifestMissingError(ref.ID, "not found")
	}
	if err != nil {
		return nil, err
	}
	if stored != ref {
		return nil, manifestMissingError(ref.ID, fmt.Sprintf("changed (stored %d players, hash %s; carried %d, %s)",
			stored.Count, stored.Hash, ref.Count, ref.Hash))
	}
	players, err := a.Manifests.ManifestPlayers(ctx, ref.ID, input.From, input.To)
	if err != nil {
		return nil, err
	}
	if len(players) != input.To-input.From {
		return nil, manifestMissingError(ref.ID, fmt.Sprintf("holds %d players in [%d, %d)", len(players), input.From, input.To))
	}
	return players, nil
}

// RecordErrorsInput is the per-player errors of one completed batch.
type RecordErrorsInput struct {
	ManifestID string
	Errors     []PlayerError
}

// RecordProcessPlayersErrors upserts a completed batch's errors into the
// manifest's error table and logs each one, so the worker log keeps every
// error even though the workflow carries only a sample. Retried or
// overlapping attempts with the same input converge on the same rows. The
// errors are logged before the write so they reach the log even when the
// write fails for good (the manifest is gone); a retried attempt logs them
// again, with its attempt number.
func (a *Activities) RecordProcessPlayersErrors(ctx context.Context, input RecordErrorsInput) error {
	attempt := activity.GetInfo(ctx).Attempt
	for _, e := range input.Errors {
		log.Error().Str("manifest", input.ManifestID).Int64("player_id", e.PlayerID).Str("stage", e.Stage).
			Int32("attempt", attempt).Str("error", e.Message).Msg("ProcessPlayers per-player error")
	}
	err := a.Manifests.RecordErrors(ctx, input.ManifestID, input.Errors)
	switch {
	case errors.Is(err, ErrManifestNotFound):
		return manifestMissingError(input.ManifestID, "not found")
	case errors.Is(err, ErrPlayerNotInManifest):
		return temporal.NewNonRetryableApplicationError(err.Error(), PlayerNotInManifestErrorType, err)
	}
	return err
}

// FinishProcessPlayersManifest marks the manifest finished and returns how
// many errors it recorded.
func (a *Activities) FinishProcessPlayersManifest(ctx context.Context, manifestID string) (int, error) {
	count, err := a.Manifests.FinishManifest(ctx, manifestID)
	if errors.Is(err, ErrManifestNotFound) {
		return 0, manifestMissingError(manifestID, "not found")
	}
	return count, err
}
