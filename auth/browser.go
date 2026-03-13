package auth

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

const (
	loginTimeout       = 5 * time.Minute
	cookiePollInterval = 500 * time.Millisecond
	grafanaSessionName = "grafana_session"
)

// BrowserLogin opens a real browser window to the Grafana login page and waits
// for the user to complete SSO authentication. Once authenticated, it captures
// the grafana_session cookie and returns it.
//
// A persistent Chrome profile is used so that IdP sessions (Okta, Google, etc.)
// are remembered. If the profile is locked by another Chrome instance, a
// temporary profile is used as fallback.
func BrowserLogin(grafanaURL string) (string, error) {
	parsedURL, err := url.Parse(grafanaURL)
	if err != nil {
		return "", fmt.Errorf("invalid Grafana URL: %w", err)
	}
	domain := parsedURL.Hostname()
	loginURL := strings.TrimRight(grafanaURL, "/") + "/login"

	sendNotification(
		"Grafana MCP - Login Required",
		"A browser window will open for Grafana SSO login. Please complete authentication.",
	)

	cookie, err := tryBrowserLogin(loginURL, domain, chromeProfileDir())
	if err != nil {
		slog.Warn("Browser login with persistent profile failed, retrying with temp profile", "error", err)
		cookie, err = tryBrowserLogin(loginURL, domain, "")
	}
	if err != nil {
		sendNotification(
			"Grafana MCP - Login Failed",
			fmt.Sprintf("Browser login timed out after %s. Please try again.", loginTimeout),
		)
		return "", fmt.Errorf("browser login failed: %w", err)
	}

	return cookie, nil
}

func tryBrowserLogin(loginURL, domain, profileDir string) (string, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),
		chromedp.Flag("disable-gpu", false),
		chromedp.WindowSize(1024, 768),
	)

	if profileDir != "" {
		if err := os.MkdirAll(profileDir, 0o700); err != nil {
			return "", fmt.Errorf("create chrome profile dir: %w", err)
		}
		opts = append(opts, chromedp.UserDataDir(profileDir))
		slog.Info("Starting browser login", "url", loginURL, "profile", profileDir)
	} else {
		slog.Info("Starting browser login with temporary profile", "url", loginURL)
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer allocCancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, loginTimeout)
	defer timeoutCancel()

	var sessionCookie string

	err := chromedp.Run(timeoutCtx,
		chromedp.Navigate(loginURL),
		chromedp.ActionFunc(func(ctx context.Context) error {
			slog.Info("Browser opened, waiting for SSO login to complete...")
			for {
				select {
				case <-ctx.Done():
					return fmt.Errorf(
						"login timed out after %s — please complete SSO in the browser window that opened",
						loginTimeout,
					)
				default:
				}

				cookies, err := network.GetCookies().Do(ctx)
				if err != nil {
					return fmt.Errorf("failed to get cookies: %w", err)
				}

				for _, c := range cookies {
					if c.Name == grafanaSessionName && matchesDomain(c.Domain, domain) {
						sessionCookie = c.Value
						slog.Info("Session cookie captured", "domain", c.Domain)
						return nil
					}
				}

				time.Sleep(cookiePollInterval)
			}
		}),
	)
	if err != nil {
		return "", err
	}

	if sessionCookie == "" {
		return "", fmt.Errorf("no grafana_session cookie found after login")
	}

	return sessionCookie, nil
}

func chromeProfileDir() string {
	return filepath.Join(configDir(), "chrome-profile")
}

// matchesDomain checks if a cookie domain matches the target domain.
// Cookie domains may have a leading dot (e.g., ".jfrog.io").
func matchesDomain(cookieDomain, targetDomain string) bool {
	cookieDomain = strings.TrimPrefix(cookieDomain, ".")
	return strings.EqualFold(cookieDomain, targetDomain) ||
		strings.HasSuffix(targetDomain, "."+cookieDomain)
}
