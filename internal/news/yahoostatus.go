package news

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// yahooStatusIDPrefix namespaces a Yahoo player's status article.
	yahooStatusIDPrefix = "status:"
	// yahooPlayerURLFormat links a Yahoo player page.
	yahooPlayerURLFormat = "https://sports.yahoo.com/nhl/players/%d/"
	// yahooNoStatusText is the evidence text of a player Yahoo stopped
	// listing a status for. Classify maps it to a reinstatement.
	yahooNoStatusText = "Yahoo no longer lists a status for this player."
)

// yahooStatusCategories maps Yahoo player status codes to categories. A code
// not listed here is kept as evidence but starts no incident.
var yahooStatusCategories = map[string]Category{
	"DTD":   CategoryInjury,
	"GTD":   CategoryInjury,
	"O":     CategoryInjury,
	"INJ":   CategoryInjury,
	"IR":    CategoryInjury,
	"IR-LT": CategoryInjury,
	"IR-NR": CategoryInjury,
	"SUSP":  CategorySuspension,
	"NA":    CategoryRoleChange,
}

// YahooStatus is one player's status in the imported Yahoo league pools.
type YahooStatus struct {
	YahooPlayerID  int
	Name           string
	Status         string
	StatusFull     string
	InjuryNote     string
	OnDisabledList bool
	FetchedAt      time.Time
}

// listsStatus reports whether Yahoo lists anything about the player's
// availability.
func (s YahooStatus) listsStatus() bool {
	return s.Status != "" || s.InjuryNote != "" || s.OnDisabledList
}

// YahooStatusItems turns each player's Yahoo status into one item per
// player. The same status seen through several leagues is one fact: callers
// pass one entry per player (the newest fetch). A player with no status is
// only recorded as a change of an earlier listed status; Yahoo listing
// nothing does not mean the player is healthy, and the report says so.
func YahooStatusItems(statuses []YahooStatus) []Item {
	items := make([]Item, 0, len(statuses))
	for _, s := range statuses {
		item := Item{
			ExternalID:  yahooStatusIDPrefix + strconv.Itoa(s.YahooPlayerID),
			URL:         fmt.Sprintf(yahooPlayerURLFormat, s.YahooPlayerID),
			RetrievedAt: s.FetchedAt,
			// The NHL mapping is left to player resolution: a mapping added
			// later must not look like a new status.
			Subjects:  []Subject{{YahooPlayerID: s.YahooPlayerID, Name: s.Name}},
			TeamHints: []string{},
		}
		if s.listsStatus() {
			item.Title = fmt.Sprintf("%s: %s", s.Name, statusLabel(s))
			item.Text = statusText(s)
			item.CategoryHint = yahooStatusCategories[strings.ToUpper(s.Status)]
		} else {
			item.Title = fmt.Sprintf("%s: no status listed", s.Name)
			item.Text = yahooNoStatusText
			item.CategoryHint = CategoryReinstatement
			item.OnlyIfKnown = true
		}
		items = append(items, normalizeItem(item))
	}
	return items
}

func statusLabel(s YahooStatus) string {
	switch {
	case s.StatusFull != "":
		return s.StatusFull
	case s.Status != "":
		return s.Status
	case s.InjuryNote != "":
		return s.InjuryNote
	default:
		return "on a disabled list"
	}
}

func statusText(s YahooStatus) string {
	parts := make([]string, 0, 3)
	switch {
	case s.Status != "" && s.StatusFull != "":
		parts = append(parts, fmt.Sprintf("Yahoo status %s (%s).", s.Status, s.StatusFull))
	case s.Status != "":
		parts = append(parts, fmt.Sprintf("Yahoo status %s.", s.Status))
	}
	if s.InjuryNote != "" {
		parts = append(parts, fmt.Sprintf("Injury note: %s.", s.InjuryNote))
	}
	if s.OnDisabledList {
		parts = append(parts, "Listed on a Yahoo disabled list.")
	}
	return strings.Join(parts, " ")
}
