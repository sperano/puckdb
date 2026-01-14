package redis

import "fmt"

type OAuth2TokenMissingError struct{}

func (e *OAuth2TokenMissingError) Error() string {
	return "no oauth2 token found in redis. Please navigate to api server at /yahoo/login to get a new token"
}

func NewOAuth2TokenMissingError() *OAuth2TokenMissingError {
	return &OAuth2TokenMissingError{}
}

type CacheReadError struct {
	Key string
	Err error
}

func (e *CacheReadError) Error() string {
	return fmt.Errorf("error reading %s from cache: %w", e.Key, e.Err).Error()
}

func NewCacheReadError(key string, err error) *CacheReadError {
	return &CacheReadError{Key: key, Err: err}
}
