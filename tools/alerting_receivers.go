package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

const receiversAPIGroup = "notifications.alerting.grafana.app"

// receiverDetail describes a receiver that the provisioning contact point API
// does not return. Grafana's GET /api/v1/provisioning/contact-points lists one
// entry per v1 integration, so a receiver with no integrations, or with only
// integrations imported from a Mimir or Alertmanager configuration, is
// missing there even though notification policies route to it.
type receiverDetail struct {
	Name         string                `json:"name"`
	UID          string                `json:"uid"`
	Integrations []receiverIntegration `json:"integrations"`
	Note         string                `json:"note"`
}

type receiverIntegration struct {
	UID                   string         `json:"uid,omitempty"`
	Type                  string         `json:"type"`
	Version               string         `json:"version,omitempty"`
	DisableResolveMessage bool           `json:"disable_resolve_message,omitempty"`
	Settings              map[string]any `json:"settings,omitempty"`
	SecureFields          []string       `json:"secure_fields,omitempty"`
}

type receiverObject struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Title        string `json:"title"`
		Integrations []struct {
			UID                   string          `json:"uid"`
			Type                  string          `json:"type"`
			Version               string          `json:"version"`
			DisableResolveMessage bool            `json:"disableResolveMessage"`
			Settings              map[string]any  `json:"settings"`
			SecureFields          map[string]bool `json:"secureFields"`
		} `json:"integrations"`
	} `json:"spec"`
}

// findReceiverByTitle looks the receiver up through the receivers API. It
// returns nil without an error when that API is not served or no receiver
// has the title, so callers can keep their own not-found message.
func findReceiverByTitle(ctx context.Context, title string) (*receiverDetail, error) {
	k8s := mcpgrafana.KubernetesClientFromContext(ctx)
	if k8s == nil {
		return nil, nil
	}
	versions, err := k8s.GroupVersions(ctx, receiversAPIGroup)
	if err != nil {
		return nil, err
	}
	version := preferredReceiversVersion(versions)
	if version == "" {
		return nil, nil
	}
	namespace, err := mcpgrafana.GrafanaNamespace(ctx)
	if err != nil {
		return nil, err
	}
	desc := mcpgrafana.ResourceDescriptor{Group: receiversAPIGroup, Version: version, Resource: "receivers"}
	opts := &mcpgrafana.ListOptions{}
	for {
		list, err := k8s.List(ctx, desc, namespace, opts)
		if err != nil {
			return nil, err
		}
		for _, item := range list.Items {
			receiver, err := decodeReceiver(item)
			if err != nil {
				return nil, err
			}
			if receiver.Spec.Title == title {
				return receiver.detail(), nil
			}
		}
		next, _ := list.Metadata["continue"].(string)
		if next == "" {
			return nil, nil
		}
		opts.Continue = next
	}
}

// preferredReceiversVersion picks the stable version when Grafana serves it.
func preferredReceiversVersion(versions []string) string {
	for _, v := range []string{"v1beta1", "v0alpha1"} {
		if slices.Contains(versions, v) {
			return v
		}
	}
	if len(versions) > 0 {
		return versions[0]
	}
	return ""
}

func decodeReceiver(item map[string]any) (*receiverObject, error) {
	raw, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	var receiver receiverObject
	if err := json.Unmarshal(raw, &receiver); err != nil {
		return nil, fmt.Errorf("decode receiver: %w", err)
	}
	return &receiver, nil
}

func (r *receiverObject) detail() *receiverDetail {
	d := &receiverDetail{
		Name:         r.Spec.Title,
		UID:          r.Metadata.Name,
		Integrations: make([]receiverIntegration, 0, len(r.Spec.Integrations)),
	}
	for _, in := range r.Spec.Integrations {
		secure := make([]string, 0, len(in.SecureFields))
		for field := range in.SecureFields {
			secure = append(secure, field)
		}
		slices.Sort(secure)
		d.Integrations = append(d.Integrations, receiverIntegration{
			UID:                   in.UID,
			Type:                  in.Type,
			Version:               in.Version,
			DisableResolveMessage: in.DisableResolveMessage,
			Settings:              in.Settings,
			SecureFields:          secure,
		})
	}
	switch {
	case len(d.Integrations) == 0:
		d.Note = "This contact point exists but has no integrations, so notifications routed to it are not sent anywhere."
	case slices.ContainsFunc(d.Integrations, func(in receiverIntegration) bool { return !isV1Integration(in.Version) }):
		d.Note = "This contact point has integrations imported from a Mimir or Alertmanager configuration (version other than v1), which Grafana's provisioning API does not list. They are shown here from the receivers API."
	default:
		d.Note = "Grafana's provisioning API did not return this contact point. It is shown here from the receivers API."
	}
	return d
}

// isV1Integration mirrors Grafana's check for integrations the provisioning
// API returns.
func isV1Integration(version string) bool {
	return version == "" || version == "v1"
}
