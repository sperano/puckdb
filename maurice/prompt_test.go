package maurice

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSystemPrompt(t *testing.T) {
	assert.NotEmpty(t, SystemPrompt)
	assert.Contains(t, SystemPrompt, "Maurice")
	assert.Contains(t, SystemPrompt, "PuckDB")
	assert.Contains(t, SystemPrompt, "query the database")
}
