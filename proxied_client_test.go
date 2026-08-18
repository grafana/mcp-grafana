package mcpgrafana

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithRetry(t *testing.T) {
	t.Run("succeeds on first attempt without retrying", func(t *testing.T) {
		calls := 0
		result, err := withRetry(context.Background(), retryPolicy{maxAttempts: 3, backoff: time.Millisecond}, "test",
			func(attemptNum int) (int, error) {
				calls++
				return 42, nil
			})

		require.NoError(t, err)
		assert.Equal(t, 42, result)
		assert.Equal(t, 1, calls)
	})

	t.Run("deterministic error is not retried", func(t *testing.T) {
		calls := 0
		wantErr := errors.New("deterministic boom")
		_, err := withRetry(context.Background(), retryPolicy{maxAttempts: 3, backoff: time.Millisecond}, "test",
			func(attemptNum int) (int, error) {
				calls++
				return 0, wantErr
			})

		require.Error(t, err)
		assert.Equal(t, 1, calls)
		assert.Same(t, wantErr, err)
		assert.False(t, isTransient(err))
	})

	t.Run("transient error is retried then succeeds", func(t *testing.T) {
		calls := 0
		result, err := withRetry(context.Background(), retryPolicy{maxAttempts: 2, backoff: time.Millisecond}, "test",
			func(attemptNum int) (int, error) {
				calls++
				if attemptNum == 1 {
					return 0, newTransientError(errors.New("flaky"))
				}
				return 7, nil
			})

		require.NoError(t, err)
		assert.Equal(t, 7, result)
		assert.Equal(t, 2, calls)
	})

	t.Run("exhausts retries on persistent transient error", func(t *testing.T) {
		calls := 0
		_, err := withRetry(context.Background(), retryPolicy{maxAttempts: 3, backoff: time.Millisecond}, "probe X",
			func(attemptNum int) (int, error) {
				calls++
				return 0, newTransientError(errors.New("always flaky"))
			})

		require.Error(t, err)
		assert.Equal(t, 3, calls)
		assert.True(t, isTransient(err))
		assert.Contains(t, err.Error(), "probe X")
		assert.Contains(t, err.Error(), "failed after 3 attempts")
		assert.Contains(t, err.Error(), "always flaky")
	})

	t.Run("context cancellation during backoff aborts early", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		_, err := withRetry(ctx, retryPolicy{maxAttempts: 3, backoff: time.Hour}, "test",
			func(attemptNum int) (int, error) {
				calls++
				cancel() // cancel immediately after the first attempt so the backoff wait aborts
				return 0, newTransientError(errors.New("flaky"))
			})

		require.Error(t, err)
		assert.Equal(t, 1, calls)
		assert.True(t, isTransient(err))
	})
}

func TestClassifyConnectError(t *testing.T) {
	t.Run("nil error stays nil", func(t *testing.T) {
		assert.NoError(t, classifyConnectError(nil))
	})

	t.Run("auth error is deterministic", func(t *testing.T) {
		err := fmt.Errorf("failed to initialize MCP client: %w", &transport.AuthorizationRequiredError{})
		got := classifyConnectError(err)
		assert.False(t, isTransient(got))
		assert.Same(t, err, got)
	})

	t.Run("oauth error is deterministic", func(t *testing.T) {
		err := fmt.Errorf("failed to initialize MCP client: %w", &transport.OAuthAuthorizationRequiredError{
			AuthorizationRequiredError: transport.AuthorizationRequiredError{},
		})
		got := classifyConnectError(err)
		assert.False(t, isTransient(got))
	})

	t.Run("generic error is transient", func(t *testing.T) {
		err := errors.New("connection reset")
		got := classifyConnectError(err)
		assert.True(t, isTransient(got))
	})
}

func TestIsTransient(t *testing.T) {
	t.Run("unwraps through fmt.Errorf wrapping", func(t *testing.T) {
		err := fmt.Errorf("outer: %w", newTransientError(errors.New("inner")))
		assert.True(t, isTransient(err))
	})

	t.Run("plain error is not transient", func(t *testing.T) {
		assert.False(t, isTransient(errors.New("plain")))
	})

	t.Run("nil is not transient", func(t *testing.T) {
		assert.False(t, isTransient(nil))
	})

	t.Run("error message does not leak wrapper type name", func(t *testing.T) {
		err := newTransientError(errors.New("inner message"))
		assert.Equal(t, "inner message", err.Error())
	})
}
