package store

// BoxscorePlayer holds minimal player info extracted from boxscore appearances.
// This is used to carry player data from extraction to download phases.
type BoxscorePlayer struct {
	ID        int64  `json:"id"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Position  string `json:"position"`
}
