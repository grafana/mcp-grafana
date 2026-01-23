package tools

import (
	"errors"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
)

// Error Detection Strategy Summary (based on investigation testing):
//
// Prometheus: Uses typed errors from the client library (promv1.Error with Type field)
//   - Grafana proxy preserves the typed error structure
//   - Detection: errors.As() type assertion, check Type == "bad_data"
//
// Loki: Uses custom typed error (LokiAPIError with StatusCode field)
//   - Grafana proxy returns HTTP 400 for validation errors
//   - Detection: errors.As() type assertion, check StatusCode == 400

// IsPrometheusValidationError detects Prometheus query validation errors using typed errors.
//
// Investigation testing confirmed that Grafana's datasource proxy preserves typed promv1.Error,
// so we use errors.As() for precise type checking without any fallback.
//
// Returns true if the error is a Prometheus validation error (bad_data type).
func IsPrometheusValidationError(err error) bool {
	if err == nil {
		return false
	}

	var promErr *promv1.Error
	if !errors.As(err, &promErr) {
		return false
	}

	// Official Prometheus error type from API spec:
	// https://prometheus.io/docs/prometheus/latest/querying/api/
	// "bad_data" indicates 400 Bad Request - invalid query syntax/parameters
	return promErr.Type == "bad_data"
}

// httpStatusCodeError is an interface for errors that expose an HTTP status code.
// This allows us to check status codes without depending on concrete error types.
type httpStatusCodeError interface {
	error
	HTTPStatusCode() int
}

// IsLokiValidationError detects Loki query validation errors using typed errors.
//
// Investigation testing confirmed that Loki returns HTTP 400 for validation errors.
// We check if the error implements httpStatusCodeError and has status code 400.
//
// Returns true if the error is a Loki validation error (400 Bad Request).
func IsLokiValidationError(err error) bool {
	if err == nil {
		return false
	}

	// Type assert to httpStatusCodeError interface
	var statusErr httpStatusCodeError
	if !errors.As(err, &statusErr) {
		return false
	}

	// HTTP 400 Bad Request indicates validation errors (invalid LogQL syntax)
	return statusErr.HTTPStatusCode() == 400
}

// NewValidationErrorResult creates a CallToolResult for validation errors.
// This returns a successful MCP response with IsError=true, allowing LLMs
// to see the error details and retry with corrected input.
//
// The context parameter helps LLMs understand where the error occurred
// (e.g., "Prometheus query", "Loki query").
func NewValidationErrorResult(err error, context string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: fmt.Sprintf("Validation error in %s: %v", context, err),
			},
		},
		IsError: true,
	}
}
