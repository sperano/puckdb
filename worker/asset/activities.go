package asset

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/httpx"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/store"
	"github.com/sperano/puckdb/worker/shared"
	"go.temporal.io/sdk/activity"
)

// Batch and validation constants.
const (
	// DefaultAssetBatchSize is the default number of assets processed per
	// FetchAssetBatch activity invocation.
	DefaultAssetBatchSize = 100

	// heartbeatEveryNRows is how often FetchAssetBatch records a Temporal
	// heartbeat while processing rows.
	heartbeatEveryNRows = 10

	// minimumAssetBytes is the minimum size in bytes for a valid asset.
	// Responses shorter than this are rejected even if the MIME type is correct.
	minimumAssetBytes = 100

	// mimeSniffLen is the byte window http.DetectContentType examines.
	// Matches the stdlib's documented sniff window.
	mimeSniffLen = 512
)

// validImageMIMETypes is the set of MIME types accepted for raster images via
// http.DetectContentType. SVG is handled separately via byte sniffing because
// http.DetectContentType returns text/xml or text/html for SVG content, never
// image/svg+xml. See validateAssetBytes.
var validImageMIMETypes = map[string]struct{}{
	"image/png":  {},
	"image/jpeg": {},
	"image/gif":  {},
	"image/webp": {},
}

// assetOrigins maps each asset FileType to the DataOrigin that represents a
// successful remote fetch. NHL types use OriginRemoteNHLCDN; Yahoo types use
// OriginRemoteYahooCDN.
var assetOrigins = map[core.FileType]core.DataOrigin{
	core.PlayerHeadshot:    core.OriginRemoteNHLCDN,
	core.PlayerHeroImage:   core.OriginRemoteNHLCDN,
	core.TeamLogo:          core.OriginRemoteNHLCDN,
	core.PlayerYahooImage:  core.OriginRemoteYahooCDN,
	core.YahooTeamLogo:     core.OriginRemoteYahooCDN,
	core.YahooLeagueLogo:   core.OriginRemoteYahooCDN,
	core.YahooManagerImage: core.OriginRemoteYahooCDN,
}

// assetQueries defines the database query methods required by Activities.
// Each List method corresponds to a generated sqlc query that returns the
// (id, url) pair(s) for one asset class. The matching Count method returns
// the row count for progress-bar sizing in the parent FetchAssetsWorkflow.
// *sqlcdb.Queries satisfies this interface implicitly.
type assetQueries interface {
	ListPlayerHeadshots(ctx context.Context) ([]sqlcdb.ListPlayerHeadshotsRow, error)
	ListPlayerHeroImages(ctx context.Context) ([]sqlcdb.ListPlayerHeroImagesRow, error)
	ListPlayerYahooImages(ctx context.Context) ([]sqlcdb.ListPlayerYahooImagesRow, error)
	ListTeamLogos(ctx context.Context) ([]sqlcdb.ListTeamLogosRow, error)
	ListYahooTeamLogos(ctx context.Context) ([]sqlcdb.ListYahooTeamLogosRow, error)
	ListYahooLeagueLogos(ctx context.Context) ([]sqlcdb.ListYahooLeagueLogosRow, error)
	ListYahooManagerImages(ctx context.Context) ([]sqlcdb.ListYahooManagerImagesRow, error)

	CountPlayerHeadshots(ctx context.Context) (int64, error)
	CountPlayerHeroImages(ctx context.Context) (int64, error)
	CountPlayerYahooImages(ctx context.Context) (int64, error)
	CountTeamLogos(ctx context.Context) (int64, error)
	CountYahooTeamLogos(ctx context.Context) (int64, error)
	CountYahooLeagueLogos(ctx context.Context) (int64, error)
	CountYahooManagerImages(ctx context.Context) (int64, error)
}

// Activities holds the dependencies for asset download activities.
type Activities struct {
	Storage  store.Storage
	Download shared.Downloader // function type, not httpx.Client (delta 6)
	Queries  assetQueries
}

// FetchAssetBatchInput is the input to the FetchAssetBatch activity.
type FetchAssetBatchInput struct {
	Assets         []Asset
	RefreshCurrent bool // when true, skip the Storage.Exists short-circuit (delta 10)
}

// FetchAssetResult is the per-row outcome for a single Asset in a batch.
type FetchAssetResult struct {
	Origin core.DataOrigin
	Err    string // empty on success
}

// FetchAssetBatchResult is the output of FetchAssetBatch, with one entry
// per input asset, in the same order.
type FetchAssetBatchResult struct {
	Results []FetchAssetResult // parallel to FetchAssetBatchInput.Assets
}

// heartbeatFunc is the type for recording a heartbeat. The default
// implementation calls activity.RecordHeartbeat; tests substitute a counter.
type heartbeatFunc func(ctx context.Context, msg string)

func defaultHeartbeat(ctx context.Context, msg string) {
	activity.RecordHeartbeat(ctx, msg)
}

// FetchAssetBatch downloads a batch of assets and writes them to Storage.
//
// The activity returns a non-nil error only for catastrophic failures (context
// cancellation, etc.). Per-row failures — bad URL extension, HTTP errors, MIME
// validation failures, storage write errors — are surfaced in Results[i].Err
// so that Temporal retries do not re-run the entire batch when a single URL is
// permanently dead.
func (a *Activities) FetchAssetBatch(ctx context.Context, in FetchAssetBatchInput) (FetchAssetBatchResult, error) {
	return a.fetchAssetBatch(ctx, in, defaultHeartbeat)
}

// fetchAssetBatch is the internal implementation, accepting an injectable
// heartbeat function so tests can verify cadence without relying on the
// Temporal SDK's coalescing behavior.
func (a *Activities) fetchAssetBatch(ctx context.Context, in FetchAssetBatchInput, heartbeat heartbeatFunc) (FetchAssetBatchResult, error) {
	out := FetchAssetBatchResult{
		Results: make([]FetchAssetResult, len(in.Assets)),
	}

	for i, asset := range in.Assets {
		// Respect context cancellation between rows; this is the only place we
		// return a hard error to abort the whole batch.
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		default:
		}

		res, err := a.processAsset(ctx, asset, in.RefreshCurrent)
		if err != nil {
			// processAsset returns a non-nil error only for context cancellation
			// (or deadline). Abort the whole batch so Temporal retries the
			// unprocessed remainder, rather than recording the cancellation as a
			// spurious permanent per-row failure and continuing.
			return out, err
		}
		out.Results[i] = res

		// Heartbeat every heartbeatEveryNRows rows so Temporal knows the activity
		// is still alive during large batches.
		if (i+1)%heartbeatEveryNRows == 0 {
			heartbeat(ctx, fmt.Sprintf("%d/%d", i+1, len(in.Assets)))
		}
	}

	return out, nil
}

// processAsset executes the full per-row pipeline for a single asset.
//
// The returned error is non-nil only for context cancellation (context.Canceled
// or context.DeadlineExceeded), which is a batch-level abort condition: the
// caller must stop processing and let Temporal retry the remaining rows. All
// other per-row failures — bad URL extension, HTTP errors, MIME validation
// failures, non-context storage write errors — are encoded in
// FetchAssetResult.Err with a nil error so the batch keeps running.
func (a *Activities) processAsset(ctx context.Context, ast Asset, refreshCurrent bool) (FetchAssetResult, error) {
	// Step 1: empty URL — defensive no-op; Phase 3 SQL filters before dispatch.
	if ast.URL == "" {
		return FetchAssetResult{Origin: core.OriginUnknown}, nil
	}

	// Step 2: compute the local cache path from (FileType, IDs, URL extension).
	localPath, err := ast.Path()
	if err != nil {
		return FetchAssetResult{Err: err.Error()}, nil
	}

	// Step 3: idempotency check — skip download if the file is already cached
	// (unless RefreshCurrent overrides this).
	if !refreshCurrent && a.Storage.Exists(ctx, localPath) {
		metrics.IncDownload(ast.FileType, metrics.ResultHit)
		return FetchAssetResult{Origin: core.OriginFileSystem}, nil
	}

	// Step 4: download the asset bytes.
	data, err := a.Download(ctx, ast.URL)
	if err != nil {
		// Context cancellation must abort the batch, not be stringified into a
		// per-row Err entry. Surface it as the batch error.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return FetchAssetResult{}, err
		}
		var httpErr *httpx.HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			// 404 — URL is permanently dead. Record via metric; do not write to
			// disk. This is a per-row outcome, not a batch failure.
			metrics.IncDownload(ast.FileType, metrics.ResultMissing)
			return FetchAssetResult{Origin: core.OriginUnknown}, nil
		}
		// Other HTTP or I/O error.
		metrics.IncDownload(ast.FileType, metrics.ResultError)
		return FetchAssetResult{Err: err.Error()}, nil
	}

	// Step 5: validate bytes — MIME type and minimum size.
	if err := validateAssetBytes(data, localPath); err != nil {
		metrics.IncDownload(ast.FileType, metrics.ResultError)
		return FetchAssetResult{Err: fmt.Sprintf("validation: %s for %s", err, ast.URL)}, nil
	}

	// Step 6: write to storage.
	if err := a.Storage.Write(ctx, localPath, data); err != nil {
		// Context cancellation aborts the batch; surface as the batch error.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return FetchAssetResult{}, err
		}
		// Storage write failures often indicate broader trouble (full disk,
		// JuiceFS mount issues). Log a warning but keep the batch running.
		log.Warn().Err(err).Str("path", localPath).Msg("asset: storage write failed")
		metrics.IncDownload(ast.FileType, metrics.ResultError)
		return FetchAssetResult{Err: err.Error()}, nil
	}

	// Step 7: record success.
	origin := assetOrigins[ast.FileType]
	metrics.IncDownload(ast.FileType, metrics.ResultMiss)
	return FetchAssetResult{Origin: origin}, nil
}

// validateAssetBytes verifies that the response body looks like a valid image
// asset. http.DetectContentType returns image/* MIME types only for raster
// formats; SVG content sniffs as text/xml or text/html, so SVG is allowed only
// when the local cache path's extension is .svg AND the bytes look like SVG.
//
// Returns nil if validation passes. A non-nil error describes the failure mode
// (size, MIME, sniff mismatch) without including the URL — callers add URL
// context when stringifying.
func validateAssetBytes(data []byte, localPath string) error {
	if len(data) < minimumAssetBytes {
		return fmt.Errorf("response too small (%d bytes)", len(data))
	}

	sniffLen := min(len(data), mimeSniffLen)
	mimeType := http.DetectContentType(data[:sniffLen])

	// Raster image formats: direct MIME match.
	if _, ok := validImageMIMETypes[mimeType]; ok {
		return nil
	}

	// SVG: only when the URL extension says svg AND bytes look like SVG.
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(localPath)), ".")
	if ext == "svg" && looksLikeSVG(data[:sniffLen]) {
		return nil
	}

	return fmt.Errorf("unexpected MIME type %q", mimeType)
}

// svgLeadingTrim is the set of leading bytes stripped before SVG sniffing:
// space, tab, carriage return, line feed, and the UTF-8 BOM.
const svgLeadingTrim = " \t\r\n\xef\xbb\xbf"

// looksLikeSVG reports whether the given bytes appear to start an SVG document.
// Accepts both bare <svg> roots and XML-prologue-prefixed documents.
func looksLikeSVG(b []byte) bool {
	s := bytes.TrimLeft(b, svgLeadingTrim)
	if bytes.HasPrefix(s, []byte("<svg")) {
		return true
	}
	if bytes.HasPrefix(s, []byte("<?xml")) && bytes.Contains(s, []byte("<svg")) {
		return true
	}
	return false
}
