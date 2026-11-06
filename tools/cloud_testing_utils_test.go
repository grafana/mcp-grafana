//go:build cloud
// +build cloud

package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/cenkalti/backoff/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetryCloudTestOperationRetriesTransientErrors(t *testing.T) {
	attempts := 0
	result, err := retryCloudTestOperationWithBackOff(context.Background(), t, func() (string, error) {
		attempts++
		if attempts < cloudTestMaxAttempts {
			return "", fmt.Errorf("request failed with status 503")
		}
		return "success", nil
	}, &backoff.ZeroBackOff{})

	require.NoError(t, err)
	assert.Equal(t, "success", result)
	assert.Equal(t, cloudTestMaxAttempts, attempts)
}

func TestRetryCloudTestOperationDoesNotRetryPermanentErrors(t *testing.T) {
	expectedErr := errors.New("request failed with status 400")
	attempts := 0
	_, err := retryCloudTestOperationWithBackOff(context.Background(), t, func() (string, error) {
		attempts++
		return "", expectedErr
	}, &backoff.ZeroBackOff{})

	require.ErrorIs(t, err, expectedErr)
	assert.Equal(t, 1, attempts)
}

func TestRetryCloudTestOperationStopsAfterMaxAttempts(t *testing.T) {
	expectedErr := errors.New("request failed with status 503")
	attempts := 0
	_, err := retryCloudTestOperationWithBackOff(context.Background(), t, func() (string, error) {
		attempts++
		return "", expectedErr
	}, &backoff.ZeroBackOff{})

	require.ErrorIs(t, err, expectedErr)
	assert.Equal(t, cloudTestMaxAttempts, attempts)
}

func TestIsRetryableCloudTestError(t *testing.T) {
	testCases := map[string]struct {
		err       error
		retryable bool
	}{
		"status":                {err: errors.New("query returned status 522"), retryable: true},
		"status in parentheses": {err: errors.New("lookup failed (status 504)"), retryable: true},
		"status code":           {err: errors.New("unexpected status code from settings API: 522"), retryable: true},
		"HTTP status":           {err: errors.New("request failed: HTTP 503"), retryable: true},
		"EOF":                   {err: fmt.Errorf("reading response: %w", io.EOF), retryable: true},
		"network error":         {err: net.UnknownNetworkError("connection reset"), retryable: true},
		"cancelled context":     {err: context.Canceled, retryable: false},
		"non-server status":     {err: errors.New("request failed with status 400"), retryable: false},
		"nested server status":  {err: errors.New(`status 400: {"status":500}`), retryable: false},
		"nil":                   {err: nil, retryable: false},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, testCase.retryable, isRetryableCloudTestError(testCase.err))
		})
	}
}
