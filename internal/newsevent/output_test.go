package newsevent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const outputValidEventJSON = `{"events": [{"player": "P1", "type": "injury", "report_status": "confirmed",
  "attribution": "", "effective_from": null, "duration": null, "change": null, "evidence": []}]}`

func TestDecodeOutputValid(t *testing.T) {
	events, err := decodeOutput(outputValidEventJSON)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "P1", events[0].Player)
	assert.Equal(t, "injury", events[0].Type)
	assert.Equal(t, "confirmed", events[0].ReportStatus)
}

func TestDecodeOutputCodeFenceTolerated(t *testing.T) {
	t.Run("json-tagged fence", func(t *testing.T) {
		events, err := decodeOutput("```json\n" + outputValidEventJSON + "\n```")
		require.NoError(t, err)
		assert.Len(t, events, 1)
	})
	t.Run("bare fence", func(t *testing.T) {
		events, err := decodeOutput("```\n" + outputValidEventJSON + "\n```")
		require.NoError(t, err)
		assert.Len(t, events, 1)
	})
}

func TestDecodeOutputTextAfterObjectRejected(t *testing.T) {
	_, err := decodeOutput(outputValidEventJSON + "\nHope that helps!")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidOutput)
}

func TestDecodeOutputUnknownFieldRejected(t *testing.T) {
	t.Run("unknown top-level field", func(t *testing.T) {
		_, err := decodeOutput(`{"events": [], "return_date": "2026-10-01"}`)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidOutput)
	})
	t.Run("unknown field inside an event", func(t *testing.T) {
		reply := `{"events": [{"player": "P1", "type": "injury", "report_status": "confirmed",
		  "attribution": "", "effective_from": null, "duration": null, "change": null,
		  "evidence": [], "fantasy_impact": "high"}]}`
		_, err := decodeOutput(reply)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidOutput)
	})
}

func TestDecodeOutputMissingEventsRejected(t *testing.T) {
	_, err := decodeOutput(`{}`)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidOutput)
}

func TestDecodeOutputTooManyEventsRejected(t *testing.T) {
	const oneEvent = `{"player": "P1", "type": "injury", "report_status": "confirmed",
	  "attribution": "", "effective_from": null, "duration": null, "change": null, "evidence": []}`
	var b strings.Builder
	b.WriteString(`{"events": [`)
	for i := 0; i < maxEventsPerOutput+1; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprint(&b, oneEvent)
	}
	b.WriteString(`]}`)

	_, err := decodeOutput(b.String())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidOutput)
}

func TestDecodeOutputNotJSONRejected(t *testing.T) {
	_, err := decodeOutput("Sorry, I can't help with that request.")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidOutput)
}
