package config

import "errors"

const (
	DefaultTemporalNamespace = "puckdb"
	DefaultUser              = "eric"
	DefaultLogLevel          = "info"
)

var ErrNotImplementedYet = errors.New("not implemented yet")
