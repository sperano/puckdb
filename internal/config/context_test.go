package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUserFromContext(t *testing.T) {
	t.Parallel()
	user, err := UserFromContext(context.WithValue(context.TODO(), CtxUser, "foo"))
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, "foo", user)
}

func TestUserFromContext_Missing(t *testing.T) {
	t.Parallel()
	user, err := UserFromContext(context.TODO())
	assert.Equal(t, "", user)
	assert.Equal(t, ErrNoUserInContext, err)
}

func TestUserFromContext_CastError(t *testing.T) {
	t.Parallel()
	user, err := UserFromContext(context.WithValue(context.TODO(), CtxUser, 123))
	assert.Empty(t, user)
	assert.Equal(t, ErrCantCastCtxUser, err)
}
