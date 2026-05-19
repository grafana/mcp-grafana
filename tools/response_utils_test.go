//go:build unit

package tools

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadResponseBody(t *testing.T) {
	t.Run("reads body within limit", func(t *testing.T) {
		body := strings.NewReader("hello world")
		data, err := readResponseBody(body, 100)
		require.NoError(t, err)
		assert.Equal(t, "hello world", string(data))
	})

	t.Run("reads body at exactly the limit", func(t *testing.T) {
		content := strings.Repeat("x", 50)
		body := strings.NewReader(content)
		data, err := readResponseBody(body, 50)
		require.NoError(t, err)
		assert.Equal(t, content, string(data))
	})

	t.Run("returns error when body exceeds limit by one byte", func(t *testing.T) {
		content := strings.Repeat("x", 51)
		body := strings.NewReader(content)
		_, err := readResponseBody(body, 50)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "response body exceeds maximum size of 50 bytes")
		assert.Contains(t, err.Error(), "try narrowing your query")
	})

	t.Run("propagates reader errors", func(t *testing.T) {
		body := &errReader{err: io.ErrUnexpectedEOF}
		_, err := readResponseBody(body, 100)
		require.Error(t, err)
		assert.ErrorIs(t, err, io.ErrUnexpectedEOF)
	})

	t.Run("handles empty body", func(t *testing.T) {
		body := strings.NewReader("")
		data, err := readResponseBody(body, 100)
		require.NoError(t, err)
		assert.Empty(t, data)
	})
}

type errReader struct {
	err error
}

func (r *errReader) Read([]byte) (int, error) {
	return 0, r.err
}
