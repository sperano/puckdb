package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseTimestamp(t *testing.T) {
	t.Parallel()
	time, err := ParseTimestamp("20210930024225")
	assert.Nil(t, err)
	assert.Equal(t, 2021, time.Year())
	//assert.Equal(t, "Module test not found", (&ErrModuleNotFound{ModuleID("test")}).Error())
}
