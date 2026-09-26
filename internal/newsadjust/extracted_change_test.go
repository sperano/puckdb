package newsadjust

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTeamDirectory_Resolve(t *testing.T) {
	d := NewTeamDirectory([]Team{
		{ID: 19, Abbrev: "STL", FullName: "St. Louis Blues", CommonName: "Blues"},
		{ID: 52, Abbrev: "WPG", FullName: "Winnipeg Jets", CommonName: "Jets"},
		{ID: 11, Abbrev: "ATL", FullName: "Atlanta Thrashers", CommonName: "Jets"},
	})
	for name, want := range map[string]int64{"stl": 19, "the St Louis Blues": 19, "BLUES": 19, "Winnipeg Jets": 52} {
		got, found := d.Resolve(name)
		assert.True(t, found, name)
		assert.Equal(t, want, got, name)
	}
	for _, name := range []string{"Jets", "Seattle", ""} {
		_, found := d.Resolve(name)
		assert.False(t, found, "%q is ambiguous or unknown", name)
	}
}

func TestLeagueMove(t *testing.T) {
	cases := map[string]move{
		"AHL":                         moveAssignment,
		"assigned to the AHL's Moose": moveAssignment,
		"loaned to Europe":            moveAssignment,
		"NHL":                         moveRecall,
		"the NHL roster":              moveRecall,
		"Hockey Canada":               moveUnknown,
	}
	for to, want := range cases {
		assert.Equal(t, want, leagueMove(to), to)
	}
}

func TestRoleFromText(t *testing.T) {
	cases := map[string]RoleChange{
		"starting goaltender":             {GoalieRole: GoalieRoleStarter},
		"1A in a tandem":                  {GoalieRole: GoalieRoleTandem},
		"backup to the starter":           {GoalieRole: GoalieRoleBackup},
		"top power-play unit":             {PowerPlay: DirectionUp},
		"removed from the top power play": {PowerPlay: DirectionDown},
		"PP2":                             {PowerPlay: DirectionDown},
		"first line and PP1":              {IceTime: DirectionUp, PowerPlay: DirectionUp},
		"fourth-line center":              {IceTime: DirectionDown},
		"captain":                         {},
		"topped the lineup":               {},
	}
	for to, want := range cases {
		assert.Equal(t, want, *roleFromText(to), to)
	}
}
