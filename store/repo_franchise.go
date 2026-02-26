package store

import (
	"encoding/json"
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
)

// FranchiseRepo provides access to NHL franchise data.
type FranchiseRepo struct {
	storage Storage
}

// NewFranchiseRepo creates a new FranchiseRepo.
func NewFranchiseRepo(storage Storage) *FranchiseRepo {
	return &FranchiseRepo{storage: storage}
}

// Get returns the parsed franchises list.
func (r *FranchiseRepo) Get() ([]nhl.Franchise, error) {
	data, err := r.storage.Read(FranchisesPath())
	if err != nil {
		return nil, err
	}

	var franchises []nhl.Franchise
	if err := json.Unmarshal(data, &franchises); err != nil {
		return nil, fmt.Errorf("parse franchises: %w", err)
	}

	return franchises, nil
}

// GetRaw returns the raw franchises data.
func (r *FranchiseRepo) GetRaw() ([]byte, error) {
	return r.storage.Read(FranchisesPath())
}

// Save stores franchises data.
func (r *FranchiseRepo) Save(data []byte) error {
	return r.storage.Write(FranchisesPath(), data)
}

// Exists returns true if franchises data exists.
func (r *FranchiseRepo) Exists() bool {
	return r.storage.Exists(FranchisesPath())
}
