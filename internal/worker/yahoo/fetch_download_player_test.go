package yahoo

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/sperano/puckdb/internal/httpx"
	"github.com/stretchr/testify/assert"
)

// ────────────────────────────────────────────────────────────────────────────
// Shared XML fixtures
// ────────────────────────────────────────────────────────────────────────────

const (
	// minimalMatchupXML is a valid two-team matchup for week 1.
	minimalMatchupXML = `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <league>
    <scoreboard>
      <week>1</week>
      <matchups count="1">
        <matchup>
          <week>1</week>
          <status>postevent</status>
          <is_playoffs>0</is_playoffs>
          <is_consolation>0</is_consolation>
          <teams count="2">
            <team>
              <team_id>1</team_id>
              <team_points><total>55.5</total></team_points>
            </team>
            <team>
              <team_id>2</team_id>
              <team_points><total>42.0</total></team_points>
            </team>
          </teams>
        </matchup>
      </matchups>
    </scoreboard>
  </league>
</fantasy_content>`
)

func TestIsEndOfMatchupWeeks(t *testing.T) {
	t.Parallel()
	wrap := func(err error) error { return fmt.Errorf("%w: Matchups: %w", ErrDownload, err) }
	cases := map[string]struct {
		err  error
		want bool
	}{
		"400 rejection":         {wrap(&httpx.HTTPError{StatusCode: http.StatusBadRequest}), true},
		"404":                   {wrap(&httpx.HTTPError{StatusCode: http.StatusNotFound}), false},
		"429 throttled":         {wrap(&httpx.HTTPError{StatusCode: http.StatusTooManyRequests}), false},
		"500":                   {wrap(&httpx.HTTPError{StatusCode: http.StatusInternalServerError}), false},
		"transport":             {wrap(errors.New("timeout")), false},
		"4xx not from download": {&httpx.HTTPError{StatusCode: http.StatusBadRequest}, false},
		"nil":                   {nil, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, isEndOfMatchupWeeks(tc.err))
		})
	}
}
