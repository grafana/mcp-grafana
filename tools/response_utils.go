package tools

import (
	"errors"
	"fmt"
	"io"
)

const defaultResponseLimitBytes int64 = 10 * 1024 * 1024 // 10MB

var errResponseBodyTooLarge = errors.New("response body exceeds maximum size")

// readResponseBody reads up to limit bytes from r. If the response exceeds
// limit, it returns an error instead of silently truncating.
func readResponseBody(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w of %d bytes; try narrowing your query", errResponseBodyTooLarge, limit)
	}
	return data, nil
}
