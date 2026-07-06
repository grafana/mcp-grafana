package tools

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// The functions in this file translate the Grafana Professional Services
// Loki label strategy into deterministic, machine-readable tools:
//
//   - analyze_loki_labels  (live + static; optionally diagnoses query perf)
//
// The scoring rules live in this file so the verdicts are easy to audit
// and adjust without touching transport code. The Alloy config-generation
// tool (suggest_loki_alloy_label_config) is a separate toolset; see
// tools/config.go.

// ---------------------------------------------------------------------------
// Cardinality bands
// ---------------------------------------------------------------------------

const (
	bandLow       = "low"       // < 10 unique values
	bandMedium    = "medium"    // 10 .. 99
	bandHigh      = "high"      // 100 .. 999
	bandVeryHigh  = "very_high" // 1000 .. 9999
	bandExtreme   = "extreme"   // >= 10000
	bandUnknown   = "unknown"   // caller didn't supply a count
	bandUnbounded = "unbounded" // caller asserted the label is unbounded
)

// classifyCardinality maps a unique-value count to a band string. Counts
// below zero are normalized to "unknown" so callers don't have to pre-clean
// their input.
func classifyCardinality(count int) string {
	switch {
	case count < 0:
		return bandUnknown
	case count < 10:
		return bandLow
	case count < 100:
		return bandMedium
	case count < 1000:
		return bandHigh
	case count < 10000:
		return bandVeryHigh
	default:
		return bandExtreme
	}
}

// ---------------------------------------------------------------------------
// Known-label catalogues
//
// These come straight from the skill. They are intentionally small — we
// recognise the labels everyone always asks about and let everything else
// be judged on cardinality + access pattern.
// ---------------------------------------------------------------------------

// recommendedBaseLabels lists labels that nearly every log source benefits
// from. Missing-base-label warnings reference this set.
var recommendedBaseLabels = []string{
	"app", "service", "env", "cluster", "region", "level",
	"job", "team", "source", "classification",
}

// alwaysRemoveLabels are unbounded or transient by nature and should never
// be Loki index labels regardless of cardinality measured in a sample.
var alwaysRemoveLabels = map[string]string{
	"user_id":     "Unbounded per-user identifier — move to structured metadata.",
	"request_id":  "Unbounded per-request identifier — move to structured metadata.",
	"trace_id":    "Unbounded per-trace identifier — keep in structured metadata only.",
	"span_id":     "Unbounded per-span identifier — keep in structured metadata only.",
	"session_id":  "Unbounded per-session identifier — move to structured metadata.",
	"transaction": "Looks like a per-transaction identifier — move to structured metadata.",
	"uuid":        "UUID-shaped value — move to structured metadata.",
}

// preferMetadataLabels are *usually* better as structured metadata even
// when cardinality looks tame in a small window (values churn over time).
var preferMetadataLabels = map[string]string{
	"pod":          "Transient in Kubernetes — typical 5x stream reduction when removed.",
	"node":         "Transient and rarely used in selectors — move to structured metadata.",
	"container_id": "Transient identifier — move to structured metadata.",
	"instance":     "Often high cardinality; evaluate whether queries actually filter on it.",
	"version":      "Churns on every release — move to structured metadata.",
	"image":        "Churns on every release — move to structured metadata.",
	"tag":          "Churns on every release — move to structured metadata.",
	"process_id":   "Transient identifier — move to structured metadata.",
	"filename":     "Highly variable in Kubernetes — normalise or move out of labels.",
}

// goodStaticLabels are the canonical low-cardinality labels: keeping them
// is essentially free and almost always helps queries.
var goodStaticLabels = map[string]bool{
	"app":            true,
	"service":        true,
	"env":            true,
	"environment":    true,
	"cluster":        true,
	"region":         true,
	"level":          true,
	"job":            true,
	"team":           true,
	"squad":          true,
	"source":         true,
	"classification": true,
	"namespace":      true,
	"workload":       true,
	"container":      true,
	"platform":       true,
}

// ---------------------------------------------------------------------------
// get_loki_label_cardinality
// ---------------------------------------------------------------------------

// LokiLabelCardinality is the per-label measurement returned by
// get_loki_label_cardinality. SampleValues is capped to keep responses small.
type LokiLabelCardinality struct {
	Label        string   `json:"label"`
	UniqueValues int      `json:"uniqueValues"`
	Band         string   `json:"band"`
	SampleValues []string `json:"sampleValues,omitempty"`
	Truncated    bool     `json:"sampleValuesTruncated,omitempty"`
}

// GetLokiLabelCardinalityParams is the request shape for the tool.
type GetLokiLabelCardinalityParams struct {
	DatasourceUID   string   `json:"datasourceUid" jsonschema:"required,description=Loki/VictoriaLogs datasource UID."`
	LabelNames      []string `json:"labelNames,omitempty" jsonschema:"description=Labels to measure. Omit to auto-discover."`
	MaxLabels       int      `json:"maxLabels,omitempty" jsonschema:"description=Cap on auto-discovered labels (default 50)."`
	MaxSampleValues int      `json:"maxSampleValues,omitempty" jsonschema:"description=Sample values per label (default 10\\, max 50)."`
	StartRFC3339    string   `json:"startRfc3339,omitempty" jsonschema:"description=RFC3339 or relative (e.g. 'now-1h') start (default: 1h ago)."`
	EndRFC3339      string   `json:"endRfc3339,omitempty" jsonschema:"description=RFC3339 or relative (e.g. 'now') end (default: now)."`
}

func getLokiLabelCardinality(ctx context.Context, args GetLokiLabelCardinalityParams) ([]LokiLabelCardinality, error) {
	backend, err := lokiBackendForDatasource(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating Loki backend: %w", err)
	}

	start, err := parseStartTime(args.StartRFC3339)
	if err != nil {
		return nil, fmt.Errorf("parsing start time: %w", err)
	}
	end, err := parseEndTime(args.EndRFC3339)
	if err != nil {
		return nil, fmt.Errorf("parsing end time: %w", err)
	}

	labels := args.LabelNames
	if len(labels) == 0 {
		all, err := backend.ListLabelNames(ctx, start, end)
		if err != nil {
			return nil, fmt.Errorf("listing label names: %w", err)
		}
		max := args.MaxLabels
		if max <= 0 {
			max = 50
		}
		if len(all) > max {
			all = all[:max]
		}
		labels = all
	}

	sampleCap := args.MaxSampleValues
	if sampleCap <= 0 {
		sampleCap = 10
	}
	if sampleCap > 50 {
		sampleCap = 50
	}

	out := make([]LokiLabelCardinality, 0, len(labels))
	for _, name := range labels {
		values, err := backend.ListLabelValues(ctx, name, start, end)
		if err != nil {
			return nil, fmt.Errorf("listing values for %q: %w", name, err)
		}

		// Sort values for determinism, then sample.
		sort.Strings(values)
		samples := values
		truncated := false
		if len(values) > sampleCap {
			samples = values[:sampleCap]
			truncated = true
		}

		out = append(out, LokiLabelCardinality{
			Label:        name,
			UniqueValues: len(values),
			Band:         classifyCardinality(len(values)),
			SampleValues: samples,
			Truncated:    truncated,
		})
	}

	// Sort by cardinality descending so the worst offenders surface first.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].UniqueValues > out[j].UniqueValues
	})
	return out, nil
}

// ---------------------------------------------------------------------------
// Label-strategy audit (internal — exposed via analyze_loki_labels)
// ---------------------------------------------------------------------------

// LabelDescriptor is the static-mode input for one label. Fields are
// optional so users can supply only what they know; the scorer degrades
// gracefully (lower-confidence verdicts) when data is missing.
// LabelDescriptor describes one label for static-mode scoring. Only Name
// is required; supply UniqueValues/SampleValues/Unbounded/UsedInQueries/
// ValueKind to refine the verdict.
type LabelDescriptor struct {
	Name          string   `json:"name" jsonschema:"required"`
	UniqueValues  int      `json:"uniqueValues,omitempty"`
	SampleValues  []string `json:"sampleValues,omitempty"`
	Unbounded     bool     `json:"unbounded,omitempty"`
	UsedInQueries string   `json:"usedInQueries,omitempty"`
	ValueKind     string   `json:"valueKind,omitempty"`
}

// LabelVerdict is one row of the audit table.
type LabelVerdict struct {
	Label           string   `json:"label"`
	UniqueValues    int      `json:"uniqueValues"`
	Band            string   `json:"band"`
	Verdict         string   `json:"verdict"` // keep | review | move_to_metadata | remove | normalize
	Confidence      string   `json:"confidence"`
	Reasons         []string `json:"reasons"`
	SuggestedAction string   `json:"suggestedAction,omitempty"`
}

// AuditLokiLabelStrategyParams accepts either live inputs (datasource UID +
// optional selector) or a static label set. At least one mode must be
// populated; if both are supplied, live data is gathered and merged with
// any static hints (e.g. UsedInQueries) the caller provided.
type AuditLokiLabelStrategyParams struct {
	DatasourceUID      string            `json:"datasourceUid,omitempty" jsonschema:"description=Live mode: Loki/VictoriaLogs datasource UID. Either this or 'labels' is required."`
	Selector           string            `json:"selector,omitempty" jsonschema:"description=Optional LogQL selector for live stats sample."`
	MaxLabels          int               `json:"maxLabels,omitempty" jsonschema:"description=Live-mode label cap (default 50)."`
	StartRFC3339       string            `json:"startRfc3339,omitempty" jsonschema:"description=RFC3339 or relative (e.g. 'now-1h') start (default: 1h ago)."`
	EndRFC3339         string            `json:"endRfc3339,omitempty" jsonschema:"description=RFC3339 or relative (e.g. 'now') end (default: now)."`
	Labels             []LabelDescriptor `json:"labels,omitempty" jsonschema:"description=Static mode: caller-supplied label set."`
	ExpectedBaseLabels []string          `json:"expectedBaseLabels,omitempty" jsonschema:"description=Override the default recommended base labels."`
}

// LabelStrategyAudit is the structured response.
type LabelStrategyAudit struct {
	Mode                string         `json:"mode"` // "live" | "static" | "hybrid"
	Summary             string         `json:"summary"`
	Verdicts            []LabelVerdict `json:"verdicts"`
	MissingBaseLabels   []string       `json:"missingBaseLabels,omitempty"`
	NormalizationIssues []string       `json:"normalizationIssues,omitempty"`
	NamingIssues        []string       `json:"namingIssues,omitempty"`
	RecommendedLabelSet []string       `json:"recommendedLabelSet"`
	EstimatedStreamGain string         `json:"estimatedStreamGain,omitempty"`
	StatsObserved       *Stats         `json:"statsObserved,omitempty"`
}

func auditLokiLabelStrategy(ctx context.Context, args AuditLokiLabelStrategyParams) (*LabelStrategyAudit, error) {
	if args.DatasourceUID == "" && len(args.Labels) == 0 {
		return nil, fmt.Errorf("either datasourceUid (live mode) or labels (static mode) must be supplied")
	}

	mode := "static"
	descriptors := append([]LabelDescriptor(nil), args.Labels...)
	var stats *Stats

	if args.DatasourceUID != "" {
		// Live mode: pull cardinality and merge with any caller-supplied hints.
		liveDescriptors, observedStats, err := gatherLiveDescriptors(ctx, args)
		if err != nil {
			return nil, err
		}
		stats = observedStats

		if len(descriptors) > 0 {
			mode = "hybrid"
			descriptors = mergeDescriptors(liveDescriptors, descriptors)
		} else {
			mode = "live"
			descriptors = liveDescriptors
		}
	}

	baseLabels := args.ExpectedBaseLabels
	if len(baseLabels) == 0 {
		baseLabels = recommendedBaseLabels
	}

	verdicts := make([]LabelVerdict, 0, len(descriptors))
	for _, d := range descriptors {
		verdicts = append(verdicts, scoreLabel(d))
	}
	sort.SliceStable(verdicts, func(i, j int) bool {
		return verdictPriority(verdicts[i].Verdict) < verdictPriority(verdicts[j].Verdict)
	})

	missing := detectMissingBaseLabels(descriptors, baseLabels)
	naming := detectNamingIssues(descriptors)
	normalization := detectNormalizationIssues(descriptors)

	recommended := buildRecommendedLabelSet(verdicts)

	gain := estimateStreamGain(verdicts)

	audit := &LabelStrategyAudit{
		Mode:                mode,
		Summary:             buildSummary(mode, verdicts, missing, normalization, naming),
		Verdicts:            verdicts,
		MissingBaseLabels:   missing,
		NormalizationIssues: normalization,
		NamingIssues:        naming,
		RecommendedLabelSet: recommended,
		EstimatedStreamGain: gain,
		StatsObserved:       stats,
	}

	return audit, nil
}

// gatherLiveDescriptors pulls label names + values from a datasource and
// optionally a stats sample for the provided selector. Cardinality of each
// label is recorded; UsedInQueries is left blank because that signal isn't
// available from Loki's API.
func gatherLiveDescriptors(ctx context.Context, args AuditLokiLabelStrategyParams) ([]LabelDescriptor, *Stats, error) {
	cardinalities, err := getLokiLabelCardinality(ctx, GetLokiLabelCardinalityParams{
		DatasourceUID:   args.DatasourceUID,
		MaxLabels:       args.MaxLabels,
		MaxSampleValues: 5,
		StartRFC3339:    args.StartRFC3339,
		EndRFC3339:      args.EndRFC3339,
	})
	if err != nil {
		return nil, nil, err
	}

	descriptors := make([]LabelDescriptor, 0, len(cardinalities))
	for _, c := range cardinalities {
		descriptors = append(descriptors, LabelDescriptor{
			Name:         c.Label,
			UniqueValues: c.UniqueValues,
			SampleValues: c.SampleValues,
		})
	}

	var stats *Stats
	if args.Selector != "" {
		backend, err := lokiBackendForDatasource(ctx, args.DatasourceUID)
		if err == nil {
			start, _ := parseStartTime(args.StartRFC3339)
			end, _ := parseEndTime(args.EndRFC3339)
			if s, err := backend.QueryStats(ctx, args.Selector, start, end); err == nil {
				stats = s
			}
			// stats are best-effort: a failure here doesn't fail the audit.
		}
	}

	return descriptors, stats, nil
}

// mergeDescriptors overlays caller-supplied hints onto live measurements.
// Live measurements win for UniqueValues/SampleValues; caller hints fill in
// fields the API can't surface (UsedInQueries, ValueKind, Unbounded).
func mergeDescriptors(live, hints []LabelDescriptor) []LabelDescriptor {
	byName := make(map[string]LabelDescriptor, len(live))
	for _, d := range live {
		byName[d.Name] = d
	}
	for _, h := range hints {
		d, ok := byName[h.Name]
		if !ok {
			byName[h.Name] = h
			continue
		}
		if h.UsedInQueries != "" {
			d.UsedInQueries = h.UsedInQueries
		}
		if h.ValueKind != "" {
			d.ValueKind = h.ValueKind
		}
		if h.Unbounded {
			d.Unbounded = true
		}
		byName[h.Name] = d
	}

	out := make([]LabelDescriptor, 0, len(byName))
	for _, d := range byName {
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// scoreLabel applies the Grafana label evaluation framework to one label
// descriptor and returns a verdict.
func scoreLabel(d LabelDescriptor) LabelVerdict {
	v := LabelVerdict{
		Label:        d.Name,
		UniqueValues: d.UniqueValues,
		Band:         classifyCardinality(d.UniqueValues),
		Verdict:      "keep",
		Confidence:   "medium",
	}
	if d.UniqueValues == 0 {
		v.Band = bandUnknown
		v.Confidence = "low"
	}
	if d.Unbounded {
		v.Band = bandUnbounded
	}

	lower := strings.ToLower(d.Name)

	// Hard removals first — names that are unbounded by definition.
	if reason, bad := alwaysRemoveLabels[lower]; bad || d.Unbounded {
		v.Verdict = "move_to_metadata"
		v.Confidence = "high"
		if bad {
			v.Reasons = append(v.Reasons, reason)
		} else {
			v.Reasons = append(v.Reasons, "Caller marked label as unbounded.")
		}
		v.SuggestedAction = fmt.Sprintf("Drop %q from index labels; emit it as structured metadata or in the log line.", d.Name)
		return v
	}

	// Names where Grafana PS strongly prefers structured metadata.
	if reason, prefer := preferMetadataLabels[lower]; prefer {
		v.Verdict = "move_to_metadata"
		v.Confidence = "high"
		v.Reasons = append(v.Reasons, reason)
		v.SuggestedAction = fmt.Sprintf("Move %q to structured metadata (Loki >= 3.0) or embed in the log line.", d.Name)
		return v
	}

	// Cardinality-driven decisions.
	switch v.Band {
	case bandExtreme:
		v.Verdict = "remove"
		v.Confidence = "high"
		v.Reasons = append(v.Reasons,
			fmt.Sprintf("Cardinality is %s (%d unique values) — every query that omits %q must scan every stream.", v.Band, d.UniqueValues, d.Name))
		v.SuggestedAction = "Remove from the index; emit as structured metadata."
	case bandVeryHigh:
		if d.UsedInQueries == "always" {
			v.Verdict = "review"
			v.Confidence = "medium"
			v.Reasons = append(v.Reasons,
				fmt.Sprintf("Very high cardinality (%d), but caller reports the label is always present in queries.", d.UniqueValues))
			v.SuggestedAction = "Verify queries truly always include it; if any path omits it, performance falls off a cliff."
		} else {
			v.Verdict = "move_to_metadata"
			v.Confidence = "high"
			v.Reasons = append(v.Reasons,
				fmt.Sprintf("Very high cardinality (%d) — likely to overwhelm the index unless every query filters on it.", d.UniqueValues))
			v.SuggestedAction = "Move to structured metadata."
		}
	case bandHigh:
		switch d.UsedInQueries {
		case "always", "often":
			v.Verdict = "keep"
			v.Confidence = "medium"
			v.Reasons = append(v.Reasons,
				fmt.Sprintf("High cardinality (%d) but reported as a frequent query selector.", d.UniqueValues))
		case "rarely", "never":
			v.Verdict = "move_to_metadata"
			v.Confidence = "high"
			v.Reasons = append(v.Reasons,
				fmt.Sprintf("High cardinality (%d) and rarely used in selectors — classic stream-bloat label.", d.UniqueValues))
			v.SuggestedAction = "Move to structured metadata."
		default:
			v.Verdict = "review"
			v.Confidence = "low"
			v.Reasons = append(v.Reasons,
				fmt.Sprintf("High cardinality (%d) — confirm the label is used as a selector in 9 out of 10 queries.", d.UniqueValues))
			v.SuggestedAction = "If queries don't always include this label, move it to structured metadata."
		}
	case bandMedium, bandLow:
		if goodStaticLabels[lower] {
			v.Verdict = "keep"
			v.Confidence = "high"
			v.Reasons = append(v.Reasons, "Canonical low-cardinality static label.")
		} else {
			v.Verdict = "keep"
			v.Reasons = append(v.Reasons,
				fmt.Sprintf("Cardinality is %s (%d) — acceptable for an index label.", v.Band, d.UniqueValues))
		}
	case bandUnknown:
		v.Verdict = "review"
		v.Confidence = "low"
		v.Reasons = append(v.Reasons, "Cardinality unknown — run get_loki_label_cardinality on this label.")
	}

	// Dynamic label warning: extracted fields must stay tightly bounded.
	if strings.EqualFold(d.ValueKind, "dynamic") && d.UniqueValues >= 10 {
		v.Reasons = append(v.Reasons,
			"Dynamic (per-line) labels must stay in single-digit / low-tens cardinality.")
		if v.Verdict == "keep" {
			v.Verdict = "review"
		}
	}

	return v
}

// verdictPriority orders the report so the most actionable findings come first.
func verdictPriority(v string) int {
	switch v {
	case "remove":
		return 0
	case "move_to_metadata":
		return 1
	case "normalize":
		return 2
	case "review":
		return 3
	case "keep":
		return 4
	default:
		return 5
	}
}

// detectMissingBaseLabels returns the recommended labels that aren't
// present in the supplied set.
func detectMissingBaseLabels(descriptors []LabelDescriptor, base []string) []string {
	present := make(map[string]bool, len(descriptors))
	for _, d := range descriptors {
		present[strings.ToLower(d.Name)] = true
	}
	// Treat env/environment, app/service, team/squad as interchangeable: any
	// member of a group satisfies the rest. Order within a group doesn't matter.
	aliasGroups := [][]string{
		{"env", "environment"},
		{"app", "service"},
		{"team", "squad"},
	}
	for _, group := range aliasGroups {
		anyPresent := false
		for _, name := range group {
			if present[name] {
				anyPresent = true
				break
			}
		}
		if anyPresent {
			for _, name := range group {
				present[name] = true
			}
		}
	}

	missing := make([]string, 0)
	for _, name := range base {
		if !present[strings.ToLower(name)] {
			missing = append(missing, name)
		}
	}
	return missing
}

// detectNamingIssues looks for case-sensitive duplicates and mixed casing
// conventions (snake_case vs camelCase). These create phantom streams
// because Level != level in Loki.
var camelCase = regexp.MustCompile(`^[a-z]+([A-Z][a-z0-9]*)+$`)

func detectNamingIssues(descriptors []LabelDescriptor) []string {
	if len(descriptors) == 0 {
		return nil
	}

	var issues []string

	// Case-sensitive duplicates: same name with different casing.
	lowerCounts := make(map[string][]string)
	for _, d := range descriptors {
		key := strings.ToLower(d.Name)
		lowerCounts[key] = append(lowerCounts[key], d.Name)
	}
	for _, names := range lowerCounts {
		if len(names) > 1 {
			issues = append(issues,
				fmt.Sprintf("Label name appears with multiple casings: %s — pick one (Loki is case-sensitive).", strings.Join(names, ", ")))
		}
	}

	// Mixed conventions across the set.
	camel, snake := 0, 0
	for _, d := range descriptors {
		switch {
		case strings.Contains(d.Name, "_"):
			snake++
		case camelCase.MatchString(d.Name):
			camel++
		}
	}
	if camel > 0 && snake > 0 {
		issues = append(issues,
			fmt.Sprintf("Mixed naming conventions detected (%d snake_case and %d camelCase labels) — converge on one.", snake, camel))
	}

	return issues
}

// detectNormalizationIssues inspects sample values for the classic
// log-level normalization problem ("INFO" + "info" + "Info").
func detectNormalizationIssues(descriptors []LabelDescriptor) []string {
	var issues []string
	for _, d := range descriptors {
		if len(d.SampleValues) < 2 {
			continue
		}
		seen := make(map[string]map[string]bool)
		for _, v := range d.SampleValues {
			lower := strings.ToLower(v)
			if seen[lower] == nil {
				seen[lower] = make(map[string]bool)
			}
			seen[lower][v] = true
		}
		for lower, originals := range seen {
			if len(originals) > 1 {
				keys := make([]string, 0, len(originals))
				for k := range originals {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				issues = append(issues,
					fmt.Sprintf("Label %q has un-normalised values (%s collapse to %q) — fold to a single casing in Alloy with stage.replace.",
						d.Name, strings.Join(keys, ", "), lower))
			}
		}
	}
	return issues
}

// buildRecommendedLabelSet collects every label whose verdict is "keep"
// (after the audit applied any downgrades).
func buildRecommendedLabelSet(verdicts []LabelVerdict) []string {
	out := make([]string, 0)
	for _, v := range verdicts {
		if v.Verdict == "keep" {
			out = append(out, v.Label)
		}
	}
	sort.Strings(out)
	return out
}

// estimateStreamGain produces a coarse, human-readable estimate of stream
// reduction. It is intentionally qualitative — exact numbers require
// re-running ingestion, which is outside this tool's scope.
func estimateStreamGain(verdicts []LabelVerdict) string {
	var heavyRemovals int
	for _, v := range verdicts {
		if v.Verdict == "move_to_metadata" || v.Verdict == "remove" {
			switch v.Band {
			case bandHigh, bandVeryHigh, bandExtreme, bandUnbounded:
				heavyRemovals++
			}
		}
	}
	switch heavyRemovals {
	case 0:
		return "No major stream reductions expected — strategy already follows best practices for cardinality."
	case 1:
		return "Removing one high-cardinality label typically yields a meaningful stream reduction (often 2-5x in K8s)."
	default:
		return fmt.Sprintf("Removing %d high-cardinality labels can compound — expect order-of-magnitude stream reductions.", heavyRemovals)
	}
}

func buildSummary(mode string, verdicts []LabelVerdict, missing, normalization, naming []string) string {
	var keep, remove, move, review int
	for _, v := range verdicts {
		switch v.Verdict {
		case "keep":
			keep++
		case "remove":
			remove++
		case "move_to_metadata":
			move++
		case "review":
			review++
		}
	}

	displayMode := mode
	if displayMode != "" {
		displayMode = strings.ToUpper(displayMode[:1]) + displayMode[1:]
	}
	parts := []string{
		fmt.Sprintf("%s audit of %d labels: %d keep, %d move-to-metadata, %d remove, %d review.",
			displayMode, len(verdicts), keep, move, remove, review),
	}
	if len(missing) > 0 {
		parts = append(parts, fmt.Sprintf("Missing %d recommended base labels.", len(missing)))
	}
	if len(normalization) > 0 {
		parts = append(parts, fmt.Sprintf("Found %d value-normalisation issue(s).", len(normalization)))
	}
	if len(naming) > 0 {
		parts = append(parts, fmt.Sprintf("Found %d naming issue(s).", len(naming)))
	}
	return strings.Join(parts, " ")
}

// ---------------------------------------------------------------------------
// diagnose_loki_query_performance
// ---------------------------------------------------------------------------

// QueryPerfMetrics captures the runtime signals a user pulls from the
// querier metrics.go log line. All fields are optional — the diagnoser
// produces best-effort findings from whatever subset is supplied.
// QueryPerfMetrics holds runtime signals from the querier metrics.go log
// (queue_time, chunk_refs_fetch_time, store_chunks_download_time, exec_time,
// total_bytes/cache_chunk_req, total_lines/post_filter_lines).
type QueryPerfMetrics struct {
	QueueTimeSec               float64 `json:"queueTimeSec,omitempty"`
	ChunkRefsFetchTimeSec      float64 `json:"chunkRefsFetchTimeSec,omitempty"`
	StoreChunksDownloadTimeSec float64 `json:"storeChunksDownloadTimeSec,omitempty"`
	ExecutionTimeSec           float64 `json:"executionTimeSec,omitempty"`
	TotalBytes                 int64   `json:"totalBytes,omitempty"`
	CacheChunkReqs             int64   `json:"cacheChunkReqs,omitempty"`
	TotalLines                 int64   `json:"totalLines,omitempty"`
	PostFilterLines            int64   `json:"postFilterLines,omitempty"`
}

// QueryPerfFinding is one entry in the diagnosis output.
type QueryPerfFinding struct {
	Bottleneck string `json:"bottleneck"`
	Severity   string `json:"severity"` // info | warning | critical
	Evidence   string `json:"evidence"`
	Fix        string `json:"fix"`
}

// DiagnoseLokiQueryPerformanceParams accepts a query (optional), a
// datasource UID (optional, enables live stats lookup), and the metrics
// the operator pulled from logs.
type DiagnoseLokiQueryPerformanceParams struct {
	DatasourceUID string           `json:"datasourceUid,omitempty" jsonschema:"description=Datasource UID for live stats."`
	LogQL         string           `json:"logql,omitempty" jsonschema:"description=Label-only selector for live stats."`
	Metrics       QueryPerfMetrics `json:"metrics,omitempty" jsonschema:"description=Runtime metrics from metrics.go."`
	StartRFC3339  string           `json:"startRfc3339,omitempty" jsonschema:"description=RFC3339 or relative (e.g. 'now-1h') start."`
	EndRFC3339    string           `json:"endRfc3339,omitempty" jsonschema:"description=RFC3339 or relative (e.g. 'now') end."`
}

// QueryPerfDiagnosis is the structured output.
type QueryPerfDiagnosis struct {
	Findings []QueryPerfFinding `json:"findings"`
	Stats    *Stats             `json:"stats,omitempty"`
	Summary  string             `json:"summary"`
}

func diagnoseLokiQueryPerformance(ctx context.Context, args DiagnoseLokiQueryPerformanceParams) (*QueryPerfDiagnosis, error) {
	var stats *Stats
	if args.DatasourceUID != "" && args.LogQL != "" {
		backend, err := lokiBackendForDatasource(ctx, args.DatasourceUID)
		if err == nil {
			start, _ := parseStartTime(args.StartRFC3339)
			end, _ := parseEndTime(args.EndRFC3339)
			s, statsErr := backend.QueryStats(ctx, args.LogQL, start, end)
			if statsErr == nil {
				stats = s
			}
		}
	}

	findings := scoreQueryPerf(args.Metrics, stats)
	if len(findings) == 0 {
		findings = append(findings, QueryPerfFinding{
			Bottleneck: "no_signals",
			Severity:   "info",
			Evidence:   "No metrics or stats indicated a clear bottleneck.",
			Fix:        "Supply metrics from the querier metrics.go log (queue_time, chunk_refs_fetch_time, etc.) or pass `datasourceUid` + `logql` for live stats.",
		})
	}

	return &QueryPerfDiagnosis{
		Findings: findings,
		Stats:    stats,
		Summary:  buildPerfSummary(findings, stats),
	}, nil
}

// scoreQueryPerf applies the bottleneck checklist from the skill.
func scoreQueryPerf(m QueryPerfMetrics, stats *Stats) []QueryPerfFinding {
	var findings []QueryPerfFinding

	if m.QueueTimeSec > 1.0 {
		findings = append(findings, QueryPerfFinding{
			Bottleneck: "queue_time",
			Severity:   severityFor(m.QueueTimeSec, 1.0, 5.0),
			Evidence:   fmt.Sprintf("queue_time = %.2fs (threshold > 1s).", m.QueueTimeSec),
			Fix:        "Add more queriers, or reduce per-query parallelism in the query frontend.",
		})
	}

	if m.ChunkRefsFetchTimeSec > 1.0 {
		findings = append(findings, QueryPerfFinding{
			Bottleneck: "index_gateway",
			Severity:   severityFor(m.ChunkRefsFetchTimeSec, 1.0, 5.0),
			Evidence:   fmt.Sprintf("chunk_refs_fetch_time = %.2fs (threshold > 1s).", m.ChunkRefsFetchTimeSec),
			Fix:        "Add index gateway replicas and verify CPU headroom on existing instances.",
		})
	}

	if m.StoreChunksDownloadTimeSec > 2.0 {
		findings = append(findings, QueryPerfFinding{
			Bottleneck: "chunk_download",
			Severity:   severityFor(m.StoreChunksDownloadTimeSec, 2.0, 10.0),
			Evidence:   fmt.Sprintf("store_chunks_download_time = %.2fs (threshold > 2s).", m.StoreChunksDownloadTimeSec),
			Fix:        "Check average chunk size; if chunks are small (KB rather than MB) consolidate streams by removing high-cardinality labels.",
		})
	}

	// Average chunk size = total_bytes / cache_chunk_req. Small chunks = label over-splitting.
	if m.TotalBytes > 0 && m.CacheChunkReqs > 0 {
		avg := float64(m.TotalBytes) / float64(m.CacheChunkReqs)
		if avg < 64*1024 {
			findings = append(findings, QueryPerfFinding{
				Bottleneck: "chunks_too_small",
				Severity:   "critical",
				Evidence:   fmt.Sprintf("Average chunk size %.0f bytes (< 64KB) — streams are over-split.", avg),
				Fix:        "Remove high-cardinality labels (especially pod/filename/instance) to consolidate streams; target chunks in the low MB range.",
			})
		} else if avg < 256*1024 {
			findings = append(findings, QueryPerfFinding{
				Bottleneck: "chunks_small",
				Severity:   "warning",
				Evidence:   fmt.Sprintf("Average chunk size %.0f bytes (< 256KB).", avg),
				Fix:        "Investigate label cardinality — chunks under ~256KB usually indicate over-splitting.",
			})
		}
	}

	if m.ExecutionTimeSec > 5.0 {
		findings = append(findings, QueryPerfFinding{
			Bottleneck: "execution_time",
			Severity:   severityFor(m.ExecutionTimeSec, 5.0, 30.0),
			Evidence:   fmt.Sprintf("exec_time = %.2fs (threshold > 5s).", m.ExecutionTimeSec),
			Fix:        "Replace regex line filters (|~) with exact filters (|=) where possible; trim log line size; add label selectivity to narrow scope before line filters.",
		})
	}

	// Post-filter ratio: how many scanned lines actually mattered.
	// Require both fields > 0 — PostFilterLines defaults to 0 when omitted, so guarding
	// on TotalLines alone would fire a false low_selectivity warning whenever a caller
	// supplies TotalLines without PostFilterLines.
	if m.TotalLines > 0 && m.PostFilterLines > 0 {
		ratio := float64(m.PostFilterLines) / float64(m.TotalLines)
		if ratio < 0.05 {
			findings = append(findings, QueryPerfFinding{
				Bottleneck: "low_selectivity",
				Severity:   "warning",
				Evidence:   fmt.Sprintf("post_filter_lines / total_lines = %.1f%% — most scanned lines are discarded.", ratio*100),
				Fix:        "Add labels that match how users actually query (container, workload, level) so the query selector narrows the stream set before line filters run.",
			})
		}
	}

	// Live stats hint: very high stream count for the matched selector
	// almost always means cardinality bloat.
	if stats != nil && stats.Streams > 5000 {
		findings = append(findings, QueryPerfFinding{
			Bottleneck: "selector_too_broad",
			Severity:   "warning",
			Evidence:   fmt.Sprintf("Selector matches %d streams — likely too coarse for the time window.", stats.Streams),
			Fix:        "Tighten the selector (add namespace/workload/container) or remove the high-cardinality label that's inflating the stream count.",
		})
	}

	return findings
}

func severityFor(value, warn, crit float64) string {
	switch {
	case value >= crit:
		return "critical"
	case value >= warn:
		return "warning"
	default:
		return "info"
	}
}

func buildPerfSummary(findings []QueryPerfFinding, stats *Stats) string {
	if len(findings) == 0 {
		return "No findings."
	}
	var critical, warning int
	for _, f := range findings {
		switch f.Severity {
		case "critical":
			critical++
		case "warning":
			warning++
		}
	}
	parts := []string{fmt.Sprintf("%d findings (%d critical, %d warning).", len(findings), critical, warning)}
	if stats != nil {
		parts = append(parts, fmt.Sprintf("Selector matches %d streams / %d chunks / %d bytes.", stats.Streams, stats.Chunks, stats.Bytes))
	}
	return strings.Join(parts, " ")
}

// ---------------------------------------------------------------------------
// Registration helper
// ---------------------------------------------------------------------------

// AddLokiLabelAnalyzerTools registers the label-analyzer tool set.
// AddLokiTools calls this so the standard Loki bundle includes them.
func AddLokiLabelAnalyzerTools(s *server.MCPServer) {
	AnalyzeLokiLabels.Register(s)
}

// ---------------------------------------------------------------------------
// analyze_loki_labels — unified entry point
// ---------------------------------------------------------------------------

// AnalyzeLokiLabelsParams composes the inputs that used to belong to three
// separate tools: live cardinality measurement, label-strategy audit, and
// query-performance diagnosis. Either DatasourceUID (live) or Labels
// (static) must be set; perf diagnosis runs when PerfMetrics is supplied
// or when DatasourceUID + Selector together enable live stats lookup.
type AnalyzeLokiLabelsParams struct {
	DatasourceUID      string            `json:"datasourceUid,omitempty" jsonschema:"description=Datasource UID (live mode)."`
	Labels             []LabelDescriptor `json:"labels,omitempty" jsonschema:"description=Caller-supplied labels (static mode)."`
	Selector           string            `json:"selector,omitempty" jsonschema:"description=Optional LogQL selector for stats / perf diagnosis."`
	MaxLabels          int               `json:"maxLabels,omitempty"`
	StartRFC3339       string            `json:"startRfc3339,omitempty"`
	EndRFC3339         string            `json:"endRfc3339,omitempty"`
	ExpectedBaseLabels []string          `json:"expectedBaseLabels,omitempty"`
	PerfMetrics        *QueryPerfMetrics `json:"perfMetrics,omitempty" jsonschema:"description=Runtime metrics; presence triggers perf diagnosis."`
}

// AnalyzeLokiLabelsResult is a composite: audit is always present, the
// query-performance diagnosis is included only when there's enough signal
// to produce it.
type AnalyzeLokiLabelsResult struct {
	Audit            *LabelStrategyAudit `json:"audit"`
	QueryPerformance *QueryPerfDiagnosis `json:"queryPerformance,omitempty"`
}

func analyzeLokiLabels(ctx context.Context, args AnalyzeLokiLabelsParams) (*AnalyzeLokiLabelsResult, error) {
	if args.DatasourceUID == "" && len(args.Labels) == 0 {
		return nil, fmt.Errorf("either datasourceUid or labels must be supplied")
	}

	audit, err := auditLokiLabelStrategy(ctx, AuditLokiLabelStrategyParams{
		DatasourceUID:      args.DatasourceUID,
		Selector:           args.Selector,
		MaxLabels:          args.MaxLabels,
		StartRFC3339:       args.StartRFC3339,
		EndRFC3339:         args.EndRFC3339,
		Labels:             args.Labels,
		ExpectedBaseLabels: args.ExpectedBaseLabels,
	})
	if err != nil {
		return nil, err
	}

	out := &AnalyzeLokiLabelsResult{Audit: audit}

	// Perf diagnosis runs if the caller provided runtime metrics, OR if
	// they gave us enough to do a live stats lookup (datasource + selector).
	hasMetrics := args.PerfMetrics != nil
	canLive := args.DatasourceUID != "" && args.Selector != ""
	if hasMetrics || canLive {
		var metrics QueryPerfMetrics
		if hasMetrics {
			metrics = *args.PerfMetrics
		}
		diag, err := diagnoseLokiQueryPerformance(ctx, DiagnoseLokiQueryPerformanceParams{
			DatasourceUID: args.DatasourceUID,
			LogQL:         args.Selector,
			Metrics:       metrics,
			StartRFC3339:  args.StartRFC3339,
			EndRFC3339:    args.EndRFC3339,
		})
		if err == nil {
			out.QueryPerformance = diag
		}
		// Perf diagnosis is best-effort; the audit is the primary result.
	}

	return out, nil
}

// AnalyzeLokiLabels is the unified tool wrapper.
var AnalyzeLokiLabels = mcpgrafana.MustTool(
	"analyze_loki_labels",
	"Audits a Loki label strategy and optionally diagnoses query performance. Returns per-label verdicts, missing base labels, normalisation issues, and a recommended set. Pass datasourceUid for live cardinality or labels for static scoring; both may be combined.",
	analyzeLokiLabels,
	mcp.WithTitleAnnotation("Analyze Loki label strategy"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)
