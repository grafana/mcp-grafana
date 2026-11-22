package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var sharedCredentialEnv = []string{
	"GRAFANA_SERVICE_ACCOUNT_TOKEN",
	"GRAFANA_SERVICE_ACCOUNT_TOKEN_FILE",
	"GRAFANA_API_KEY",
	"GRAFANA_USERNAME",
	"GRAFANA_PASSWORD",
}

func TestValidateUserMode(t *testing.T) {
	for _, name := range sharedCredentialEnv {
		t.Setenv(name, "")
	}
	t.Setenv("GRAFANA_EXTRA_HEADERS", "")
	t.Setenv("GRAFANA_FORWARD_HEADERS", "")
	on := callerAuthConfig{requireUserCredentials: true}

	assert.NoError(t, callerAuthConfig{}.validateUserMode("stdio"), "off is a no-op")
	assert.NoError(t, on.validateUserMode("streamable-http"))
	for _, transport := range []string{"stdio", "sse"} {
		assert.ErrorContains(t, on.validateUserMode(transport), "streamable-http")
	}

	for _, name := range sharedCredentialEnv {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "shared-secret")
			err := on.validateUserMode("streamable-http")
			require.Error(t, err)
			assert.Contains(t, err.Error(), name)
			assert.NotContains(t, err.Error(), "shared-secret")
		})
	}
}

func TestValidateUserMode_ExtraHeaders(t *testing.T) {
	for _, name := range sharedCredentialEnv {
		t.Setenv(name, "")
	}
	t.Setenv("GRAFANA_FORWARD_HEADERS", "X-Webauth-User")
	on := callerAuthConfig{requireUserCredentials: true, userIdentityHeaders: "X-Webauth-User"}

	t.Setenv("GRAFANA_EXTRA_HEADERS", `{"X-Scope-OrgID":"tenant"}`)
	assert.NoError(t, on.validateUserMode("streamable-http"))

	for _, extra := range []string{`{"Authorization":"Bearer sa-secret"}`, `{"Cookie":"sa-secret"}`, `{"X-Webauth-User":"sa-secret"}`} {
		t.Setenv("GRAFANA_EXTRA_HEADERS", extra)
		err := on.validateUserMode("streamable-http")
		require.Error(t, err, extra)
		assert.Contains(t, err.Error(), "GRAFANA_EXTRA_HEADERS[")
		assert.NotContains(t, err.Error(), "sa-secret")
	}
}

func TestValidateUserMode_IdentityHeaderMustBeForwarded(t *testing.T) {
	for _, name := range sharedCredentialEnv {
		t.Setenv(name, "")
	}
	t.Setenv("GRAFANA_EXTRA_HEADERS", "")
	t.Setenv("GRAFANA_FORWARD_HEADERS", "Authorization")
	on := callerAuthConfig{requireUserCredentials: true, userIdentityHeaders: "X-Webauth-User"}
	assert.ErrorContains(t, on.validateUserMode("streamable-http"), "X-Webauth-User")
}

func TestCallerAuthConfigApplyEnv(t *testing.T) {
	t.Run("env enables both", func(t *testing.T) {
		t.Setenv(requireUserCredentialsEnvVar, "true")
		t.Setenv(logToolCallsEnvVar, "1")
		var ca callerAuthConfig
		require.NoError(t, ca.applyEnv(map[string]bool{}))
		assert.True(t, ca.requireUserCredentials)
		assert.True(t, ca.logToolCalls)
	})
	t.Run("identity headers from env", func(t *testing.T) {
		t.Setenv(userIdentityHeadersEnvVar, "X-Webauth-User")
		var ca callerAuthConfig
		require.NoError(t, ca.applyEnv(map[string]bool{}))
		assert.Equal(t, "X-Webauth-User", ca.userIdentityHeaders)
	})
	t.Run("explicit flag wins", func(t *testing.T) {
		t.Setenv(requireUserCredentialsEnvVar, "true")
		var ca callerAuthConfig
		require.NoError(t, ca.applyEnv(map[string]bool{"require-user-credentials": true}))
		assert.False(t, ca.requireUserCredentials)
	})
	t.Run("invalid value", func(t *testing.T) {
		t.Setenv(logToolCallsEnvVar, "maybe")
		var ca callerAuthConfig
		assert.ErrorContains(t, ca.applyEnv(map[string]bool{}), logToolCallsEnvVar)
	})
}
