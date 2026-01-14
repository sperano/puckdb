package database

import (
	"context"
	"embed"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"github.com/sperano/yfh/config"
	"github.com/sperano/yfh/sqlcdb"
	"github.com/spf13/viper"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	log_ "log"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func getDSN(host string, user string, password string, dbname string, port int, sslmode string, timezone string) string {
	return fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%d sslmode=%s timezone=%s",
		host, user, password, dbname, port, sslmode, timezone)
}

func GetDSN() string {
	return getDSN(viper.GetString(config.FlagPostgresHost),
		viper.GetString(config.FlagPostgresUser),
		viper.GetString(config.FlagPostgresPassword),
		viper.GetString(config.FlagPostgresDatabase),
		viper.GetInt(config.FlagPostgresPort),
		viper.GetString(config.FlagPostgresSSLMode),
		viper.GetString(config.FlagPostgresTimeZone))
}

func getDSNForDisplay() string {
	return getDSN(viper.GetString(config.FlagPostgresHost),
		viper.GetString(config.FlagPostgresUser),
		strings.Repeat("*", len(viper.GetString(config.FlagPostgresPassword))),
		viper.GetString(config.FlagPostgresDatabase),
		viper.GetInt(config.FlagPostgresPort),
		viper.GetString(config.FlagPostgresSSLMode),
		viper.GetString(config.FlagPostgresTimeZone))
}

// GetDatabaseURL returns a PostgreSQL connection URL for pgx
func GetDatabaseURL() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		viper.GetString(config.FlagPostgresUser),
		viper.GetString(config.FlagPostgresPassword),
		viper.GetString(config.FlagPostgresHost),
		viper.GetInt(config.FlagPostgresPort),
		viper.GetString(config.FlagPostgresDatabase),
		viper.GetString(config.FlagPostgresSSLMode))
}

// OpenPGXPool opens a pgx connection pool for use with SQLC
func OpenPGXPool(ctx context.Context) (*pgxpool.Pool, error) {
	dbURL := GetDatabaseURL()
	log.Info().Str("host", viper.GetString(config.FlagPostgresHost)).Msg("Initializing pgx pool")

	poolConfig, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return nil, fmt.Errorf("parse pgx config: %w", err)
	}

	poolConfig.MaxConns = int32(viper.GetInt(config.FlagMaxOpenDBConns))
	poolConfig.MinConns = int32(viper.GetInt(config.FlagMaxIdleDBConns))
	poolConfig.MaxConnLifetime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create pgx pool: %w", err)
	}

	return pool, nil
}

// NewQueries creates a new SQLC Queries instance from a pgx pool
func NewQueries(pool *pgxpool.Pool) *sqlcdb.Queries {
	return sqlcdb.New(pool)
}

func OpenGorm() (*gorm.DB, error) {
	log.Info().Str("dsn", getDSNForDisplay()).Msg("Initializing GORM")
	newLogger := logger.New(
		log_.New(os.Stdout, "\n", log_.LstdFlags),
		logger.Config{
			SlowThreshold:             2 * time.Second, // Slow SQL threshold
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: false,
			Colorful:                  true,
		},
	)
	db, err := gorm.Open(postgres.Open(GetDSN()), &gorm.Config{Logger: newLogger})
	if err != nil {
		return nil, err
	}
	sqldb, err := db.DB()
	if err == nil {
		sqldb.SetMaxIdleConns(viper.GetInt(config.FlagMaxIdleDBConns))
		sqldb.SetMaxOpenConns(viper.GetInt(config.FlagMaxOpenDBConns))
		sqldb.SetConnMaxLifetime(5 * time.Minute) // TODO config
	} else {
		log.Warn().Msg(err.Error())
	}
	return db, nil
}

// RunSQLMigrations runs the embedded SQL migrations
func RunSQLMigrations() error {
	dbURL := GetDatabaseURL()
	log.Info().Msg("Running SQL migrations")

	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("create iofs source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", source, dbURL)
	if err != nil {
		return fmt.Errorf("create migrate instance: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("run migrations: %w", err)
	}

	version, dirty, _ := m.Version()
	log.Info().Uint("version", version).Bool("dirty", dirty).Msg("SQL migrations completed")
	return nil
}

// RunSQLMigrationsDown runs all down migrations
func RunSQLMigrationsDown() error {
	dbURL := GetDatabaseURL()
	log.Info().Msg("Running SQL down migrations")

	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("create iofs source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", source, dbURL)
	if err != nil {
		return fmt.Errorf("create migrate instance: %w", err)
	}
	defer m.Close()

	if err := m.Down(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("run down migrations: %w", err)
	}

	log.Info().Msg("SQL down migrations completed")
	return nil
}

func DoMigration(db *gorm.DB) error {
	log.Info().Msg("Starting database migration")

	// Run SQL migrations first (handles nhl_conferences, nhl_divisions, nhl_teams, players)
	if err := RunSQLMigrations(); err != nil {
		return err
	}

	// GORM AutoMigrate for remaining tables (not NHL tables - those are in SQL migrations)
	err := db.AutoMigrate(
		&League{},
		&StatDefinition{},
		&RosterPosition{},
		&PlayerStats{},
		&Game{},
		&Team{},
		&RosterPlayer{},
		&Standing{},
		&TeamSummary{},
	)
	if err != nil {
		return err
	}
	log.Info().Msg("Database migration completed")
	return nil
}

func DropEverything(db *gorm.DB) error {
	log.Info().Msg("Dropping all tables")
	migrator := db.Migrator()

	// Drop GORM-managed tables first (they may have FKs to SQL-managed tables)
	err := migrator.DropTable(
		"league_roster_positions",
		"league_stat_definitions",
		&TeamSummary{},
		&Standing{},
		&RosterPlayer{},
		&Team{},
		&Game{},
		&PlayerStats{},
		&RosterPosition{},
		&StatDefinition{},
		&League{},
	)
	if err != nil {
		return err
	}

	// Run SQL down migrations (handles players, nhl_teams, nhl_divisions, nhl_conferences)
	if err := RunSQLMigrationsDown(); err != nil {
		return err
	}

	// Drop the schema_migrations table used by golang-migrate
	if err := migrator.DropTable("schema_migrations"); err != nil {
		log.Warn().Err(err).Msg("Failed to drop schema_migrations table")
	}

	log.Info().Msg("All tables dropped")
	return nil
}

const (
	Eastern      = 1
	Western      = 2
	Atlantic     = 2
	Central      = 3
	Pacific      = 4
	Metropolitan = 7
)

// EnsureNHLWithSQLC seeds the NHL reference data using SQLC
func EnsureNHLWithSQLC(ctx context.Context, q *sqlcdb.Queries) error {
	if err := EnsureNHLConferencesWithSQLC(ctx, q); err != nil {
		return err
	}
	if err := EnsureNHLDivisionsWithSQLC(ctx, q); err != nil {
		return err
	}
	return EnsureNHLTeamsWithSQLC(ctx, q)
}

func EnsureNHLConferencesWithSQLC(ctx context.Context, q *sqlcdb.Queries) error {
	conferences := []sqlcdb.UpsertNHLConferenceParams{
		{ID: Eastern, Name: "Eastern"},
		{ID: Western, Name: "Western"},
	}
	for _, c := range conferences {
		if err := q.UpsertNHLConference(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

func EnsureNHLDivisionsWithSQLC(ctx context.Context, q *sqlcdb.Queries) error {
	divisions := []sqlcdb.UpsertNHLDivisionParams{
		{ID: Atlantic, Name: "Atlantic", NhlConferenceID: Eastern},
		{ID: Metropolitan, Name: "Metropolitan", NhlConferenceID: Eastern},
		{ID: Central, Name: "Central", NhlConferenceID: Western},
		{ID: Pacific, Name: "Pacific", NhlConferenceID: Western},
	}
	for _, d := range divisions {
		if err := q.UpsertNHLDivision(ctx, d); err != nil {
			return err
		}
	}
	return nil
}

func EnsureNHLTeamsWithSQLC(ctx context.Context, q *sqlcdb.Queries) error {
	// Teams use NHL API IDs as primary key and Yahoo IDs for cross-referencing
	// NHL API IDs from https://api.nhle.com/stats/rest/en/team
	teams := []sqlcdb.UpsertNHLTeamParams{
		// Atlantic Division
		{ID: 6, YahooID: pgtype.Int8{Int64: 1, Valid: true}, NhlDivisionID: Atlantic, Abbreviation: "BOS", City: "Boston", Name: "Bruins",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210311/70x70/boston-bruins_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210311/500x500/boston-bruins_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/bruins",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/boston/",
			AllStars:      false},
		{ID: 7, YahooID: pgtype.Int8{Int64: 2, Valid: true}, NhlDivisionID: Atlantic, Abbreviation: "BUF", City: "Buffalo", Name: "Sabres",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210311/70x70/buffalo-sabres_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210311/500x500/buffalo-sabres_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/sabres",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/buffalo/",
			AllStars:      false},
		{ID: 17, YahooID: pgtype.Int8{Int64: 5, Valid: true}, NhlDivisionID: Atlantic, Abbreviation: "DET", City: "Detroit", Name: "Red Wings",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181126/nhl/70x70/redwings_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181126/nhl/500x500/redwings_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/redwings",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/detroit/",
			AllStars:      false},
		{ID: 13, YahooID: pgtype.Int8{Int64: 26, Valid: true}, NhlDivisionID: Atlantic, Abbreviation: "FLA", City: "Florida", Name: "Panthers",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210312/70x70/florida_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210312/500x500/florida_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/panthers",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/florida/",
			AllStars:      false},
		{ID: 8, YahooID: pgtype.Int8{Int64: 10, Valid: true}, NhlDivisionID: Atlantic, Abbreviation: "MTL", City: "Montreal", Name: "Canadiens",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181126/nhl/70x70/canadiens_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181126/nhl/500x500/canadiens_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/canadiens",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/montreal/",
			AllStars:      false},
		{ID: 9, YahooID: pgtype.Int8{Int64: 14, Valid: true}, NhlDivisionID: Atlantic, Abbreviation: "OTT", City: "Ottawa", Name: "Senators",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20201006/70x70/senators_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20201006/500x500/senators_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/senators",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/ottawa/",
			AllStars:      false},
		{ID: 14, YahooID: pgtype.Int8{Int64: 20, Valid: true}, NhlDivisionID: Atlantic, Abbreviation: "TBL", City: "Tampa Bay", Name: "Lightning",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210312/70x70/tampa-bay-lightning_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210312/500x500/tampa-bay-lightning_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/lightning",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/tampa-bay/",
			AllStars:      false},
		{ID: 10, YahooID: pgtype.Int8{Int64: 21, Valid: true}, NhlDivisionID: Atlantic, Abbreviation: "TOR", City: "Toronto", Name: "Maple Leafs",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181126/nhl/70x70/mapleleafs_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181126/nhl/500x500/mapleleafs_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/mapleleafs",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/toronto/",
			AllStars:      false},

		// Metropolitan Division
		{ID: 12, YahooID: pgtype.Int8{Int64: 7, Valid: true}, NhlDivisionID: Metropolitan, Abbreviation: "CAR", City: "Carolina", Name: "Hurricanes",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181123/nhl/70x70/hurricanes_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181123/nhl/500x500/hurricanes_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/hurricanes",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/carolina/",
			AllStars:      false},
		{ID: 29, YahooID: pgtype.Int8{Int64: 29, Valid: true}, NhlDivisionID: Metropolitan, Abbreviation: "CBJ", City: "Columbus", Name: "Blue Jackets",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181126/nhl/70x70/bluejackets_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181126/nhl/500x500/bluejackets_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/bluejackets",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/columbus/",
			AllStars:      false},
		{ID: 1, YahooID: pgtype.Int8{Int64: 11, Valid: true}, NhlDivisionID: Metropolitan, Abbreviation: "NJD", City: "New Jersey", Name: "Devils",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181126/nhl/70x70/devils_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181122/nhl/500x500/devils_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/devils",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/new-jersey/",
			AllStars:      false},
		{ID: 2, YahooID: pgtype.Int8{Int64: 12, Valid: true}, NhlDivisionID: Metropolitan, Abbreviation: "NYI", City: "New York", Name: "Islanders",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181126/nhl/70x70/islanders_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181126/nhl/500x500/islanders_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/islanders",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/ny-islanders/",
			AllStars:      false},
		{ID: 3, YahooID: pgtype.Int8{Int64: 13, Valid: true}, NhlDivisionID: Metropolitan, Abbreviation: "NYR", City: "New York", Name: "Rangers",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181122/nhl/70x70/rangers_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181122/nhl/500x500/rangers_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/rangers",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/ny-rangers/",
			AllStars:      false},
		{ID: 4, YahooID: pgtype.Int8{Int64: 15, Valid: true}, NhlDivisionID: Metropolitan, Abbreviation: "PHI", City: "Philadelphia", Name: "Flyers",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210312/70x70/philadelphia-flyers_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210312/500x500/philadelphia-flyers_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/flyers",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/philadelphia/",
			AllStars:      false},
		{ID: 5, YahooID: pgtype.Int8{Int64: 16, Valid: true}, NhlDivisionID: Metropolitan, Abbreviation: "PIT", City: "Pittsburgh", Name: "Penguins",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181126/nhl/70x70/penguins_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181126/nhl/500x500/penguins_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/penguins",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/pittsburgh/",
			AllStars:      false},
		{ID: 15, YahooID: pgtype.Int8{Int64: 23, Valid: true}, NhlDivisionID: Metropolitan, Abbreviation: "WSH", City: "Washington", Name: "Capitals",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210325/70x70/washington-capitals_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210325/500x500/washington-capitals_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/capitals",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/washington/",
			AllStars:      false},

		// Central Division
		{ID: 53, YahooID: pgtype.Int8{Int64: 24, Valid: true}, NhlDivisionID: Central, Abbreviation: "ARI", City: "Arizona", Name: "Coyotes",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20211001/70x70/arizona-coyotes_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20211001/500x500/arizona-coyotes_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/coyotes",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/arizona/",
			AllStars:      false},
		{ID: 16, YahooID: pgtype.Int8{Int64: 4, Valid: true}, NhlDivisionID: Central, Abbreviation: "CHI", City: "Chicago", Name: "Blackhawks",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181126/nhl/70x70/blackhawks_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181126/nhl/500x500/blackhawks_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/blackhawks",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/chicago/",
			AllStars:      false},
		{ID: 21, YahooID: pgtype.Int8{Int64: 17, Valid: true}, NhlDivisionID: Central, Abbreviation: "COL", City: "Colorado", Name: "Avalanche",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210311/70x70/colorado-avalanche_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210311/500x500/colorado-avalanche_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/avalanche",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/colorado/",
			AllStars:      false},
		{ID: 25, YahooID: pgtype.Int8{Int64: 9, Valid: true}, NhlDivisionID: Central, Abbreviation: "DAL", City: "Dallas", Name: "Stars",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210311/70x70/dallas-stars_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210311/500x500/dallas-stars_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/stars",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/dallas/",
			AllStars:      false},
		{ID: 30, YahooID: pgtype.Int8{Int64: 30, Valid: true}, NhlDivisionID: Central, Abbreviation: "MIN", City: "Minnesota", Name: "Wild",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181126/nhl/70x70/wild_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181126/nhl/500x500/wild_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/wild",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/minnesota/",
			AllStars:      false},
		{ID: 18, YahooID: pgtype.Int8{Int64: 27, Valid: true}, NhlDivisionID: Central, Abbreviation: "NSH", City: "Nashville", Name: "Predators",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210312/70x70/nashville-predators_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210312/500x500/nashville-predators_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/predators",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/nashville/",
			AllStars:      false},
		{ID: 19, YahooID: pgtype.Int8{Int64: 19, Valid: true}, NhlDivisionID: Central, Abbreviation: "STL", City: "St. Louis", Name: "Blues",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210312/70x70/st-louis-blues_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210312/500x500/st-louis-blues_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/blues",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/st-louis/",
			AllStars:      false},
		{ID: 59, YahooID: pgtype.Int8{Int64: 60, Valid: true}, NhlDivisionID: Central, Abbreviation: "UTA", City: "Utah", Name: "Hockey Club",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20240610/70px/utah_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20240610/500px/utah_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/utah",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/utah/",
			AllStars:      false},
		{ID: 68, YahooID: pgtype.Int8{Int64: 60, Valid: true}, NhlDivisionID: Central, Abbreviation: "UTA", City: "Utah", Name: "Mammoth",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20240610/70px/utah_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20240610/500px/utah_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/utah",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/utah/",
			AllStars:      false},
		{ID: 52, YahooID: pgtype.Int8{Int64: 28, Valid: true}, NhlDivisionID: Central, Abbreviation: "WPG", City: "Winnipeg", Name: "Jets",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181126/nhl/70x70/jets_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181126/nhl/500x500/jets_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/jets",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/winnipeg/",
			AllStars:      false},

		// Pacific Division
		{ID: 24, YahooID: pgtype.Int8{Int64: 25, Valid: true}, NhlDivisionID: Pacific, Abbreviation: "ANA", City: "Anaheim", Name: "Ducks",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181122/nhl/70x70/ducks_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181122/nhl/500x500/ducks_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/ducks",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/anaheim/",
			AllStars:      false},
		{ID: 20, YahooID: pgtype.Int8{Int64: 3, Valid: true}, NhlDivisionID: Pacific, Abbreviation: "CGY", City: "Calgary", Name: "Flames",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210311/70x70/calgary-flames_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210311/500x500/calgary-flames_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/flames",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/calgary/",
			AllStars:      false},
		{ID: 22, YahooID: pgtype.Int8{Int64: 6, Valid: true}, NhlDivisionID: Pacific, Abbreviation: "EDM", City: "Edmonton", Name: "Oilers",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20220917/70x70/oilers1_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20220917/500x500/oilers1_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/oilers",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/edmonton/",
			AllStars:      false},
		{ID: 26, YahooID: pgtype.Int8{Int64: 8, Valid: true}, NhlDivisionID: Pacific, Abbreviation: "LAK", City: "Los Angeles", Name: "Kings",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20200508/70x70/kings_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20200508/500x500/kings_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/kings",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/los-angeles/",
			AllStars:      false},
		{ID: 55, YahooID: pgtype.Int8{Int64: 59, Valid: true}, NhlDivisionID: Pacific, Abbreviation: "SEA", City: "Seattle", Name: "Kraken",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210715/70x70/seattle-kraken_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210715/500x500/seattle-kraken_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/kraken",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/seattle/",
			AllStars:      false},
		{ID: 28, YahooID: pgtype.Int8{Int64: 18, Valid: true}, NhlDivisionID: Pacific, Abbreviation: "SJS", City: "San Jose", Name: "Sharks",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181126/nhl/70x70/sharks_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20181126/nhl/500x500/sharks_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/sharks",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/san-jose/",
			AllStars:      false},
		{ID: 23, YahooID: pgtype.Int8{Int64: 22, Valid: true}, NhlDivisionID: Pacific, Abbreviation: "VAN", City: "Vancouver", Name: "Canucks",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20190924/nhl/70x70/canucks_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/20190924/nhl/500x500/canucks_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/canucks",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/vancouver/",
			AllStars:      false},
		{ID: 54, YahooID: pgtype.Int8{Int64: 58, Valid: true}, NhlDivisionID: Pacific, Abbreviation: "VGK", City: "Vegas", Name: "Golden Knights",
			SmallLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210312/70x70/vegas-golden-knights_wbg.png",
			LargeLogoUrl:  "https://s.yimg.com/cv/apiv2/default/nhl/20210312/500x500/vegas-golden-knights_wbg.png",
			NhlHomeLink:   "https://www.nhl.com/goldenknights",
			YahooHomeLink: "https://sports.yahoo.com/nhl/teams/vegas/",
			AllStars:      false},

		// All-Star teams (use high IDs to avoid conflicts with real NHL teams)
		{ID: 100, YahooID: pgtype.Int8{Int64: 31, Valid: true}, NhlDivisionID: Atlantic, Abbreviation: "AAS", City: "Atlantic", Name: "All-Stars",
			SmallLogoUrl: "", LargeLogoUrl: "", NhlHomeLink: "", YahooHomeLink: "", AllStars: true},
		{ID: 101, YahooID: pgtype.Int8{Int64: 32, Valid: true}, NhlDivisionID: Central, Abbreviation: "CAS", City: "Central", Name: "All-Stars",
			SmallLogoUrl: "", LargeLogoUrl: "", NhlHomeLink: "", YahooHomeLink: "", AllStars: true},
		{ID: 102, YahooID: pgtype.Int8{Int64: 33, Valid: true}, NhlDivisionID: Metropolitan, Abbreviation: "MAS", City: "Metropolitan", Name: "All-Stars",
			SmallLogoUrl: "", LargeLogoUrl: "", NhlHomeLink: "", YahooHomeLink: "", AllStars: true},
		{ID: 103, YahooID: pgtype.Int8{Int64: 34, Valid: true}, NhlDivisionID: Pacific, Abbreviation: "PAS", City: "Pacific", Name: "All-Stars",
			SmallLogoUrl: "", LargeLogoUrl: "", NhlHomeLink: "", YahooHomeLink: "", AllStars: true},
	}
	for _, team := range teams {
		if err := q.UpsertNHLTeam(ctx, team); err != nil {
			return err
		}
	}
	return nil
}

const contextKey = "gorm"
const sqlcContextKey = "sqlc"

func Middleware(next http.Handler) http.Handler {
	db, err := OpenGorm()
	if err != nil {
		log.Error().Err(err).Msg("can't open database")
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err != nil {
			// TODO return http error
			log.Error().Err(err).Msg("can't open database")
			return
		}
		ctx := context.WithValue(r.Context(), contextKey, db)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func FromContext(ctx context.Context) *gorm.DB {
	return ctx.Value(contextKey).(*gorm.DB)
}

// SQLCMiddleware adds SQLC queries to the request context
func SQLCMiddleware(next http.Handler) http.Handler {
	pool, err := OpenPGXPool(context.Background())
	if err != nil {
		log.Error().Err(err).Msg("can't open pgx pool")
	}
	queries := sqlcdb.New(pool)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err != nil {
			log.Error().Err(err).Msg("can't open pgx pool")
			http.Error(w, "database error", http.StatusInternalServerError)
			return
		}
		ctx := context.WithValue(r.Context(), sqlcContextKey, queries)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// QueriesFromContext retrieves SQLC queries from context
func QueriesFromContext(ctx context.Context) *sqlcdb.Queries {
	return ctx.Value(sqlcContextKey).(*sqlcdb.Queries)
}
