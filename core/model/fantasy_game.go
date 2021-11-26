package model

import "gorm.io/gorm"

type FantasyGame struct {
	gorm.Model
	ID                 int
	Key                int
	Name               string
	Code               string
	Type               string
	URL                string
	Season             int
	IsRegistrationOver bool
	IsGameOver         bool
	IsOffseason        bool
}
