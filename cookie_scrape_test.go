package mcpgrafana

import (
	"net/http"
	"testing"
	"time"

	"github.com/browserutils/kooky"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeBrowser satisfies kooky.BrowserInfo so tests can attribute cookies to a
// browser profile without opening a real cookie store.
type fakeBrowser struct {
	name      string
	profile   string
	isDefault bool
}

func (b fakeBrowser) Browser() string        { return b.name }
func (b fakeBrowser) Profile() string        { return b.profile }
func (b fakeBrowser) IsDefaultProfile() bool { return b.isDefault }
func (b fakeBrowser) FilePath() string       { return "" }

func cookie(name, value, domain string, browser kooky.BrowserInfo) *kooky.Cookie {
	return &kooky.Cookie{
		Cookie:  http.Cookie{Name: name, Value: value, Domain: domain},
		Browser: browser,
	}
}

func TestGroupSessionCookies(t *testing.T) {
	chrome := fakeBrowser{name: "chrome", isDefault: true}
	firefox := fakeBrowser{name: "firefox", isDefault: true}
	const host = "grafana.example.com"

	t.Run("pairs a session cookie with its expiry companion", func(t *testing.T) {
		got := groupSessionCookies(kooky.Cookies{
			cookie(grafanaSessionCookie, "abc", host, chrome),
			cookie(grafanaSessionExpiryCookie, "999", host, chrome),
		}, host)

		require.Len(t, got, 1)
		assert.Equal(t, "abc", got[0].value)
		assert.Equal(t, "999", got[0].expiry)
		assert.Equal(t, "chrome", got[0].source)
	})

	t.Run("keeps profiles separate", func(t *testing.T) {
		work := fakeBrowser{name: "chrome", profile: "Work"}
		got := groupSessionCookies(kooky.Cookies{
			cookie(grafanaSessionCookie, "personal", host, chrome),
			cookie(grafanaSessionCookie, "work", host, work),
			cookie(grafanaSessionExpiryCookie, "111", host, work),
		}, host)

		require.Len(t, got, 2)
		// Different profiles are independent sessions; pairing across them would
		// attach one profile's expiry to another profile's cookie.
		assert.Equal(t, "personal", got[0].value)
		assert.Empty(t, got[0].expiry)
		assert.Equal(t, "work", got[1].value)
		assert.Equal(t, "111", got[1].expiry)
		assert.Equal(t, "chrome (Work)", got[1].source)
	})

	t.Run("preserves discovery order across browsers", func(t *testing.T) {
		got := groupSessionCookies(kooky.Cookies{
			cookie(grafanaSessionCookie, "first", host, chrome),
			cookie(grafanaSessionCookie, "second", host, firefox),
		}, host)

		require.Len(t, got, 2)
		assert.Equal(t, "first", got[0].value)
		assert.Equal(t, "second", got[1].value)
	})

	t.Run("drops cookies for other domains", func(t *testing.T) {
		got := groupSessionCookies(kooky.Cookies{
			cookie(grafanaSessionCookie, "other", "grafana.elsewhere.com", chrome),
			cookie(grafanaSessionCookie, "mine", host, firefox),
		}, host)

		require.Len(t, got, 1)
		assert.Equal(t, "mine", got[0].value)
	})

	t.Run("accepts a parent-domain cookie", func(t *testing.T) {
		got := groupSessionCookies(kooky.Cookies{
			cookie(grafanaSessionCookie, "abc", ".example.com", chrome),
		}, host)

		require.Len(t, got, 1)
		assert.Equal(t, "abc", got[0].value)
	})

	t.Run("drops an expiry cookie with no session cookie", func(t *testing.T) {
		got := groupSessionCookies(kooky.Cookies{
			cookie(grafanaSessionExpiryCookie, "999", host, chrome),
		}, host)

		assert.Empty(t, got)
	})

	t.Run("carries the cookie's own expiry", func(t *testing.T) {
		expires := time.Now().Add(time.Hour).Truncate(time.Second)
		c := cookie(grafanaSessionCookie, "abc", host, chrome)
		c.Expires = expires

		got := groupSessionCookies(kooky.Cookies{c}, host)
		require.Len(t, got, 1)
		assert.Equal(t, expires.Unix(), got[0].expires)
	})

	t.Run("tolerates nil and empty entries", func(t *testing.T) {
		got := groupSessionCookies(kooky.Cookies{
			nil,
			cookie(grafanaSessionCookie, "", host, chrome),
			cookie(grafanaSessionCookie, "abc", host, firefox),
		}, host)

		require.Len(t, got, 1)
		assert.Equal(t, "abc", got[0].value)
	})

	t.Run("labels a cookie with no browser info", func(t *testing.T) {
		got := groupSessionCookies(kooky.Cookies{
			cookie(grafanaSessionCookie, "abc", host, nil),
		}, host)

		require.Len(t, got, 1)
		assert.Equal(t, "browser", got[0].source)
	})
}

func TestScrapeBrowserCookiesEmptyHost(t *testing.T) {
	// Guards against scanning every cookie store when the Grafana URL has no
	// parseable host.
	assert.Nil(t, scrapeBrowserCookies("", discardLogger()))
}

func TestGrafanaCookieHost(t *testing.T) {
	assert.Equal(t, "grafana.example.com", grafanaCookieHost("https://grafana.example.com"))
	assert.Equal(t, "localhost", grafanaCookieHost("http://localhost:3000"))
	assert.Equal(t, "", grafanaCookieHost("://bad"))
}
