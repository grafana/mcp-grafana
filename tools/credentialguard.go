package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"runtime"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

const credentialGuardMessage = "This input was blocked: do not send passwords, API keys, access tokens, private keys, or requests to configure basic/digest authentication (or other credential-based auth) through this tool. Configure credentials in the Grafana UI instead."

// ---- auth intent patterns (ported from mcp-manage-datasources credentialguard) ----

var datasourceOrGrafanaContext = regexp.MustCompile(
	`(?i)\b(datasource|data\s*source|grafana|prometheus|loki|tempo|elasticsearch|influx|postgres|mysql|clickhouse)\b`,
)

var authSetupVerbThenAuthPhrase = regexp.MustCompile(
	`(?i)\b(?:add|enable|configure|set\s+up|turn\s+on|implement)\s+(?:[\w-]+\s+){0,8}(?:auth(?:entication)?|credentials?|basic\s+auth(?:entication)?)\b`,
)

var authPhraseTowardBackend = regexp.MustCompile(
	`(?i)\bauth(?:entication)?\s+(?:for|on|to|with)\s+(?:the\s+|this\s+|a\s+|an\s+|my\s+|our\s+|that\s+)?(?:[\w-]+\s+){0,3}(?:datasource|data\s*source|grafana|prometheus|loki|tempo|elasticsearch|influx|postgres|mysql|clickhouse)\b`,
)

var authIntentPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\badd\s+(?:\w+\s+){0,4}authentication\b`),
	regexp.MustCompile(`(?i)\b(add|enable|configure|set\s+up|turn\s+on)\s+(?:[\w-]+\s+){0,4}basic\s+auth(?:entication)?\b`),
	regexp.MustCompile(`(?i)\bbasic\s+auth(?:entication)?\s+(?:to|for|on)\s+(?:the\s+|a\s+|my\s+)?(?:grafana\s+)?(?:datasource|data\s*source|instance)\b`),
	regexp.MustCompile(`(?i)\bauthentication\s+with\b.*\b(username|user\s*name|password|passwd|credential)\b`),
	regexp.MustCompile(`(?i)\b(username|user\s*name)\s+and\s+password\b`),
	regexp.MustCompile(`(?i)\b(basic|digest)\s+auth(?:entication)?\b.*\b(with|using)\b.*\b(user|pass|password|credential)\b`),
	regexp.MustCompile(`(?i)\b(basic|digest)\s+auth(?:entication)?\b.*\b(datasource|data\s*source|grafana)\b`),
	regexp.MustCompile(`(?i)\benable\s+(?:\w+\s+){0,2}(basic\s+)?auth\b.*\b(password|credential|username)\b`),
	regexp.MustCompile(`(?i)\bconfigure\s+(?:\w+\s+){0,3}(credentials?|auth(?:entication)?)\b.*\b(password|token|secret|username)\b`),
	regexp.MustCompile(`(?i)\b(store|save|paste|inject)\s+(?:my\s+)?(?:password|api[_\s-]?key|access\s+token|bearer\s+token|secret)\b`),
	regexp.MustCompile(`(?i)\blog\s*in\s+with\s+(?:my\s+)?(?:password|username)\b`),
}

// ---- secret detection patterns (ported from mcp-manage-datasources credentialguard) ----

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`-----BEGIN\s+(?:RSA\s+|EC\s+|DSA\s+|OPENSSH\s+)?PRIVATE\s+KEY(?:\s+BLOCK)?-----`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bghp_[a-zA-Z0-9]{36}\b`),
	regexp.MustCompile(`\bghs_[a-zA-Z0-9]{36}\b`),
	regexp.MustCompile(`\bglpat-[a-zA-Z0-9\-]{20,}\b`),
	regexp.MustCompile(`\bxox[baprs]-[0-9a-zA-Z\-]{10,}\b`),
	regexp.MustCompile(`(?i)(?:password|passwd|api[_-]?key|secret[_-]?key|auth[_-]?token)\s*[=:]\s*[^\s"']{8,}`),
	regexp.MustCompile(`(?i)Bearer\s+[a-zA-Z0-9\-._~+/]{20,}={0,2}`),
}

func matchesAuthIntent(text string) bool {
	if datasourceOrGrafanaContext.MatchString(text) {
		if authSetupVerbThenAuthPhrase.MatchString(text) {
			return true
		}
		if authPhraseTowardBackend.MatchString(text) {
			return true
		}
	}
	for _, p := range authIntentPatterns {
		if p.MatchString(text) {
			return true
		}
	}
	return false
}

func matchesSecretLike(text string) bool {
	for _, p := range secretPatterns {
		if p.MatchString(text) {
			return true
		}
	}
	return false
}

// checkDatasourceCredentials checks CreateDatasourceParams for credential policy violations.
// It mirrors DatasourceInputCredentialViolation from mcp-manage-datasources.
// Returns a reason code or "" if no violation.
func checkDatasourceCredentials(args CreateDatasourceParams) string {
	if args.BasicAuth {
		return "basic_auth_enabled_via_mcp_disallowed"
	}
	if args.BasicAuthUser != "" {
		return "basic_auth_user_via_mcp_disallowed"
	}
	if len(args.SecureJSONData) > 0 {
		return "secure_json_data_found"
	}

	// Collect all string field values and jsonData string values for scanning.
	candidates := []string{args.Name, args.Type, args.URL, args.Access, args.Database, args.User}
	for _, v := range args.JSONData {
		if s, ok := v.(string); ok {
			candidates = append(candidates, s)
		}
	}
	for _, s := range candidates {
		if strings.TrimSpace(s) == "" {
			continue
		}
		if matchesAuthIntent(s) {
			return "auth_credential_instructions"
		}
		if matchesSecretLike(s) {
			return "embedded_secret_or_token"
		}
	}
	return ""
}

// datasourceConfigPageURL builds the Grafana UI URL for datasource configuration.
// When uid is non-empty, links to the edit page for that datasource; otherwise
// links to the new datasource page. Prefers the public URL fetched from Grafana's
// frontend settings, falling back to the configured URL.
func datasourceConfigPageURL(ctx context.Context, uid string) string {
	var base string
	if gc := mcpgrafana.GrafanaClientFromContext(ctx); gc != nil && gc.PublicURL != "" {
		base = gc.PublicURL
	} else {
		base = strings.TrimRight(mcpgrafana.GrafanaConfigFromContext(ctx).URL, "/")
	}
	if base == "" {
		return ""
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	origin := fmt.Sprintf("%s://%s", u.Scheme, u.Host)
	var page string
	if uid != "" {
		page = "connections/datasources/edit/" + url.PathEscape(uid)
	} else {
		page = "connections/datasources/new"
	}
	out, err := url.Parse(origin + "/" + page)
	if err != nil {
		return ""
	}
	return out.String()
}

func openBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	return cmd.Start()
}

// credentialViolationResult builds a *mcp.CallToolResult for a credential policy
// violation. It opens the Grafana datasource creation page in the browser when
// possible and includes a resource_link content item pointing to it.
func credentialViolationResult(reason, configURL string) *mcp.CallToolResult {
	payload := map[string]any{
		"outcome":           "credential_policy_redirect",
		"reason":            reason,
		"credential_policy": credentialGuardMessage,
		"message":           "Credentials and secrets cannot be set through this tool. Configure them in the Grafana UI; the datasource configuration page was opened in your browser when possible. If user has entered a password, token or credential, they should change it or revoke it",
	}

	if configURL != "" {
		payload["open_config_page_url"] = configURL
		openBrowser(configURL) // best-effort: only works when server and client are on the same machine
	}

	b, _ := json.MarshalIndent(payload, "", "  ")
	content := []mcp.Content{
		mcp.TextContent{Type: "text", Text: string(b)},
	}
	if configURL != "" {
		content = append(content, mcp.NewResourceLink(
			configURL,
			"grafana-datasource-config",
			"Configure authentication and secrets in the Grafana UI; this tool does not accept credentials.",
			"",
		))
	}
	return &mcp.CallToolResult{Content: content, IsError: true}
}
