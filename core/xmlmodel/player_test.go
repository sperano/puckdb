package xmlmodel

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPlayerNameValidate(t *testing.T) {
	t.Parallel()
	pn := PlayerName{
		Full:       "Bob McBob",
		First:      "Bob",
		Last:       "McBob",
		ASCIIFirst: "Bob",
		ASCIILast:  "McBob",
	}
	err := pn.Validate()
	assert.Nil(t, err)
}

func TestPlayerNameValidateErrInvalidFullName(t *testing.T) {
	t.Parallel()
	pn := PlayerName{
		Full:       "Bob McBobz",
		First:      "Bob",
		Last:       "McBob",
		ASCIIFirst: "Bob",
		ASCIILast:  "McBob",
	}
	err := pn.Validate()
	assert.IsType(t, &ErrInvalidFullName{}, err)
	assert.Equal(t, `"Bob McBobz" != "Bob" + " " + "McBob"`, err.Error())
}

func TestPlayerNameValidateErrASCIIFirstName(t *testing.T) {
	t.Parallel()
	pn := PlayerName{
		Full:       "Bob McBob",
		First:      "Bob",
		Last:       "McBob",
		ASCIIFirst: "Bobz",
		ASCIILast:  "McBob",
	}
	err := pn.Validate()
	assert.IsType(t, &ErrASCIIFirstName{}, err)
	assert.Equal(t, `error ASCII First Name "Bob" != "Bobz"`, err.Error())
}

func TestPlayerNameValidateErrASCIILastName(t *testing.T) {
	t.Parallel()
	pn := PlayerName{
		Full:       "Bob McBob",
		First:      "Bob",
		Last:       "McBob",
		ASCIIFirst: "Bob",
		ASCIILast:  "McBobz",
	}
	err := pn.Validate()
	assert.IsType(t, &ErrASCIILastName{}, err)
	assert.Equal(t, `error ASCII Last Name "McBob" != "McBobz"`, err.Error())
}

/*
func TestToPlayerModel(t *testing.T) {
	t.Parallel()
	p := Player{
		Name: PlayerName{
			Full:       "Bob McBob",
			First:      "Bob",
			Last:       "McBob",
			ASCIIFirst: "Bob",
			ASCIILast:  "McBob",
		},
		ID:                 123,
		Key:                "foo key",
		EditorialPlayerKey: "foo editorial player key",
	}
	player, err := p.ToPlayerModel()
	assert.Nil(t, err)
	assert.Equal(t, "Bob", player.FirstName)
	assert.Equal(t, "McBob", player.LastName)
	assert.Equal(t, 123, player.ID)
	assert.Equal(t, "foo key", player.Key)
	//assert.Equal(t, "foo editorial player key", player.EditorialPlayerKey)
}

func TestToPlayerModelError(t *testing.T) {
	t.Parallel()
	p := Player{
		Name: PlayerName{
			Full:       "Bob McBob",
			First:      "Bob",
			Last:       "McBob",
			ASCIIFirst: "Bob",
			ASCIILast:  "McBobz",
		},
	}
	player, err := p.ToPlayerModel()
	assert.Nil(t, player)
	assert.IsType(t, &ErrASCIILastName{}, err)
	assert.Equal(t, `error ASCII Last Name "McBob" != "McBobz"`, err.Error())
}
*/
