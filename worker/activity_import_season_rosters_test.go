package worker

import (
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/stretchr/testify/assert"
)

func TestLocalizedStringToText(t *testing.T) {
	t.Parallel()

	t.Run("nil pointer", func(t *testing.T) {
		result := localizedStringToText(nil)
		assert.False(t, result.Valid)
	})

	t.Run("empty string", func(t *testing.T) {
		ls := &nhl.LocalizedString{Default: ""}
		result := localizedStringToText(ls)
		assert.True(t, result.Valid)
		assert.Equal(t, "", result.String)
	})

	t.Run("non-empty string", func(t *testing.T) {
		ls := &nhl.LocalizedString{Default: "Montréal"}
		result := localizedStringToText(ls)
		assert.True(t, result.Valid)
		assert.Equal(t, "Montréal", result.String)
	})
}
