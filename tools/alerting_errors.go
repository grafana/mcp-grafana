package tools

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/go-openapi/runtime"
)

// Permissions Grafana accepts for the alerting provisioning endpoints the
// tools call, from pkg/services/ngalert/api/authorization.go. Each lists the
// least-privileged option a Grafana admin would grant.
const (
	contactPointsReadPermissions  = "alert.notifications:read or alert.notifications.receivers:read"
	contactPointsWritePermissions = "alert.notifications.receivers:create (or alert.notifications:write) together with alert.provisioning.provenance:write"
	timeIntervalsReadPermissions  = "alert.notifications:read or alert.notifications.time-intervals:read"
	alertRuleCreatePermissions    = "alert.rules:create on the destination folder and query access to the rule's data sources, together with alert.provisioning.provenance:write"
)

// explainForbidden turns a 403 from the generated client, which drops
// Grafana's response body and prints it as "{}", into a message naming the
// permissions Grafana checks. Retrying cannot succeed, so the message says
// to report the missing permission instead. Other errors are returned as is.
func explainForbidden(err error, needed string) error {
	var apiErr *runtime.APIError
	if !errors.As(err, &apiErr) || !apiErr.IsCode(http.StatusForbidden) {
		return err
	}
	return fmt.Errorf("permission denied (HTTP 403 Forbidden): the Grafana user, or the token acting for them, needs %s. Retrying will not help; tell the user which permission to ask a Grafana admin for: %w", needed, err)
}
