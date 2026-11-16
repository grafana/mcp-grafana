package mcpgrafana

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// rateLimitMaxRetries is how many times a rate-limited request is retried
	// after the first attempt.
	rateLimitMaxRetries = 2
	// rateLimitMaxWait is the longest single wait. A Retry-After beyond it
	// returns the response instead: retrying sooner would likely be refused
	// again, and waiting longer would stall the tool call.
	rateLimitMaxWait = 10 * time.Second
	// rateLimitFallbackWait is the first wait for a 429 without a usable
	// Retry-After header; it doubles on each retry.
	rateLimitFallbackWait = time.Second
)

// rateLimitRetryTransport retries requests that Grafana refused with 429 Too
// Many Requests, waiting as long as Retry-After asks. A 429 means the request
// wasn't processed, so retrying is safe for any method. A 503 Service
// Unavailable with Retry-After is retried only for idempotent methods: a
// proxy may return it after the upstream processed the request, and
// replaying a POST could create a duplicate. It gives up, returning the
// last response, when the wait would outlast the request's context deadline
// or the body can't be replayed.
type rateLimitRetryTransport struct {
	next http.RoundTripper
}

func (t *rateLimitRetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		resp, err := t.next.RoundTrip(req)
		if err != nil || attempt == rateLimitMaxRetries {
			return resp, err
		}
		wait, ok := rateLimitRetryDelay(resp, req.Method, attempt, time.Now())
		if !ok || !rateLimitCanReplay(req) {
			return resp, nil
		}
		if deadline, ok := req.Context().Deadline(); ok && time.Until(deadline) < wait {
			return resp, nil
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()

		timer := time.NewTimer(wait)
		select {
		case <-req.Context().Done():
			timer.Stop()
			return nil, req.Context().Err()
		case <-timer.C:
		}

		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			req = req.Clone(req.Context())
			req.Body = body
		}
	}
}

func rateLimitCanReplay(req *http.Request) bool {
	return req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
}

// rateLimitRetryDelay returns how long to wait before retrying resp, and
// false if it shouldn't be retried.
func rateLimitRetryDelay(resp *http.Response, method string, attempt int, now time.Time) (time.Duration, bool) {
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
	case http.StatusServiceUnavailable:
		if !rateLimitIdempotent(method) {
			return 0, false
		}
	default:
		return 0, false
	}
	wait, ok := parseRetryAfter(resp.Header.Get("Retry-After"), now)
	switch {
	case !ok && resp.StatusCode == http.StatusServiceUnavailable:
		// A 503 without Retry-After may be an outage, not a rate limit.
		return 0, false
	case !ok:
		wait = rateLimitFallbackWait << attempt
	}
	return wait, wait <= rateLimitMaxWait
}

func rateLimitIdempotent(method string) bool {
	switch method {
	case "", http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPut, http.MethodDelete:
		return true
	}
	return false
}

// parseRetryAfter parses a Retry-After value, which is either a number of
// seconds or an HTTP date (RFC 9110 section 10.2.3).
func parseRetryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.ParseInt(v, 10, 64); err == nil {
		if secs < 0 {
			return 0, false
		}
		// Clamped so a huge value can't overflow into a negative Duration.
		return time.Duration(min(secs, 1<<31)) * time.Second, true
	}
	at, err := http.ParseTime(v)
	if err != nil {
		return 0, false
	}
	return max(at.Sub(now), 0), true
}
