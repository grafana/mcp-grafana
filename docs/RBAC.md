# RBAC & Permissions Guide

This guide explains the Role-Based Access Control (RBAC) permissions required for the Grafana MCP server and how to configure them properly.

## Table of Contents

- [Overview](#overview)
- [Understanding RBAC](#understanding-rbac)
- [Permissions vs Scopes](#permissions-vs-scopes)
- [Required Permissions by Tool](#required-permissions-by-tool)
- [Common Permission Configurations](#common-permission-configurations)
- [Setting Up Service Accounts](#setting-up-service-accounts)
- [Special Cases](#special-cases)
- [Troubleshooting](#troubleshooting)

## Overview

Each tool in the Grafana MCP server requires specific RBAC permissions to function properly. When creating a service account for the MCP server, you must grant it the appropriate permissions based on which tools you plan to use.

**Key Concepts:**
- **Permissions** define what actions can be performed (e.g., `dashboards:read`, `datasources:query`)
- **Scopes** define which resources those permissions apply to (e.g., `dashboards:*`, `datasources:uid:prometheus-uid`)

> **Best Practice:** Follow the principle of least privilege—grant only the permissions needed for the tools you'll actually use.

## Understanding RBAC

Grafana's RBAC system consists of:

1. **Actions** - Specific operations that can be performed (e.g., read, write, delete)
2. **Scopes** - Resources that actions apply to (e.g., specific dashboards, all datasources)
3. **Roles** - Collections of permissions that can be assigned to users or service accounts

The Grafana MCP server uses service accounts with fine-grained RBAC permissions for most operations, with some exceptions (see [Special Cases](#special-cases)).

For more information about Grafana RBAC, see the [official documentation](https://grafana.com/docs/grafana/latest/administration/roles-and-permissions/access-control/).

## Permissions vs Scopes

### Permissions (Actions)

Permissions define **what** can be done. Examples:

- `dashboards:read` - Read dashboard configuration
- `dashboards:write` - Modify dashboard configuration
- `dashboards:create` - Create new dashboards
- `datasources:read` - Read datasource configuration
- `datasources:query` - Query datasources
- `teams:read` - List and view teams
- `users:read` - List and view users
- `alert.rules:read` - Read alert rules

### Scopes (Resources)

Scopes define **where** permissions apply. Examples:

- `dashboards:*` - All dashboards
- `dashboards:uid:abc123` - Specific dashboard with UID `abc123`
- `datasources:*` - All datasources
- `datasources:uid:prometheus-uid` - Specific Prometheus datasource
- `folders:*` - All folders
- `teams:*` - All teams
- `global.users:*` - All users in the organization

### Combining Permissions and Scopes

Permissions and scopes work together. For example:
- Permission `dashboards:read` + Scope `dashboards:*` = Read all dashboards
- Permission `dashboards:read` + Scope `dashboards:uid:abc123` = Read only dashboard `abc123`
- Permission `datasources:query` + Scope `datasources:uid:prometheus-uid` = Query only the specific Prometheus datasource

## Required Permissions by Tool

### Admin Tools

**Tools:** `list_teams`, `list_users_by_org`

| Permission | Scope | Description |
|------------|-------|-------------|
| `teams:read` | `teams:*` or `teams:id:N` | List and view teams |
| `users:read` | `global.users:*` or `global.users:id:N` | List and view users |

**Minimal configuration:**
```
Permission: teams:read
Scope: teams:*

Permission: users:read
Scope: global.users:*
```

### Dashboard Tools

**Tools:** `search_dashboards`, `get_dashboard_by_uid`, `get_dashboard_summary`, `get_dashboard_property`, `get_dashboard_panel_queries`, `update_dashboard`, `patch_dashboard`

| Permission | Scope | Description |
|------------|-------|-------------|
| `dashboards:read` | `dashboards:*` or specific UIDs | Read dashboard configuration |
| `dashboards:write` | `dashboards:*` or specific UIDs | Modify existing dashboards |
| `dashboards:create` | `dashboards:*` and `folders:*` | Create new dashboards |

**Read-only configuration:**
```
Permission: dashboards:read
Scope: dashboards:*
```

**Full access configuration:**
```
Permission: dashboards:read
Scope: dashboards:*

Permission: dashboards:write
Scope: dashboards:*

Permission: dashboards:create
Scope: dashboards:*

Permission: folders:read
Scope: folders:*
```

> **Note:** Creating dashboards requires `folders:*` scope since dashboards must be placed in folders.

### Datasource Tools

**Tools:** `list_datasources`, `get_datasource_by_uid`, `get_datasource_by_name`

| Permission | Scope | Description |
|------------|-------|-------------|
| `datasources:read` | `datasources:*` or specific UIDs | Read datasource configuration |

**Configuration:**
```
Permission: datasources:read
Scope: datasources:*
```

### Prometheus Tools

**Tools:** `query_prometheus`, `list_prometheus_metric_metadata`, `list_prometheus_metric_names`, `list_prometheus_label_names`, `list_prometheus_label_values`

| Permission | Scope | Description |
|------------|-------|-------------|
| `datasources:query` | `datasources:*` or specific UIDs | Execute queries against Prometheus |

**Configuration:**
```
Permission: datasources:query
Scope: datasources:*
```

**Limited access (specific datasource only):**
```
Permission: datasources:query
Scope: datasources:uid:prometheus-prod
```

### Loki Tools

**Tools:** `query_loki_logs`, `list_loki_label_names`, `list_loki_label_values`, `query_loki_stats`

| Permission | Scope | Description |
|------------|-------|-------------|
| `datasources:query` | `datasources:*` or specific UIDs | Execute queries against Loki |

**Configuration:**
```
Permission: datasources:query
Scope: datasources:*
```

### Pyroscope Tools

**Tools:** `list_pyroscope_label_names`, `list_pyroscope_label_values`, `list_pyroscope_profile_types`, `fetch_pyroscope_profile`

| Permission | Scope | Description |
|------------|-------|-------------|
| `datasources:query` | `datasources:*` or specific UIDs | Query Pyroscope datasources |

**Configuration:**
```
Permission: datasources:query
Scope: datasources:*
```

### Alerting Tools

**Tools:** `list_alert_rules`, `get_alert_rule_by_uid`, `list_contact_points`

| Permission | Scope | Description |
|------------|-------|-------------|
| `alert.rules:read` | `folders:*` or specific folder UIDs | Read alert rules |
| `alert.notifications:read` | Global scope | Read notification settings |

**Configuration:**
```
Permission: alert.rules:read
Scope: folders:*

Permission: alert.notifications:read
Scope: (global - no specific scope needed)
```

### OnCall Tools

**Tools:** `list_oncall_schedules`, `get_oncall_shift`, `get_current_oncall_users`, `list_oncall_teams`, `list_oncall_users`, `list_alert_groups`, `get_alert_group`

| Permission | Scope | Description |
|------------|-------|-------------|
| `grafana-oncall-app.schedules:read` | Plugin-specific | Read OnCall schedules |
| `grafana-oncall-app.user-settings:read` | Plugin-specific | Read OnCall users and teams |
| `grafana-oncall-app.alert-groups:read` | Plugin-specific | Read OnCall alert groups |

**Configuration:**
```
Permission: grafana-oncall-app.schedules:read
Scope: (plugin-specific)

Permission: grafana-oncall-app.user-settings:read
Scope: (plugin-specific)

Permission: grafana-oncall-app.alert-groups:read
Scope: (plugin-specific)
```

> **Note:** OnCall permissions are plugin-specific. Consult Grafana OnCall documentation for exact scope requirements.

### Navigation Tools

**Tools:** `generate_deeplink`

| Permission | Scope | Description |
|------------|-------|-------------|
| None | N/A | Read-only URL generation requires no permissions |

**Configuration:** No permissions required.

### Asserts Tools

**Tools:** `get_assertions`

| Permission | Scope | Description |
|------------|-------|-------------|
| Plugin-specific | Plugin-specific | Depends on Asserts plugin configuration |

**Configuration:** Consult Grafana Asserts plugin documentation for exact requirements.

## Common Permission Configurations

### Full MCP Server Access

Grant broad permissions for all tools:

```
# Admin
Permission: teams:read, Scope: teams:*
Permission: users:read, Scope: global.users:*

# Dashboards
Permission: dashboards:read, Scope: dashboards:*
Permission: dashboards:write, Scope: dashboards:*
Permission: dashboards:create, Scope: dashboards:*

# Folders (required for dashboard creation)
Permission: folders:read, Scope: folders:*

# Datasources
Permission: datasources:read, Scope: datasources:*
Permission: datasources:query, Scope: datasources:*

# Alerting
Permission: alert.rules:read, Scope: folders:*
Permission: alert.notifications:read, Scope: (global)

# OnCall
Permission: grafana-oncall-app.schedules:read, Scope: (plugin-specific)
Permission: grafana-oncall-app.user-settings:read, Scope: (plugin-specific)
Permission: grafana-oncall-app.alert-groups:read, Scope: (plugin-specific)
```

### Read-Only Configuration

Minimal permissions for viewing only:

```
Permission: dashboards:read, Scope: dashboards:*
Permission: datasources:read, Scope: datasources:*
Permission: datasources:query, Scope: datasources:*
Permission: alert.rules:read, Scope: folders:*
```

### Limited Datasource Access

Query only specific datasources:

```
# Read datasource configuration
Permission: datasources:read, Scope: datasources:*

# Query only production Prometheus
Permission: datasources:query, Scope: datasources:uid:prometheus-prod

# Query only production Loki
Permission: datasources:query, Scope: datasources:uid:loki-prod
```

### Dashboard-Specific Access

Access only specific dashboards:

```
# Read specific dashboards
Permission: dashboards:read, Scope: dashboards:uid:monitoring-dashboard
Permission: dashboards:read, Scope: dashboards:uid:alerts-dashboard

# Query datasources (if needed for panel queries)
Permission: datasources:query, Scope: datasources:*
```

### Monitoring-Only Configuration

For observability use cases without modification rights:

```
# Read dashboards
Permission: dashboards:read, Scope: dashboards:*

# Query datasources
Permission: datasources:read, Scope: datasources:*
Permission: datasources:query, Scope: datasources:*

# View alerts
Permission: alert.rules:read, Scope: folders:*

# View OnCall schedules (if using OnCall)
Permission: grafana-oncall-app.schedules:read, Scope: (plugin-specific)
```

## Setting Up Service Accounts

### Step 1: Create Service Account

1. Log in to Grafana as an administrator
2. Navigate to **Administration** → **Service Accounts**
3. Click **Add service account**
4. Enter a name (e.g., "MCP Server")
5. Select appropriate role (typically "Viewer" as base)

### Step 2: Assign Permissions

1. In the service account details, scroll to **Permissions**
2. Click **Add permission**
3. Select the action (e.g., `dashboards:read`)
4. Select the scope (e.g., `dashboards:*`)
5. Click **Save**
6. Repeat for all required permissions

### Step 3: Generate Token

1. In the service account details, find **Tokens** section
2. Click **Add service account token**
3. Enter a name (e.g., "MCP Server Token")
4. Set expiration (optional but recommended)
5. Click **Generate token**
6. **Copy the token immediately** (it won't be shown again)

### Step 4: Configure MCP Server

Use the token in your MCP server configuration:

```bash
export GRAFANA_SERVICE_ACCOUNT_TOKEN=<your-token>
export GRAFANA_URL=http://localhost:3000
```

## Special Cases

### Incident and Sift Tools

Unlike other tools, Incident and Sift tools use **basic Grafana roles** instead of fine-grained RBAC permissions:

**Incident Tools:**
- `list_incidents`, `get_incident` - Require **Viewer role**
- `create_incident`, `add_activity_to_incident` - Require **Editor role**

**Sift Tools:**
- `list_sift_investigations`, `get_sift_investigation`, `get_sift_analysis` - Require **Viewer role**
- `find_error_pattern_logs`, `find_slow_requests` - Require **Editor role**

**Configuration:**
Assign the appropriate base role when creating the service account:
- For read-only Incident/Sift access: Select "Viewer" role
- For full Incident/Sift access: Select "Editor" role

### Plugin-Specific Permissions

Some tools require plugin-specific permissions:

**Grafana OnCall:**
- Permissions are managed by the OnCall plugin
- Consult [Grafana OnCall documentation](https://grafana.com/docs/oncall/) for details

**Grafana Asserts:**
- Permissions are managed by the Asserts plugin
- Consult Asserts plugin documentation for details

## Troubleshooting

### Permission Denied Errors

**Symptom:** Tools fail with 403 Forbidden or permission denied errors.

**Solutions:**
1. Verify the service account has the required permission
2. Check the scope includes the target resource
3. Ensure the token hasn't expired
4. Verify the service account is active

### Cannot Create Dashboards

**Symptom:** `update_dashboard` fails when creating new dashboards.

**Solutions:**
1. Add `dashboards:create` permission
2. Add `folders:*` scope (dashboards must be in folders)
3. Verify folder exists and is accessible

### Cannot Query Datasource

**Symptom:** Prometheus or Loki queries fail.

**Solutions:**
1. Add `datasources:query` permission
2. Verify scope includes the datasource UID
3. Check datasource is configured correctly
4. Ensure datasource is reachable from Grafana

### OnCall Tools Not Working

**Symptom:** OnCall tools fail to work.

**Solutions:**
1. Verify Grafana OnCall plugin is installed and enabled
2. Check plugin-specific permissions are granted
3. Ensure service account has access to OnCall

### Incident/Sift Tools Not Working

**Symptom:** Incident or Sift tools fail with permission errors.

**Solutions:**
1. Verify the service account has appropriate base role (Viewer or Editor)
2. Check Grafana Incident or Sift is properly configured
3. Ensure plugins are installed and licensed (if required)

## Verification Checklist

Before deploying the MCP server, verify:

- [ ] Service account is created
- [ ] All required permissions are granted
- [ ] Scopes are correctly configured
- [ ] Token is generated and stored securely
- [ ] Token hasn't expired
- [ ] Service account is active (not disabled)
- [ ] Base role is appropriate for Incident/Sift tools (if used)
- [ ] Plugin-specific permissions are configured (if using OnCall/Asserts)

## Next Steps

- [Configure your MCP client](CONFIGURATION.md)
- [Review available tools](TOOLS_REFERENCE.md)
- [Explore features](FEATURES.md)
- [Troubleshoot issues](TROUBLESHOOTING.md)