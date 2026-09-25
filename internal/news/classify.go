package news

import "strings"

// Category is the kind of event an incident candidate is about. It is a
// coarse grouping key from keywords or structured status, not a validated
// event: extracting what actually happened is a later step.
type Category string

const (
	CategoryNone          Category = ""
	CategoryInjury        Category = "injury"
	CategorySuspension    Category = "suspension"
	CategoryReinstatement Category = "reinstatement"
	CategoryTrade         Category = "trade"
	CategoryRoleChange    Category = "role_change"
)

// categoryRule lists the phrases (lowercase words separated by single
// spaces, matched on whole words) that put a story in a category.
type categoryRule struct {
	category Category
	phrases  []string
}

// categoryRules are tried in order and the first match wins, so a story
// about a player "reinstated from suspension" is a reinstatement, not a new
// suspension.
var categoryRules = []categoryRule{
	{CategoryReinstatement, []string{
		"reinstated", "reinstatement", "reinstates", "activated from", "activated off",
		"cleared to play", "cleared to return", "cleared for contact", "returns from injury",
		"return from injury", "back from injury", "suspension lifted", "lifts suspension",
		"off injured reserve", "no longer lists a status",
	}},
	{CategorySuspension, []string{"suspended", "suspension", "suspends", "banned"}},
	{CategoryTrade, []string{"traded", "acquired", "acquires", "acquire", "dealt to", "in a trade", "trade sends"}},
	{CategoryInjury, []string{
		"injury", "injured", "injuries", "hurt", "day to day", "week to week", "month to month",
		"concussion", "surgery", "sidelined", "upper body", "lower body", "out indefinitely",
		"fracture", "fractured", "torn", "sprain", "strain", "illness",
	}},
	{CategoryRoleChange, []string{
		"assign", "assigned", "assigns", "sent down", "loaned", "loans", "recalled", "recalls", "called up",
		"placed on waivers", "waived", "claimed off waivers", "named captain", "demoted",
		"reassigned", "retires", "retirement", "unconditional waivers",
	}},
}

// Classify returns the category of a story: its title decides when it
// matches a rule, otherwise its text does.
func Classify(title, text string) Category {
	if c := classifyText(title); c != CategoryNone {
		return c
	}
	return classifyText(text)
}

func classifyText(s string) Category {
	words := " " + strings.Join(Words(s), " ") + " "
	for _, rule := range categoryRules {
		for _, phrase := range rule.phrases {
			if strings.Contains(words, " "+phrase+" ") {
				return rule.category
			}
		}
	}
	return CategoryNone
}
