package auth

import "errors"

var ErrCantCastCtxUser = errors.New("can't cast ctx user")
var ErrNoUserInContext = errors.New("no user found in context")
