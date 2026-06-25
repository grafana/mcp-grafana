package mcpgrafana

import (
	"net"
	"net/http"
	"slices"
	"strings"
)

type HostOriginPolicy struct {
	AllowedHosts   []string
	AllowedOrigins []string
}

// DNSRebindingProtectionMiddleware rejects requests whose Host (or Origin,
// when present) is not in the configured allowlists, defending HTTP/SSE
// transports against DNS-rebinding attacks. An empty AllowedOrigins rejects
// any request carrying an Origin header; a literal "*" disables either check.
func DNSRebindingProtectionMiddleware(policy HostOriginPolicy) func(http.Handler) http.Handler {
	hosts := normalizeAllowlist(policy.AllowedHosts)
	origins := normalizeAllowlist(policy.AllowedOrigins)
	skipHosts := slices.Contains(hosts, "*")
	skipOrigins := slices.Contains(origins, "*")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !skipHosts && len(hosts) > 0 {
				if !slices.Contains(hosts, strings.ToLower(r.Host)) {
					http.Error(w, "forbidden: host not allowed", http.StatusForbidden)
					return
				}
			}
			if !skipOrigins {
				if origin := r.Header.Get("Origin"); origin != "" {
					if !slices.Contains(origins, strings.ToLower(origin)) {
						http.Error(w, "forbidden: origin not allowed", http.StatusForbidden)
						return
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func normalizeAllowlist(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(strings.ToLower(v))
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// DefaultAllowedHosts derives a Host allowlist from a bind address. Wildcard
// binds (0.0.0.0, ::, empty host) return all loopback variants; "localhost"
// adds 127.0.0.1 and [::1]; specific hostnames return only themselves.
func DefaultAllowedHosts(address string) []string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return []string{strings.ToLower(address)}
	}
	host = strings.ToLower(host)

	switch host {
	case "", "0.0.0.0", "::":
		return []string{
			net.JoinHostPort("localhost", port),
			net.JoinHostPort("127.0.0.1", port),
			net.JoinHostPort("::1", port),
		}
	case "localhost":
		return []string{
			net.JoinHostPort("localhost", port),
			net.JoinHostPort("127.0.0.1", port),
			net.JoinHostPort("::1", port),
		}
	default:
		return []string{net.JoinHostPort(host, port)}
	}
}
