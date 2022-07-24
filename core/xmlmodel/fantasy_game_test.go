package xmlmodel

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToFantasyGameModel(t *testing.T) {
	t.Parallel()
	xmlfg := FantasyGame{
		Code:               "nhl",
		ID:                 411,
		IsGameOver:         true,
		IsOffseason:        true,
		IsRegistrationOver: true,
		Key:                411,
		Name:               "Hockey",
		Season:             2021,
		Type:               "full",
		URL:                "https://hockey.fantasysports.yahoo.com/hockey",
	}
	fg := xmlfg.ToFantasyGameModel()
	assert.Equal(t, "nhl", fg.Code)
	assert.Equal(t, 411, int(fg.ID))
	assert.True(t, fg.IsGameOver)
	assert.True(t, fg.IsOffseason)
	assert.True(t, fg.IsRegistrationOver)
	assert.Equal(t, 411, fg.Key)
	assert.Equal(t, "Hockey", fg.Name)
	assert.Equal(t, 2021, fg.Season)
	assert.Equal(t, "full", fg.Type)
	assert.Equal(t, "https://hockey.fantasysports.yahoo.com/hockey", fg.URL)
}
