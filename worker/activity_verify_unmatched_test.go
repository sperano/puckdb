package worker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNamesMatchForVerification_ExactMatch(t *testing.T) {
	t.Parallel()

	assert.True(t, namesMatchForVerification("Connor McDavid", "Connor McDavid"))
	assert.True(t, namesMatchForVerification("CONNOR MCDAVID", "connor mcdavid"))
}

func TestNamesMatchForVerification_AccentNormalization(t *testing.T) {
	t.Parallel()

	assert.True(t, namesMatchForVerification("Daniel Brière", "Daniel Briere"))
	assert.True(t, namesMatchForVerification("Juuso Välimäki", "Juuso Valimaki"))
}

func TestNamesMatchForVerification_CompoundFirstName(t *testing.T) {
	t.Parallel()

	// Charles Alexis Legault case: NHL has "Charles Alexis" + "Legault"
	// but search might return "Charles Alexis Legault" as full name
	assert.True(t, namesMatchForVerification("Charles Alexis Legault", "Charles Alexis Legault"))

	// Also test when comparing Yahoo's parsed version
	// Yahoo: "Charles" + "Alexis Legault" -> "Charles Alexis Legault"
	// NHL API result: "Charles Alexis Legault"
	assert.True(t, namesMatchForVerification("Charles Alexis Legault", "Charles Alexis Legault"))
}

func TestNamesMatchForVerification_PrefixMatch(t *testing.T) {
	t.Parallel()

	// Prefix matching: "alex" is a prefix of "alexander"
	assert.True(t, namesMatchForVerification("Alex Smith", "Alexander Smith"))
	assert.True(t, namesMatchForVerification("Alexander Smith", "Alex Smith"))

	// Mike/Michael - won't match because "mike" is not a prefix of "michael"
	// (the verification function is simpler than the main matcher and doesn't have nickname aliases)
	assert.False(t, namesMatchForVerification("Mike Smith", "Michael Smith"))

	// Bob/Robert - won't match because "bob" is not a prefix of "robert"
	assert.False(t, namesMatchForVerification("Bob Smith", "Robert Smith"))
}

func TestNamesMatchForVerification_DifferentLastName(t *testing.T) {
	t.Parallel()

	assert.False(t, namesMatchForVerification("Connor McDavid", "Connor Brown"))
	assert.False(t, namesMatchForVerification("Mathieu Bizier", "Mathieu Garon"))
}

func TestNamesMatchForVerification_DifferentPerson(t *testing.T) {
	t.Parallel()

	// Completely different people
	assert.False(t, namesMatchForVerification("Wayne Gretzky", "Connor McDavid"))
	assert.False(t, namesMatchForVerification("Drew Fortescue", "Drew Miller"))
}

func TestNormalizeNameForVerification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"Connor McDavid", "connor mcdavid"},
		{"UPPER CASE", "upper case"},
		{"  Spaces  ", "spaces"},
		{"Daniel Brière", "daniel briere"},
		{"Juuso Välimäki", "juuso valimaki"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, normalizeNameForVerification(tt.input))
		})
	}
}
