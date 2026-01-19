package worker

import (
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
)

func TestFilterRegularSeasonGames(t *testing.T) {
	tests := []struct {
		name     string
		gameIDs  []nhl.GameID
		wantLen  int
		wantIDs  []nhl.GameID
	}{
		{
			name:    "empty input",
			gameIDs: []nhl.GameID{},
			wantLen: 0,
			wantIDs: []nhl.GameID{},
		},
		{
			name: "all regular season games",
			gameIDs: []nhl.GameID{
				nhl.GameID(2024020001), // regular season
				nhl.GameID(2024020002), // regular season
			},
			wantLen: 2,
			wantIDs: []nhl.GameID{nhl.GameID(2024020001), nhl.GameID(2024020002)},
		},
		{
			name: "all preseason games",
			gameIDs: []nhl.GameID{
				nhl.GameID(2024010001), // preseason
				nhl.GameID(2024010044), // preseason (the problematic game)
			},
			wantLen: 0,
			wantIDs: []nhl.GameID{},
		},
		{
			name: "mixed games - filters preseason",
			gameIDs: []nhl.GameID{
				nhl.GameID(2024010001), // preseason
				nhl.GameID(2024020001), // regular season
				nhl.GameID(2024010044), // preseason
				nhl.GameID(2024030001), // playoffs
			},
			wantLen: 2,
			wantIDs: []nhl.GameID{nhl.GameID(2024020001), nhl.GameID(2024030001)},
		},
		{
			name: "all-star game passes through",
			gameIDs: []nhl.GameID{
				nhl.GameID(2024040001), // all-star
			},
			wantLen: 1,
			wantIDs: []nhl.GameID{nhl.GameID(2024040001)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterRegularSeasonGames(tt.gameIDs)
			if len(got) != tt.wantLen {
				t.Errorf("filterRegularSeasonGames() returned %d games, want %d", len(got), tt.wantLen)
			}
			for i, id := range got {
				if id != tt.wantIDs[i] {
					t.Errorf("filterRegularSeasonGames()[%d] = %d, want %d", i, id, tt.wantIDs[i])
				}
			}
		})
	}
}
