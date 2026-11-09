//go:build cloud
// +build cloud

package tools

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v5"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

const (
	cloudTestMaxAttempts = 3
	cloudTestRetryDelay  = time.Second
)

// Cloud clients use several status-error formats, including "status code from ...: 522".
var cloudTestServerErrorPattern = regexp.MustCompile(
	`(?i)\b(?:status(?: code)?(?: from [^:\n]+)?:?\s*|HTTP\s+)5\d{2}\b`,
)

func retryCloudTestOperation[T any](
	ctx context.Context,
	t testing.TB,
	operation func() (T, error),
) (T, error) {
	t.Helper()
	return retryCloudTestOperationWithBackOff(
		ctx,
		t,
		operation,
		backoff.NewConstantBackOff(cloudTestRetryDelay),
	)
}

func retryCloudTestOperationWithBackOff[T any](
	ctx context.Context,
	t testing.TB,
	operation func() (T, error),
	retryBackOff backoff.BackOff,
) (T, error) {
	t.Helper()
	return backoff.Retry(ctx, func() (T, error) {
		result, err := operation()
		if err == nil || isRetryableCloudTestError(err) {
			return result, err
		}
		return result, backoff.Permanent(err)
	},
		backoff.WithBackOff(retryBackOff),
		backoff.WithMaxTries(cloudTestMaxAttempts),
		backoff.WithNotify(func(err error, delay time.Duration) {
			t.Logf("cloud test operation failed with a transient error; retrying in %s: %v", delay, err)
		}),
	)
}

func isRetryableCloudTestError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	var networkError net.Error
	return errors.As(err, &networkError) || cloudTestServerErrorPattern.MatchString(err.Error())
}

// createCloudTestContext creates a context with a Grafana URL, Grafana service account token and
// Grafana client for cloud integration tests.
// The test will be skipped if required environment variables are not set.
// testName is used to customize the skip message (e.g. "OnCall", "Incident")
// urlEnv and apiKeyEnv specify the environment variable names for the Grafana URL and API key (deprecated).
// The function will automatically try the new SERVICE_ACCOUNT_TOKEN pattern first, then fall back to API_KEY.
func createCloudTestContext(t *testing.T, testName, urlEnv, apiKeyEnv string) context.Context {
	ctx := context.Background()

	grafanaURL := os.Getenv(urlEnv)
	if grafanaURL == "" {
		t.Skipf("%s environment variable not set, skipping cloud %s integration tests", urlEnv, testName)
	}

	// Try the new service account token environment variable first
	serviceAccountTokenEnv := strings.Replace(apiKeyEnv, "API_KEY", "SERVICE_ACCOUNT_TOKEN", 1)
	grafanaApiKey := os.Getenv(serviceAccountTokenEnv)

	if grafanaApiKey == "" {
		// Fall back to the deprecated API key environment variable
		grafanaApiKey = os.Getenv(apiKeyEnv)
		if grafanaApiKey != "" {
			t.Logf("Warning: %s is deprecated, please use %s instead", apiKeyEnv, serviceAccountTokenEnv)
		}
	}

	if grafanaApiKey == "" {
		t.Skipf("Neither %s nor %s environment variables are set, skipping cloud %s integration tests", serviceAccountTokenEnv, apiKeyEnv, testName)
	}

	client := mcpgrafana.NewGrafanaClient(ctx, grafanaURL, grafanaApiKey, nil)

	config := mcpgrafana.GrafanaConfig{
		URL:    grafanaURL,
		APIKey: grafanaApiKey,
	}
	ctx = mcpgrafana.WithGrafanaConfig(ctx, config)
	ctx = mcpgrafana.WithGrafanaClient(ctx, client)

	return ctx
}
