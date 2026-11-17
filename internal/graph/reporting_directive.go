package graph

import (
	"context"
	"errors"
	"fmt"

	"github.com/99designs/gqlgen/graphql"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/appuser"
	"github.com/sperano/puckdb/internal/graph/generated"
	"github.com/sperano/puckdb/internal/httpx"
)

var (
	// errReportingDenied is returned by @mauriceReporting to a request
	// without an Authentik identity in the admin group, or on an instance
	// whose login provider is not Authentik.
	errReportingDenied = errors.New("access denied: maurice reporting requires the Authentik admin group")
	// errReportingDisabled is returned by @mauriceReporting to an admin
	// while --maurice-reporting-enabled is off.
	errReportingDisabled = errors.New("maurice reporting is disabled")
)

// ReportingAuth configures the @mauriceReporting directive, which gates the
// cross-instance Maurice reporting fields. The command layer reads it from
// --maurice-reporting-enabled, --auth-provider and --admin-group.
type ReportingAuth struct {
	// Enabled turns the reporting fields on; off (the default) refuses them.
	Enabled bool
	// Provider is the instance's login provider; only
	// appuser.ProviderAuthentik can authorize reporting.
	Provider string
	// Group is the Authentik admin group a requester must be in.
	Group string
}

// Validate refuses reporting enabled where nothing could authorize it: on
// an instance whose login provider is not Authentik, or without an admin
// group. Disabled reporting is always valid.
func (auth ReportingAuth) Validate() error {
	if !auth.Enabled {
		return nil
	}
	if auth.Provider != appuser.ProviderAuthentik {
		return fmt.Errorf("maurice reporting needs the %s login provider, not %q", appuser.ProviderAuthentik, auth.Provider)
	}
	if auth.Group == "" {
		return errors.New("maurice reporting needs an admin group")
	}
	return nil
}

// NewMauriceReportingDirective returns the @mauriceReporting directive. It
// authorizes a request only when every condition holds:
//   - the instance's login provider is Authentik,
//   - the request carries an Authentik identity (X-authentik-uid),
//   - its X-authentik-groups header contains auth.Group,
//   - reporting is enabled.
//
// The X-Admin-Token never authorizes it. A requester who is not an admin is
// denied before the enabled check, so the feature state is not disclosed
// to them.
func NewMauriceReportingDirective(auth ReportingAuth) func(ctx context.Context, obj any, next graphql.Resolver) (any, error) {
	group := AdminAuth{Group: auth.Group}
	return func(ctx context.Context, _ any, next graphql.Resolver) (any, error) {
		headers := httpx.HeadersFromContext(ctx)
		_, identified := appuser.IdentityFromHeaders(headers)
		if auth.Provider != appuser.ProviderAuthentik || !identified || !group.hasAdminGroup(headers) {
			log.Warn().
				Str("username", headers.Get(headerAuthentikUsername)).
				Msg("maurice reporting directive: access denied")
			return nil, errReportingDenied
		}
		if !auth.Enabled {
			return nil, errReportingDisabled
		}
		return next(ctx)
	}
}

// NewDirectiveRoot returns every directive of the schema.
func NewDirectiveRoot(admin AdminAuth, reporting ReportingAuth) generated.DirectiveRoot {
	return generated.DirectiveRoot{
		Admin:            NewAdminDirective(admin),
		MauriceReporting: NewMauriceReportingDirective(reporting),
	}
}
