package auth

import (
	"context"
)

const (
	CtxUser = "user"
)

func UserFromContext(ctx context.Context) (string, error) {
	value := ctx.Value(CtxUser)
	if value == nil {

		return "", ErrNoUserInContext
	}
	user, ok := value.(string)
	if ok {
		return user, nil
	}
	return "", ErrCantCastCtxUser
}
