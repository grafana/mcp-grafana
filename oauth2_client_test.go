package mcpgrafana

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOAuth2ClientValidateToken(t *testing.T) {
	// Mock OAuth2 userinfo endpoint
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/userinfo" {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer valid-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"sub":                  "user123",
				"preferred_username":   "john.doe",
				"email":                "john.doe@example.com",
				"name":                 "John Doe",
				"groups":               []string{"ldap-admins", "ldap-users"},
				"roles":                []string{"admin", "viewer"},
			})
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	config := OAuth2Config{
		Enabled:          true,
		ProviderURL:      server.URL,
		UserInfoEndpoint: "/userinfo",
		TokenCacheTTL:    60,
	}

	client := NewOAuth2Client(config, server.Client())

	// Test valid token
	userInfo, err := client.ValidateToken(context.Background(), "valid-token")
	if err != nil {
		t.Fatalf("ValidateToken failed: %v", err)
	}

	if userInfo.ID != "user123" {
		t.Errorf("Expected ID 'user123', got '%s'", userInfo.ID)
	}
	if userInfo.Username != "john.doe" {
		t.Errorf("Expected Username 'john.doe', got '%s'", userInfo.Username)
	}
	if userInfo.Email != "john.doe@example.com" {
		t.Errorf("Expected Email 'john.doe@example.com', got '%s'", userInfo.Email)
	}
	if len(userInfo.Groups) != 2 {
		t.Errorf("Expected 2 groups, got %d", len(userInfo.Groups))
	}

	// Test invalid token
	_, err = client.ValidateToken(context.Background(), "invalid-token")
	if err == nil {
		t.Error("Expected error for invalid token")
	}

	// Test empty token
	_, err = client.ValidateToken(context.Background(), "")
	if err == nil {
		t.Error("Expected error for empty token")
	}
}

func TestOAuth2ClientTokenCaching(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if r.URL.Path == "/userinfo" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"sub":                "user123",
				"preferred_username": "john.doe",
			})
		}
	}))
	defer server.Close()

	config := OAuth2Config{
		Enabled:          true,
		ProviderURL:      server.URL,
		UserInfoEndpoint: "/userinfo",
		TokenCacheTTL:    60,
	}

	client := NewOAuth2Client(config, server.Client())

	// First call
	_, err := client.ValidateToken(context.Background(), "token1")
	if err != nil {
		t.Fatalf("First call failed: %v", err)
	}

	initialCount := callCount

	// Second call with same token (should use cache)
	_, err = client.ValidateToken(context.Background(), "token1")
	if err != nil {
		t.Fatalf("Second call failed: %v", err)
	}

	if callCount > initialCount {
		t.Errorf("Expected token to be cached, but made additional request")
	}

	// Clear cache
	client.ClearCache()

	// Third call after cache clear
	_, err = client.ValidateToken(context.Background(), "token1")
	if err != nil {
		t.Fatalf("Third call failed: %v", err)
	}

	if callCount == initialCount {
		t.Error("Expected new request after cache clear")
	}
}

func TestOAuth2ClientContextFunctions(t *testing.T) {
	userInfo := &OAuth2UserInfo{
		ID:       "user123",
		Username: "john.doe",
		Email:    "john@example.com",
	}

	ctx := context.Background()
	ctx = WithOAuth2UserInfo(ctx, userInfo)

	retrieved := OAuth2UserInfoFromContext(ctx)
	if retrieved == nil {
		t.Fatal("Failed to retrieve user info from context")
	}

	if retrieved.ID != userInfo.ID {
		t.Errorf("Expected ID '%s', got '%s'", userInfo.ID, retrieved.ID)
	}

	// Test nil case
	ctx2 := context.Background()
	if OAuth2UserInfoFromContext(ctx2) != nil {
		t.Error("Expected nil for empty context")
	}
}

func TestOAuth2ClientDisabled(t *testing.T) {
	config := OAuth2Config{
		Enabled: false,
	}

	client := NewOAuth2Client(config, nil)

	_, err := client.ValidateToken(context.Background(), "token")
	if err == nil {
		t.Error("Expected error when OAuth2 is disabled")
	}
}

func TestOAuth2ClientMapResponseGroups(t *testing.T) {
	tests := []struct {
		name     string
		data     map[string]interface{}
		expected []string
	}{
		{
			name: "groups field",
			data: map[string]interface{}{
				"groups": []interface{}{"admin", "users"},
			},
			expected: []string{"admin", "users"},
		},
		{
			name: "member_of field",
			data: map[string]interface{}{
				"member_of": []interface{}{"cn=admins,dc=example,dc=com", "cn=users,dc=example,dc=com"},
			},
			expected: []string{"cn=admins,dc=example,dc=com", "cn=users,dc=example,dc=com"},
		},
		{
			name: "no groups",
			data: map[string]interface{}{
				"sub": "user123",
			},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewOAuth2Client(OAuth2Config{}, nil)
			userInfo := client.mapResponseToUserInfo(tt.data)

			if len(userInfo.Groups) != len(tt.expected) {
				t.Errorf("Expected %d groups, got %d", len(tt.expected), len(userInfo.Groups))
				return
			}

			for i, g := range userInfo.Groups {
				if g != tt.expected[i] {
					t.Errorf("Expected group '%s', got '%s'", tt.expected[i], g)
				}
			}
		})
	}
}

func TestOAuth2ClientExpiredCache(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if r.URL.Path == "/userinfo" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"sub":                "user123",
				"preferred_username": "john.doe",
			})
		}
	}))
	defer server.Close()

	config := OAuth2Config{
		Enabled:          true,
		ProviderURL:      server.URL,
		UserInfoEndpoint: "/userinfo",
		TokenCacheTTL:    1, // 1 second TTL
	}

	client := NewOAuth2Client(config, server.Client())

	// First call
	_, err := client.ValidateToken(context.Background(), "token1")
	if err != nil {
		t.Fatalf("First call failed: %v", err)
	}

	initialCount := callCount

	// Wait for cache to expire
	time.Sleep(1100 * time.Millisecond)

	// Second call after expiry
	_, err = client.ValidateToken(context.Background(), "token1")
	if err != nil {
		t.Fatalf("Second call failed: %v", err)
	}

	if callCount == initialCount {
		t.Error("Expected new request after cache expiry")
	}
}
