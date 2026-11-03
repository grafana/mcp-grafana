//go:build unit

package resources

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fakeKind(name string, list func(context.Context) ([]*mcp.Resource, error)) Kind {
	return Kind{
		template: &mcp.ResourceTemplate{URITemplate: "test://" + name + "/{id}", Name: name},
		read: func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return nil, nil
		},
		list: list,
	}
}

func listing(uris ...string) func(context.Context) ([]*mcp.Resource, error) {
	return func(context.Context) ([]*mcp.Resource, error) {
		var resources []*mcp.Resource
		for _, uri := range uris {
			resources = append(resources, &mcp.Resource{URI: uri, Name: uri})
		}
		return resources, nil
	}
}

// Every kind is listed, in registration order, except those that fail, time
// out or panic: those are left out without failing the list, and one that
// ignores its context doesn't hold the list up.
func TestListSkipsFailingKinds(t *testing.T) {
	old := listTimeout
	listTimeout = 50 * time.Millisecond
	t.Cleanup(func() { listTimeout = old })

	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	Register(s,
		fakeKind("a", listing("test://a/1", "test://a/2")),
		fakeKind("failing", func(context.Context) ([]*mcp.Resource, error) { return nil, errors.New("no IRM") }),
		fakeKind("slow", func(ctx context.Context) ([]*mcp.Resource, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}),
		fakeKind("panicking", func(context.Context) ([]*mcp.Resource, error) { panic("boom") }),
		fakeKind("ignoring", func(context.Context) ([]*mcp.Resource, error) {
			time.Sleep(time.Second)
			return listing("test://ignoring/1")(context.Background())
		}),
		fakeKind("b", listing("test://b/1")),
	)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go func() { _ = s.Run(context.Background(), serverTransport) }()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "0"}, nil).Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	start := time.Now()
	result, err := session.ListResources(t.Context(), nil)
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 500*time.Millisecond, "a kind ignoring its context held up the list")
	var uris []string
	for _, r := range result.Resources {
		uris = append(uris, r.URI)
	}
	assert.Equal(t, []string{"test://a/1", "test://a/2", "test://b/1"}, uris)
	assert.Equal(t, "private", result.CacheScope)
}
