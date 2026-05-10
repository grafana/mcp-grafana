package auth

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
)

const bootstrapPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>mcp-grafana: connect your Grafana account</title>
<style>
body { font-family: system-ui, sans-serif; max-width: 600px; margin: 4em auto; padding: 0 1em; color: #222; }
input[type=text], input[type=password] { width: 100%; padding: 0.6em; font-family: monospace; }
button { padding: 0.6em 1.2em; font-size: 1em; }
.error { color: #b00020; margin: 1em 0; padding: 0.6em; background: #fce4ec; border-left: 3px solid #b00020; }
.help { color: #555; font-size: 0.9em; }
</style>
</head>
<body>
<h1>Connect your Grafana account</h1>
<p>Paste a Grafana service-account token. The token is encrypted at rest on this server and used only for your sessions.</p>
{{if .Error}}<div class="error">{{.Error}}</div>{{end}}
<form method="POST" action="/bootstrap">
  <input type="hidden" name="flow" value="{{.Flow}}">
  <p><label for="t">Service-account token</label><br>
     <input type="password" id="t" name="grafana_token" required autofocus></p>
  <p><button type="submit">Connect</button></p>
</form>
<p class="help">Don't have a token? <a target="_blank" rel="noopener" href="https://grafana.com/docs/grafana/latest/administration/service-accounts/#add-a-token-to-a-service-account-in-grafana">How to create a service-account token in Grafana.</a></p>
</body>
</html>`

var bootstrapTmpl = template.Must(template.New("bootstrap").Parse(bootstrapPageHTML))

// BootstrapHandler renders the paste form (GET) and validates the pasted
// token (POST) by calling Grafana's /api/user with it.
func (s *Server) BootstrapHandler(grafanaURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.renderBootstrap(w, r, "")
		case http.MethodPost:
			s.processBootstrap(w, r, grafanaURL)
		default:
			httpError(w, http.StatusMethodNotAllowed, "invalid_request", "GET or POST")
		}
	})
}

func (s *Server) renderBootstrap(w http.ResponseWriter, r *http.Request, errMsg string) {
	flow := r.URL.Query().Get("flow")
	if flow == "" {
		flow = r.FormValue("flow")
	}
	if _, ok := peekBootstrap(flow); !ok {
		httpError(w, http.StatusBadRequest, "invalid_request", "unknown or expired flow token")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if errMsg != "" {
		w.WriteHeader(http.StatusUnauthorized)
	}
	_ = bootstrapTmpl.Execute(w, map[string]string{
		"Flow":  flow,
		"Error": errMsg,
	})
}

func (s *Server) processBootstrap(w http.ResponseWriter, r *http.Request, grafanaURL string) {
	if err := r.ParseForm(); err != nil {
		httpError(w, http.StatusBadRequest, "invalid_request", "form parse")
		return
	}
	flow := r.FormValue("flow")
	token := strings.TrimSpace(r.FormValue("grafana_token"))
	if flow == "" || token == "" {
		s.renderBootstrap(w, r, "Please paste a token.")
		return
	}

	pb, ok := peekBootstrap(flow)
	if !ok {
		httpError(w, http.StatusBadRequest, "invalid_request", "unknown or expired flow token")
		return
	}
	// peekBootstrap already enforces bootstrapTTL under the mutex (returns
	// ok=false for expired entries), so a duplicate freshness check here
	// would be unreachable. The race window the consume below covers — the
	// entry expiring while validateGrafanaToken's network call is in
	// flight — is handled by consumeBootstrap returning ok=false.

	if err := validateGrafanaToken(r.Context(), grafanaURL, token); err != nil {
		s.logger().Warn("bootstrap_token_rejected", "user_id", pb.identity.String(), "error", err.Error())
		s.renderBootstrap(w, r, "Grafana rejected that token. Double-check and try again.")
		return
	}

	// Token good — atomically claim the flow before issuing an auth code.
	// peekBootstrap above is a non-consuming read; a concurrent POST for
	// the same flow token could race in between, OR the entry could
	// expire while validateGrafanaToken was running (it's a network call
	// that can take seconds). consumeBootstrap returns ok=false in both
	// cases and we can't tell them apart at this layer, so the message
	// covers both — the user is told to log in again either way.
	claimed, ok := consumeBootstrap(flow)
	if !ok {
		httpError(w, http.StatusBadRequest, "invalid_request", "flow expired or already consumed; please log in again")
		return
	}
	pb = *claimed

	ct, err := s.Encryptor.Seal([]byte(token))
	if err != nil {
		s.logger().Error("auth.bootstrap_encrypt_failed", "user_id", pb.identity.String(), "error", err.Error())
		httpError(w, http.StatusInternalServerError, "server_error", "encryption failed")
		return
	}
	s.logger().Info("auth.bootstrap_token_validated", "user_id", pb.identity.String())

	pf := &pendingFlow{
		clientID:            pb.clientID,
		redirectURI:         pb.redirectURI,
		codeChallenge:       pb.codeChallenge,
		codeChallengeMethod: pb.codeChallengeMethod,
		clientState:         pb.clientState,
		createdAt:           pb.createdAt,
	}
	s.completeAuthCode(w, r, pf, pb.identity, ct)
}

// validateGrafanaToken pings Grafana's /api/user with the bearer.
func validateGrafanaToken(ctx context.Context, grafanaURL, token string) error {
	ctx, span := otel.Tracer("mcp-grafana-auth").Start(ctx, "auth.bootstrap_validate")
	defer span.End()

	target, err := url.JoinPath(grafanaURL, "/api/user")
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	c := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("call grafana: %w", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("grafana returned status %d", resp.StatusCode)
	}
	return nil
}
