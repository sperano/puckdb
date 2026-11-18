package appuser

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Member is one entry of an instance's members file: who a login email
// belongs to. Both fields are optional.
type Member struct {
	// Name is the person's first name, used as their display name and
	// greeting instead of what the login provider gives (Cloudflare Access
	// gives none).
	Name string
	// YahooEmail is the email of the person's Yahoo account, when it differs
	// from the login email: Yahoo team managers with that email are linked
	// to them too (yahoolink.SyncEmailLinks).
	YahooEmail string
}

// Members maps login emails, compared case-insensitively, to their Member.
// The zero value has no entries.
type Members struct {
	byEmail map[string]Member
}

// memberEntry is one entry of the members file, as written.
type memberEntry struct {
	Name       string `yaml:"name"`
	YahooEmail string `yaml:"yahoo_email"`
}

// LoadMembers reads and parses the members file at path (ParseMembers).
func LoadMembers(path string) (Members, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Members{}, fmt.Errorf("read members file: %w", err)
	}
	members, err := ParseMembers(data)
	if err != nil {
		return Members{}, fmt.Errorf("members file %s: %w", path, err)
	}
	return members, nil
}

// ParseMembers parses a members file: a YAML mapping of login emails to
// entries with an optional name and yahoo_email, for example
//
//	friend@gmail.com:
//	  name: Jean
//	  yahoo_email: jean.t@yahoo.ca
//	you@example.com:
//
// It refuses a key or yahoo_email that is not a bare email address, an
// unknown field, and two keys naming the same email in different case. An
// empty file has no entries.
func ParseMembers(data []byte) (Members, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var entries map[string]*memberEntry
	if err := dec.Decode(&entries); err != nil && !errors.Is(err, io.EOF) {
		return Members{}, fmt.Errorf("parse: %w", err)
	}
	members := Members{byEmail: make(map[string]Member, len(entries))}
	for email, entry := range entries {
		key, err := memberEmail(email)
		if err != nil {
			return Members{}, fmt.Errorf("login email %q: %w", email, err)
		}
		if _, dup := members.byEmail[key]; dup {
			return Members{}, fmt.Errorf("login email %q is listed twice (emails are compared case-insensitively)", email)
		}
		member, err := memberFromEntry(entry)
		if err != nil {
			return Members{}, fmt.Errorf("login email %q: %w", email, err)
		}
		members.byEmail[key] = member
	}
	return members, nil
}

// memberFromEntry validates one entry; a nil entry (a key with no value) is
// a member with neither field.
func memberFromEntry(entry *memberEntry) (Member, error) {
	if entry == nil {
		return Member{}, nil
	}
	member := Member{Name: strings.TrimSpace(entry.Name)}
	if yahoo := strings.TrimSpace(entry.YahooEmail); yahoo != "" {
		if _, err := memberEmail(yahoo); err != nil {
			return Member{}, fmt.Errorf("yahoo_email %q: %w", entry.YahooEmail, err)
		}
		member.YahooEmail = yahoo
	}
	return member, nil
}

// memberEmail returns the lookup key of email: its lower-case form. It
// refuses anything but a bare address (no display name, no angle brackets).
func memberEmail(email string) (string, error) {
	addr, err := mail.ParseAddress(email)
	if err != nil {
		return "", fmt.Errorf("not an email address: %w", err)
	}
	if addr.Name != "" || addr.Address != email {
		return "", errors.New("not a bare email address")
	}
	return strings.ToLower(email), nil
}

// Len is the number of entries.
func (m Members) Len() int { return len(m.byEmail) }

// Lookup returns the entry of email, compared case-insensitively; ok is
// false for an empty email or one without an entry.
func (m Members) Lookup(email string) (member Member, ok bool) {
	if email == "" {
		return Member{}, false
	}
	member, ok = m.byEmail[strings.ToLower(strings.TrimSpace(email))]
	return member, ok
}

// apply returns id with its login email's entry applied: the entry's name
// becomes the display name and first name, its Yahoo email the member Yahoo
// email. id is unchanged without an entry.
func (m Members) apply(id Identity) Identity {
	member, ok := m.Lookup(id.Email)
	if !ok {
		return id
	}
	if member.Name != "" {
		id.DisplayName = member.Name
		id.FirstName = member.Name
	}
	id.MemberYahooEmail = member.YahooEmail
	return id
}

// WithMembers returns p with members applied to every identity it
// authenticates (the entry of the identity's login email); p itself when
// members has no entries.
func WithMembers(p Provider, members Members) Provider {
	if members.Len() == 0 {
		return p
	}
	return memberProvider{Provider: p, members: members}
}

// memberProvider is a Provider whose identities carry their members file
// entry.
type memberProvider struct {
	Provider
	members Members
}

// Authenticate implements Provider.
func (p memberProvider) Authenticate(ctx context.Context, h http.Header) (Identity, error) {
	id, err := p.Provider.Authenticate(ctx, h)
	if err != nil {
		return Identity{}, err
	}
	return p.members.apply(id), nil
}
