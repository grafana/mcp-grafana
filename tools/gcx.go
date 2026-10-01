package tools

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"strconv"
	"strings"

	"github.com/grafana/gcx/embed"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

// gcxOutputLimitBytes caps the gcx output returned to the client.
const gcxOutputLimitBytes = 100 * 1024

type ExecGcxParams struct {
	Command string `json:"command" jsonschema:"required,description=A gcx command line\\, e.g. 'slo definitions list' or 'metrics query \\'up{job=\"api\"}\\' --since 1h'. Shell quoting applies but there is no shell: pipes\\, redirects and $VARS are rejected. Use gcx's own --output/--json/--jq flags to shape output."`
	Stdin   string `json:"stdin,omitempty" jsonschema:"description=Content for commands that read a file from stdin via '-f -' (e.g. a resource manifest for 'resources push -f -'). Local file paths are not available."`
}

func execGcx(access embed.Access) func(context.Context, ExecGcxParams) (string, error) {
	return func(ctx context.Context, args ExecGcxParams) (string, error) {
		grafana, err := gcxGrafana(mcpgrafana.GrafanaConfigFromContext(ctx))
		if err != nil {
			return "", err
		}

		res, err := embed.Run(ctx, args.Command, embed.Options{
			Grafana: grafana,
			Stdin:   args.Stdin,
			Access:  access,
		})
		if err != nil {
			return "", err
		}

		out := truncateGcxOutput(res.Stdout)
		if res.ExitCode != 0 {
			if stderr := strings.TrimSpace(res.Stderr); stderr != "" {
				out = strings.TrimSpace(out + "\n" + truncateGcxOutput(stderr))
			}
			return "", fmt.Errorf("gcx exited with code %d: %s", res.ExitCode, out)
		}
		return out, nil
	}
}

// gcxGrafana maps the request's Grafana config onto gcx, authenticating the
// way mcp-grafana's own transport does: on-behalf-of tokens, then an API key,
// then basic auth.
func gcxGrafana(cfg mcpgrafana.GrafanaConfig) (embed.Grafana, error) {
	url := cfg.URL
	if cfg.OverrideURL != "" {
		url = cfg.OverrideURL
	}
	if url == "" {
		return embed.Grafana{}, errors.New("grafana url not configured")
	}

	g := embed.Grafana{
		URL: url,
		// gcx needs an org (or a discoverable Cloud stack) to address
		// resources; zero means Grafana's default org, as elsewhere here.
		OrgID:   max(cfg.OrgID, 1),
		Headers: maps.Clone(cfg.ExtraHeaders),
	}
	if g.Headers == nil {
		g.Headers = map[string]string{}
	}
	if cfg.OrgID != 0 {
		// gcx uses the org only to build resource namespaces; Grafana's legacy
		// APIs select the org from this header.
		g.Headers["X-Grafana-Org-Id"] = strconv.FormatInt(cfg.OrgID, 10)
	}
	switch {
	case cfg.AccessToken != "" && cfg.IDToken != "":
		g.Headers["X-Access-Token"] = cfg.AccessToken
		g.Headers["X-Grafana-Id"] = cfg.IDToken
	case cfg.APIKey != "":
		g.Token = cfg.APIKey
	case cfg.BasicAuth != nil:
		g.User = cfg.BasicAuth.Username()
		g.Password, _ = cfg.BasicAuth.Password()
	}

	if tls := cfg.TLSConfig; tls != nil {
		g.TLS = &embed.TLS{InsecureSkipVerify: tls.SkipVerify}
		for _, f := range []struct {
			path string
			dst  *[]byte
		}{{tls.CertFile, &g.TLS.CertPEM}, {tls.KeyFile, &g.TLS.KeyPEM}, {tls.CAFile, &g.TLS.CAPEM}} {
			if f.path == "" {
				continue
			}
			data, err := os.ReadFile(f.path)
			if err != nil {
				return embed.Grafana{}, fmt.Errorf("reading TLS file: %w", err)
			}
			*f.dst = data
		}
	}
	return g, nil
}

func truncateGcxOutput(s string) string {
	if len(s) <= gcxOutputLimitBytes {
		return s
	}
	return s[:gcxOutputLimitBytes] + fmt.Sprintf("\n[output truncated at %d bytes of %d; narrow the command with --limit, --json field selection or --jq]", gcxOutputLimitBytes, len(s))
}

const execGcxDescription = "Run a gcx command against this Grafana instance. gcx is Grafana's CLI and covers dashboards, folders and other resources, " +
	"datasource queries (Prometheus, Loki, Tempo, Pyroscope and more), alerting, SLOs, IRM/OnCall, Synthetic Monitoring, Fleet, k6, Knowledge Graph and other Grafana Cloud products. " +
	"Use it for anything no dedicated tool covers. Start with 'help' or '<command> --help' to discover commands and flags; 'help-tree' prints the whole command tree compactly. " +
	"Output is JSON. Commands run as gcx itself, not in a shell, and have no local filesystem: pass file content via the stdin argument with '-f -'."

var ExecGcx = mcpgrafana.MustTool(
	"exec_gcx",
	execGcxDescription+" Commands that change or delete resources are allowed; destructive commands need --force.",
	execGcx(embed.AccessDelete),
	mcpgrafana.WithTitleAnnotation("Run gcx command"),
	mcpgrafana.WithReadOnlyHintAnnotation(false),
	mcpgrafana.WithDestructiveHintAnnotation(true),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
)

var ExecGcxReadOnly = mcpgrafana.MustTool(
	"exec_gcx",
	execGcxDescription+" This server is read-only: requests that would change anything are refused.",
	execGcx(embed.AccessRead),
	mcpgrafana.WithTitleAnnotation("Run gcx command"),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
)

// AddGcxTools registers exec_gcx. Most gcx commands query datasources, so it
// is not registered when query tools are disabled.
func AddGcxTools(s *mcp.Server, enableWriteTools, enableQueryTools bool) {
	switch {
	case !enableQueryTools:
	case enableWriteTools:
		ExecGcx.Register(s)
	default:
		ExecGcxReadOnly.Register(s)
	}
}
