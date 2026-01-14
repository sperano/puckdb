package config

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestGetLogLevelStr(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "debug, error, fatal, info, trace, warn",
		getLogLevelsStr())
}
