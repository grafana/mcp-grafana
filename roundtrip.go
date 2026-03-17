package mcpgrafana

import (
	"net/http"
	"net/url"
)

// NewAuthRoundTripper wraps an http.RoundTripper to add authorization headers
func NewAuthRoundTripper(rt http.RoundTripper, accessToken, idToken, apiKey string, basicAuth *url.Userinfo) *authRoundTripper {
	return &authRoundTripper{
		accessToken: accessToken,
		idToken:     idToken,
		apiKey:      apiKey,
		basicAuth:   basicAuth,
		underlying:  rt,
	}
}

type authRoundTripper struct {
	accessToken string
	idToken     string
	apiKey      string
	basicAuth   *url.Userinfo
	underlying  http.RoundTripper
}

func (rt *authRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if rt.accessToken != "" && rt.idToken != "" {
		req.Header.Set("X-Access-Token", rt.accessToken)
		req.Header.Set("X-Grafana-Id", rt.idToken)
	} else if rt.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+rt.apiKey)
	} else if rt.basicAuth != nil {
		password, _ := rt.basicAuth.Password()
		req.SetBasicAuth(rt.basicAuth.Username(), password)
	}

	resp, err := rt.underlying.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}
