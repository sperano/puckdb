package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestParseTimestamp(t *testing.T) {
	t.Parallel()
	timestamp, err := ParseTimestamp("20210930024225")
	assert.Nil(t, err)
	assert.Equal(t, 2021, timestamp.Year())
	assert.Equal(t, time.September, timestamp.Month())
	assert.Equal(t, 30, timestamp.Day())
	assert.Equal(t, 2, timestamp.Hour())
	assert.Equal(t, 42, timestamp.Minute())
	assert.Equal(t, 25, timestamp.Second())
}

func TestGetTimestamp(t *testing.T) {
	t.Parallel()
	str := "20210930024225"
	timestamp, _ := ParseTimestamp(str)
	assert.Equal(t, str, GetTimestamp(timestamp))
}
