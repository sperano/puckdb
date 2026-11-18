package graph

import (
	"context"

	"github.com/sperano/puckdb/internal/graph/model"
)

// mauriceModel answers the model Maurice is configured with, to any user of
// the instance.
func (r *Resolver) mauriceModel(ctx context.Context) (*model.MauriceModel, error) {
	svc, _, err := r.scopedMaurice(ctx)
	if err != nil {
		return nil, err
	}
	m := svc.Model()
	return &model.MauriceModel{Provider: m.Provider, Model: m.Model}, nil
}
