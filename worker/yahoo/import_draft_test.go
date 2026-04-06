package yahoo

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseYahooTeamKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		key     string
		want    int
		wantErr bool
	}{
		{"valid key", "423.l.12345.t.1", 1, false},
		{"multi-digit team", "423.l.12345.t.12", 12, false},
		{"no .t. separator", "423.l.12345", 0, true},
		{"empty string", "", 0, true},
		{"non-numeric team ID", "423.l.12345.t.abc", 0, true},
		{"multiple .t. separators", "423.t.1.t.2", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseYahooTeamKey(tt.key)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestParseYahooPlayerKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		key     string
		want    int
		wantErr bool
	}{
		{"valid key", "423.p.6616", 6616, false},
		{"single digit player", "423.p.1", 1, false},
		{"no .p. separator", "423.6616", 0, true},
		{"empty string", "", 0, true},
		{"non-numeric player ID", "423.p.abc", 0, true},
		{"multiple .p. separators", "423.p.1.p.2", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseYahooPlayerKey(tt.key)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}
