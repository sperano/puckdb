package worker

import (
	"context"

	"github.com/sperano/nhl-api-go/nhl"
)

// NHLClient defines the NHL API methods used by worker activities.
// The real implementation is nhl.Client; tests can provide mock implementations.
type NHLClient interface {
	PlayerLanding(ctx context.Context, playerID nhl.PlayerID) (*nhl.PlayerLanding, error)
	Boxscore(ctx context.Context, gameID nhl.GameID) (*nhl.Boxscore, error)
	DailySchedule(ctx context.Context, date nhl.GameDate) (*nhl.DailySchedule, error)
}

// Compile-time check that nhl.Client implements NHLClient
var _ NHLClient = (*nhl.Client)(nil)
