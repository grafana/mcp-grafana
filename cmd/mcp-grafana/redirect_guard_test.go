package main

import "testing"

func TestRedirectGuardEnvPrecedence(t *testing.T) {
	t.Setenv("GRAFANA_ALLOW_CROSS_ORIGIN_REDIRECTS", "true")

	fromEnv := grafanaConfig{}
	if err := fromEnv.applyRedirectEnv(nil); err != nil {
		t.Fatal(err)
	}
	if !fromEnv.allowCrossOriginRedirects {
		t.Fatal("environment opt-out was ignored")
	}

	fromFlag := grafanaConfig{allowCrossOriginRedirects: false}
	if err := fromFlag.applyRedirectEnv(map[string]bool{"allow-cross-origin-redirects": true}); err != nil {
		t.Fatal(err)
	}
	if fromFlag.allowCrossOriginRedirects {
		t.Fatal("explicit false flag did not override environment opt-out")
	}
}

func TestRedirectGuardInvalidEnv(t *testing.T) {
	t.Setenv("GRAFANA_ALLOW_CROSS_ORIGIN_REDIRECTS", "not-a-boolean")
	var cfg grafanaConfig
	if err := cfg.applyRedirectEnv(nil); err == nil {
		t.Fatal("invalid environment value was accepted")
	}
}
