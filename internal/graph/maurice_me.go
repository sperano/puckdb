package graph

import (
	"context"

	"github.com/sperano/puckdb/internal/appuser"
	"github.com/sperano/puckdb/internal/graph/model"
)

// mauriceMe returns the caller's greeting name, resolving their login
// identity (so it fails like every Maurice field without one).
func (r *Resolver) mauriceMe(ctx context.Context) (*model.MauriceMe, error) {
	user, err := appuser.Current(ctx)
	if err != nil {
		return nil, err
	}
	return &model.MauriceMe{FirstName: user.FirstName}, nil
}
