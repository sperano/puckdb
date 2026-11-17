package graph

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/sperano/puckdb/internal/appuser"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/graph/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

const testAdminToken = "s3cr3t"

// adminHeaders is a request of an Authentik admin.
func adminHeaders() http.Header {
	return http.Header{
		"X-Authentik-Uid":      {"eric-uid"},
		"X-Authentik-Username": {"eric"},
		"X-Authentik-Groups":   {"users|" + config.DefaultAdminGroup},
	}
}

var enabledReporting = ReportingAuth{Enabled: true, Provider: appuser.ProviderAuthentik, Group: config.DefaultAdminGroup}

func TestMauriceReportingDirective(t *testing.T) {
	t.Parallel()
	cloudflare := enabledReporting
	cloudflare.Provider = appuser.ProviderCloudflare
	disabled := enabledReporting
	disabled.Enabled = false
	tests := []struct {
		name    string
		auth    ReportingAuth
		headers http.Header
		wantErr error
	}{
		{"admin", enabledReporting, adminHeaders(), nil},
		{"no headers", enabledReporting, nil, errReportingDenied},
		{"identity without the group", enabledReporting, http.Header{"X-Authentik-Uid": {"eric-uid"}, "X-Authentik-Groups": {"users"}}, errReportingDenied},
		{"group without identity", enabledReporting, http.Header{"X-Authentik-Groups": {config.DefaultAdminGroup}}, errReportingDenied},
		{"group prefix only", enabledReporting, http.Header{"X-Authentik-Uid": {"eric-uid"}, "X-Authentik-Groups": {config.DefaultAdminGroup + "-ro"}}, errReportingDenied},
		{"group in another case", enabledReporting, http.Header{"X-Authentik-Uid": {"eric-uid"}, "X-Authentik-Groups": {"PUCKDB-ADMINS"}}, errReportingDenied},
		{"admin token only", enabledReporting, http.Header{"X-Admin-Token": {testAdminToken}}, errReportingDenied},
		{"admin token with identity", enabledReporting, http.Header{"X-Authentik-Uid": {"eric-uid"}, "X-Admin-Token": {testAdminToken}}, errReportingDenied},
		{"public provider with forged Authentik headers", cloudflare, adminHeaders(), errReportingDenied},
		{"disabled, admin", disabled, adminHeaders(), errReportingDisabled},
		{"disabled, not an admin", disabled, http.Header{"X-Authentik-Uid": {"bob-uid"}}, errReportingDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			called := false
			next := func(context.Context) (any, error) { called = true; return "ok", nil }
			got, err := NewMauriceReportingDirective(tt.auth)(contextWithHeaders(t, tt.headers), nil, next)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, got)
				assert.False(t, called, "the resolver must not run")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "ok", got)
			assert.True(t, called)
		})
	}
}

func TestReportingAuthValidate(t *testing.T) {
	t.Parallel()
	assert.NoError(t, ReportingAuth{}.Validate(), "disabled needs nothing")
	assert.NoError(t, ReportingAuth{Provider: appuser.ProviderCloudflare}.Validate(), "disabled on the public instance")
	assert.NoError(t, enabledReporting.Validate())

	cloudflare := enabledReporting
	cloudflare.Provider = appuser.ProviderCloudflare
	assert.EqualError(t, cloudflare.Validate(), `maurice reporting needs the authentik login provider, not "cloudflare"`)
	noGroup := enabledReporting
	noGroup.Group = ""
	assert.EqualError(t, noGroup.Validate(), "maurice reporting needs an admin group")
}

// Every reporting field carries both directives, and nothing else carries
// @mauriceReporting without @admin.
func TestReportingFieldsCarryBothDirectives(t *testing.T) {
	t.Parallel()
	schema := generated.NewExecutableSchema(generated.Config{}).Schema()
	var reporting []string
	for _, def := range schema.Types {
		for _, field := range def.Fields {
			gated := field.Directives.ForName("mauriceReporting") != nil
			if gated {
				reporting = append(reporting, def.Name+"."+field.Name)
				assert.NotNil(t, field.Directives.ForName("admin"), "%s.%s lacks @admin", def.Name, field.Name)
			}
			if def.Kind == ast.Object && def.Name == "Query" && strings.HasPrefix(field.Name, "mauriceReporting") {
				assert.True(t, gated, "Query.%s lacks @mauriceReporting", field.Name)
			}
		}
	}
	assert.ElementsMatch(t, []string{
		"Query.mauriceReportingInstances", "Query.mauriceReportingUsers",
		"Query.mauriceReportingConversations", "Query.mauriceReportingConversation",
		"Query.mauriceReportingOverview", "Query.mauriceReportingUserActivity",
	}, reporting)
}

func TestClientReportingErrorHidesStorageFaults(t *testing.T) {
	t.Parallel()
	assert.Equal(t, errMauriceInternal, clientReportingError("op", errors.New("pq: relation does not exist")))
	assert.Equal(t, context.Canceled, clientReportingError("op", context.Canceled))
}
