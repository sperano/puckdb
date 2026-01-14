package auth

import (
	"context"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestUserFromContext(t *testing.T) {
	t.Parallel()
	user, err := UserFromContext(context.WithValue(context.TODO(), CtxUser, "foo"))
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, "foo", user)
}

func TestGetCtxUserMissing(t *testing.T) {
	t.Parallel()
	user, err := UserFromContext(context.TODO())
	assert.Equal(t, "", user)
	assert.Equal(t, ErrNoUserInContext, err)
}

func TestGetCtxUserCastError(t *testing.T) {
	t.Parallel()
	user, err := UserFromContext(context.WithValue(context.TODO(), CtxUser, 123))
	assert.Equal(t, 0, len(user))
	assert.Equal(t, ErrCantCastCtxUser, err)
}
