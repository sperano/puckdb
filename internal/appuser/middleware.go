package appuser

import (
	"context"
	"net/http"
	"sync"
)

type contextKey struct{}

// lazyUser resolves the user of one request at most once, on first use, so
// requests that never need a user (most GraphQL queries, draft board polls)
// cost no database write.
type lazyUser struct {
	once    sync.Once
	resolve func(ctx context.Context) (User, error)
	user    User
	err     error
}

// Middleware makes the request's user available through Current. Resolution
// is lazy: the first Current call authenticates the request with provider,
// upserts the user, resolves or opens the PuckDB session, and sets the session
// cookie when a new session was opened (before the response is written).
func Middleware(provider Provider, store Store, hasher *Hasher) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			lazy := &lazyUser{resolve: func(ctx context.Context) (User, error) {
				return resolveRequest(ctx, provider, store, hasher, w, r)
			}}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey{}, lazy)))
		})
	}
}

func resolveRequest(ctx context.Context, provider Provider, store Store, hasher *Hasher, w http.ResponseWriter, r *http.Request) (User, error) {
	identity, err := provider.Authenticate(ctx, r.Header)
	if err != nil {
		return User{}, err
	}
	token, err := newSessionToken()
	if err != nil {
		return User{}, err
	}
	req := SessionRequest{Identity: identity, FreshHash: hasher.Hash(token)}
	if cookie, err := r.Cookie(SessionCookieName); err == nil && cookie.Value != "" {
		req.PresentedHash = hasher.Hash(cookie.Value)
	}
	res, err := store.Resolve(ctx, req)
	if err != nil {
		return User{}, err
	}
	if res.Created {
		http.SetCookie(w, sessionCookie(token))
	}
	user := res.User
	user.Username = identity.Username
	return user, nil
}

// Current returns the user of the request in ctx, resolving it on first use.
// It returns ErrUnauthenticated outside Middleware or without an identity, and
// the provider's error for a rejected one.
func Current(ctx context.Context) (User, error) {
	switch v := ctx.Value(contextKey{}).(type) {
	case *lazyUser:
		v.once.Do(func() { v.user, v.err = v.resolve(ctx) })
		return v.user, v.err
	case User:
		return v, nil
	default:
		return User{}, ErrUnauthenticated
	}
}

// WithUser returns a context whose Current is user, for callers that resolve
// the user without an HTTP request.
func WithUser(ctx context.Context, user User) context.Context {
	return context.WithValue(ctx, contextKey{}, user)
}
