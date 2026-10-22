// Package worker is the root package for Temporal worker subpackages.
// Workflows live in the workflow subpackage; activities are split by data
// domain into nhl, yahoo, player, admin, asset, newsfeed, draftranking and
// simulation. shared holds helpers (progress tracking, config snapshots,
// worker pools) used by workflows and activities across those subpackages.
package worker
