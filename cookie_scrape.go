package mcpgrafana

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/browserutils/kooky"

	// Registers every supported browser's cookie store finder. Without this
	// blank import kooky finds nothing.
	_ "github.com/browserutils/kooky/browser/all"
)

// cookieScrapeTimeout bounds the search across every installed browser's cookie
// store. Reading these stores involves per-platform decryption (macOS Keychain,
// Windows DPAPI) that can block, so the scrape is not allowed to hang startup.
const cookieScrapeTimeout = 30 * time.Second

// scrapeBrowserCookies collects Grafana session cookies for host from the
// cookie stores of the user's installed browsers.
//
// Candidates are returned in the order kooky yields them and are *not*
// validated here — the caller probes each against Grafana, because a cookie
// present in a browser is very often already stale (Grafana rotates
// grafana_session server-side).
func scrapeBrowserCookies(host string, logger *slog.Logger) []cookieCandidate {
	if host == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), cookieScrapeTimeout)
	defer cancel()

	// Filter on name before kooky.Valid so that only the handful of cookies we
	// care about are decrypted, rather than every cookie in every store.
	cookies, err := kooky.ReadCookies(ctx,
		kooky.FilterFunc(func(c *kooky.Cookie) bool {
			return c.Name == grafanaSessionCookie || c.Name == grafanaSessionExpiryCookie
		}),
		kooky.Valid,
	)
	if err != nil {
		// Partial results are still returned alongside an error when only some
		// stores fail (a locked profile, a browser using an unsupported
		// encryption scheme), so this is logged rather than treated as fatal.
		logger.Debug("Some browser cookie stores could not be read", "error", err)
	}

	return groupSessionCookies(cookies, host)
}

// groupSessionCookies pairs each grafana_session cookie with the
// grafana_session_expiry from the same browser profile and returns one
// candidate per profile that has a session cookie, in discovery order.
func groupSessionCookies(cookies kooky.Cookies, host string) []cookieCandidate {
	// index maps a profile key to its position in candidates, so results keep
	// kooky's discovery order rather than Go's randomized map order.
	index := make(map[string]int)
	var candidates []cookieCandidate

	for _, c := range cookies {
		if c == nil || c.Value == "" || !cookieDomainMatches(c.Domain, host) {
			continue
		}

		key := scrapeGroupKey(c)
		i, ok := index[key]
		if !ok {
			i = len(candidates)
			index[key] = i
			candidates = append(candidates, cookieCandidate{source: browserLabel(c)})
		}

		switch c.Name {
		case grafanaSessionCookie:
			candidates[i].value = c.Value
			if !c.Expires.IsZero() {
				candidates[i].expires = c.Expires.Unix()
			}
		case grafanaSessionExpiryCookie:
			candidates[i].expiry = c.Value
		}
	}

	// Drop profiles that yielded only an expiry cookie; it is useless alone.
	out := candidates[:0]
	for _, c := range candidates {
		if c.value != "" {
			out = append(out, c)
		}
	}
	return out
}

// scrapeGroupKey identifies the browser profile a cookie came from, so that a
// session cookie is only ever paired with the expiry cookie from the same
// profile. Cookies from different profiles are independent sessions.
func scrapeGroupKey(c *kooky.Cookie) string {
	var browser, profile string
	if c.Browser != nil {
		browser, profile = c.Browser.Browser(), c.Browser.Profile()
	}
	return browser + "\x00" + profile + "\x00" + c.Container
}

// browserLabel produces a human-readable source name for logging.
func browserLabel(c *kooky.Cookie) string {
	if c.Browser == nil {
		return "browser"
	}
	name := c.Browser.Browser()
	if name == "" {
		name = "browser"
	}
	if profile := c.Browser.Profile(); profile != "" && !c.Browser.IsDefaultProfile() {
		return name + " (" + profile + ")"
	}
	return name
}

// cookieDomainMatches implements the standard cookie domain-match rule: a
// cookie applies to host when its domain equals host, or when host is a
// subdomain of it.
//
// A plain suffix comparison is not sufficient in either direction — a cookie
// scoped to .grafana.net must match myinstance.grafana.net, while a cookie
// scoped to evilgrafana.net must not match grafana.net.
func cookieDomainMatches(domain, host string) bool {
	domain = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(domain), "."))
	host = strings.ToLower(strings.TrimSpace(host))
	if domain == "" || host == "" {
		return false
	}
	if domain == host {
		return true
	}
	return strings.HasSuffix(host, "."+domain)
}
