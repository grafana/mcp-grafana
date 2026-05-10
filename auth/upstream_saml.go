package auth

import (
	"bytes"
	"compress/flate"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"log/slog"

	"github.com/crewjam/saml"
	"github.com/crewjam/saml/samlsp"
)

// SAMLUpstream implements the Upstream + SAMLValidator interfaces for
// Mode saml. Identity flows through the IdP-issued SAML assertion (POSTed
// to /saml/acs); credentials are then bootstrapped by the user pasting an
// SA token at /bootstrap (same flow as Mode C).
type SAMLUpstream struct {
	sp    samlsp.Middleware     // wraps a saml.ServiceProvider with cookie/session helpers
	rawSP *saml.ServiceProvider // direct access to AuthnRequest/Response APIs

	cfg samlConfig

	// Per-RelayState pendings tracking the SAML RequestID we issued, so the
	// ACS handler can pin the inbound assertion to the request we sent out.
	mu       sync.Mutex
	pendings map[string]*samlPending
}

type samlConfig struct {
	EntityID     string
	AcsURL       string
	SloURL       string
	NameIDFormat string
	AttrEmail    string
	AttrGroups   string
	AllowIdPInit bool
	ClockSkew    time.Duration
}

type samlPending struct {
	requestID string
	createdAt time.Time
}

// NewSAMLUpstream loads the IdP metadata, parses the SP cert/key, and
// constructs a ready-to-use SAMLUpstream.
func NewSAMLUpstream(ctx context.Context, cfg Config) (*SAMLUpstream, error) {
	publicURL, err := url.Parse(strings.TrimRight(cfg.PublicURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("public-url: %w", err)
	}

	keyPEM, err := os.ReadFile(cfg.SAMLSPKeyFile)
	if err != nil {
		return nil, fmt.Errorf("read sp key: %w", err)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("sp key file is not PEM")
	}
	key, err := parsePrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse sp key: %w", err)
	}

	certPEM, err := os.ReadFile(cfg.SAMLSPCertFile)
	if err != nil {
		return nil, fmt.Errorf("read sp cert: %w", err)
	}
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, fmt.Errorf("sp cert file is not PEM")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse sp cert: %w", err)
	}

	idpMetadata, err := loadIdPMetadata(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("load idp metadata: %w", err)
	}

	samlOpts := samlsp.Options{
		EntityID:          chooseEntityID(cfg, publicURL),
		URL:               *publicURL,
		Key:               key,
		Certificate:       cert,
		IDPMetadata:       idpMetadata,
		AllowIDPInitiated: cfg.SAMLAllowIdPInitiated,
		SignRequest:       true,
	}
	mw, err := samlsp.New(samlOpts)
	if err != nil {
		return nil, fmt.Errorf("samlsp.New: %w", err)
	}

	// The samlsp Middleware wraps a saml.ServiceProvider that has the
	// ACS URL, SP entity, signing key, etc. all wired up.
	sp := &mw.ServiceProvider
	sp.AcsURL = *mustJoin(publicURL, "/saml/acs")
	sp.SloURL = *mustJoin(publicURL, "/saml/sls")
	sp.EntityID = chooseEntityID(cfg, publicURL)
	if cfg.SAMLNameIDFormat != "" {
		sp.AuthnNameIDFormat = saml.NameIDFormat(cfg.SAMLNameIDFormat)
	}

	upstream := &SAMLUpstream{
		sp:    *mw,
		rawSP: sp,
		cfg: samlConfig{
			EntityID:     sp.EntityID,
			AcsURL:       sp.AcsURL.String(),
			SloURL:       sp.SloURL.String(),
			NameIDFormat: cfg.SAMLNameIDFormat,
			AttrEmail:    cfg.SAMLAttributeEmail,
			AttrGroups:   cfg.SAMLAttributeGroups,
			AllowIdPInit: cfg.SAMLAllowIdPInitiated,
			ClockSkew:    cfg.SAMLClockSkew,
		},
		pendings: make(map[string]*samlPending),
	}

	if cfg.SAMLEnableSLO {
		slog.Warn("SAML Single Logout enabled but inbound LogoutRequest signature validation is not implemented. Restrict /saml/sls via mTLS or IP allowlist as defense-in-depth.")
	}

	return upstream, nil
}

func chooseEntityID(cfg Config, publicURL *url.URL) string {
	if cfg.SAMLEntityID != "" {
		return cfg.SAMLEntityID
	}
	return publicURL.String() + "/saml/metadata"
}

// mustJoin joins a path segment onto base, preserving any path prefix on
// base. Panics on a parse error since ref is always a hardcoded literal.
func mustJoin(base *url.URL, ref string) *url.URL {
	joined, err := url.JoinPath(base.String(), ref)
	if err != nil {
		panic(err)
	}
	u, err := url.Parse(joined)
	if err != nil {
		panic(err)
	}
	return u
}

// parsePrivateKey accepts either PKCS#1 or PKCS#8-encoded RSA keys.
func parsePrivateKey(der []byte) (*rsa.PrivateKey, error) {
	if k, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return k, nil
	}
	if k, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		if rk, ok := k.(*rsa.PrivateKey); ok {
			return rk, nil
		}
		return nil, fmt.Errorf("unsupported PKCS#8 key type %T", k)
	}
	return nil, fmt.Errorf("unsupported private key format")
}

// loadIdPMetadata fetches the IdP metadata from URL or file.
func loadIdPMetadata(ctx context.Context, cfg Config) (*saml.EntityDescriptor, error) {
	if cfg.SAMLIdPMetadataFile != "" {
		b, err := os.ReadFile(cfg.SAMLIdPMetadataFile)
		if err != nil {
			return nil, err
		}
		return samlsp.ParseMetadata(b)
	}
	u, err := url.Parse(cfg.SAMLIdPMetadataURL)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	return samlsp.FetchMetadata(ctx, client, *u)
}

// --- Upstream interface ---

func (u *SAMLUpstream) Mode() Mode { return ModeSAML }

// AuthorizeURL constructs an HTTP-Redirect AuthnRequest URL with RelayState.
// The state is propagated as RelayState; when the IdP POSTs to /saml/acs,
// we use it to look up the pendingFlow that drove the original /authorize.
func (u *SAMLUpstream) AuthorizeURL(_redirectURI, state string) string {
	req, err := u.rawSP.MakeAuthenticationRequest(u.rawSP.GetSSOBindingLocation(saml.HTTPRedirectBinding), saml.HTTPRedirectBinding, saml.HTTPPostBinding)
	if err != nil {
		// MakeAuthenticationRequest only fails if the SP is misconfigured,
		// which we'd have caught at NewSAMLUpstream. Surface as a /authorize
		// 500 by returning an empty URL — the caller handles empty-string
		// as a build error.
		return ""
	}

	u.mu.Lock()
	u.pendings[state] = &samlPending{requestID: req.ID, createdAt: time.Now()}
	u.mu.Unlock()

	redirectURL, err := req.Redirect(state, u.rawSP)
	if err != nil {
		return ""
	}
	return redirectURL.String()
}

// HandleCallback is not used in SAML mode; SAML's ACS endpoint plays the
// /callback role. Return an explanatory error to make misuse loud.
func (u *SAMLUpstream) HandleCallback(_ context.Context, _ url.Values) (CallbackResult, error) {
	return CallbackResult{}, fmt.Errorf("saml: /callback is not used; SAML responses arrive at /saml/acs")
}

// Refresh is not supported: SAML assertions are one-shot, and Mode S follows
// the Mode C pattern of bootstrapped SA tokens (which don't rotate on a
// schedule).
func (u *SAMLUpstream) Refresh(_ context.Context, _ []byte) (CallbackResult, error) {
	return CallbackResult{}, ErrRefreshNotSupported
}

// --- SAMLValidator interface (Tasks 5-7 implement) ---

// MetadataXML returns the SP entity metadata as XML.
func (u *SAMLUpstream) MetadataXML() ([]byte, error) {
	md := u.rawSP.Metadata()
	return xml.MarshalIndent(md, "", "  ")
}

// ValidateAssertion validates a POSTed SAMLResponse, extracting identity
// and attributes. It pins the assertion to a pending request by RelayState.
func (u *SAMLUpstream) ValidateAssertion(r *http.Request) (samlAssertion, error) {
	if err := r.ParseForm(); err != nil {
		return samlAssertion{}, fmt.Errorf("parse acs form: %w", err)
	}

	// Pin to expected RequestID(s). We accept all currently-pending requests.
	u.mu.Lock()
	expectedIDs := make([]string, 0, len(u.pendings))
	for _, p := range u.pendings {
		expectedIDs = append(expectedIDs, p.requestID)
	}
	u.mu.Unlock()

	// ParseResponse reads the request form, validates signatures, audience,
	// conditions, and replay. Returns a *saml.Assertion.
	assertion, err := u.rawSP.ParseResponse(r, expectedIDs)
	if err != nil {
		return samlAssertion{}, fmt.Errorf("%w: %v", ErrSAMLInvalidAssertion, err)
	}

	relayState := r.PostFormValue("RelayState")

	// Consume the matched pending — if RelayState mapped to one.
	// IdP-initiated flows have no RelayState; allow them iff configured.
	if relayState != "" {
		u.mu.Lock()
		delete(u.pendings, relayState)
		u.mu.Unlock()
	} else if !u.cfg.AllowIdPInit {
		return samlAssertion{}, fmt.Errorf("%w: missing RelayState (IdP-initiated SSO disabled)", ErrSAMLInvalidAssertion)
	}

	nameID := ""
	if assertion.Subject != nil && assertion.Subject.NameID != nil {
		nameID = assertion.Subject.NameID.Value
	}
	if nameID == "" {
		return samlAssertion{}, fmt.Errorf("%w: assertion has no NameID", ErrSAMLInvalidAssertion)
	}

	attrs := map[string][]string{}
	for _, st := range assertion.AttributeStatements {
		for _, a := range st.Attributes {
			vs := make([]string, 0, len(a.Values))
			for _, v := range a.Values {
				vs = append(vs, v.Value)
			}
			attrs[a.Name] = vs
		}
	}

	return samlAssertion{
		Identity:   Identity{Mode: ModeSAML, ID: nameID},
		Attributes: attrs,
		RelayState: relayState,
	}, nil
}

// BuildLogoutResponseURL parses an inbound IdP LogoutRequest and returns
// the URL the user-agent should be redirected to (the IdP's SLO endpoint
// with our LogoutResponse).
//
// SECURITY: This implementation does NOT verify the IdP's XML digital
// signature on the inbound LogoutRequest. crewjam/saml v0.5.1 does not
// expose a public API for inbound LogoutRequest validation, and
// implementing signature verification correctly requires careful XML
// canonicalization. As a result, when --saml-enable-slo is set, an
// attacker who knows a user's NameID can forge a LogoutRequest to
// destroy that user's session. Mitigations:
//  1. SLO is opt-in via --saml-enable-slo (default false).
//  2. Operators enabling SLO should put /saml/sls behind defense-in-depth
//     (mTLS, IP allowlist, or similar) until proper signature validation
//     lands as a follow-up.
//
// See: https://docs.oasis-open.org/security/saml/v2.0/saml-core-2.0-os.pdf §3.7.3
func (u *SAMLUpstream) BuildLogoutResponseURL(r *http.Request) (Identity, string, error) {
	if err := r.ParseForm(); err != nil {
		return Identity{}, "", fmt.Errorf("parse slo form: %w", err)
	}

	// Support both GET (redirect binding) and POST (post binding) logout requests.
	raw := r.Form.Get("SAMLRequest")
	if raw == "" {
		return Identity{}, "", fmt.Errorf("saml: SLO request missing SAMLRequest")
	}

	// Decode: base64 → deflate → XML (redirect binding).
	// POST binding is plain base64 without deflate; try redirect first.
	decoded, err := decodeSAMLRequest(raw)
	if err != nil {
		return Identity{}, "", fmt.Errorf("saml: decode SLO SAMLRequest: %w", err)
	}

	var req saml.LogoutRequest
	if err := xml.Unmarshal(decoded, &req); err != nil {
		return Identity{}, "", fmt.Errorf("saml: parse LogoutRequest XML: %w", err)
	}

	nameID := ""
	if req.NameID != nil {
		nameID = req.NameID.Value
	}
	if nameID == "" {
		return Identity{}, "", fmt.Errorf("saml: logout request has no NameID")
	}

	// Build a LogoutResponse URL directing the user-agent back to the IdP.
	sloURL, err := u.rawSP.MakeRedirectLogoutResponse(req.ID, "")
	if err != nil {
		return Identity{}, "", fmt.Errorf("saml: build logout response: %w", err)
	}

	return Identity{Mode: ModeSAML, ID: nameID}, sloURL.String(), nil
}

// decodeSAMLRequest decodes a SAMLRequest parameter from either HTTP-Redirect
// binding (base64 + deflate) or HTTP-POST binding (plain base64).
func decodeSAMLRequest(encoded string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	// Try deflate decompress (redirect binding).
	r := flate.NewReader(bytes.NewReader(b))
	deflated, deflateErr := io.ReadAll(r)
	if deflateErr == nil {
		_ = r.Close()
		return deflated, nil
	}
	// Fall back to treating as raw XML (POST binding).
	return b, nil
}
