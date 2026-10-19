# lokiguard

`lokiguard` decides whether a LogQL query is cheap enough to run, before it
runs. It exists because Loki does not enforce `max_query_bytes_read` on log
queries without a line filter, so a broad stream selector over a wide window
passes every server-side check while scanning terabytes, and LLM callers reach
for exactly that shape.

It is a library with no dependencies outside the standard library. It returns a
decision. The caller chooses what to do with it: reject the query, ask a user,
warn, or just record it.

```go
import "github.com/grafana/mcp-grafana/lokiguard"

policy := lokiguard.Policy{
    MaxBytes: 100 << 30,        // estimated bytes scanned, summed over selectors
    MaxRange: 24 * time.Hour,   // query window plus the largest range vector
}

// stats wraps your Loki client's GET /loki/api/v1/index/stats call for a bare
// stream selector. Pass nil to skip the byte budget.
stats := func(ctx context.Context, selector string, start, end time.Time) (int64, error) {
    return lokiClient.IndexStatsBytes(ctx, selector, start, end)
}

d := lokiguard.Assess(ctx, lokiguard.Query{LogQL: logql, Start: start, End: end}, policy, stats)
switch d.Verdict {
case lokiguard.Block:
    return errors.New(lokiguard.Guidance(d, policy, "use label discovery to scope the query first"))
case lokiguard.Unknown:
    // d.Cause is CauseUnparseable or CauseEstimateFailed (and d.Err the stats
    // error). Fail closed or open, depending on who is calling.
}
```

## What it checks

In order, stopping the stats round trips as soon as a cheaper check blocks:

1. **Selective selectors.** Every stream selector needs a positive matcher that
   actually narrows the stream set. `{}`, `{job=~".+"}`, and selectors with only
   negative matchers are blocked.
2. **Time range.** The query window plus the largest range-vector duration
   (`[30d]`) must stay within `Policy.MaxRange`. Instant queries are sized by
   their range vector alone.
3. **Byte budget.** The `StatsFunc` is asked about each distinct selector over
   the effective window, and the estimates are summed. Both sides of a binary
   expression are scanned, so both count.

`Unknown` is a separate verdict, not a form of `Allow`: the scanner found no
selector it understands (and the time-range cap, which needs no selector, did
not already block the query), or the estimate failed. Whether that is fatal depends
on the caller. For example, an autonomous agent can block while an interactive
user is warned.

## Compatibility

`testdata/cases.json` is the cross-language contract. The Go tests run it, and
other implementations of the guard (for example a TypeScript port) should run
the same file so their verdicts cannot drift.

## Versioning

`lokiguard` is its own Go module, released independently of the
`mcp-grafana` binary. Release it by pushing a tag with the module path prefix,
for example `lokiguard/v0.1.0`.
