package news

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyFromTitle(t *testing.T) {
	tests := []struct {
		name  string
		title string
		want  Category
	}{
		{"injury", "McNabb day to day with lower-body injury", CategoryInjury},
		{"suspension", "Player suspended three games", CategorySuspension},
		{"trade", "Canucks acquire defenceman in trade", CategoryTrade},
		{"role change", "Predators assign forward to AHL", CategoryRoleChange},
		{"reinstatement", "Player reinstated from suspension", CategoryReinstatement},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Classify(tt.title, ""))
		})
	}
}

func TestClassifyFromTextWhenTitleDoesNotMatch(t *testing.T) {
	tests := []struct {
		name string
		text string
		want Category
	}{
		{"injury", "The forward suffered a concussion in practice.", CategoryInjury},
		{"suspension", "He was suspended for the incident.", CategorySuspension},
		{"trade", "The team traded its captain.", CategoryTrade},
		{"role change", "The winger was recalled from the minors.", CategoryRoleChange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Classify("Weekly update", tt.text))
		})
	}
}

func TestClassifyTitleTakesPrecedenceOverText(t *testing.T) {
	got := Classify("Player day to day with injury", "The team also traded a prospect.")
	assert.Equal(t, CategoryInjury, got)
}

func TestClassifyReinstatementBeatsSuspension(t *testing.T) {
	got := Classify("Player reinstated from suspension", "")
	assert.Equal(t, CategoryReinstatement, got)
}

func TestClassifyMatchesWholeWordsOnly(t *testing.T) {
	assert.Equal(t, CategoryNone, Classify("", "The rookie is hurting for ice time."), `"hurting" must not match "hurt"`)
	assert.Equal(t, CategoryNone, Classify("", "It came down to a tradeoff between size and speed."), `"tradeoff" must not match "trade"`)
	assert.Equal(t, CategoryRoleChange, Classify("Assigned to AHL", ""))
}

func TestClassifyFineIsNotACategory(t *testing.T) {
	assert.Equal(t, CategoryNone, Classify("Player fined for hit", "The league fined the player $5,000."))
}
