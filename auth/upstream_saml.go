package auth

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

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

	return &SAMLUpstream{
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
	}, nil
}

func chooseEntityID(cfg Config, publicURL *url.URL) string {
	if cfg.SAMLEntityID != "" {
		return cfg.SAMLEntityID
	}
	return publicURL.String() + "/saml/metadata"
}

func mustJoin(base *url.URL, ref string) *url.URL {
	u, err := base.Parse(strings.TrimLeft(ref, "/"))
	if err != nil {
		// base is a parsed URL; ref is a hardcoded path — this can't fail.
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
	return samlsp.FetchMetadata(ctx, http.DefaultClient, *u)
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

// ValidateAssertion is implemented in Task 6.
func (u *SAMLUpstream) ValidateAssertion(r *http.Request) (samlAssertion, error) {
	return samlAssertion{}, fmt.Errorf("not yet implemented")
}

// BuildLogoutResponseURL is implemented in Task 7.
func (u *SAMLUpstream) BuildLogoutResponseURL(r *http.Request) (Identity, string, error) {
	return Identity{}, "", fmt.Errorf("not yet implemented")
}
