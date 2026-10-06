package appuser

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIdentityFromHeaders(t *testing.T) {
	h := http.Header{}
	_, ok := IdentityFromHeaders(h)
	assert.False(t, ok, "no subject, no identity")

	h.Set(HeaderUsername, "eric")
	_, ok = IdentityFromHeaders(h)
	assert.False(t, ok, "a username alone is not an identity")

	h.Set(HeaderUID, " abc123 ")
	h.Set(HeaderName, "Eric S")
	id, ok := IdentityFromHeaders(h)
	require.True(t, ok)
	assert.Equal(t, Identity{Subject: "abc123", Username: "eric", DisplayName: "Eric S"}, id)
}

func TestHasher(t *testing.T) {
	_, err := NewHasher(nil)
	require.Error(t, err)

	a, err := NewHasher([]byte("key-a"))
	require.NoError(t, err)
	b, err := NewHasher([]byte("key-b"))
	require.NoError(t, err)
	assert.Equal(t, a.Hash("token"), a.Hash("token"), "deterministic")
	assert.NotEqual(t, a.Hash("token"), a.Hash("other"))
	assert.NotEqual(t, a.Hash("token"), b.Hash("token"), "keyed: another key, another hash")
	assert.NotContains(t, string(a.Hash("token")), "token", "the raw token is not stored")

	key, err := RandomHashKey()
	require.NoError(t, err)
	assert.Len(t, key, HashKeyBytes)
}

// fakeStore records requests and answers with a fixed resolution.
type fakeStore struct {
	mu       sync.Mutex
	requests []SessionRequest
	res      Resolution
	err      error
}

func (f *fakeStore) Resolve(_ context.Context, req SessionRequest) (Resolution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	return f.res, f.err
}

// serve runs one request through Middleware, calling Current calls times.
func serve(t *testing.T, store Store, req *http.Request, calls int) (*httptest.ResponseRecorder, []User, []error) {
	t.Helper()
	hasher, err := NewHasher([]byte("test-key"))
	require.NoError(t, err)
	var users []User
	var errs []error
	handler := Middleware(store, hasher)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for range calls {
			u, err := Current(r.Context())
			users, errs = append(users, u), append(errs, err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec, users, errs
}

func authenticated() *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/graphql/query", nil)
	req.Header.Set(HeaderUID, "subject-1")
	req.Header.Set(HeaderUsername, "eric")
	return req
}

func TestMiddleware_NewSessionSetsCookieOnce(t *testing.T) {
	store := &fakeStore{res: Resolution{User: User{ID: "u1", SessionID: "s1"}, Created: true}}
	rec, users, errs := serve(t, store, authenticated(), 2)

	require.NoError(t, errs[0])
	assert.Equal(t, User{ID: "u1", SessionID: "s1"}, users[0])
	assert.Equal(t, users[0], users[1])
	require.Len(t, store.requests, 1, "resolution happens once per request")
	assert.Nil(t, store.requests[0].PresentedHash)
	assert.Equal(t, Identity{Subject: "subject-1", Username: "eric"}, store.requests[0].Identity)

	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)
	c := cookies[0]
	assert.Equal(t, SessionCookieName, c.Name)
	assert.True(t, c.HttpOnly)
	assert.True(t, c.Secure)
	hasher, err := NewHasher([]byte("test-key"))
	require.NoError(t, err)
	assert.True(t, bytes.Equal(hasher.Hash(c.Value), store.requests[0].FreshHash),
		"the store keeps the hash of exactly the token sent as the cookie")
}

func TestMiddleware_ExistingSessionSendsNoCookie(t *testing.T) {
	store := &fakeStore{res: Resolution{User: User{ID: "u1", SessionID: "s1"}}}
	req := authenticated()
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "existing-token"})
	rec, _, errs := serve(t, store, req, 1)

	require.NoError(t, errs[0])
	assert.Empty(t, rec.Result().Cookies())
	hasher, err := NewHasher([]byte("test-key"))
	require.NoError(t, err)
	assert.Equal(t, hasher.Hash("existing-token"), store.requests[0].PresentedHash)
}

func TestMiddleware_UnusedUserCostsNothing(t *testing.T) {
	store := &fakeStore{}
	serve(t, store, authenticated(), 0)
	assert.Empty(t, store.requests, "requests that never ask for the user do not touch the store")
}

func TestMiddleware_Errors(t *testing.T) {
	store := &fakeStore{}
	_, _, errs := serve(t, store, httptest.NewRequest(http.MethodPost, "/", nil), 1)
	require.ErrorIs(t, errs[0], ErrUnauthenticated)
	assert.Empty(t, store.requests)

	failing := &fakeStore{err: ErrUserDisabled}
	rec, _, errs := serve(t, failing, authenticated(), 1)
	require.ErrorIs(t, errs[0], ErrUserDisabled)
	assert.Empty(t, rec.Result().Cookies())
}

func TestCurrent_OutsideMiddleware(t *testing.T) {
	_, err := Current(context.Background())
	require.ErrorIs(t, err, ErrUnauthenticated)

	user := User{ID: "u", SessionID: "s"}
	got, err := Current(WithUser(context.Background(), user))
	require.NoError(t, err)
	assert.Equal(t, user, got)
}

func TestMiddleware_StoreErrorIsReturned(t *testing.T) {
	boom := errors.New("db down")
	_, _, errs := serve(t, &fakeStore{err: boom}, authenticated(), 1)
	require.ErrorIs(t, errs[0], boom)
}
