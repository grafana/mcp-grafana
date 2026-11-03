package mcpgrafana

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/grafana/incident-go"
)

// rejectIncidentErrorStatuses makes the incident client return an error for an
// error (4xx or 5xx) response. The generated incident-go client checks the status only
// when the body isn't JSON, so a JSON error such as Grafana's 403 ("Permissions
// needed: plugins.app:access") decoded as an empty success: list_incidents
// told a caller without access that there were no incidents.
func rejectIncidentErrorStatuses(c *incident.Client) {
	base := c.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	c.HTTPClient.Transport = incidentStatusTransport{base: base}
}

type incidentStatusTransport struct {
	base http.RoundTripper
}

func (t incidentStatusTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	// Redirects pass through so http.Client can follow them.
	if err != nil || resp.StatusCode < http.StatusBadRequest {
		return resp, err
	}
	defer func() { _ = resp.Body.Close() }()
	// incident-go asks for gzip and decodes it itself, so the transport
	// leaves error bodies compressed.
	var body io.Reader = resp.Body
	if strings.Contains(resp.Header.Get("Content-Encoding"), "gzip") {
		if zr, err := gzip.NewReader(resp.Body); err == nil {
			defer func() { _ = zr.Close() }()
			body = zr
		}
	}
	msg, _ := io.ReadAll(io.LimitReader(body, 4096))
	return nil, &IncidentAPIError{StatusCode: resp.StatusCode, Status: resp.Status, Body: strings.TrimSpace(string(msg))}
}

// IncidentAPIError is an error (4xx or 5xx) response from the incident API.
// Match it with errors.As, for example to recognise a 404.
type IncidentAPIError struct {
	StatusCode int
	Status     string
	Body       string
}

func (e *IncidentAPIError) Error() string {
	return fmt.Sprintf("incident API returned %s: %s", e.Status, e.Body)
}
