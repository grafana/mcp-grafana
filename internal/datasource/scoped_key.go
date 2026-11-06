package datasource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

// ScopedKey scopes a cache key to the Grafana URL, organization, credentials,
// and the caller-provided kind and identifier.
func ScopedKey(ctx context.Context, kind, id string) string {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	h := sha256.New()
	for _, part := range []string{cfg.APIKey, cfg.AccessToken, cfg.IDToken} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	if cfg.BasicAuth != nil {
		if password, ok := cfg.BasicAuth.Password(); ok {
			h.Write([]byte(cfg.BasicAuth.Username() + ":" + password))
		} else {
			h.Write([]byte(cfg.BasicAuth.Username()))
		}
	}
	h.Write([]byte{0})
	headers := make([]string, 0, len(cfg.ExtraHeaders))
	for key := range cfg.ExtraHeaders {
		headers = append(headers, key)
	}
	sort.Strings(headers)
	for _, key := range headers {
		h.Write([]byte(key))
		h.Write([]byte{0})
		h.Write([]byte(cfg.ExtraHeaders[key]))
		h.Write([]byte{0})
	}
	credentialHash := h.Sum(nil)
	return strings.TrimRight(cfg.URL, "/") + "\x00" + strconv.FormatInt(cfg.OrgID, 10) + "\x00" +
		hex.EncodeToString(credentialHash[:8]) + "\x00" + kind + "\x00" + id
}
