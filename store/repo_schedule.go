package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
)

// ScheduleRepo provides access to daily schedule data.
type ScheduleRepo struct {
	storage Storage
}

// NewScheduleRepo creates a new ScheduleRepo.
func NewScheduleRepo(storage Storage) *ScheduleRepo {
	return &ScheduleRepo{storage: storage}
}

// Get returns the parsed daily schedule for a date.
func (r *ScheduleRepo) Get(date time.Time) (*nhl.DailySchedule, error) {
	data, err := r.storage.Read(DailySchedulePath(date))
	if err != nil {
		return nil, err
	}

	var schedule nhl.DailySchedule
	if err := json.Unmarshal(data, &schedule); err != nil {
		return nil, fmt.Errorf("parse daily schedule for %s: %w", date.Format("2006-01-02"), err)
	}

	return &schedule, nil
}

// GetRaw returns the raw daily schedule data for a date.
func (r *ScheduleRepo) GetRaw(date time.Time) ([]byte, error) {
	return r.storage.Read(DailySchedulePath(date))
}

// Save stores daily schedule data for a date.
func (r *ScheduleRepo) Save(date time.Time, data []byte) error {
	return r.storage.Write(DailySchedulePath(date), data)
}

// Exists returns true if a daily schedule exists for the date.
func (r *ScheduleRepo) Exists(date time.Time) bool {
	return r.storage.Exists(DailySchedulePath(date))
}
