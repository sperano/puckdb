package model

import "gorm.io/gorm"

type Player struct {
	gorm.Model
	ID                 int
	Key                string
	FirstName          string
	LastName           string
	EditorialPlayerKey string
}
