package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyCardinality(t *testing.T) {
	cases := []struct {
		count int
		want  string
	}{
		{-1, bandUnknown},
		{0, bandLow},
		{9, bandLow},
		{10, bandMedium},
		{99, bandMedium},
		{100, bandHigh},
		{999, bandHigh},
		{1000, bandVeryHigh},
		{9999, bandVeryHigh},
		{10000, bandExtreme},
		{1000000, bandExtreme},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, classifyCardinality(tc.count), "count=%d", tc.count)
	}
}

func TestScoreLabel_AlwaysRemove(t *testing.T) {
	v := scoreLabel(LabelDescriptor{Name: "user_id", UniqueValues: 5})
	assert.Equal(t, "move_to_metadata", v.Verdict)
	assert.Equal(t, "high", v.Confidence)
	assert.NotEmpty(t, v.Reasons)
}

func TestScoreLabel_UnboundedFlagForcesMove(t *testing.T) {
	v := scoreLabel(LabelDescriptor{Name: "custom_token", UniqueValues: 3, Unbounded: true})
	assert.Equal(t, "move_to_metadata", v.Verdict)
	assert.Equal(t, bandUnbounded, v.Band)
}

func TestScoreLabel_PreferMetadata(t *testing.T) {
	v := scoreLabel(LabelDescriptor{Name: "pod", UniqueValues: 50})
	assert.Equal(t, "move_to_metadata", v.Verdict)
	assert.Contains(t, v.Reasons[0], "Transient in Kubernetes")
}

func TestScoreLabel_ExtremeCardinality(t *testing.T) {
	v := scoreLabel(LabelDescriptor{Name: "custom_label", UniqueValues: 50000})
	assert.Equal(t, "remove", v.Verdict)
	assert.Equal(t, bandExtreme, v.Band)
}

func TestScoreLabel_VeryHighWithAlwaysUsed(t *testing.T) {
	v := scoreLabel(LabelDescriptor{Name: "tenant_id", UniqueValues: 2500, UsedInQueries: "always"})
	assert.Equal(t, "review", v.Verdict)
}

func TestScoreLabel_HighRarelyUsed(t *testing.T) {
	v := scoreLabel(LabelDescriptor{Name: "custom_label", UniqueValues: 500, UsedInQueries: "rarely"})
	assert.Equal(t, "move_to_metadata", v.Verdict)
}

func TestScoreLabel_HighOftenUsed(t *testing.T) {
	v := scoreLabel(LabelDescriptor{Name: "custom_label", UniqueValues: 500, UsedInQueries: "often"})
	assert.Equal(t, "keep", v.Verdict)
}

func TestScoreLabel_GoodStaticLabel(t *testing.T) {
	v := scoreLabel(LabelDescriptor{Name: "env", UniqueValues: 3})
	assert.Equal(t, "keep", v.Verdict)
	assert.Equal(t, "high", v.Confidence)
}

func TestScoreLabel_UnknownCardinality(t *testing.T) {
	v := scoreLabel(LabelDescriptor{Name: "custom"})
	assert.Equal(t, "review", v.Verdict)
	assert.Equal(t, "low", v.Confidence)
}

func TestScoreLabel_DynamicLabelDowngrade(t *testing.T) {
	// Low cardinality but dynamic-extracted with > 10 values triggers review.
	v := scoreLabel(LabelDescriptor{Name: "extracted", UniqueValues: 25, ValueKind: "dynamic"})
	// Initial band is medium and verdict "keep" -> downgraded to "review".
	assert.Equal(t, "review", v.Verdict)
}

func TestDetectMissingBaseLabels(t *testing.T) {
	descriptors := []LabelDescriptor{
		{Name: "app"}, {Name: "env"}, {Name: "cluster"},
	}
	missing := detectMissingBaseLabels(descriptors, recommendedBaseLabels)
	// We expect everything in the base set except the three above.
	for _, want := range []string{"region", "level", "job", "team", "source", "classification"} {
		assert.Contains(t, missing, want)
	}
	for _, present := range []string{"app", "env", "cluster"} {
		assert.NotContains(t, missing, present)
	}
}

func TestDetectMissingBaseLabels_TreatsServiceAsApp(t *testing.T) {
	descriptors := []LabelDescriptor{{Name: "service"}}
	missing := detectMissingBaseLabels(descriptors, []string{"app"})
	assert.Empty(t, missing, "service should satisfy the 'app' requirement")
}

func TestDetectMissingBaseLabels_AliasIsBidirectional(t *testing.T) {
	// app should satisfy service, env should satisfy environment, team should satisfy squad.
	descriptors := []LabelDescriptor{{Name: "app"}, {Name: "env"}, {Name: "team"}}
	missing := detectMissingBaseLabels(descriptors, []string{"service", "environment", "squad"})
	assert.Empty(t, missing, "alias groups should satisfy in both directions")
}

func TestDetectMissingBaseLabels_AppDoesNotFlagServiceAsMissing(t *testing.T) {
	// Both "app" and "service" are in recommendedBaseLabels; supplying one
	// must not cause the other to be reported missing.
	descriptors := []LabelDescriptor{{Name: "app"}, {Name: "env"}, {Name: "cluster"}, {Name: "region"}, {Name: "level"}, {Name: "job"}, {Name: "team"}, {Name: "source"}, {Name: "classification"}}
	missing := detectMissingBaseLabels(descriptors, recommendedBaseLabels)
	assert.NotContains(t, missing, "service")
	assert.NotContains(t, missing, "app")
}

func TestDetectNamingIssues_CaseDuplicates(t *testing.T) {
	issues := detectNamingIssues([]LabelDescriptor{{Name: "Level"}, {Name: "level"}})
	require.Len(t, issues, 1)
	assert.Contains(t, issues[0], "multiple casings")
}

func TestDetectNamingIssues_MixedConventions(t *testing.T) {
	issues := detectNamingIssues([]LabelDescriptor{
		{Name: "app_name"}, {Name: "logLevel"}, {Name: "podName"},
	})
	require.NotEmpty(t, issues)
	found := false
	for _, i := range issues {
		if strings.Contains(i, "Mixed naming conventions") {
			found = true
		}
	}
	assert.True(t, found)
}

func TestDetectNormalizationIssues(t *testing.T) {
	issues := detectNormalizationIssues([]LabelDescriptor{
		{Name: "level", SampleValues: []string{"INFO", "info", "Info"}},
		{Name: "env", SampleValues: []string{"prod", "staging"}},
	})
	require.Len(t, issues, 1)
	assert.Contains(t, issues[0], "level")
	assert.Contains(t, issues[0], "un-normalised")
}

func TestMergeDescriptors_LiveWinsForCardinality(t *testing.T) {
	live := []LabelDescriptor{{Name: "pod", UniqueValues: 10000}}
	hints := []LabelDescriptor{{Name: "pod", UniqueValues: 0, UsedInQueries: "always"}}

	merged := mergeDescriptors(live, hints)
	require.Len(t, merged, 1)
	assert.Equal(t, 10000, merged[0].UniqueValues, "live cardinality should win")
	assert.Equal(t, "always", merged[0].UsedInQueries, "hint should fill in usage")
}

func TestMergeDescriptors_AddsHintOnlyLabels(t *testing.T) {
	live := []LabelDescriptor{{Name: "app", UniqueValues: 5}}
	hints := []LabelDescriptor{{Name: "team", UsedInQueries: "always"}}

	merged := mergeDescriptors(live, hints)
	require.Len(t, merged, 2)
}

func TestAuditLokiLabelStrategy_StaticMode(t *testing.T) {
	audit, err := auditLokiLabelStrategy(context.Background(), AuditLokiLabelStrategyParams{
		Labels: []LabelDescriptor{
			{Name: "app", UniqueValues: 8},
			{Name: "env", UniqueValues: 3},
			{Name: "pod", UniqueValues: 4000},
			{Name: "user_id", UniqueValues: 1000000},
			{Name: "level", UniqueValues: 4, SampleValues: []string{"INFO", "info"}},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "static", audit.Mode)

	// Verdicts ordered by priority: remove -> move_to_metadata -> review -> keep
	require.Len(t, audit.Verdicts, 5)
	assert.Equal(t, "move_to_metadata", audit.Verdicts[0].Verdict, "user_id should come first") // both user_id and pod are move_to_metadata, sort stable
	assert.Contains(t, []string{"user_id", "pod"}, audit.Verdicts[0].Label)

	// Recommended set should keep at least app, env, level.
	for _, label := range []string{"app", "env", "level"} {
		assert.Contains(t, audit.RecommendedLabelSet, label)
	}
	assert.NotContains(t, audit.RecommendedLabelSet, "pod")
	assert.NotContains(t, audit.RecommendedLabelSet, "user_id")

	// Should flag the missing base labels.
	assert.Contains(t, audit.MissingBaseLabels, "cluster")

	// Normalization issue caught.
	require.NotEmpty(t, audit.NormalizationIssues)
	assert.Contains(t, audit.NormalizationIssues[0], "level")

	// Stream-gain hint should be non-empty because we have heavy removals.
	assert.NotEmpty(t, audit.EstimatedStreamGain)
}

func TestAuditLokiLabelStrategy_RejectsEmpty(t *testing.T) {
	_, err := auditLokiLabelStrategy(context.Background(), AuditLokiLabelStrategyParams{})
	require.Error(t, err)
}

func TestScoreQueryPerf_Bottlenecks(t *testing.T) {
	findings := scoreQueryPerf(QueryPerfMetrics{
		QueueTimeSec:          3.0,
		ChunkRefsFetchTimeSec: 6.0,
		TotalBytes:            10000,
		CacheChunkReqs:        100, // -> 100 bytes/chunk, well under 64KB
		TotalLines:            10000,
		PostFilterLines:       100, // 1% ratio
	}, nil)

	kinds := map[string]bool{}
	for _, f := range findings {
		kinds[f.Bottleneck] = true
	}
	for _, want := range []string{"queue_time", "index_gateway", "chunks_too_small", "low_selectivity"} {
		assert.True(t, kinds[want], "expected finding %q in %v", want, kinds)
	}
}

func TestScoreQueryPerf_NoSignals(t *testing.T) {
	findings := scoreQueryPerf(QueryPerfMetrics{}, nil)
	assert.Empty(t, findings)
}

func TestScoreQueryPerf_TotalLinesWithoutPostFilterLinesNoFalseLowSelectivity(t *testing.T) {
	// PostFilterLines defaults to 0; supplying only TotalLines should not trip the
	// low_selectivity finding (the ratio would otherwise be a spurious 0%).
	findings := scoreQueryPerf(QueryPerfMetrics{TotalLines: 10000}, nil)
	for _, f := range findings {
		assert.NotEqual(t, "low_selectivity", f.Bottleneck,
			"low_selectivity must not fire when PostFilterLines is omitted")
	}
}

func TestScoreQueryPerf_BroadSelector(t *testing.T) {
	findings := scoreQueryPerf(QueryPerfMetrics{}, &Stats{Streams: 12000})
	require.Len(t, findings, 1)
	assert.Equal(t, "selector_too_broad", findings[0].Bottleneck)
}

func TestSuggestLokiAlloyLabelConfig_HappyPath(t *testing.T) {
	res, err := suggestLokiAlloyLabelConfig(context.Background(), SuggestLokiAlloyLabelConfigParams{
		ApprovedLabels:    []string{"app", "env", "cluster", "level"},
		RequiredLabels:    []string{"team"},
		NormalizeLogLevel: true,
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	assert.Contains(t, res.Alloy, `loki.process "enforce_labels"`)
	assert.Contains(t, res.Alloy, `forward_to = [loki.write.default.receiver]`)
	assert.Contains(t, res.Alloy, "stage.label_keep")
	// Required labels are unioned into the kept set so soft-enforcement
	// placeholders aren't immediately dropped. Labels are sorted alphabetically.
	assert.Contains(t, res.Alloy, `["app", "cluster", "env", "level", "team"]`)
	// Level normalization stages present and anchored so substrings like "CRITICAL"
	// or "trace" aren't mangled by partial matches.
	assert.Contains(t, res.Alloy, `^(?i)I(nfo)?$`)
	assert.Contains(t, res.Alloy, `^(?i)W(arn(ing)?)?$`)
	assert.Contains(t, res.Alloy, `^(?i)E(rr(or)?)?$`)
	assert.Contains(t, res.Alloy, `^(?i)D(ebug)?$`)
	// Soft-enforcement template for required label.
	assert.Contains(t, res.Alloy, `source = "team"`)
}

func TestSuggestLokiAlloyLabelConfig_RejectsEmpty(t *testing.T) {
	_, err := suggestLokiAlloyLabelConfig(context.Background(), SuggestLokiAlloyLabelConfigParams{})
	require.Error(t, err)
}

// Regression test: a required label that isn't also in ApprovedLabels must
// still appear in stage.label_keep, otherwise the "unknown" placeholder
// injected by stage.template is immediately dropped on the next stage.
func TestSuggestLokiAlloyLabelConfig_RequiredLabelUnionedIntoKeep(t *testing.T) {
	res, err := suggestLokiAlloyLabelConfig(context.Background(), SuggestLokiAlloyLabelConfigParams{
		ApprovedLabels: []string{"app", "env"},
		RequiredLabels: []string{"team"},
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	assert.Contains(t, res.Alloy, `["app", "env", "team"]`)
	// Sanity: soft-enforcement template should still fire for `team`.
	assert.Contains(t, res.Alloy, `source = "team"`)
}

// Duplicates between ApprovedLabels and RequiredLabels must not double-emit
// inside stage.label_keep.
func TestSuggestLokiAlloyLabelConfig_DuplicateRequiredApprovedNotDoubled(t *testing.T) {
	res, err := suggestLokiAlloyLabelConfig(context.Background(), SuggestLokiAlloyLabelConfigParams{
		ApprovedLabels: []string{"app", "team"},
		RequiredLabels: []string{"team"},
	})
	require.NoError(t, err)
	assert.Contains(t, res.Alloy, `["app", "team"]`)
}

func TestAnalyzeLokiLabels_StaticAuditOnly(t *testing.T) {
	res, err := analyzeLokiLabels(context.Background(), AnalyzeLokiLabelsParams{
		Labels: []LabelDescriptor{
			{Name: "app", UniqueValues: 5},
			{Name: "pod", UniqueValues: 2000},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotNil(t, res.Audit)
	assert.Equal(t, "static", res.Audit.Mode)
	assert.Nil(t, res.QueryPerformance, "perf diagnosis should be absent without metrics or live stats")
}

func TestAnalyzeLokiLabels_StaticAuditWithPerfMetrics(t *testing.T) {
	res, err := analyzeLokiLabels(context.Background(), AnalyzeLokiLabelsParams{
		Labels: []LabelDescriptor{{Name: "app", UniqueValues: 5}},
		PerfMetrics: &QueryPerfMetrics{
			QueueTimeSec: 3.0,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, res.Audit)
	require.NotNil(t, res.QueryPerformance, "perf diagnosis should run when perfMetrics is supplied")
	require.NotEmpty(t, res.QueryPerformance.Findings)
	assert.Equal(t, "queue_time", res.QueryPerformance.Findings[0].Bottleneck)
}

func TestAnalyzeLokiLabels_RejectsEmpty(t *testing.T) {
	_, err := analyzeLokiLabels(context.Background(), AnalyzeLokiLabelsParams{})
	require.Error(t, err)
}

func TestSuggestLokiAlloyLabelConfig_CustomComponentAndForward(t *testing.T) {
	res, err := suggestLokiAlloyLabelConfig(context.Background(), SuggestLokiAlloyLabelConfigParams{
		ApprovedLabels: []string{"app"},
		ComponentName:  "my_pipeline",
		ForwardTo:      "loki.write.cloud.receiver",
	})
	require.NoError(t, err)
	assert.Contains(t, res.Alloy, `loki.process "my_pipeline"`)
	assert.Contains(t, res.Alloy, `forward_to = [loki.write.cloud.receiver]`)
}
