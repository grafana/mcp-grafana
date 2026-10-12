package tools

import (
	"fmt"
	"io"
	"net/http"
)

// prometheusResponseLimitTransport bounds responses before the Prometheus client
// buffers and decodes them. The underlying HTTP transport decompresses gzip, so
// the limit also applies to the uncompressed response size.
type prometheusResponseLimitTransport struct {
	underlying http.RoundTripper
}

func (t *prometheusResponseLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.underlying.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	resp.Body = &prometheusLimitedResponseBody{
		ReadCloser: resp.Body,
		reader:     io.LimitedReader{R: resp.Body, N: defaultResponseLimitBytes + 1},
	}
	return resp, nil
}

type prometheusLimitedResponseBody struct {
	io.ReadCloser
	reader io.LimitedReader
}

func (b *prometheusLimitedResponseBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	if b.reader.N == 0 {
		return n, fmt.Errorf("response body exceeds maximum size of %d bytes; try narrowing your query", defaultResponseLimitBytes)
	}
	return n, err
}
