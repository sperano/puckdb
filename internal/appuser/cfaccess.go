package appuser

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

const (
	// cfAccessCertsPath is the team's JWKS path on its team domain.
	cfAccessCertsPath = "/cdn-cgi/access/certs"
	// jwtAlgRS256 is the only signing algorithm accepted.
	jwtAlgRS256 = "RS256"
	// jwtParts is the number of dot-separated parts of a JWS compact token.
	jwtParts = 3
	// maxTokenBytes bounds the work done on a presented token.
	maxTokenBytes = 16 << 10
	// tokenClockSkew tolerates clock drift between Cloudflare and this host
	// on exp and nbf.
	tokenClockSkew = 30 * time.Second
)

// CloudflareAccessConfig identifies the Cloudflare Access application whose
// tokens an instance accepts.
type CloudflareAccessConfig struct {
	// TeamDomain is the Zero Trust team domain, <team>.cloudflareaccess.com:
	// the token issuer is https://<TeamDomain> and its signing keys are
	// served under it.
	TeamDomain string
	// Audience is the Access application's AUD tag.
	Audience string
}

// AccessClaims are the verified claims of a Cloudflare Access token.
type AccessClaims struct {
	Subject string
	Email   string
}

// AccessVerifier verifies Cloudflare Access JWTs (RS256) against the team's
// signing keys, checking issuer, audience and expiry.
type AccessVerifier struct {
	issuer   string
	audience string
	keys     *keySet
	now      func() time.Time
}

// NewAccessVerifier returns a verifier for cfg's application.
func NewAccessVerifier(cfg CloudflareAccessConfig) (*AccessVerifier, error) {
	if err := validTeamDomain(cfg.TeamDomain); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Audience) == "" {
		return nil, errors.New("cloudflare access audience (AUD tag) is empty")
	}
	issuer := "https://" + cfg.TeamDomain
	return newAccessVerifier(issuer, cfg.Audience, newKeySet(issuer+cfAccessCertsPath, nil, time.Now), time.Now), nil
}

func newAccessVerifier(issuer, audience string, keys *keySet, now func() time.Time) *AccessVerifier {
	return &AccessVerifier{issuer: issuer, audience: strings.TrimSpace(audience), keys: keys, now: now}
}

// validTeamDomain accepts a bare host name such as team.cloudflareaccess.com.
func validTeamDomain(domain string) error {
	if domain == "" {
		return errors.New("cloudflare access team domain is empty")
	}
	u, err := url.Parse("https://" + domain)
	if err != nil || u.Host != domain || u.Hostname() != domain || u.Path != "" || u.User != nil {
		return fmt.Errorf("cloudflare access team domain %q is not a bare host name (want <team>.cloudflareaccess.com)", domain)
	}
	return nil
}

// jwtHeader is the JOSE header of a token.
type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

// jwtClaims are the registered and Cloudflare claims read from a token.
type jwtClaims struct {
	Issuer    string   `json:"iss"`
	Audience  audience `json:"aud"`
	Subject   string   `json:"sub"`
	Email     string   `json:"email"`
	ExpiresAt *int64   `json:"exp"`
	NotBefore *int64   `json:"nbf"`
}

// audience is the aud claim: a string or an array of strings.
type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	if bytes.HasPrefix(bytes.TrimSpace(b), []byte(`"`)) {
		var one string
		if err := json.Unmarshal(b, &one); err != nil {
			return err
		}
		*a = audience{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

// Verify checks token's signature, issuer, audience and expiry and returns
// its identity claims. A token failing a check returns ErrInvalidToken; a
// failure to obtain the signing keys returns another error.
func (v *AccessVerifier) Verify(ctx context.Context, token string) (AccessClaims, error) {
	payload, err := v.verifySignature(ctx, token)
	if err != nil {
		return AccessClaims{}, err
	}
	var claims jwtClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return AccessClaims{}, invalidToken("claims: %v", err)
	}
	if err := v.checkClaims(claims); err != nil {
		return AccessClaims{}, err
	}
	return AccessClaims{Subject: claims.Subject, Email: strings.TrimSpace(claims.Email)}, nil
}

// verifySignature parses token and verifies its RS256 signature with the
// key its kid names, returning the raw payload.
func (v *AccessVerifier) verifySignature(ctx context.Context, token string) ([]byte, error) {
	if len(token) > maxTokenBytes {
		return nil, invalidToken("longer than %d bytes", maxTokenBytes)
	}
	parts := strings.Split(token, ".")
	if len(parts) != jwtParts {
		return nil, invalidToken("not a compact JWS")
	}
	var header jwtHeader
	if err := decodeSegment(parts[0], &header); err != nil {
		return nil, invalidToken("header: %v", err)
	}
	if header.Alg != jwtAlgRS256 {
		return nil, invalidToken("algorithm %q, want %s", header.Alg, jwtAlgRS256)
	}
	if header.Kid == "" {
		return nil, invalidToken("no key id")
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return nil, invalidToken("payload: %v", err)
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil {
		return nil, invalidToken("signature: %v", err)
	}
	key, err := v.keys.key(ctx, header.Kid)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return nil, invalidToken("bad signature")
	}
	return payload, nil
}

// checkClaims checks issuer, audience, validity window and identity.
func (v *AccessVerifier) checkClaims(c jwtClaims) error {
	now := v.now()
	switch {
	case c.Issuer != v.issuer:
		return invalidToken("issuer %q, want %q", c.Issuer, v.issuer)
	case !slices.Contains(c.Audience, v.audience):
		return invalidToken("audience does not include the application's AUD tag")
	case c.ExpiresAt == nil:
		return invalidToken("no expiry")
	case !now.Before(time.Unix(*c.ExpiresAt, 0).Add(tokenClockSkew)):
		return invalidToken("expired")
	case c.NotBefore != nil && now.Add(tokenClockSkew).Before(time.Unix(*c.NotBefore, 0)):
		return invalidToken("not valid yet")
	case strings.TrimSpace(c.Subject) == "":
		return invalidToken("no subject")
	case strings.TrimSpace(c.Email) == "":
		return invalidToken("no email (service tokens are not users)")
	}
	return nil
}

// decodeSegment decodes one base64url JSON segment into v.
func decodeSegment(segment string, v any) error {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(segment)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func invalidToken(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidToken, fmt.Sprintf(format, args...))
}
