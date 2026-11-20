package matching

import (
	"slices"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/store"
)

// nhlLeagueAbbrev is the league abbreviation of an NHL season total.
const nhlLeagueAbbrev = "NHL"

// NamesakeSeasons returns the NHL seasons (start years) each other NHL
// player with the landing's name played, one set per player; an empty
// result means the player has no namesake. MatchYahooIDBySeasons calls it
// only when its seasons step runs. An error skips the step.
type NamesakeSeasons func() ([][]int, error)

// MatchYahooIDBySeasons is MatchYahooID with one more step between the birth
// date (3) and team (4) steps, for any name match still tied after the
// birth date step (a retired player's Yahoo Sports page is gone, or its
// birth date does not single one candidate out): the seasons whose game player list names each candidate
// (store.YahooPlayer.Seasons) against the NHL seasons the player played
// (NHLSeasonsPlayed). It picks a candidate only when every candidate has
// list seasons, exactly one candidate's seasons overlap the player's, and no
// namesake's seasons overlap that candidate's. The last check keeps a
// namesake from taking the other's ID: when two namesakes both played the
// only season one Yahoo ID is listed in, and the other ID is listed only
// in later seasons (a player Yahoo listed after his NHL games), the
// seasons cannot tell which namesake the first ID is. When the step picks
// nothing the team step follows, as in MatchYahooID. A nil namesakes skips
// the step.
func MatchYahooIDBySeasons(
	landing *nhl.PlayerLanding,
	nhlTeamAbbrev string,
	nhlBirthDate time.Time,
	namesakes NamesakeSeasons,
	pool map[store.YahooPlayerID]*store.YahooPlayer,
) (YahooIDMatchResult, error) {
	return matchYahooID(landing, nhlTeamAbbrev, nhlBirthDate, namesakes, pool)
}

// NHLSeasonsPlayed returns, sorted and without duplicates, the start years
// of the NHL seasons in which landing's player played at least one regular
// season or playoff game.
func NHLSeasonsPlayed(landing *nhl.PlayerLanding) []int {
	var seasons []int
	for _, st := range landing.SeasonTotals {
		if st.LeagueAbbrev != nhlLeagueAbbrev || st.GamesPlayed <= 0 {
			continue
		}
		if st.GameType != nhl.GameTypeRegularSeason && st.GameType != nhl.GameTypePlayoffs {
			continue
		}
		seasons = append(seasons, st.Season.StartYear())
	}
	slices.Sort(seasons)
	return slices.Compact(seasons)
}

// singleBySeasons is the seasons step: the one candidate it picks, if any.
func singleBySeasons(landing *nhl.PlayerLanding, candidates []candidateMatch, namesakes NamesakeSeasons) (candidateMatch, bool) {
	played := NHLSeasonsPlayed(landing)
	if len(played) == 0 {
		return candidateMatch{}, false
	}
	var fits []candidateMatch
	for _, c := range candidates {
		if len(c.player.Seasons) == 0 {
			return candidateMatch{}, false
		}
		if seasonsOverlap(c.player.Seasons, played) {
			fits = append(fits, c)
		}
	}
	if len(fits) != 1 {
		return candidateMatch{}, false
	}
	others, err := namesakes()
	if err != nil {
		log.Warn().Err(err).Int64("nhl_id", landing.PlayerID.Int64()).
			Msg("NHL namesakes' seasons unavailable, seasons step skipped")
		return candidateMatch{}, false
	}
	for _, other := range others {
		if seasonsOverlap(fits[0].player.Seasons, other) {
			return candidateMatch{}, false
		}
	}
	return fits[0], true
}

// seasonsOverlap reports whether a and b share a season.
func seasonsOverlap(a, b []int) bool {
	for _, season := range a {
		if slices.Contains(b, season) {
			return true
		}
	}
	return false
}
