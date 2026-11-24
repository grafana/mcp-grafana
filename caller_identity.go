package mcpgrafana

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sync/singleflight"
)

// sharedCredentialEnvVars give every caller the same Grafana identity.
var sharedCredentialEnvVars = []string{
	grafanaServiceAccountTokenEnvVar,
	grafanaServiceAccountTokenFileEnvVar,
	grafanaAPIEnvVar,
	grafanaUsernameEnvVar,
	grafanaPasswordEnvVar,
}

// grafanaAuthHeaders authenticate a request to Grafana when they are sent.
var grafanaAuthHeaders = []string{"Authorization", "Cookie"}

// SharedCredentialsFromEnv names every shared Grafana credential in the
// environment: credential vars, user:password in GRAFANA_URL, and credential
// or forwarded headers in GRAFANA_EXTRA_HEADERS. Values are never returned.
func SharedCredentialsFromEnv(identityHeaders []string) []string {
	var set []string
	for _, name := range sharedCredentialEnvVars {
		if getEnv(name) != "" {
			set = append(set, name)
		}
	}
	// net/http turns URL userinfo into Authorization: Basic when none is set.
	if u, err := url.Parse(normalizeGrafanaURL(getEnv(grafanaURLEnvVar))); err == nil && u.User != nil {
		set = append(set, grafanaURLEnvVar+" userinfo")
	}
	var extra map[string]string
	if json.Unmarshal([]byte(os.Getenv(grafanaExtraHeadersEnvVar)), &extra) != nil {
		return set // invalid JSON is ignored at runtime too, so nothing is sent
	}
	credential := slices.Concat(grafanaAuthHeaders, identityHeaders,
		[]string{grafanaServiceAccountTokenHeader, grafanaAPIKeyHeader})
	forwarded := forwardHeaderNamesFromEnv()
	for name := range extra {
		if containsFold(credential, name) || containsFold(forwarded, name) {
			set = append(set, grafanaExtraHeadersEnvVar+"["+name+"]")
		}
	}
	sort.Strings(set)
	return set
}

// UnforwardedHeaders returns the names not listed in GRAFANA_FORWARD_HEADERS.
func UnforwardedHeaders(names []string) []string {
	forwarded := forwardHeaderNamesFromEnv()
	var missing []string
	for _, name := range names {
		if !containsFold(forwarded, name) {
			missing = append(missing, name)
		}
	}
	return missing
}

func containsFold(list []string, name string) bool {
	return slices.ContainsFunc(list, func(s string) bool { return strings.EqualFold(s, name) })
}

// RequireUserCredentials rejects with 401 any request without its own Grafana credential.
// Forwarded headers count only if they are Authorization, Cookie or in identityHeaders.
func RequireUserCredentials(identityHeaders []string, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	credential := slices.Concat(grafanaAuthHeaders, identityHeaders)
	var forwarded []string
	for _, name := range forwardHeaderNamesFromEnv() {
		if containsFold(credential, name) {
			forwarded = append(forwarded, name)
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodOptions || hasUserCredentials(r, forwarded) {
				next.ServeHTTP(w, r)
				return
			}
			logger.Warn("Rejected request without per-user Grafana credentials",
				"remote_addr", r.RemoteAddr, "method", r.Method, "path", r.URL.Path)
			// No WWW-Authenticate: Bearer would start OAuth discovery for a Grafana problem.
			http.Error(w, "unauthorized: missing per-user Grafana credentials", http.StatusUnauthorized)
		})
	}
}

// hasUserCredentials checks the request's own credentials; forwarded holds
// only the forwarded header names that authenticate to Grafana.
func hasUserCredentials(r *http.Request, forwarded []string) bool {
	if apiKeyFromHeaders(r) != "" {
		return true
	}
	if user, pass, ok := r.BasicAuth(); ok && (user != "" || pass != "") {
		return true
	}
	for _, name := range forwarded {
		if r.Header.Get(name) != "" {
			return true
		}
	}
	return false
}

// callerIdentityTTL bounds how long a resolved identity is reused.
const callerIdentityTTL = 5 * time.Minute

// callerIdentityTimeout bounds the /api/user lookup made for the call log.
const callerIdentityTimeout = 5 * time.Second

// callerIdentity is the subset of /api/user the tool call log records.
type callerIdentity struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
	OrgID int64  `json:"orgId"`
}

// identityCache lives on a GrafanaClient, which ClientCache keys by
// clientCacheKey, so it is per credential and forwarded headers. Only 200s are cached.
type identityCache struct {
	mu      sync.Mutex
	value   callerIdentity
	expires time.Time
	flight  singleflight.Group
}

type identityResult struct {
	identity callerIdentity
	status   int
}

// cached returns the identity if it has not expired.
func (c *identityCache) cached() (callerIdentity, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value, time.Now().Before(c.expires)
}

// lookup returns the identity and the HTTP status of /api/user.
// Status 0 means Grafana could not be reached.
func (c *identityCache) lookup(ctx context.Context, cfg *GrafanaConfig) (callerIdentity, int) {
	if id, ok := c.cached(); ok {
		return id, http.StatusOK
	}
	v, _, _ := c.flight.Do("", func() (any, error) {
		// Re-check: a flight that just finished may have filled the cache.
		if id, ok := c.cached(); ok {
			return identityResult{identity: id, status: http.StatusOK}, nil
		}
		id, status := fetchCallerIdentity(ctx, cfg)
		if status == http.StatusOK {
			c.mu.Lock()
			c.value, c.expires = id, time.Now().Add(callerIdentityTTL)
			c.mu.Unlock()
		}
		return identityResult{identity: id, status: status}, nil
	})
	r := v.(identityResult)
	return r.identity, r.status
}

func fetchCallerIdentity(ctx context.Context, cfg *GrafanaConfig) (callerIdentity, int) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), callerIdentityTimeout)
	defer cancel()
	var id callerIdentity
	err := grafanaGetJSON(ctx, cfg, "/api/user", &id)
	if err == nil {
		return id, http.StatusOK
	}
	var statusErr *grafanaStatusError
	if errors.As(err, &statusErr) {
		return callerIdentity{}, statusErr.status
	}
	return callerIdentity{}, 0
}

// callerIdentityFromContext resolves the caller via the client's cache when present.
func callerIdentityFromContext(ctx context.Context) (callerIdentity, int) {
	cfg := GrafanaConfigFromContext(ctx)
	if gc := GrafanaClientFromContext(ctx); gc != nil && gc.identity != nil {
		return gc.identity.lookup(ctx, &cfg)
	}
	return fetchCallerIdentity(ctx, &cfg)
}

// ToolCallLogMiddleware logs one line per tools/call with the caller's identity.
// Arguments are never logged. Register it inside GrafanaContextMiddleware.
func ToolCallLogMiddleware(logger *slog.Logger) mcp.Middleware {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			call, ok := req.(*mcp.CallToolRequest)
			if method != "tools/call" || !ok || call.Params == nil || !logger.Enabled(ctx, slog.LevelInfo) {
				return next(ctx, method, req)
			}
			start := time.Now()
			lookup := startIdentityLookup(ctx)
			result, err := next(ctx, method, req)
			d := time.Since(start)
			logToolCall(ctx, logger, call, result, err, d, lookup.wait(ctx))
			return result, err
		}
	}
}

// identityLookup is an /api/user lookup running beside the tool call.
type identityLookup struct{ done chan identityResult }

// startIdentityLookup runs the lookup in a goroutine so a slow Grafana does not
// delay the tool result. The goroutine ends within callerIdentityTimeout and
// sends on a buffered channel, so it cannot leak if nobody waits.
func startIdentityLookup(ctx context.Context) identityLookup {
	done := make(chan identityResult, 1)
	go func() {
		id, status := callerIdentityFromContext(ctx)
		done <- identityResult{identity: id, status: status}
	}()
	return identityLookup{done: done}
}

// wait returns the lookup result, or status 0 if ctx is done first.
func (l identityLookup) wait(ctx context.Context) identityResult {
	select {
	case r := <-l.done:
		return r
	case <-ctx.Done():
		return identityResult{}
	}
}

func logToolCall(ctx context.Context, logger *slog.Logger, call *mcp.CallToolRequest, result mcp.Result, err error, d time.Duration, who identityResult) {
	id, status := who.identity, who.status
	orgID := GrafanaConfigFromContext(ctx).OrgID
	if orgID == 0 {
		orgID = id.OrgID
	}
	outcome := "ok"
	if r, isCall := result.(*mcp.CallToolResult); err != nil || (isCall && r != nil && r.IsError) {
		outcome = "error"
	}

	attrs := make([]slog.Attr, 0, 10)
	attrs = append(attrs,
		slog.String("tool", call.Params.Name),
		slog.String("grafana_user", id.Login),
		slog.Int64("user_id", id.ID),
		slog.Int64("org_id", orgID),
		slog.String("result", outcome),
		slog.Int("identity_status", status),
		slog.Duration("duration", d),
	)
	if uid := datasourceUIDFromArgs(call.Params.Arguments); uid != "" {
		attrs = append(attrs, slog.String("datasource_uid", uid))
	}
	if u, _ := ctx.Value(grafanaOverrideKey{}).(string); u != "" {
		// The caller chose the target, so grafana_user is whatever that target answered.
		attrs = append(attrs, slog.Bool("override", true))
	}
	if call.Session != nil {
		if sid := call.Session.ID(); sid != "" {
			attrs = append(attrs, slog.String("session", sid))
		}
	}
	logger.LogAttrs(ctx, slog.LevelInfo, "Tool call", attrs...)
}

// datasourceUIDFromArgs reads only the top-level datasourceUid argument.
func datasourceUIDFromArgs(args json.RawMessage) string {
	if !bytes.Contains(args, []byte(`"datasourceUid"`)) {
		return ""
	}
	var v struct {
		DatasourceUID string `json:"datasourceUid"`
	}
	if json.Unmarshal(args, &v) != nil {
		return ""
	}
	return v.DatasourceUID
}
