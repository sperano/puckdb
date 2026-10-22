package nhl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"go.temporal.io/sdk/activity"
)

// DayImporterVersion identifies what ImportDay writes from a day's cached
// files. Bump it whenever the same files would now produce different rows: a
// new or changed column mapping, a new table filled per day, a parser
// dependency upgrade that changes parsed values, or a migration that drops
// or rebuilds day-level rows. The next import then re-imports every day once.
//
// Version 2: Yahoo summaries also write every category to
// yahoo_team_summary_stats, and each league's standings snapshot fills the
// summaries' standings columns.
const DayImporterVersion = 2

// Reasons ImportDayIfChanged logs for importing a day; dayImportReasonNoChange
// is the skip verdict and never logged.
const (
	dayImportReasonForced   = "forced"
	dayImportReasonFirst    = "never imported"
	dayImportReasonVersion  = "importer version changed"
	dayImportReasonInputs   = "cached files changed"
	dayImportReasonNoChange = ""
)

// DayImportStore records the days ImportDayIfChanged imported.
type DayImportStore interface {
	GetNHLDayImport(ctx context.Context, arg sqlcdb.GetNHLDayImportParams) (sqlcdb.GetNHLDayImportRow, error)
	UpsertNHLDayImport(ctx context.Context, arg sqlcdb.UpsertNHLDayImportParams) error
}

// ImportDayIfChangedInput is ImportDayInput plus the full re-import switch.
type ImportDayIfChangedInput struct {
	ImportDayInput
	// Force imports the day even when its cached files and the importer
	// version match its last successful import.
	Force bool `json:"force"`
}

// ImportDayIfChangedResult reports one day of ImportDayIfChanged.
type ImportDayIfChangedResult struct {
	Origins core.OriginCounts `json:"origins"`
	// Skipped is true when the day was left alone because nothing it reads
	// changed since its last successful import.
	Skipped bool `json:"skipped"`
	// SkippedRows counts rows the import skipped after an error; a day with
	// any is not recorded, so the next run imports it again.
	SkippedRows int `json:"skippedRows"`
}

// dayFingerprint identifies the cached files a day import reads.
type dayFingerprint struct {
	Hash  string
	Files int
}

// ImportDayIfChanged runs ImportDay unless the day's cached files and
// DayImporterVersion are unchanged since its last successful import, then
// records what it imported. The fingerprint is taken before the import reads
// anything, so a file rewritten during the import leaves the older
// fingerprint behind and the next run imports the day again. Both read the
// cached files from storage, not Redis: the fingerprint stats the files in
// storage, and a Redis entry that outlived a rewrite of its file would let the
// import write the old contents under the new fingerprint, so every later run
// would skip the day.
func (a *ImportActivities) ImportDayIfChanged(ctx context.Context, input ImportDayIfChangedInput) (ImportDayIfChangedResult, error) {
	defer metrics.TrackActivityDuration("ImportDayIfChanged")()
	files := a.withStorageFirstReads()
	dayLabel := input.Date.Format(config.DateFormat)
	fingerprint, err := files.dayInputsFingerprint(ctx, input.ImportDayInput)
	if err != nil {
		return ImportDayIfChangedResult{}, fmt.Errorf("fingerprint cached files for %s: %w", dayLabel, err)
	}
	reason := dayImportReasonForced
	if !input.Force {
		if reason, err = a.dayImportReason(ctx, input.ImportDayInput, fingerprint); err != nil {
			return ImportDayIfChangedResult{}, fmt.Errorf("read import record for %s: %w", dayLabel, err)
		}
	}
	logger := activity.GetLogger(ctx)
	if reason == dayImportReasonNoChange {
		logger.Debug("Day unchanged since its last import, skipped", "date", dayLabel)
		return ImportDayIfChangedResult{Origins: core.OriginCounts{}, Skipped: true}, nil
	}
	imported, err := files.importDay(ctx, input.ImportDayInput)
	result := ImportDayIfChangedResult{Origins: imported.Origins, SkippedRows: imported.SkippedRows}
	if err != nil {
		return result, err
	}
	if imported.SkippedRows > 0 {
		logger.Warn("Imported day with skipped rows, not recorded so the next run retries it",
			"date", dayLabel, "reason", reason, "skippedRows", imported.SkippedRows)
		return result, nil
	}
	if err := a.DayImports.UpsertNHLDayImport(ctx, sqlcdb.UpsertNHLDayImportParams{
		Season:          int32(input.SeasonID),
		Date:            dayImportDate(input.Date),
		ImporterVersion: DayImporterVersion,
		InputsHash:      fingerprint.Hash,
		InputFiles:      int32(fingerprint.Files),
	}); err != nil {
		return result, fmt.Errorf("record import of %s: %w", dayLabel, err)
	}
	logger.Info("Imported day", "date", dayLabel, "reason", reason, "files", fingerprint.Files)
	return result, nil
}

// withStorageFirstReads returns a copy of a whose cached-file reads, its own
// and its Yahoo importer's, parse storage and refresh Redis instead of trusting
// a Redis entry (see cache.GobCache.StorageFirst and ImportDayIfChanged).
func (a *ImportActivities) withStorageFirstReads() *ImportActivities {
	view := *a
	view.GobCache = a.GobCache.StorageFirst()
	if a.Yahoo != nil {
		yahooView := *a.Yahoo
		yahooView.GobCache = a.Yahoo.GobCache.StorageFirst()
		view.Yahoo = &yahooView
	}
	return &view
}

// dayImportReason compares the day's last import record with the current
// fingerprint and importer version; dayImportReasonNoChange means skip.
func (a *ImportActivities) dayImportReason(ctx context.Context, input ImportDayInput, fingerprint dayFingerprint) (string, error) {
	record, err := a.DayImports.GetNHLDayImport(ctx, sqlcdb.GetNHLDayImportParams{
		Season: int32(input.SeasonID),
		Date:   dayImportDate(input.Date),
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return dayImportReasonFirst, nil
	case err != nil:
		return "", err
	case record.ImporterVersion != DayImporterVersion:
		return dayImportReasonVersion, nil
	case record.InputsHash != fingerprint.Hash:
		return dayImportReasonInputs, nil
	default:
		return dayImportReasonNoChange, nil
	}
}

func dayImportDate(day time.Time) pgtype.Date {
	return pgtype.Date{Time: day, Valid: true}
}

// dayInputsFingerprint hashes the path, size and modification time of every
// cached file ImportDay reads for the day, and the absence of the ones it
// would read if they existed (a game's files appear once it is final). The
// storage writes each file to a temporary name and renames it into place, so
// a re-fetched file always gets a new modification time.
func (a *ImportActivities) dayInputsFingerprint(ctx context.Context, input ImportDayInput) (dayFingerprint, error) {
	inputs, err := a.dayInputs(ctx, input)
	if err != nil {
		return dayFingerprint{}, err
	}
	h := sha256.New()
	for _, r := range inputs {
		if err := writeFileFingerprint(ctx, h, a.Storage, r); err != nil {
			return dayFingerprint{}, err
		}
	}
	return dayFingerprint{Hash: hex.EncodeToString(h.Sum(nil)), Files: len(inputs)}, nil
}

func writeFileFingerprint(ctx context.Context, h hash.Hash, s store.Storage, r core.Resource) error {
	info, err := resource.Stat(ctx, s, r)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		_, err = fmt.Fprintf(h, "%s\tmissing\n", r.Path())
	case err != nil:
		return fmt.Errorf("stat %s: %w", r.Path(), err)
	default:
		_, err = fmt.Fprintf(h, "%s\t%d\t%d\n", r.Path(), info.Size(), info.ModTime().UnixNano())
	}
	return err
}

// dayInputs lists the cached files ImportDay reads for the day: the daily
// schedule, every scheduled game's five game files, the daily standings and
// the Yahoo files of yahoo.DateInputs (each team's roster and summary, each
// league's standings snapshot).
func (a *ImportActivities) dayInputs(ctx context.Context, input ImportDayInput) ([]core.Resource, error) {
	scheduleRes := resource.DailySchedule{Date: input.Date}
	inputs := []core.Resource{scheduleRes}
	if resource.Exists(ctx, a.Storage, scheduleRes) {
		schedule, _, err := a.GobCache.ReadParsedCached(ctx, a.Storage, scheduleRes)
		if err != nil {
			return nil, fmt.Errorf("read daily schedule: %w", err)
		}
		for _, game := range schedule.Games {
			inputs = append(inputs,
				resource.Boxscore{Date: input.Date, GameID: game.ID},
				resource.GameStory{Date: input.Date, GameID: game.ID},
				resource.PlayByPlay{Date: input.Date, GameID: game.ID},
				resource.ShiftChart{Date: input.Date, GameID: game.ID},
				resource.SeasonSeries{Date: input.Date, GameID: game.ID},
			)
		}
	}
	inputs = append(inputs, resource.DailyStandings{Date: input.Date})
	inputs = append(inputs, yahoo.DateInputs(yahooDateInput(input))...)
	return inputs, nil
}
