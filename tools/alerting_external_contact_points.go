package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"

	"github.com/prometheus/alertmanager/config"
	"gopkg.in/yaml.v3"
)

func createExternalContactPoint(ctx context.Context, args ManageRoutingWriteParams) (*contactPointSummary, error) {
	settings := maps.Clone(args.Settings)
	settings["send_resolved"] = !args.DisableResolveMessage
	receiver := map[string]any{"name": args.Name, args.Type + "_configs": []any{settings}}
	// Validate the new receiver only. Never serialize a typed config back to the
	// backend: Alertmanager's secret types would replace passwords with <secret>.
	encodedYAML, err := yaml.Marshal(receiver)
	if err != nil {
		return nil, fmt.Errorf("invalid native Alertmanager receiver settings")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(encodedYAML))
	decoder.KnownFields(true)
	var checked config.Receiver
	if err := decoder.Decode(&checked); err != nil {
		return nil, fmt.Errorf("invalid native Alertmanager integration type or settings, use Alertmanager keys such as email.to or webhook.url")
	}
	client, err := newAlertingClientFromContext(ctx)
	if err != nil {
		return nil, err
	}
	ds, err := client.resolveExternalAlertingDatasource(ctx, args.DatasourceUID, true)
	if err != nil {
		return nil, err
	}
	unlock, err := lockExternalAlertingWrite(ctx, client.externalWriteKey(ctx, ds.UID, "", "", true))
	if err != nil {
		return nil, err
	}
	defer unlock()

	path := "/api/alertmanager/" + url.PathEscape(ds.UID) + "/config/api/v1/alerts"
	body, status, err := client.externalAlertingRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("read external Alertmanager config: HTTP %d, an existing writable configuration is required", status)
	}
	envelope, err := decodeExternalAlertingObject(body)
	if err != nil {
		return nil, err
	}
	alertmanager, err := decodeExternalAlertingObject(envelope["alertmanager_config"])
	if err != nil {
		return nil, err
	}
	// A root route is needed for a valid config. Do not invent notification routing
	// just to create a contact point on an unconfigured Alertmanager.
	if _, err := decodeExternalAlertingObject(alertmanager["route"]); err != nil {
		return nil, fmt.Errorf("external Alertmanager needs an existing root route, configure it before creating contact points")
	}
	var secretCheck any
	if json.Unmarshal(body, &secretCheck) != nil || hasRedactedAlertingSecret(secretCheck) {
		return nil, fmt.Errorf("external Alertmanager returned redacted secrets, refusing to overwrite the configuration")
	}
	var receivers []json.RawMessage
	if err := json.Unmarshal(alertmanager["receivers"], &receivers); err != nil || len(receivers) == 0 {
		return nil, fmt.Errorf("external Alertmanager needs an existing receiver configuration")
	}
	for _, raw := range receivers {
		var existing struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &existing) != nil || existing.Name == "" {
			return nil, fmt.Errorf("invalid existing Alertmanager receiver")
		}
		if existing.Name == args.Name {
			return nil, fmt.Errorf("a receiver with this name already exists, nothing was created")
		}
	}
	encoded, err := json.Marshal(receiver)
	if err != nil {
		return nil, fmt.Errorf("encode external Alertmanager receiver")
	}
	if err := setExternalAlertingField(alertmanager, "receivers", append(receivers, encoded)); err != nil {
		return nil, err
	}
	if err := setExternalAlertingField(envelope, "alertmanager_config", alertmanager); err != nil {
		return nil, err
	}
	if err := client.postExternalAlerting(ctx, path, nil, envelope); err != nil {
		return nil, err
	}
	return &contactPointSummary{Name: args.Name, Type: &args.Type, DatasourceUID: ds.UID}, nil
}

func hasRedactedAlertingSecret(value any) bool {
	switch v := value.(type) {
	case string:
		return v == "<secret>" || v == "********"
	case map[string]any:
		for _, child := range v {
			if hasRedactedAlertingSecret(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if hasRedactedAlertingSecret(child) {
				return true
			}
		}
	}
	return false
}
