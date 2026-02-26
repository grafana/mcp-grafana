package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// assertsSubscriptionManager tracks entity assertion subscriptions and
// delivers notifications via MCP server-initiated events.
// Requires SSE or StreamableHTTP transport.
type assertsSubscriptionManager struct {
	mu            sync.Mutex
	mcpServer     *server.MCPServer
	subscriptions map[string]*assertionSubscription
	stopCh        chan struct{}
	running       bool
}

type assertionSubscription struct {
	ID            string                    `json:"id"`
	SessionID     string                    `json:"sessionId"`
	EntityType    string                    `json:"entityType"`
	EntityName    string                    `json:"entityName"`
	Env           string                    `json:"env,omitempty"`
	Site          string                    `json:"site,omitempty"`
	Namespace     string                    `json:"namespace,omitempty"`
	CreatedAt     time.Time                 `json:"createdAt"`
	LastCheck     time.Time                 `json:"lastCheck"`
	GrafanaConfig mcpgrafana.GrafanaConfig  `json:"-"`
}

var (
	globalSubManager     *assertsSubscriptionManager
	globalSubManagerOnce sync.Once
)

func getOrCreateSubManager(s *server.MCPServer) *assertsSubscriptionManager {
	globalSubManagerOnce.Do(func() {
		globalSubManager = &assertsSubscriptionManager{
			mcpServer:     s,
			subscriptions: make(map[string]*assertionSubscription),
			stopCh:        make(chan struct{}),
		}
	})
	return globalSubManager
}

func (m *assertsSubscriptionManager) subscribe(sub *assertionSubscription) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subscriptions[sub.ID] = sub
	if !m.running {
		m.running = true
		go m.pollLoop()
	}
}

func (m *assertsSubscriptionManager) unsubscribe(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, existed := m.subscriptions[id]
	delete(m.subscriptions, id)
	if len(m.subscriptions) == 0 && m.running {
		close(m.stopCh)
		m.running = false
		m.stopCh = make(chan struct{})
	}
	return existed
}

func (m *assertsSubscriptionManager) listSubscriptions() []*assertionSubscription {
	m.mu.Lock()
	defer m.mu.Unlock()
	subs := make([]*assertionSubscription, 0, len(m.subscriptions))
	for _, sub := range m.subscriptions {
		subs = append(subs, sub)
	}
	return subs
}

func (m *assertsSubscriptionManager) pollLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		m.mu.Lock()
		stopCh := m.stopCh
		m.mu.Unlock()

		select {
		case <-stopCh:
			return
		case <-ticker.C:
			m.checkAssertions()
		}
	}
}

func (m *assertsSubscriptionManager) checkAssertions() {
	m.mu.Lock()
	subs := make([]*assertionSubscription, 0, len(m.subscriptions))
	for _, sub := range m.subscriptions {
		subs = append(subs, sub)
	}
	m.mu.Unlock()

	for _, sub := range subs {
		now := time.Now()
		ctx := context.Background()

		ctx = mcpgrafana.WithGrafanaConfig(ctx, sub.GrafanaConfig)

		client, err := newAssertsClient(ctx)
		if err != nil {
			continue
		}

		entityScope := make(map[string]string)
		if sub.Env != "" {
			entityScope["env"] = sub.Env
		}
		if sub.Site != "" {
			entityScope["site"] = sub.Site
		}
		if sub.Namespace != "" {
			entityScope["namespace"] = sub.Namespace
		}

		reqBody := assertionsSummaryRequestDTO{
			StartTime: sub.LastCheck.UnixMilli(),
			EndTime:   now.UnixMilli(),
			EntityKeys: []entityKeyDTO{
				{
					Type:  sub.EntityType,
					Name:  sub.EntityName,
					Scope: entityScope,
				},
			},
		}

		data, err := client.fetchAssertsData(ctx, "/v1/assertions/summary", "POST", reqBody)
		if err != nil {
			continue
		}

		m.mu.Lock()
		if s, ok := m.subscriptions[sub.ID]; ok {
			s.LastCheck = now
		}
		m.mu.Unlock()

		notification := map[string]any{
			"subscriptionId": sub.ID,
			"entityType":     sub.EntityType,
			"entityName":     sub.EntityName,
			"checkTime":      now.Format(time.RFC3339),
			"summary":        json.RawMessage(data),
		}

		_ = m.mcpServer.SendNotificationToSpecificClient(
			sub.SessionID,
			"notifications/asserts/assertions",
			notification,
		)
	}
}

// --- subscribe_entity_assertions ---

type SubscribeEntityAssertionsParams struct {
	EntityType string `json:"entityType" jsonschema:"required,description=Entity type to watch"`
	EntityName string `json:"entityName" jsonschema:"required,description=Entity name to watch"`
	Env        string `json:"env,omitempty" jsonschema:"description=Environment"`
	Site       string `json:"site,omitempty" jsonschema:"description=Site"`
	Namespace  string `json:"namespace,omitempty" jsonschema:"description=Namespace"`
}

func subscribeEntityAssertions(ctx context.Context, args SubscribeEntityAssertionsParams) (string, error) {
	if globalSubManager == nil {
		return "", fmt.Errorf("streaming not initialized. Requires SSE or StreamableHTTP transport")
	}

	sessionID := ""
	if sid, ok := ctx.Value(sessionIDKey).(string); ok {
		sessionID = sid
	}

	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)

	subID := fmt.Sprintf("%s/%s/%s/%s/%s", args.EntityType, args.EntityName, args.Env, args.Site, args.Namespace)

	sub := &assertionSubscription{
		ID:            subID,
		SessionID:     sessionID,
		EntityType:    args.EntityType,
		EntityName:    args.EntityName,
		Env:           args.Env,
		Site:          args.Site,
		Namespace:     args.Namespace,
		CreatedAt:     time.Now(),
		LastCheck:     time.Now(),
		GrafanaConfig: cfg,
	}

	globalSubManager.subscribe(sub)

	result, err := json.Marshal(map[string]any{
		"subscriptionId": subID,
		"status":         "active",
		"pollInterval":   "30s",
		"note":           "Assertion changes will be delivered via MCP notifications (notifications/asserts/assertions). Requires SSE or StreamableHTTP transport.",
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal result: %w", err)
	}
	return string(result), nil
}

type contextKey string

const sessionIDKey contextKey = "mcp_session_id"

var SubscribeEntityAssertions = mcpgrafana.MustTool(
	"subscribe_entity_assertions",
	"Subscribe to assertion changes for a Knowledge Graph entity. Notifications are delivered via MCP server events every 30 seconds when assertions change. Requires SSE or StreamableHTTP transport.",
	subscribeEntityAssertions,
	mcp.WithTitleAnnotation("Subscribe to KG entity assertions"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// --- unsubscribe_entity_assertions ---

type UnsubscribeEntityAssertionsParams struct {
	SubscriptionID string `json:"subscriptionId" jsonschema:"required,description=Subscription ID returned by subscribe_entity_assertions"`
}

func unsubscribeEntityAssertions(_ context.Context, args UnsubscribeEntityAssertionsParams) (string, error) {
	if globalSubManager == nil {
		return "", fmt.Errorf("streaming not initialized")
	}

	existed := globalSubManager.unsubscribe(args.SubscriptionID)

	result, err := json.Marshal(map[string]any{
		"subscriptionId": args.SubscriptionID,
		"status":         "removed",
		"existed":        existed,
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal result: %w", err)
	}
	return string(result), nil
}

var UnsubscribeEntityAssertions = mcpgrafana.MustTool(
	"unsubscribe_entity_assertions",
	"Unsubscribe from assertion change notifications for a Knowledge Graph entity.",
	unsubscribeEntityAssertions,
	mcp.WithTitleAnnotation("Unsubscribe from KG entity assertions"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// --- list_assertion_subscriptions ---

type ListAssertionSubscriptionsParams struct{}

func listAssertionSubscriptions(_ context.Context, _ ListAssertionSubscriptionsParams) (string, error) {
	if globalSubManager == nil {
		return "[]", nil
	}

	subs := globalSubManager.listSubscriptions()
	result, err := json.Marshal(subs)
	if err != nil {
		return "", fmt.Errorf("failed to marshal subscriptions: %w", err)
	}
	return string(result), nil
}

var ListAssertionSubscriptions = mcpgrafana.MustTool(
	"list_assertion_subscriptions",
	"List all active assertion subscriptions for Knowledge Graph entities.",
	listAssertionSubscriptions,
	mcp.WithTitleAnnotation("List assertion subscriptions"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// InitAssertsStreaming initializes the streaming subscription manager.
// Call this from AddAssertsTools when the MCP server supports SSE or StreamableHTTP.
func InitAssertsStreaming(s *server.MCPServer) {
	getOrCreateSubManager(s)
}

// AddAssertsStreamingTools registers the streaming tools.
func AddAssertsStreamingTools(s *server.MCPServer) {
	InitAssertsStreaming(s)
	SubscribeEntityAssertions.Register(s)
	UnsubscribeEntityAssertions.Register(s)
	ListAssertionSubscriptions.Register(s)
}
