# OAuth2 Testdata Guide

This directory contains files used by the local OAuth2/Auth Proxy test flow.

## Files

- `oauth2-setup.sh`: starts local services and prepares `.env.oauth2-test`.
- `oauth2-test.sh`: helper for tokens and end-to-end checks.
- `keycloak-realm.json`: Keycloak realm, users, groups, and clients for local testing.

## Quick use

From repository root:

```bash
chmod +x testdata/oauth2-setup.sh testdata/oauth2-test.sh
./testdata/oauth2-setup.sh
source .env.oauth2-test
go run ./cmd/mcp-grafana/main.go
```

In another terminal:

```bash
./testdata/oauth2-test.sh test-flow john.doe password123
```

## Useful commands

Get user token:

```bash
./testdata/oauth2-test.sh token john.doe password123
```

Stop local containers:

```bash
./testdata/oauth2-test.sh cleanup
```

## Canonical docs

Use `docs/oauth2-auth-proxy-setup.md` as the main OAuth2 documentation.
