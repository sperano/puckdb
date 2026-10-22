// Package worker is the root package for Temporal worker subpackages. Most
// activities are split by data domain into nhl, yahoo, player, asset,
// newsfeed and draftranking, with their workflows in the sibling workflow
// package. admin (database/Redis administration, not a data domain) and
// simulation define their own workflows and activities instead of using
// workflow. shared holds helpers (progress tracking, config snapshots,
// worker pools) used by workflows and activities across these subpackages.
package worker
