package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGetDateSuccessFirstHalf(t *testing.T) {
	t.Parallel()
	config := Config{
		SeasonStartYear:  2021,
		SeasonStartMonth: 10,
		SeasonStartDay:   30,
	}
	tm := config.GetSeasonStart()
	assert.Equal(t, time.October, tm.Month())
	assert.Equal(t, 30, tm.Day())
	assert.Equal(t, 2021, tm.Year())
}
