package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

func TestNHLConferenceGraphQLModel(t *testing.T) {
	t.Parallel()
	c := &NHLConference{
		Model: gorm.Model{
			ID: 76,
		},
		Name: "my-conf",
	}
	g := c.GraphQLModel()
	assert.Equal(t, 76, g.ID)
	assert.Equal(t, "my-conf", g.Name)
}

func TestNHLDivisionGraphQLModel(t *testing.T) {
	t.Parallel()
	d := &NHLDivision{
		Model: gorm.Model{
			ID: 76,
		},
		Name: "my-div",
		NHLConference: NHLConference{
			Model: gorm.Model{
				ID: 123,
			},
		},
		NHLConferenceID: 123,
	}
	g := d.GraphQLModel()
	assert.Equal(t, 76, g.ID)
	assert.Equal(t, "my-div", g.Name)
	assert.Equal(t, 123, g.Conference.ID)
}

func TestNHLTeamGraphQLModel(t *testing.T) {
	t.Parallel()
	d := &NHLTeam{
		Model: gorm.Model{
			ID: 76,
		},
		City: "my-city",
		Name: "my-name",
		NHLDivision: NHLDivision{
			Model: gorm.Model{ID: 456},
			NHLConference: NHLConference{
				Model: gorm.Model{ID: 123},
			},
			NHLConferenceID: 123,
		},
		NHLDivisionID: 456,
	}
	g := d.GraphQLModel()
	assert.Equal(t, 76, g.ID)
	assert.Equal(t, "my-city", g.City)
	assert.Equal(t, "my-name", g.Name)
	assert.Equal(t, 456, g.Division.ID)
	assert.Equal(t, 123, g.Division.Conference.ID)
}
