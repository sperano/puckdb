package draftrecommend

import (
	"math"
	"testing"

	"github.com/sperano/puckdb/internal/draft"
	"github.com/stretchr/testify/assert"
)

const strategyValidationStatID = 1

func TestEvaluateValidatesSharedCategoryWeightLimit(t *testing.T) {
	tests := map[string]struct {
		weight  float64
		wantErr bool
	}{
		"maximum":       {weight: draft.MaxCategoryWeight},
		"above maximum": {weight: math.Nextafter(draft.MaxCategoryWeight, math.Inf(1)), wantErr: true},
		"negative":      {weight: -1, wantErr: true},
		"not a number":  {weight: math.NaN(), wantErr: true},
		"infinity":      {weight: math.Inf(1), wantErr: true},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			input := recommendationInput(t)
			input.Strategy.CategoryWeights = map[int]float64{strategyValidationStatID: test.weight}
			_, err := Evaluate(input)
			if test.wantErr {
				assert.ErrorIs(t, err, ErrInvalidInput)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
