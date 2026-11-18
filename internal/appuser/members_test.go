package appuser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const membersYAML = `
friend@gmail.com:
  name: " Jean "
  yahoo_email: jean.t@yahoo.ca
Eric@Example.com:
  name: Éric
nobody@example.com:
`

func TestParseMembers(t *testing.T) {
	members, err := ParseMembers([]byte(membersYAML))
	require.NoError(t, err)
	assert.Equal(t, 3, members.Len())

	jean, ok := members.Lookup("friend@gmail.com")
	require.True(t, ok)
	assert.Equal(t, Member{Name: "Jean", YahooEmail: "jean.t@yahoo.ca"}, jean, "the name is trimmed")

	eric, ok := members.Lookup(" eric@EXAMPLE.com ")
	require.True(t, ok, "lookup ignores case and surrounding space")
	assert.Equal(t, Member{Name: "Éric"}, eric)

	nobody, ok := members.Lookup("nobody@example.com")
	require.True(t, ok, "a key without a value is an entry")
	assert.Equal(t, Member{}, nobody)

	_, ok = members.Lookup("stranger@example.com")
	assert.False(t, ok)
	_, ok = members.Lookup("")
	assert.False(t, ok)
}

func TestParseMembers_EmptyFileHasNoEntries(t *testing.T) {
	for _, data := range []string{"", "\n", "# nobody yet\n", "{}\n"} {
		members, err := ParseMembers([]byte(data))
		require.NoError(t, err, "%q", data)
		assert.Equal(t, 0, members.Len(), "%q", data)
	}
}

func TestParseMembers_Refuses(t *testing.T) {
	cases := map[string]struct {
		yaml string
		want string
	}{
		"case-insensitive duplicate": {
			yaml: "a@example.com: {}\nA@example.com: {}\n",
			want: "listed twice",
		},
		"key not an email": {
			yaml: "jean: {name: Jean}\n",
			want: `login email "jean": not an email address`,
		},
		"key with a display name": {
			yaml: "\"Jean <jean@example.com>\": {}\n",
			want: "not a bare email address",
		},
		"bad yahoo_email": {
			yaml: "jean@example.com: {yahoo_email: jean}\n",
			want: `yahoo_email "jean": not an email address`,
		},
		"unknown field": {
			yaml: "jean@example.com: {yahoo_emails: [jean@yahoo.ca]}\n",
			want: "field yahoo_emails not found",
		},
		"not a mapping": {
			yaml: "- jean@example.com\n",
			want: "cannot unmarshal !!seq into map",
		},
		"scalar root": {
			yaml: "jean@example.com\n",
			want: "cannot unmarshal !!str `jean@ex...` into map",
		},
		"null root": {
			yaml: "~\n",
			want: "parse: the file is not a mapping of login emails",
		},
		"empty document marker": {
			yaml: "---\n",
			want: "parse: the file is not a mapping of login emails",
		},
		"second document": {
			yaml: "a@example.com: {}\n---\nb@example.com: {}\n",
			want: "parse: the file holds more than one YAML document",
		},
		"malformed second document": {
			yaml: "a@example.com: {}\n---\nb@example.com: [\n",
			want: "parse: yaml: line 3: did not find expected node content",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseMembers([]byte(tc.yaml))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestLoadMembers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "members.yaml")
	require.NoError(t, os.WriteFile(path, []byte(membersYAML), 0o600))
	members, err := LoadMembers(path)
	require.NoError(t, err)
	assert.Equal(t, 3, members.Len())

	_, err = LoadMembers(filepath.Join(t.TempDir(), "missing.yaml"))
	require.ErrorIs(t, err, os.ErrNotExist)

	bad := filepath.Join(t.TempDir(), "bad.yaml")
	require.NoError(t, os.WriteFile(bad, []byte("jean: {}\n"), 0o600))
	_, err = LoadMembers(bad)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "members file "+bad)
}

func TestIdentity_GreetingName(t *testing.T) {
	cases := map[string]struct {
		id   Identity
		want string
	}{
		"first name wins":        {Identity{FirstName: "Marie Ève", DisplayName: "Marie Ève Roy", Username: "mer"}, "Marie Ève"},
		"first word of the name": {Identity{DisplayName: "  Éric Spérano ", Username: "eric"}, "Éric"},
		"hyphenated first name":  {Identity{DisplayName: "Jean-François Tremblay"}, "Jean-François"},
		"username without names": {Identity{DisplayName: "  ", Username: "eric@example.com"}, "eric@example.com"},
		"nothing":                {Identity{}, ""},
	}
	for name, tc := range cases {
		assert.Equal(t, tc.want, tc.id.GreetingName(), name)
	}
}

// stubProvider authenticates every request as id.
type stubProvider struct {
	AuthentikProvider
	id  Identity
	err error
}

func (p stubProvider) Authenticate(context.Context, http.Header) (Identity, error) {
	return p.id, p.err
}

func TestWithMembers_AppliesTheLoginEmailsEntry(t *testing.T) {
	members, err := ParseMembers([]byte(membersYAML))
	require.NoError(t, err)
	ctx := context.Background()

	cloudflare := Identity{Provider: ProviderCloudflare, Subject: "s", Username: "Friend@Gmail.com", Email: "Friend@Gmail.com"}
	got, err := WithMembers(stubProvider{id: cloudflare}, members).Authenticate(ctx, nil)
	require.NoError(t, err)
	want := cloudflare
	want.DisplayName, want.FirstName, want.MemberYahooEmail = "Jean", "Jean", "jean.t@yahoo.ca"
	assert.Equal(t, want, got)

	nameless := Identity{Provider: ProviderAuthentik, Subject: "s", Username: "nobody", DisplayName: "No Body", Email: "nobody@example.com"}
	got, err = WithMembers(stubProvider{id: nameless}, members).Authenticate(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, nameless, got, "an entry without fields keeps the provider's name")

	stranger := Identity{Provider: ProviderCloudflare, Subject: "s", Username: "x@example.com", Email: "x@example.com"}
	got, err = WithMembers(stubProvider{id: stranger}, members).Authenticate(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, stranger, got, "no entry, no change")

	_, err = WithMembers(stubProvider{err: ErrInvalidToken}, members).Authenticate(ctx, nil)
	require.ErrorIs(t, err, ErrInvalidToken)
}

func TestWithMembers_KeepsTheProvidersNameAndHeaders(t *testing.T) {
	members, err := ParseMembers([]byte(membersYAML))
	require.NoError(t, err)
	wrapped := WithMembers(&CloudflareProvider{}, members)
	assert.Equal(t, ProviderCloudflare, wrapped.Name())
	assert.Equal(t, headerPrefixAuthentik, wrapped.foreignPrefix(), "the foreign headers are still stripped")

	plain := AuthentikProvider{}
	assert.Equal(t, Provider(plain), WithMembers(plain, Members{}), "no entries: the provider itself")
}

func TestMiddleware_UserCarriesTheGreetingName(t *testing.T) {
	members, err := ParseMembers([]byte("eric@example.com: {name: Éric}\n"))
	require.NoError(t, err)
	store := &fakeStore{res: Resolution{User: User{ID: "u1", SessionID: "s1"}}}

	req := authenticated()
	req.Header.Set(HeaderName, "Éric Spérano")
	_, users, errs := serveWith(t, AuthentikProvider{}, store, req, 1)
	require.NoError(t, errs[0])
	assert.Equal(t, "Éric", users[0].FirstName, "first word of the Authentik name")

	req = authenticated()
	req.Header.Set(HeaderName, "Eric S")
	req.Header.Set(HeaderEmail, "eric@example.com")
	_, users, errs = serveWith(t, WithMembers(AuthentikProvider{}, members), store, req, 1)
	require.NoError(t, errs[0])
	assert.Equal(t, "Éric", users[0].FirstName, "the entry's name")
	assert.Equal(t, "Éric", store.requests[1].Identity.DisplayName, "the store gets the entry's name")

	_, users, errs = serve(t, store, httptest.NewRequest(http.MethodPost, "/", nil), 1)
	require.ErrorIs(t, errs[0], ErrUnauthenticated)
	assert.Empty(t, users[0].FirstName)
}
