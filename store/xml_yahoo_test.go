package store

import (
	"encoding/xml"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFlexTimestamp_UnmarshalXML(t *testing.T) {
	t.Parallel()

	type wrapper struct {
		XMLName xml.Name      `xml:"root"`
		Value   FlexTimestamp `xml:"value"`
	}

	t.Run("unix epoch integer", func(t *testing.T) {
		input := `<root><value>1081152249</value></root>`
		var w wrapper
		require.NoError(t, xml.Unmarshal([]byte(input), &w))
		assert.Equal(t, FlexTimestamp(1081152249), w.Value)
	})

	t.Run("ISO 8601 datetime", func(t *testing.T) {
		input := `<root><value>2003-10-05T14:55:00</value></root>`
		var w wrapper
		require.NoError(t, xml.Unmarshal([]byte(input), &w))
		assert.Equal(t, FlexTimestamp(1065365700), w.Value)
	})

	t.Run("empty string", func(t *testing.T) {
		input := `<root><value></value></root>`
		var w wrapper
		require.NoError(t, xml.Unmarshal([]byte(input), &w))
		assert.Equal(t, FlexTimestamp(0), w.Value)
	})

	t.Run("invalid format", func(t *testing.T) {
		input := `<root><value>not-a-timestamp</value></root>`
		var w wrapper
		err := xml.Unmarshal([]byte(input), &w)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parse league_update_timestamp")
	})
}

func TestLeagueUnmarshal_2003DraftTime(t *testing.T) {
	t.Parallel()
	// Minimal reproduction of the 2003 league XML that caused strconv.ParseInt failures.
	input := `<fantasy_content>
		<league>
			<league_id>7539</league_id>
			<league_update_timestamp>1081152249</league_update_timestamp>
			<settings>
				<draft_time>2003-10-05T14:55:00</draft_time>
			</settings>
		</league>
	</fantasy_content>`

	var content FantasyContent
	require.NoError(t, xml.Unmarshal([]byte(input), &content))
	assert.Equal(t, 7539, content.League.ID)
	assert.Equal(t, FlexTimestamp(1081152249), content.League.LeagueUpdateTimestamp)
	assert.Equal(t, FlexTimestamp(1065365700), content.League.Settings.DraftTime)
}

func TestPlayerName_Validate(t *testing.T) {
	t.Parallel()

	t.Run("valid name", func(t *testing.T) {
		name := PlayerName{
			Full:       "Wayne Gretzky",
			First:      "Wayne",
			Last:       "Gretzky",
			ASCIIFirst: "Wayne",
			ASCIILast:  "Gretzky",
		}
		err := name.Validate()
		require.NoError(t, err)
	})

	t.Run("full name mismatch", func(t *testing.T) {
		name := PlayerName{
			Full:       "Wayne Wrong",
			First:      "Wayne",
			Last:       "Gretzky",
			ASCIIFirst: "Wayne",
			ASCIILast:  "Gretzky",
		}
		err := name.Validate()
		require.Error(t, err)

		var invalidErr *ErrInvalidFullName
		assert.ErrorAs(t, err, &invalidErr)
		assert.Equal(t, "Wayne Wrong", invalidErr.Full)
		assert.Equal(t, "Wayne", invalidErr.First)
		assert.Equal(t, "Gretzky", invalidErr.Last)
	})

	t.Run("ASCII first name mismatch", func(t *testing.T) {
		name := PlayerName{
			Full:       "José García",
			First:      "José",
			Last:       "García",
			ASCIIFirst: "Jose", // Different from First
			ASCIILast:  "García",
		}
		err := name.Validate()
		require.Error(t, err)

		var asciiErr *ErrASCIIFirstName
		assert.ErrorAs(t, err, &asciiErr)
		assert.Equal(t, "José", asciiErr.Name)
		assert.Equal(t, "Jose", asciiErr.ASCII)
	})

	t.Run("ASCII last name mismatch", func(t *testing.T) {
		name := PlayerName{
			Full:       "Jose García",
			First:      "Jose",
			Last:       "García",
			ASCIIFirst: "Jose",
			ASCIILast:  "Garcia", // Different from Last
		}
		err := name.Validate()
		require.Error(t, err)

		var asciiErr *ErrASCIILastName
		assert.ErrorAs(t, err, &asciiErr)
		assert.Equal(t, "García", asciiErr.Name)
		assert.Equal(t, "Garcia", asciiErr.ASCII)
	})
}

func TestErrInvalidFullName_Error(t *testing.T) {
	t.Parallel()

	err := &ErrInvalidFullName{
		Full:  "Full Name",
		First: "First",
		Last:  "Last",
	}
	msg := err.Error()
	assert.Contains(t, msg, "Full Name")
	assert.Contains(t, msg, "First")
	assert.Contains(t, msg, "Last")
}

func TestErrASCIIFirstName_Error(t *testing.T) {
	t.Parallel()

	err := &ErrASCIIFirstName{
		Name:  "José",
		ASCII: "Jose",
	}
	msg := err.Error()
	assert.Contains(t, msg, "José")
	assert.Contains(t, msg, "Jose")
	assert.Contains(t, msg, "ASCII First Name")
}

func TestErrASCIILastName_Error(t *testing.T) {
	t.Parallel()

	err := &ErrASCIILastName{
		Name:  "García",
		ASCII: "Garcia",
	}
	msg := err.Error()
	assert.Contains(t, msg, "García")
	assert.Contains(t, msg, "Garcia")
	assert.Contains(t, msg, "ASCII Last Name")
}

func TestPlayerID_ID(t *testing.T) {
	t.Parallel()

	t.Run("valid player ID", func(t *testing.T) {
		pid := playerID("419.p.12345")
		id, err := pid.ID()
		require.NoError(t, err)
		assert.Equal(t, uint(12345), id)
	})

	t.Run("invalid format - too few parts", func(t *testing.T) {
		pid := playerID("419.12345")
		_, err := pid.ID()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid playerID")
	})

	t.Run("invalid format - too many parts", func(t *testing.T) {
		pid := playerID("419.p.12345.extra")
		_, err := pid.ID()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid playerID")
	})

	t.Run("non-numeric ID", func(t *testing.T) {
		pid := playerID("419.p.abc")
		_, err := pid.ID()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid playerID")
	})
}

func TestPositionID_ID(t *testing.T) {
	t.Parallel()

	t.Run("valid position ID", func(t *testing.T) {
		pid := positionID("419.pos.5")
		id, err := pid.ID()
		require.NoError(t, err)
		assert.Equal(t, uint(5), id)
	})

	t.Run("invalid format - too few parts", func(t *testing.T) {
		pid := positionID("419.5")
		_, err := pid.ID()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid positionID")
	})

	t.Run("non-numeric ID", func(t *testing.T) {
		pid := positionID("419.pos.xyz")
		_, err := pid.ID()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid positionID")
	})
}

func TestNhlTeamID_ID(t *testing.T) {
	t.Parallel()

	t.Run("valid team ID", func(t *testing.T) {
		tid := nhlTeamID("419.t.22")
		id, err := tid.ID()
		require.NoError(t, err)
		assert.Equal(t, uint(22), id)
	})

	t.Run("invalid format - too few parts", func(t *testing.T) {
		tid := nhlTeamID("419.22")
		_, err := tid.ID()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid nhlTeamID")
	})

	t.Run("non-numeric ID", func(t *testing.T) {
		tid := nhlTeamID("419.t.abc")
		_, err := tid.ID()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid nhlTeamID")
	})
}
