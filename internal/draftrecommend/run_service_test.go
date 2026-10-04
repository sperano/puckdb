package draftrecommend

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type recordingRunStore struct {
	id     uuid.UUID
	err    error
	saves  int
	result Result
}

func (s *recordingRunStore) Save(_ context.Context, _ Input, result Result) (uuid.UUID, error) {
	s.saves++
	s.result = result
	return s.id, s.err
}

func TestServiceRecommendPersistsBeforeReturning(t *testing.T) {
	store := &recordingRunStore{id: uuid.New()}
	run, err := NewService(store).Recommend(context.Background(), recommendationInput(t))
	if err != nil {
		t.Fatal(err)
	}
	if store.saves != 1 || run.ID != store.id || store.result.RecommendationVersion != RecommendationVersion {
		t.Fatalf("stored run = %+v, store = %+v", run, store)
	}
}

func TestServiceRecommendReturnsStoreError(t *testing.T) {
	want := errors.New("write failed")
	store := &recordingRunStore{err: want}
	_, err := NewService(store).Recommend(context.Background(), recommendationInput(t))
	if !errors.Is(err, want) || store.saves != 1 {
		t.Fatalf("Recommend error = %v, saves = %d", err, store.saves)
	}
}
