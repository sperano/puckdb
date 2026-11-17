package yahoolink

// DefaultSeason returns the Yahoo season a user's "my team" refers to: the
// newest of seasons that is not after current. Both are Yahoo seasons, the
// start year of the hockey season (yahoo_leagues.season: 2026 for 2026-27;
// for the current NHL season, nhl.Current().StartYear()). Taking the newest
// one not after current, rather than current itself, keeps a default in the
// off-season before the new season's leagues are imported. It reports false
// when every season is after current, or there is none.
func DefaultSeason(seasons []int32, current int32) (int32, bool) {
	var best int32
	found := false
	for _, s := range seasons {
		if s <= current && (!found || s > best) {
			best, found = s, true
		}
	}
	return best, found
}
