package mcpgrafana

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// OAuth2Config holds OAuth2 provider configuration
type OAuth2Config struct {
	// Enable OAuth2 token validation
	Enabled bool
	// OAuth2 provider base URL (e.g., http://keycloak:8080/auth/realms/master)
	ProviderURL string
	// User info endpoint (e.g., /protocol/openid-connect/userinfo)
	UserInfoEndpoint string
	// JWKS endpoint for JWT validation (e.g., /protocol/openid-connect/certs)
	JWKSEndpoint string
	// Cache validated tokens for N seconds
	TokenCacheTTL int
}

// OAuth2UserInfo represents user data from OAuth2 provider
type OAuth2UserInfo struct {
	// Subject/user ID from OAuth2
	ID string `json:"sub"`
	// Username from LDAP (preferred_username in Keycloak)
	Username string `json:"preferred_username"`
	// Email address
	Email string `json:"email"`
	// Full name
	Name string `json:"name"`
	// LDAP groups or roles
	Groups []string `json:"groups"`
	// Additional roles
	Roles []string `json:"roles"`
	// Raw attributes from provider
	Attributes map[string]interface{} `json:"-"`
}

// OAuth2Client handles token validation and user info fetching
type OAuth2Client struct {
	config     OAuth2Config
	httpClient *http.Client
	tokenCache map[string]*cachedToken
	cacheMutex sync.RWMutex
}

type cachedToken struct {
	userInfo *OAuth2UserInfo
	expiry   time.Time
}

// NewOAuth2Client creates a new OAuth2 client
func NewOAuth2Client(config OAuth2Config, httpClient *http.Client) *OAuth2Client {
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 10 * time.Second,
		}
	}
	return &OAuth2Client{
		config:     config,
		httpClient: httpClient,
		tokenCache: make(map[string]*cachedToken),
	}
}

// ValidateToken validates an OAuth2 token and returns user information
func (c *OAuth2Client) ValidateToken(ctx context.Context, token string) (*OAuth2UserInfo, error) {
	if !c.config.Enabled {
		return nil, fmt.Errorf("OAuth2 validation disabled")
	}

	if token == "" {
		return nil, fmt.Errorf("empty token")
	}

	// Purge expired entries first to prevent unbounded cache growth.
	if c.config.TokenCacheTTL > 0 {
		now := time.Now()
		c.cacheMutex.Lock()
		c.evictExpiredTokensLocked(now)
		if cached, ok := c.tokenCache[token]; ok {
			c.cacheMutex.Unlock()
			return cached.userInfo, nil
		}
		c.cacheMutex.Unlock()
	}

	userInfo, err := c.fetchUserInfo(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("failed to validate token: %w", err)
	}

	// Cache the result
	if c.config.TokenCacheTTL > 0 {
		c.cacheMutex.Lock()
		c.tokenCache[token] = &cachedToken{
			userInfo: userInfo,
			expiry:   time.Now().Add(time.Duration(c.config.TokenCacheTTL) * time.Second),
		}
		c.cacheMutex.Unlock()
	}

	return userInfo, nil
}

func (c *OAuth2Client) evictExpiredTokensLocked(now time.Time) {
	for token, cached := range c.tokenCache {
		if !now.Before(cached.expiry) {
			delete(c.tokenCache, token)
		}
	}
}

// fetchUserInfo gets user information from userinfo endpoint
func (c *OAuth2Client) fetchUserInfo(ctx context.Context, token string) (*OAuth2UserInfo, error) {
	endpoint := c.buildURL(c.config.UserInfoEndpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("userinfo endpoint not configured")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("userinfo request failed: %d %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode userinfo response: %w", err)
	}

	return c.mapResponseToUserInfo(result), nil
}

// mapResponseToUserInfo converts OAuth2 response to OAuth2UserInfo
func (c *OAuth2Client) mapResponseToUserInfo(data map[string]interface{}) *OAuth2UserInfo {
	info := &OAuth2UserInfo{
		Attributes: data,
	}

	// Extract standard OIDC claims
	if sub, ok := data["sub"].(string); ok {
		info.ID = sub
	}
	if username, ok := data["preferred_username"].(string); ok {
		info.Username = username
	}
	if email, ok := data["email"].(string); ok {
		info.Email = email
	}
	if name, ok := data["name"].(string); ok {
		info.Name = name
	}

	// Extract groups (varies by provider)
	info.Groups = c.extractStringArray(data, "groups", "group", "member_of")
	info.Roles = c.extractStringArray(data, "roles", "role", "realm_access")

	return info
}

// extractStringArray tries to extract string arrays from various field names
func (c *OAuth2Client) extractStringArray(data map[string]interface{}, fieldNames ...string) []string {
	for _, fieldName := range fieldNames {
		if val, ok := data[fieldName]; ok {
			switch v := val.(type) {
			case []interface{}:
				result := make([]string, 0, len(v))
				for _, item := range v {
					if s, ok := item.(string); ok {
						result = append(result, s)
					}
				}
				if len(result) > 0 {
					return result
				}
			case []string:
				return v
			}
		}
	}
	return []string{}
}

// buildURL constructs full URL from provider and endpoint
func (c *OAuth2Client) buildURL(endpoint string) string {
	if c.config.ProviderURL == "" || endpoint == "" {
		return ""
	}

	base := strings.TrimSuffix(c.config.ProviderURL, "/")
	ep := strings.TrimPrefix(endpoint, "/")

	return base + "/" + ep
}

// ClearCache clears the token cache
func (c *OAuth2Client) ClearCache() {
	c.cacheMutex.Lock()
	defer c.cacheMutex.Unlock()
	c.tokenCache = make(map[string]*cachedToken)
}

// ContextKey type for storing values in context
type contextKeyOAuth2 string

const (
	oauth2UserInfoKey contextKeyOAuth2 = "oauth2_user_info"
	oauth2ClientKey   contextKeyOAuth2 = "oauth2_client"
)

// WithOAuth2UserInfo adds OAuth2 user info to context
func WithOAuth2UserInfo(ctx context.Context, userInfo *OAuth2UserInfo) context.Context {
	return context.WithValue(ctx, oauth2UserInfoKey, userInfo)
}

// OAuth2UserInfoFromContext retrieves OAuth2 user info from context
func OAuth2UserInfoFromContext(ctx context.Context) *OAuth2UserInfo {
	userInfo, ok := ctx.Value(oauth2UserInfoKey).(*OAuth2UserInfo)
	if !ok {
		return nil
	}
	return userInfo
}

// WithOAuth2Client adds OAuth2 client to context
func WithOAuth2Client(ctx context.Context, client *OAuth2Client) context.Context {
	return context.WithValue(ctx, oauth2ClientKey, client)
}

// OAuth2ClientFromContext retrieves OAuth2 client from context
func OAuth2ClientFromContext(ctx context.Context) *OAuth2Client {
	client, ok := ctx.Value(oauth2ClientKey).(*OAuth2Client)
	if !ok {
		return nil
	}
	return client
}
