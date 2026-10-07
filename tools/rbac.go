package tools

// Shared RBAC action groups for mcpgrafana.RequiresPermissions. Spread one
// into the option, and add a second RequiresPermissions call for any extras:
//
//	mcpgrafana.RequiresPermissions(datasourceQuery...),

var (
	// datasourceQuery covers tools that query a datasource through Grafana.
	datasourceQuery = []string{"datasources:read", "datasources:query"}

	// fetchDashboard reads through dashboard.grafana.app when Grafana serves
	// it, and the legacy API otherwise.
	dashboardRead = []string{"dashboards:read", "dashboard.grafana.app/dashboards:get"}

	resourcePermissionsRead = []string{
		"dashboards.permissions:read",
		"datasources.permissions:read",
		"folders.permissions:read",
		"serviceaccounts.permissions:read",
		"teams.permissions:read",
		"users.permissions:read",
	}

	incidentRead  = []string{"plugins.app:access", "grafana-incident-app.incidents:read"}
	incidentWrite = []string{"plugins.app:access", "grafana-incident-app.incidents:read", "grafana-incident-app.incidents:write"}

	agento11yRead = []string{
		"plugins.app:access",
		"grafana-agento11y-app.conversations:read",
		"grafana-agento11y-app.data:read",
	}
	agento11yWrite = append(append([]string{}, agento11yRead...), "grafana-agento11y-app.eval:write")
)
