package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfigGetTeamIDs(t *testing.T) {
	t.Parallel()
	expected := []uint{1, 2, 3, 4}
	assert.Equal(t, expected, getTeamIDs("1,2,3,4"))
}

func BenchmarkGetTeamIDs(b *testing.B) {
	for n := 0; n < b.N; n++ {
		getTeamIDs("1,2,3,4,5,6,7,8,9,10,11")
	}
}
