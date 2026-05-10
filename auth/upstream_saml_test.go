package auth

import (
	"bytes"
	"compress/flate"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crewjam/saml"
)

func TestNewSAMLUpstream_ConstructsServiceProvider(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSPKeyPair(t, dir)
	metadataPath := writeMockIdPMetadata(t, dir)

	cfg := Config{
		Mode:                ModeSAML,
		PublicURL:           "https://mcp.example.com",
		SAMLIdPMetadataFile: metadataPath,
		SAMLSPCertFile:      certPath,
		SAMLSPKeyFile:       keyPath,
		SAMLNameIDFormat:    "urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress",
		SAMLAttributeEmail:  "email",
		SAMLAttributeGroups: "groups",
		SAMLClockSkew:       60 * time.Second,
		SAMLClockSkewSet:    true,
	}
	up, err := NewSAMLUpstream(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if up.Mode() != ModeSAML {
		t.Errorf("Mode() = %q", up.Mode())
	}
	if up.cfg.AcsURL != "https://mcp.example.com/saml/acs" {
		t.Errorf("AcsURL = %q", up.cfg.AcsURL)
	}
	if up.cfg.EntityID != "https://mcp.example.com/saml/metadata" {
		t.Errorf("default EntityID = %q", up.cfg.EntityID)
	}
}

// TestNewSAMLUpstream_AppliesExplicitZeroClockSkew confirms that an
// operator who explicitly passes --saml-clock-skew=0s (which sets
// SAMLClockSkewSet=true and SAMLClockSkew=0 in main.go) gets strict zero
// tolerance, not the crewjam/saml library default of 180s.
func TestNewSAMLUpstream_AppliesExplicitZeroClockSkew(t *testing.T) {
	prev := saml.MaxClockSkew
	t.Cleanup(func() { saml.MaxClockSkew = prev; resetClockSkewForTest() })

	saml.MaxClockSkew = 999 * time.Second // sentinel different from 0 and lib default
	resetClockSkewForTest()

	dir := t.TempDir()
	certPath, keyPath := generateSPKeyPair(t, dir)
	metadataPath := writeMockIdPMetadata(t, dir)

	if _, err := NewSAMLUpstream(context.Background(), Config{
		Mode:                ModeSAML,
		PublicURL:           "https://mcp.example.com",
		SAMLIdPMetadataFile: metadataPath,
		SAMLSPCertFile:      certPath,
		SAMLSPKeyFile:       keyPath,
		SAMLNameIDFormat:    "urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress",
		SAMLClockSkew:       0,
		SAMLClockSkewSet:    true,
	}); err != nil {
		t.Fatal(err)
	}
	if saml.MaxClockSkew != 0 {
		t.Errorf("explicit 0s not applied: saml.MaxClockSkew = %v", saml.MaxClockSkew)
	}
}

// TestNewSAMLUpstream_UnsetClockSkewLeavesGlobalAlone verifies that a
// programmatic Config{} that doesn't touch SAMLClockSkew*  fields keeps
// saml.MaxClockSkew at whatever the library default is — protects against
// the previous footgun where a zero SAMLClockSkew silently imposed strict
// zero tolerance on every assertion.
func TestNewSAMLUpstream_UnsetClockSkewLeavesGlobalAlone(t *testing.T) {
	prev := saml.MaxClockSkew
	t.Cleanup(func() { saml.MaxClockSkew = prev; resetClockSkewForTest() })

	saml.MaxClockSkew = 42 * time.Second
	resetClockSkewForTest()

	dir := t.TempDir()
	certPath, keyPath := generateSPKeyPair(t, dir)
	metadataPath := writeMockIdPMetadata(t, dir)

	if _, err := NewSAMLUpstream(context.Background(), Config{
		Mode:                ModeSAML,
		PublicURL:           "https://mcp.example.com",
		SAMLIdPMetadataFile: metadataPath,
		SAMLSPCertFile:      certPath,
		SAMLSPKeyFile:       keyPath,
		SAMLNameIDFormat:    "urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress",
		// SAMLClockSkewSet intentionally false.
	}); err != nil {
		t.Fatal(err)
	}
	if saml.MaxClockSkew != 42*time.Second {
		t.Errorf("unset skew should not perturb global: saml.MaxClockSkew = %v", saml.MaxClockSkew)
	}
}

func TestSAMLUpstream_AuthorizeURL_ContainsSAMLRequest(t *testing.T) {
	up := mustNewSAMLUpstream(t)

	rawURL := up.AuthorizeURL("https://mcp.example.com/callback", "state-1")
	if rawURL == "" {
		t.Fatalf("AuthorizeURL returned empty string")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("SAMLRequest") == "" {
		t.Errorf("SAMLRequest query parameter missing: %s", rawURL)
	}
	if q.Get("RelayState") != "state-1" {
		t.Errorf("RelayState = %q", q.Get("RelayState"))
	}

	// The pending registry should now have an entry for state-1 with a
	// SAML RequestID. Peek (non-consuming) so the entry is still around if
	// later assertions in this test reach for it.
	p, ok := up.pendings.Peek("state-1")
	if !ok || p.requestID == "" {
		t.Errorf("expected pending RequestID stored for state-1: ok=%v p=%v", ok, p)
	}
}

func TestSAMLUpstream_HandleCallbackReturnsError(t *testing.T) {
	up := mustNewSAMLUpstream(t)
	if _, err := up.HandleCallback(context.Background(), nil); err == nil {
		t.Errorf("HandleCallback should return an error in SAML mode")
	}
}

func TestSAMLUpstream_RefreshNotSupported(t *testing.T) {
	up := mustNewSAMLUpstream(t)
	if _, err := up.Refresh(context.Background(), nil); err == nil {
		t.Errorf("Refresh should return ErrRefreshNotSupported in SAML mode")
	} else if !strings.Contains(err.Error(), "does not support") {
		t.Errorf("error doesn't mention refresh support: %v", err)
	}
}

// --- Helpers ---

func mustNewSAMLUpstream(t *testing.T) *SAMLUpstream {
	t.Helper()
	dir := t.TempDir()
	certPath, keyPath := generateSPKeyPair(t, dir)
	metadataPath := writeMockIdPMetadata(t, dir)

	up, err := NewSAMLUpstream(context.Background(), Config{
		Mode:                ModeSAML,
		PublicURL:           "https://mcp.example.com",
		SAMLIdPMetadataFile: metadataPath,
		SAMLSPCertFile:      certPath,
		SAMLSPKeyFile:       keyPath,
		SAMLNameIDFormat:    "urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress",
		// SAMLClockSkewSet left false: helper-built upstreams shouldn't
		// perturb saml.MaxClockSkew for other tests in the same process.
	})
	if err != nil {
		t.Fatal(err)
	}
	return up
}

// generateSPKeyPair creates a 2048-bit RSA key + self-signed X.509 cert
// for the SP, written as PEM files. Returns the cert path and key path.
func generateSPKeyPair(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "mcp-grafana-sp"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPath = filepath.Join(dir, "sp.crt")
	keyPath = filepath.Join(dir, "sp.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600); err != nil {
		t.Fatal(err)
	}
	return
}

// writeMockIdPMetadata creates a minimal SAML IdP metadata XML on disk.
// The IdP signing key here is the same as the SP's for simplicity — in
// tests that exercise actual assertions, real IdP keys come from the
// crewjam/saml test helpers.
func writeMockIdPMetadata(t *testing.T, dir string) string {
	t.Helper()
	// Produce a minimal EntityDescriptor with one SSO endpoint. crewjam's
	// ParseMetadata needs valid XML and a SingleSignOnService for redirect
	// binding.
	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<EntityDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata"
                  entityID="https://idp.example.com">
  <IDPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">
    <SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect"
                         Location="https://idp.example.com/sso"/>
    <SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST"
                         Location="https://idp.example.com/sso"/>
  </IDPSSODescriptor>
</EntityDescriptor>`
	path := filepath.Join(dir, "idp-metadata.xml")
	if err := os.WriteFile(path, []byte(xmlData), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDecodeSAMLRequest_RedirectBinding(t *testing.T) {
	// Construct a minimal LogoutRequest XML.
	rawXML := []byte(`<samlp:LogoutRequest xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ID="abc"/>`)

	// Apply the HTTP-Redirect binding encoding: deflate then base64.
	var buf bytes.Buffer
	fw, _ := flate.NewWriter(&buf, flate.DefaultCompression)
	_, _ = fw.Write(rawXML)
	_ = fw.Close()
	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())

	decoded, err := decodeSAMLRequest(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, rawXML) {
		t.Errorf("decoded=%q want=%q", decoded, rawXML)
	}
}

func TestDecodeSAMLRequest_PostBinding(t *testing.T) {
	rawXML := []byte(`<samlp:LogoutRequest xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ID="def"/>`)
	encoded := base64.StdEncoding.EncodeToString(rawXML)

	decoded, err := decodeSAMLRequest(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, rawXML) {
		t.Errorf("decoded=%q want=%q", decoded, rawXML)
	}
}

func TestDecodeSAMLRequest_BadInput(t *testing.T) {
	if _, err := decodeSAMLRequest("not-valid-base64!@#"); err == nil {
		t.Errorf("expected error on invalid base64")
	}
}

// Sweep / TTL behaviour is exercised at the registry level in
// pending_registry_test.go; SAMLUpstream just composes pendingRegistry.
