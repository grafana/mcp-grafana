package mcpgrafana

import (
	"net/url"
	"sync"
	"testing"

	"github.com/grafana/incident-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientCache_GrafanaClient(t *testing.T) {
	cache := NewClientCache(nil)
	defer cache.Close()

	key := clientCacheKey{url: "http://localhost:3000", apiKey: "test-key", orgID: 1}
	createCount := 0

	createFn := func() *GrafanaClient {
		createCount++
		return &GrafanaClient{}
	}

	// First call should create
	client1 := cache.GetOrCreateGrafanaClient(key, createFn)
	require.NotNil(t, client1)
	assert.Equal(t, 1, createCount)

	// Second call with same key should return cached
	client2 := cache.GetOrCreateGrafanaClient(key, createFn)
	assert.Same(t, client1, client2)
	assert.Equal(t, 1, createCount, "createFn should not be called again for same key")

	// Different key should create new client
	key2 := clientCacheKey{url: "http://other:3000", apiKey: "other-key", orgID: 2}
	client3 := cache.GetOrCreateGrafanaClient(key2, createFn)
	require.NotNil(t, client3)
	assert.NotSame(t, client1, client3)
	assert.Equal(t, 2, createCount)

	g, _ := cache.Size()
	assert.Equal(t, 2, g)
}

func TestClientCache_IncidentClient(t *testing.T) {
	cache := NewClientCache(nil)
	defer cache.Close()

	key := clientCacheKey{url: "http://localhost:3000", apiKey: "test-key", orgID: 1}
	createCount := 0

	createFn := func() *incident.Client {
		createCount++
		return incident.NewClient("http://localhost:3000/api/plugins/grafana-irm-app/resources/api/v1/", "test-key")
	}

	client1 := cache.GetOrCreateIncidentClient(key, createFn)
	require.NotNil(t, client1)
	assert.Equal(t, 1, createCount)

	client2 := cache.GetOrCreateIncidentClient(key, createFn)
	assert.Same(t, client1, client2)
	assert.Equal(t, 1, createCount)

	_, i := cache.Size()
	assert.Equal(t, 1, i)
}

func TestClientCache_ConcurrentAccess(t *testing.T) {
	cache := NewClientCache(nil)
	defer cache.Close()

	key := clientCacheKey{url: "http://localhost:3000", apiKey: "test-key", orgID: 1}

	var mu sync.Mutex
	createCount := 0

	createFn := func() *GrafanaClient {
		mu.Lock()
		createCount++
		mu.Unlock()
		return &GrafanaClient{}
	}

	const numGoroutines = 50
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	clients := make([]*GrafanaClient, numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			clients[idx] = cache.GetOrCreateGrafanaClient(key, createFn)
		}(i)
	}
	wg.Wait()

	// All goroutines should get the same client
	for i := 1; i < numGoroutines; i++ {
		assert.Same(t, clients[0], clients[i], "All goroutines should get the same cached client")
	}

	// createFn should be called exactly once
	assert.Equal(t, 1, createCount, "Client should be created exactly once")
}

func TestClientCache_DifferentCredentials(t *testing.T) {
	cache := NewClientCache(nil)
	defer cache.Close()

	keys := []clientCacheKey{
		{url: "http://host1:3000", apiKey: "key1", orgID: 1, forwardedHeaders: "X-WEBAUTH-USER=admin"},    // base key
		{url: "http://host1:3000", apiKey: "key2", orgID: 1, forwardedHeaders: "X-WEBAUTH-USER=admin"},    // different key
		{url: "http://host1:3000", apiKey: "key1", orgID: 2, forwardedHeaders: "X-WEBAUTH-USER=admin"},    // different org
		{url: "http://host2:3000", apiKey: "key1", orgID: 1, forwardedHeaders: "X-WEBAUTH-USER=admin"},    // different url
		{url: "http://host1:3000", apiKey: "key1", orgID: 1, forwardedHeaders: "X-WEBAUTH-USER=admin"},    // same as first
		{url: "http://host1:3000", apiKey: "key1", orgID: 1, forwardedHeaders: "X-WEBAUTH-USER=john.doe"}, // different user
	}

	const numUniqueKeys = 5

	clients := make([]*GrafanaClient, len(keys))
	for i, key := range keys {
		clients[i] = cache.GetOrCreateGrafanaClient(key, func() *GrafanaClient {
			return &GrafanaClient{}
		})
	}

	// First and last should be the same (same key)
	assert.Same(t, clients[0], clients[4])
	// All others should be different
	assert.NotSame(t, clients[0], clients[1])
	assert.NotSame(t, clients[0], clients[2])
	assert.NotSame(t, clients[0], clients[3])
	assert.NotSame(t, clients[0], clients[5])

	g, _ := cache.Size()
	assert.Equal(t, numUniqueKeys, g) // 5 unique keys
}

func TestCacheKeyFromRequest(t *testing.T) {
	key1 := cacheKeyFromRequest("http://localhost:3000", "key1", nil, 1, nil)
	key2 := cacheKeyFromRequest("http://localhost:3000", "key1", nil, 1, nil)
	assert.Equal(t, key1, key2)

	key3 := cacheKeyFromRequest("http://localhost:3000", "key1", url.UserPassword("admin", "pass"), 1, nil)
	assert.NotEqual(t, key1, key3)

	assert.Equal(t, "admin", key3.username)
	assert.Equal(t, "pass", key3.password)
}

func TestClientCache_Close(t *testing.T) {
	cache := NewClientCache(nil)

	key := clientCacheKey{url: "http://localhost:3000", apiKey: "key", orgID: 1}
	cache.GetOrCreateGrafanaClient(key, func() *GrafanaClient {
		return &GrafanaClient{}
	})
	cache.GetOrCreateIncidentClient(key, func() *incident.Client {
		return incident.NewClient("http://localhost:3000/incident", "key")
	})

	g, i := cache.Size()
	assert.Equal(t, 1, g)
	assert.Equal(t, 1, i)

	cache.Close()

	g, i = cache.Size()
	assert.Equal(t, 0, g)
	assert.Equal(t, 0, i)
}
