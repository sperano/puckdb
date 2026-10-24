package simulation

import (
	"path"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestActivityRegistrationsCoverActivitiesMethods(t *testing.T) {
	acts := &Activities{}
	registrations := activityRegistrations(acts)
	activityType := reflect.TypeOf(acts)

	require.Len(t, registrations, activityType.NumMethod())

	wantMethods := make(map[string]struct{}, activityType.NumMethod())
	for i := range activityType.NumMethod() {
		wantMethods[activityType.Method(i).Name] = struct{}{}
	}

	gotMethods := make(map[string]struct{}, len(registrations))
	registeredNames := make(map[string]struct{}, len(registrations))
	for _, registration := range registrations {
		methodName := boundActivityMethodName(t, registration.fn)
		require.NotContains(t, gotMethods, methodName, "activity method registered more than once")
		require.NotContains(t, registeredNames, registration.name, "serialized activity name registered more than once")
		gotMethods[methodName] = struct{}{}
		registeredNames[registration.name] = struct{}{}
	}

	require.Equal(t, wantMethods, gotMethods)
}

func boundActivityMethodName(t *testing.T, fn any) string {
	t.Helper()

	runtimeFunction := runtime.FuncForPC(reflect.ValueOf(fn).Pointer())
	require.NotNil(t, runtimeFunction)

	const boundMethodSuffix = "-fm"
	functionName := runtimeFunction.Name()
	require.True(t, strings.HasSuffix(functionName, boundMethodSuffix), functionName)

	methodName := strings.TrimPrefix(path.Ext(strings.TrimSuffix(functionName, boundMethodSuffix)), ".")
	require.NotEmpty(t, methodName)
	return methodName
}
