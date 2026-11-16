package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

const (
	promCardinalityTimeout       = 2 * time.Second
	promCardinalityDefaultTTL    = 10 * time.Minute
	promCardinalityFailureTTL    = 30 * time.Second
	promCardinalityMaxEntries    = 256
	promCardinalityResponseLimit = 1 << 20
)

var promCardinalityDefaultScope = []string{"job", "instance", "namespace", "pod", "service", "cluster"}

type promCardinalityFailOpenCause string

const (
	promCardinalityNotInTopN            promCardinalityFailOpenCause = "not_in_top_n"
	promCardinalityUnresolvedMetricName promCardinalityFailOpenCause = "unresolved_metric_name"
	promCardinalityMetadataUnavailable  promCardinalityFailOpenCause = "metadata_unavailable"
	promCardinalityFetchSlotsSaturated  promCardinalityFailOpenCause = "fetch_slots_saturated"
)

var errPromCardinalityFetchSlotsSaturated = errors.New("TSDB metadata concurrency limit")

func cardinalityUnknown(cause promCardinalityFailOpenCause) promGuardDecision {
	return promGuardDecision{Verdict: promGuardUnknown, UnknownReason: promGuardReasonCardinalityUnknown, CardinalityFailOpenCause: cause}
}

// Only exact metric names are assessed in this first policy. Regex/name-less
// selectors and metrics missing from top-N remain unknown, never evidence of
// low cardinality. Scope matchers are a policy exemption, not a cost estimate.
func assessPromCardinality(ctx context.Context, expr, queryType string, start, end time.Time, step int, backend func() (*prometheusBackend, error)) promGuardDecision {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	var window time.Duration
	if queryType == "range" && end.After(start) {
		window = end.Sub(start)
	}
	ast, err := promGuardParser.ParseExpr(promGuardSubstituteMacros(expr, window, step))
	if err != nil {
		return cardinalityUnknown(promCardinalityUnresolvedMetricName)
	}
	scope := cfg.PrometheusGuardrailCardinalityScopeLabels
	if len(scope) == 0 {
		scope = promCardinalityDefaultScope
	}
	names := map[string]struct{}{}
	var cause promCardinalityFailOpenCause
	parser.Inspect(ast, func(n parser.Node, _ []parser.Node) error {
		vs, ok := n.(*parser.VectorSelector)
		if !ok {
			return nil
		}
		name := ""
		for _, m := range vs.LabelMatchers {
			if m.Type == labels.MatchNotRegexp && classifyPromRegex(m.Value) == promRegexAll {
				return nil
			}
			if m.Type != labels.MatchEqual || m.Value == "" {
				continue
			}
			if m.Name == model.MetricNameLabel {
				if name == "" {
					name = m.Value
				}
				continue
			}
			for _, label := range scope {
				if label == m.Name {
					return nil
				}
			}
		}
		if name == "" {
			cause = promCardinalityUnresolvedMetricName
			return nil
		}
		// An exact name excluded by any other name matcher selects no series.
		for _, m := range vs.LabelMatchers {
			if m.Name == model.MetricNameLabel && !m.Matches(name) {
				return nil
			}
		}
		names[name] = struct{}{}
		return nil
	})
	if len(names) == 0 {
		if cause != "" {
			return cardinalityUnknown(cause)
		}
		return promGuardDecision{}
	}
	b, err := backend()
	if err != nil {
		return cardinalityUnknown(promCardinalityMetadataUnavailable)
	}
	stats, err := cachedPromCardinality(ctx, b, cfg)
	if err != nil {
		if errors.Is(err, errPromCardinalityFetchSlotsSaturated) {
			return cardinalityUnknown(promCardinalityFetchSlotsSaturated)
		}
		return cardinalityUnknown(promCardinalityMetadataUnavailable)
	}
	var high uint64
	for name := range names {
		count, ok := stats[name]
		if !ok {
			// Unresolved names take precedence over missing top-N entries,
			// independent of selector order and map iteration order.
			if cause == "" {
				cause = promCardinalityNotInTopN
			}
			continue
		}
		if count > uint64(cfg.PrometheusGuardrailMaxMetricCardinality) {
			high = max(high, count)
		}
	}
	if high > 0 {
		return promGuardDecision{Verdict: promGuardBlock, Violations: []promGuardViolation{{
			Reason:  promGuardReasonCardinality,
			Message: fmt.Sprintf("a metric in an unscoped selector has %d series in the TSDB head statistics, exceeding the configured metric threshold (%d); this is not the selector's matched series count", high, cfg.PrometheusGuardrailMaxMetricCardinality),
			Hint:    fmt.Sprintf("add a non-empty exact matcher on one of [%s] to each high-cardinality selector, using a value appropriate to the intended scope, or use a lower-cardinality recording rule; scope labels are a heuristic and do not guarantee a cheap query", strings.Join(scope, ", ")),
		}}}
	}
	if cause != "" {
		return cardinalityUnknown(cause)
	}
	return promGuardDecision{}
}

type promCardinalityEntry struct {
	ready   chan struct{}
	expires time.Time
	stats   map[string]uint64
	err     error
}

type promCardinalityCache struct {
	mu      sync.Mutex
	entries map[[32]byte]*promCardinalityEntry
	// Bound total metadata work, even across different tenants/cache misses.
	slots chan struct{}
}

var promTSDBCache = newPromCardinalityCache()

func newPromCardinalityCache() *promCardinalityCache {
	return &promCardinalityCache{entries: make(map[[32]byte]*promCardinalityEntry), slots: make(chan struct{}, 8)}
}

// A structured, full-length digest retains no plaintext credentials in cache
// keys. Transport-injected identities cannot be inferred, so custom transports
// do not share cached results. Auth is still enforced by the real query path.
func promCardinalityKey(b *prometheusBackend, cfg mcpgrafana.GrafanaConfig) [32]byte {
	basic := ""
	if cfg.BasicAuth != nil {
		basic = cfg.BasicAuth.String()
	}
	payload, _ := json.Marshal(struct {
		URL, Override, UID, Path, Fallback, APIKey, Basic, Access, ID, Proxy string
		Org                                                                  int64
		Headers                                                              map[string]string
		TLS                                                                  *mcpgrafana.TLSConfig
		TTL                                                                  time.Duration
	}{b.baseURL, cfg.OverrideURL, b.dsUID, b.tsdbBase, b.tsdbFallbackBase, cfg.APIKey, basic, cfg.AccessToken, cfg.IDToken, cfg.SOCKS5ProxyURL, cfg.OrgID, cfg.ExtraHeaders, cfg.TLSConfig, cfg.PrometheusGuardrailCardinalityCacheTTL})
	return sha256.Sum256(payload)
}

func cachedPromCardinality(ctx context.Context, b *prometheusBackend, cfg mcpgrafana.GrafanaConfig) (map[string]uint64, error) {
	ttl := cfg.PrometheusGuardrailCardinalityCacheTTL
	if ttl <= 0 {
		ttl = promCardinalityDefaultTTL
	}
	// Server certificate verification settings do not change caller identity.
	// Client certificates can rotate in place, so their file paths alone are
	// not a safe identity for sharing cached results.
	clientCertificate := cfg.TLSConfig != nil && (cfg.TLSConfig.CertFile != "" || cfg.TLSConfig.KeyFile != "")
	return promTSDBCache.get(ctx, promCardinalityKey(b, cfg), ttl, cfg.BaseTransport == nil && !clientCertificate, func(ctx context.Context) (map[string]uint64, error) { return b.fetchCardinality(ctx) })
}

func (c *promCardinalityCache) get(ctx context.Context, key [32]byte, ttl time.Duration, cacheable bool, fetch func(context.Context) (map[string]uint64, error)) (map[string]uint64, error) {
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, promCardinalityTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if e := c.entries[key]; cacheable && e != nil && (e.expires.IsZero() || time.Now().Before(e.expires)) {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-e.ready:
			return e.stats, e.err
		}
	}
	// Local saturation is transient. Reserve a slot before changing the cache
	// so the next request can retry as soon as capacity is available.
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		c.mu.Unlock()
		return nil, errPromCardinalityFetchSlotsSaturated
	}
	var entry *promCardinalityEntry
	if cacheable {
		// Evict the earliest completed entry. Never evict in-flight entries: that
		// would allow duplicate refreshes for the same identity.
		if len(c.entries) >= promCardinalityMaxEntries {
			var oldest [32]byte
			var expiry time.Time
			for k, e := range c.entries {
				if !e.expires.IsZero() && (expiry.IsZero() || e.expires.Before(expiry)) {
					oldest = k
					expiry = e.expires
				}
			}
			if expiry.IsZero() {
				c.mu.Unlock()
				return nil, errors.New("TSDB cache busy")
			}
			delete(c.entries, oldest)
		}
		entry = &promCardinalityEntry{ready: make(chan struct{})}
		c.entries[key] = entry
	}
	c.mu.Unlock()
	stats, err := fetch(ctx)
	if entry != nil {
		c.mu.Lock()
		entry.stats, entry.err = stats, err
		if err != nil {
			ttl = min(ttl, promCardinalityFailureTTL)
		}
		entry.expires = time.Now().Add(ttl)
		close(entry.ready)
		if parent.Err() != nil {
			delete(c.entries, key)
		}
		c.mu.Unlock()
	}
	return stats, err
}

func (b *prometheusBackend) fetchCardinality(ctx context.Context) (map[string]uint64, error) {
	// The plugin resource API need not expose status/tsdb. Use the Grafana
	// datasource proxy, with the same auth/redirect policy as /api/ds/query.
	if b.tsdbBase == "" {
		return nil, errors.New("TSDB proxy unavailable")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.baseURL+b.tsdbBase+"/api/v1/status/tsdb?limit=10", nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound && b.tsdbFallbackBase != "" {
		resp.Body.Close() //nolint:errcheck
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, b.baseURL+b.tsdbFallbackBase+"/api/v1/status/tsdb?limit=10", nil)
		if err != nil {
			return nil, err
		}
		resp, err = b.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("TSDB metadata unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, promCardinalityResponseLimit+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > promCardinalityResponseLimit {
		return nil, errors.New("TSDB metadata too large")
	}
	var envelope struct {
		Status string `json:"status"`
		Data   struct {
			Series []struct {
				Name  string  `json:"name"`
				Value *uint64 `json:"value"`
			} `json:"seriesCountByMetricName"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	if envelope.Status != "success" || envelope.Data.Series == nil || len(envelope.Data.Series) > 10 {
		return nil, errors.New("invalid TSDB metadata")
	}
	stats := make(map[string]uint64, len(envelope.Data.Series))
	for _, s := range envelope.Data.Series {
		if s.Name == "" || s.Value == nil {
			return nil, errors.New("invalid TSDB metric statistic")
		}
		if _, ok := stats[s.Name]; ok {
			return nil, errors.New("duplicate TSDB metric statistic")
		}
		stats[s.Name] = *s.Value
	}
	return stats, nil
}
