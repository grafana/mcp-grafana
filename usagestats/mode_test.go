//go:build unit

package usagestats

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveMode(t *testing.T) {
	for _, tc := range []struct {
		name       string
		flagValue  string
		flagSet    bool
		env        string
		doNotTrack string
		want       Mode
	}{
		{name: "nothing set falls back to the default", want: DefaultMode},
		{name: "env enabled", env: "enabled", want: ModeEnabled},
		{name: "env log", env: "log", want: ModeLog},
		{name: "env disabled", env: "disabled", want: ModeDisabled},
		{name: "env is case insensitive and trimmed", env: " Enabled ", want: ModeEnabled},

		{name: "flag wins over env", flagValue: "disabled", flagSet: true, env: "enabled", want: ModeDisabled},
		{name: "flag wins over env the other way", flagValue: "enabled", flagSet: true, env: "disabled", want: ModeEnabled},
		{name: "flag value ignored when the flag was not set", flagValue: "enabled", env: "log", want: ModeLog},

		// Failing toward privacy: an unrecognised value must never fall
		// through to the default, which will be "enabled" in a later release.
		{name: "unrecognised env value disables", env: "yes", want: ModeDisabled},
		{name: "unrecognised flag value disables", flagValue: "on", flagSet: true, want: ModeDisabled},
		{name: "explicitly empty flag value disables", flagValue: "", flagSet: true, env: "enabled", want: ModeDisabled},
		{name: "whitespace-only env value falls back to the default", env: "   ", want: DefaultMode},

		// DO_NOT_TRACK can only ever disable, and sits below both explicit
		// settings so a host-wide preference can be overridden per server.
		{name: "DO_NOT_TRACK=1 disables", doNotTrack: "1", want: ModeDisabled},
		{name: "DO_NOT_TRACK=true disables", doNotTrack: "true", want: ModeDisabled},
		{name: "DO_NOT_TRACK is case insensitive and trimmed", doNotTrack: " TRUE ", want: ModeDisabled},
		{name: "DO_NOT_TRACK=0 does not disable", doNotTrack: "0", want: DefaultMode},
		{name: "DO_NOT_TRACK=false does not disable", doNotTrack: "false", want: DefaultMode},
		{name: "DO_NOT_TRACK with an unrelated value is ignored", doNotTrack: "yes", want: DefaultMode},
		{name: "env wins over DO_NOT_TRACK", env: "enabled", doNotTrack: "1", want: ModeEnabled},
		{name: "flag wins over DO_NOT_TRACK", flagValue: "enabled", flagSet: true, doNotTrack: "1", want: ModeEnabled},
		{name: "DO_NOT_TRACK applies when env is only whitespace", env: "  ", doNotTrack: "1", want: ModeDisabled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ResolveMode(tc.flagValue, tc.flagSet, tc.env, tc.doNotTrack))
		})
	}
}

// TestDefaultModeIsDisabled guards the rollout: the receiving endpoint is not
// live yet, so this release must not report by default. Flipping it is a
// deliberate, separate change.
func TestDefaultModeIsDisabled(t *testing.T) {
	assert.Equal(t, ModeDisabled, DefaultMode)
	assert.Equal(t, ModeDisabled, ResolveMode("", false, "", ""))
}

// TestZeroConfigIsInert: Mode is a string, so a Config built without one has
// Mode == "". That must not report. Every other default in this package fails
// toward privacy and so must this one.
func TestZeroConfigIsInert(t *testing.T) {
	assert.False(t, New(Config{}).Enabled())
	assert.False(t, New(Config{Mode: ModeDisabled}).Enabled())
	assert.False(t, New(Config{Mode: "ENABLED"}).Enabled(), "Mode is not normalised; only ResolveMode produces valid values")
	assert.True(t, New(Config{Mode: ModeEnabled}).Enabled())
	assert.True(t, New(Config{Mode: ModeLog}).Enabled())
}

func TestResolveEndpoint(t *testing.T) {
	assert.Equal(t, DefaultEndpoint, ResolveEndpoint(""))
	assert.Equal(t, DefaultEndpoint, ResolveEndpoint("  "))
	assert.Equal(t, "http://localhost:9999/report", ResolveEndpoint(" http://localhost:9999/report "))
}

func TestTargetKind(t *testing.T) {
	for _, tc := range []struct {
		url  string
		want string
	}{
		{url: "", want: ""},
		{url: "://nonsense", want: ""},
		{url: "https://example.grafana.net", want: TargetKindCloud},
		{url: "https://EXAMPLE.Grafana.Net/", want: TargetKindCloud},
		{url: "https://grafana.example.com", want: TargetKindSelfHosted},
		{url: "http://localhost:3000", want: TargetKindSelfHosted},
		// A Cloud stack behind a custom domain is indistinguishable from a
		// self-hosted one here. The docs page states the misclassification.
		{url: "https://grafana.mycorp.example", want: TargetKindSelfHosted},
	} {
		t.Run(tc.url, func(t *testing.T) {
			assert.Equal(t, tc.want, TargetKind(tc.url))
		})
	}
}

func TestAuthMethodFor(t *testing.T) {
	assert.Equal(t, AuthMethodOnBehalfOf, AuthMethodFor(true, true, false, false))
	assert.Equal(t, AuthMethodAccessToken, AuthMethodFor(true, false, false, false))
	assert.Equal(t, AuthMethodServiceAccountToken, AuthMethodFor(false, false, true, false))
	assert.Equal(t, AuthMethodBasicAuth, AuthMethodFor(false, false, false, true))
	assert.Equal(t, AuthMethodAnonymous, AuthMethodFor(false, false, false, false))
}
