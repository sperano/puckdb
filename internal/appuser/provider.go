package appuser

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Cloudflare Access request headers. Only the JWT is read; the email header
// is unsigned and never trusted.
const (
	HeaderCfAccessJWT = "Cf-Access-Jwt-Assertion"
	// headerPrefixAuthentik and headerPrefixCfAccess name each provider's
	// header family in canonical form (http.Header keys are canonical).
	headerPrefixAuthentik = "X-Authentik-"
	headerPrefixCfAccess  = "Cf-Access-"
)

// Provider authenticates requests with one login provider. An instance uses
// exactly one.
type Provider interface {
	// Name is the provider, stored as app_users.auth_provider.
	Name() string
	// Authenticate returns the request's identity: ErrUnauthenticated
	// without one, ErrConflictingIdentities when the request also carries
	// the other provider's headers.
	Authenticate(ctx context.Context, h http.Header) (Identity, error)
	// foreignPrefix is the header family of the other provider, which this
	// one ignores.
	foreignPrefix() string
}

// AuthConfig selects and configures the instance's login provider.
type AuthConfig struct {
	// Provider is ProviderAuthentik or ProviderCloudflare.
	Provider   string
	Cloudflare CloudflareAccessConfig
}

// NewProvider returns the provider cfg selects. Cloudflare settings with the
// Authentik provider are refused: they mean the instance was meant to verify
// Cloudflare tokens, and would otherwise trust Authentik headers instead.
func NewProvider(cfg AuthConfig) (Provider, error) {
	switch cfg.Provider {
	case ProviderAuthentik:
		if cfg.Cloudflare != (CloudflareAccessConfig{}) {
			return nil, errors.New("cloudflare access settings are set but the login provider is " + ProviderAuthentik)
		}
		return AuthentikProvider{}, nil
	case ProviderCloudflare:
		return NewCloudflareProvider(cfg.Cloudflare)
	default:
		return nil, fmt.Errorf("unknown login provider %q (want %s or %s)", cfg.Provider, ProviderAuthentik, ProviderCloudflare)
	}
}

// AuthentikProvider trusts the Authentik forward-auth headers.
type AuthentikProvider struct{}

// Name implements Provider.
func (AuthentikProvider) Name() string { return ProviderAuthentik }

func (AuthentikProvider) foreignPrefix() string { return headerPrefixCfAccess }

// Authenticate implements Provider.
func (AuthentikProvider) Authenticate(_ context.Context, h http.Header) (Identity, error) {
	if err := checkSingleIdentity(h); err != nil {
		return Identity{}, err
	}
	id, ok := IdentityFromHeaders(h)
	if !ok {
		return Identity{}, ErrUnauthenticated
	}
	return id, nil
}

// CloudflareProvider trusts only a verified Cloudflare Access JWT.
type CloudflareProvider struct {
	verifier *AccessVerifier
}

// NewCloudflareProvider returns a provider verifying tokens against cfg.
func NewCloudflareProvider(cfg CloudflareAccessConfig) (*CloudflareProvider, error) {
	verifier, err := NewAccessVerifier(cfg)
	if err != nil {
		return nil, err
	}
	return &CloudflareProvider{verifier: verifier}, nil
}

// Name implements Provider.
func (*CloudflareProvider) Name() string { return ProviderCloudflare }

func (*CloudflareProvider) foreignPrefix() string { return headerPrefixAuthentik }

// Authenticate implements Provider. The subject is the JWT sub; the email,
// also the username, is the JWT email claim.
func (p *CloudflareProvider) Authenticate(ctx context.Context, h http.Header) (Identity, error) {
	if err := checkSingleIdentity(h); err != nil {
		return Identity{}, err
	}
	token := strings.TrimSpace(h.Get(HeaderCfAccessJWT))
	if token == "" {
		return Identity{}, ErrUnauthenticated
	}
	claims, err := p.verifier.Verify(ctx, token)
	if err != nil {
		return Identity{}, err
	}
	return Identity{
		Provider: ProviderCloudflare,
		Subject:  claims.Subject,
		Username: claims.Email,
		Email:    claims.Email,
	}, nil
}

// checkSingleIdentity refuses a request carrying both providers' headers.
func checkSingleIdentity(h http.Header) error {
	if hasHeaderFamily(h, headerPrefixAuthentik) && hasHeaderFamily(h, headerPrefixCfAccess) {
		return ErrConflictingIdentities
	}
	return nil
}

// hasHeaderFamily reports whether h has a non-blank header whose canonical
// name starts with prefix.
func hasHeaderFamily(h http.Header, prefix string) bool {
	for name, values := range h {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		for _, v := range values {
			if strings.TrimSpace(v) != "" {
				return true
			}
		}
	}
	return false
}

// IgnoreForeignHeaders returns middleware that removes the other provider's
// headers from every request, so nothing downstream (the @admin group check,
// override authors) reads them, and answers 401 to a request carrying both
// providers' headers.
func IgnoreForeignHeaders(p Provider) func(http.Handler) http.Handler {
	prefix := p.foreignPrefix()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := checkSingleIdentity(r.Header); err != nil {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}
			header := r.Header.Clone()
			for name := range header {
				if strings.HasPrefix(name, prefix) {
					delete(header, name)
				}
			}
			r = r.WithContext(r.Context())
			r.Header = header
			next.ServeHTTP(w, r)
		})
	}
}
