package news

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testYahooID = 5987

func TestYahooStatusItemsDayToDayPlayer(t *testing.T) {
	status := YahooStatus{
		YahooPlayerID: testYahooID, Name: "Alex Lyon", Status: "DTD", StatusFull: "Day-to-Day",
		InjuryNote: "Upper Body", OnDisabledList: true, FetchedAt: retrievedAt,
	}
	items := YahooStatusItems([]YahooStatus{status})
	require.Len(t, items, 1)
	it := items[0]

	assert.Equal(t, "Alex Lyon: Day-to-Day", it.Title)
	assert.Contains(t, it.Text, "Yahoo status DTD (Day-to-Day).")
	assert.Contains(t, it.Text, "Injury note: Upper Body.")
	assert.Contains(t, it.Text, "Listed on a Yahoo disabled list.")
	assert.Equal(t, "status:5987", it.ExternalID)
	assert.Equal(t, retrievedAt, it.RetrievedAt)
	require.Len(t, it.Subjects, 1)
	assert.Equal(t, Subject{YahooPlayerID: testYahooID, Name: "Alex Lyon"}, it.Subjects[0],
		"only the Yahoo ID and name are carried; NHL mapping is left to resolution")
	assert.False(t, it.OnlyIfKnown)
}

func TestYahooStatusItemsCategoryHints(t *testing.T) {
	tests := []struct {
		code string
		want Category
	}{
		{"DTD", CategoryInjury},
		{"IR-LT", CategoryInjury},
		{"SUSP", CategorySuspension},
		{"NA", CategoryRoleChange},
		{"BOGUS", CategoryNone},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			items := YahooStatusItems([]YahooStatus{{YahooPlayerID: testYahooID, Name: "Player", Status: tt.code, FetchedAt: retrievedAt}})
			require.Len(t, items, 1)
			assert.Equal(t, tt.want, items[0].CategoryHint)
		})
	}
}

func TestYahooStatusItemsPlayerWithNothingListed(t *testing.T) {
	status := YahooStatus{YahooPlayerID: testYahooID, Name: "Alex Lyon", FetchedAt: retrievedAt}
	items := YahooStatusItems([]YahooStatus{status})
	require.Len(t, items, 1)
	it := items[0]

	assert.Equal(t, "Alex Lyon: no status listed", it.Title)
	assert.Equal(t, yahooNoStatusText, it.Text)
	assert.Equal(t, CategoryReinstatement, it.CategoryHint)
	assert.True(t, it.OnlyIfKnown, "not news unless a status was on record before")
}
