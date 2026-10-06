// Package resource defines typed structs for every data resource in PuckDB.
//
// Each resource type (Boxscore, Roster, PlayerLanding, etc.) is a small struct
// that encodes its identity (game ID, date, player ID, etc.) and provides
// methods for locating, fetching, and parsing that resource:
//
//   - Path() returns the storage path relative to the data root
//   - URL() returns the remote API URL (on types that implement URLResource)
//   - Parse([]byte) deserializes raw bytes into a typed Go value
//   - Format(T) serializes a typed value back to bytes (on types that implement Formattable)
//   - Type() returns a FileType constant for metrics and logging
//
// These interfaces are defined in the core package:
//
//   - [core.Resource] — Path() + Type()
//   - [core.URLResource] — Resource + URL()
//   - [core.Parseable] — Resource + Parse()
//   - [core.Formattable] — Resource + Format()
//
// # Generic helpers
//
// The [ReadParsed] and [WriteParsed] functions combine storage I/O with
// type-safe parsing in a single call:
//
//	schedule, err := resource.ReadParsed(ctx, storage, resource.DailySchedule{Date: day})
//	err := resource.WriteParsed(ctx, storage, resource.SeasonStandings{Season: s}, standings)
//
// [Read], [Write], [Exists], [Delete] and [Stat] do the raw operation on a
// resource's path. Use them, rather than calling the storage with r.Path(),
// so filesystem metrics are labeled with the resource's Type(); a storage
// operation without a file type is counted as Unknown and logged once.
//
// # NHL resources (nhl.go)
//
// Game data is organized by date under a season directory:
//
//	seasons/{season}/games/{year}/{month}/{day}/boxscore-{gameID}.json
//	seasons/{season}/games/{year}/{month}/{day}/daily-schedule-{date}.json
//
// The season is derived from the date via [DeduceSeason]: September onward
// belongs to the current year, earlier months to the previous year.
//
// Singleton resources use fixed paths:
//
//	nhl/franchises.json
//	nhl/seasons-manifest.json
//	nhl/standings/standings-{seasonID}.json
//
// # Yahoo resources (yahoo.go)
//
// Yahoo Fantasy data is organized by season and league:
//
//	seasons/{season}/yahoo/{leagueID}/league/league.xml
//	seasons/{season}/yahoo/{leagueID}/rosters/team-{id}/rosters-{id}-{date}.xml
//
// Yahoo player pages (HTML) and game keys (XML) use separate directories:
//
//	yahoo-players/player-{id}.html
//	game-keys/gamekey-{season}.xml
//
// # Adding a new resource
//
// Define a struct with the identifying fields, then implement Path(), Type(),
// and Parse(). Add URL() if it can be fetched directly. Add Format() if it
// needs to be written back. The generic helpers will work automatically.
package resource
