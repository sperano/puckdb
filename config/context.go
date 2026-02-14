package config

import (
	"context"
	"errors"
)

// Context key for user identity
const CtxUser = "user"

// Errors for user context extraction
var (
	ErrNoUserInContext = errors.New("no user found in context")
	ErrCantCastCtxUser = errors.New("can't cast ctx user")
)

// UserFromContext extracts the user identity from the context.
func UserFromContext(ctx context.Context) (string, error) {
	value := ctx.Value(CtxUser)
	if value == nil {
		return "", ErrNoUserInContext
	}
	user, ok := value.(string)
	if !ok {
		return "", ErrCantCastCtxUser
	}
	return user, nil
}
