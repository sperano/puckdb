package cache

import (
	"encoding/json"
	"errors"
	"fmt"
)

// OAuth2TokenMissingError indicates the user has no usable OAuth2 token and
// must log in again: none was found in the cache, or (Reason set) the stored
// one was rejected by the provider.
type OAuth2TokenMissingError struct {
	PublicURL string
	// Reason overrides the default "not found" explanation. It must not
	// carry token material.
	Reason string
}

func (e *OAuth2TokenMissingError) Error() string {
	loginURL := "/yahoo/login"
	if e.PublicURL != "" {
		loginURL = e.PublicURL + loginURL
	}
	reason := e.Reason
	if reason == "" {
		reason = "no oauth2 token found in redis"
	}
	return fmt.Sprintf("%s. Please navigate to %s to get a new token", reason, loginURL)
}

// CacheReadError indicates an error occurred while reading from the cache.
type CacheReadError struct {
	Key string
	Err error
}

func (e *CacheReadError) Error() string {
	return fmt.Sprintf("error reading %s from cache: %v", e.Key, e.Err)
}

// Unwrap exposes the underlying cache error so callers can use errors.Is/As.
func (e *CacheReadError) Unwrap() error { return e.Err }

// TokenDecodeError indicates a cached OAuth2 token could not be decoded. It
// deliberately does not wrap the decoder's error: encoding/json quotes pieces
// of its input (a stray character, a number literal) and here the input is a
// credential. Reason carries the position and kind of the failure instead.
type TokenDecodeError struct {
	Key    string
	Reason string
}

func (e *TokenDecodeError) Error() string {
	return fmt.Sprintf("error decoding token %s from cache: %s", e.Key, e.Reason)
}

// newTokenDecodeError reduces a JSON decoding error to a payload-free reason.
func newTokenDecodeError(key string, err error) *TokenDecodeError {
	return &TokenDecodeError{Key: key, Reason: describeJSONError(err)}
}

// describeJSONError classifies a decoding error by kind and position. It never
// formats err's message, which may quote the input being decoded.
func describeJSONError(err error) string {
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return fmt.Sprintf("JSON syntax error at offset %d", syntaxErr.Offset)
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return fmt.Sprintf("field %q has the wrong JSON type at offset %d", typeErr.Field, typeErr.Offset)
	}
	// The type name is payload-free and tells e.g. a bad expiry
	// (*time.ParseError) apart from other corruption.
	return fmt.Sprintf("malformed JSON (%T)", err)
}
