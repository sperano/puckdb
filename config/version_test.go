package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetBuildNumberAsInt_ValidNumber(t *testing.T) {
	t.Parallel()
	original := BuildNumber
	t.Cleanup(func() { BuildNumber = original })

	BuildNumber = "123"
	assert.Equal(t, 123, GetBuildNumberAsInt())
}

func TestGetBuildNumberAsInt_InvalidNumber(t *testing.T) {
	t.Parallel()
	original := BuildNumber
	t.Cleanup(func() { BuildNumber = original })

	BuildNumber = "n/a"
	assert.Equal(t, -1, GetBuildNumberAsInt())
}

func TestGetBuildNumberAsInt_EmptyString(t *testing.T) {
	t.Parallel()
	original := BuildNumber
	t.Cleanup(func() { BuildNumber = original })

	BuildNumber = ""
	assert.Equal(t, -1, GetBuildNumberAsInt())
}
