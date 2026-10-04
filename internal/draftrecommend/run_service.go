package draftrecommend

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var ErrStoreRequired = errors.New("draft recommendation store is required")

// RunStore persists every recommendation result before it is returned.
type RunStore interface {
	Save(ctx context.Context, input Input, result Result) (uuid.UUID, error)
}

// Service is the durable recommendation entry point. Evaluate remains
// available for deterministic replay and tests that must not write.
type Service struct{ store RunStore }

func NewService(store RunStore) *Service { return &Service{store: store} }

// Recommend evaluates one frozen input and persists its exact input/output.
func (s *Service) Recommend(ctx context.Context, input Input) (StoredRun, error) {
	if s == nil || s.store == nil {
		return StoredRun{}, ErrStoreRequired
	}
	result, err := Evaluate(input)
	if err != nil {
		return StoredRun{}, err
	}
	id, err := s.store.Save(ctx, input, result)
	if err != nil {
		return StoredRun{}, fmt.Errorf("persist draft recommendation: %w", err)
	}
	return StoredRun{ID: id, Input: input, Result: result}, nil
}
