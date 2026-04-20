package mcpgrafana

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBasicAuthHeaderEncoding verifies that basicAuthHeader produces the
// same bytes as (*http.Request).SetBasicAuth — base64(username ":" password)
// with raw bytes per RFC 7617. Regression test for the bug where
// url.Userinfo.String() was used, which percent-escapes reserved characters
// per RFC 3986 and breaks Basic Auth for passwords containing ':', '@',
// '/', '%', or space.
func TestBasicAuthHeaderEncoding(t *testing.T) {
	cases := []struct {
		name, username, password, want string
	}{
		{
			name:     "plain ASCII",
			username: "admin",
			password: "password",
			want:     "Basic YWRtaW46cGFzc3dvcmQ=",
		},
		{
			name:     "colon in password",
			username: "admin",
			password: "p:w0rd",
			want:     "Basic YWRtaW46cDp3MHJk",
		},
		{
			name:     "at sign in password",
			username: "admin",
			password: "p@ssw0rd",
			want:     "Basic YWRtaW46cEBzc3cwcmQ=",
		},
		{
			name:     "percent in password",
			username: "admin",
			password: "p%w0rd",
			want:     "Basic YWRtaW46cCV3MHJk",
		},
		{
			name:     "space in password",
			username: "admin",
			password: "pw 0rd",
			want:     "Basic YWRtaW46cHcgMHJk",
		},
		{
			name:     "slash in password",
			username: "admin",
			password: "p/w0rd",
			want:     "Basic YWRtaW46cC93MHJk",
		},
		{
			name:     "empty password",
			username: "admin",
			password: "",
			want:     "Basic YWRtaW46",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := url.UserPassword(tc.username, tc.password)
			got := basicAuthHeader(u)
			assert.Equal(t, tc.want, got,
				"basicAuthHeader must produce raw base64(user:password) per RFC 7617, not percent-encoded userinfo")
		})
	}
}
