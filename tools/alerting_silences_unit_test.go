package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func boolPtr(b bool) *bool { return &b }

func TestManageSilencesParams_Validate(t *testing.T) {
	validMatchers := []SilenceMatcherParam{{Name: "severity", Value: "critical"}}
	start := "2026-07-11T10:00:00Z"
	end := "2026-07-11T12:00:00Z"
	comment := "maintenance window"

	tests := []struct {
		name    string
		params  ManageSilencesParams
		wantErr string
	}{
		{
			name:   "list is valid with no params",
			params: ManageSilencesParams{Operation: "list"},
		},
		{
			name: "list with rule_uid filter is valid",
			params: ManageSilencesParams{
				Operation: "list",
				RuleUID:   strPtr("abc123"),
			},
		},
		{
			name: "list with matcher filter is valid",
			params: ManageSilencesParams{
				Operation: "list",
				Matchers:  validMatchers,
			},
		},
		{
			name: "get with silence_id is valid",
			params: ManageSilencesParams{
				Operation: "get",
				SilenceID: strPtr("sil-1"),
			},
		},
		{
			name:    "get without silence_id",
			params:  ManageSilencesParams{Operation: "get"},
			wantErr: "silence_id is required",
		},
		{
			name: "get with empty silence_id",
			params: ManageSilencesParams{
				Operation: "get",
				SilenceID: strPtr(""),
			},
			wantErr: "silence_id is required",
		},
		{
			name: "create with all required fields is valid",
			params: ManageSilencesParams{
				Operation: "create",
				Matchers:  validMatchers,
				StartsAt:  &start,
				EndsAt:    &end,
				Comment:   &comment,
			},
		},
		{
			name: "create without matchers",
			params: ManageSilencesParams{
				Operation: "create",
				StartsAt:  &start,
				EndsAt:    &end,
				Comment:   &comment,
			},
			wantErr: "matchers is required",
		},
		{
			name: "create with empty matchers slice",
			params: ManageSilencesParams{
				Operation: "create",
				Matchers:  []SilenceMatcherParam{},
				StartsAt:  &start,
				EndsAt:    &end,
				Comment:   &comment,
			},
			wantErr: "matchers is required",
		},
		{
			name: "create with matcher missing name",
			params: ManageSilencesParams{
				Operation: "create",
				Matchers:  []SilenceMatcherParam{{Value: "critical"}},
				StartsAt:  &start,
				EndsAt:    &end,
				Comment:   &comment,
			},
			wantErr: "name is required",
		},
		{
			name: "create without starts_at",
			params: ManageSilencesParams{
				Operation: "create",
				Matchers:  validMatchers,
				EndsAt:    &end,
				Comment:   &comment,
			},
			wantErr: "starts_at is required",
		},
		{
			name: "create without ends_at",
			params: ManageSilencesParams{
				Operation: "create",
				Matchers:  validMatchers,
				StartsAt:  &start,
				Comment:   &comment,
			},
			wantErr: "ends_at is required",
		},
		{
			name: "create with invalid starts_at",
			params: ManageSilencesParams{
				Operation: "create",
				Matchers:  validMatchers,
				StartsAt:  strPtr("not-a-timestamp"),
				EndsAt:    &end,
				Comment:   &comment,
			},
			wantErr: "starts_at must be a valid RFC3339 timestamp",
		},
		{
			name: "create with invalid ends_at",
			params: ManageSilencesParams{
				Operation: "create",
				Matchers:  validMatchers,
				StartsAt:  &start,
				EndsAt:    strPtr("2026-07-11 12:00"),
				Comment:   &comment,
			},
			wantErr: "ends_at must be a valid RFC3339 timestamp",
		},
		{
			name: "create without comment",
			params: ManageSilencesParams{
				Operation: "create",
				Matchers:  validMatchers,
				StartsAt:  &start,
				EndsAt:    &end,
			},
			wantErr: "comment is required",
		},
		{
			name: "create with empty comment",
			params: ManageSilencesParams{
				Operation: "create",
				Matchers:  validMatchers,
				StartsAt:  &start,
				EndsAt:    &end,
				Comment:   strPtr(""),
			},
			wantErr: "comment is required",
		},
		{
			name: "update with all required fields is valid",
			params: ManageSilencesParams{
				Operation: "update",
				SilenceID: strPtr("sil-1"),
				Matchers:  validMatchers,
				StartsAt:  &start,
				EndsAt:    &end,
				Comment:   &comment,
			},
		},
		{
			name: "update without silence_id",
			params: ManageSilencesParams{
				Operation: "update",
				Matchers:  validMatchers,
				StartsAt:  &start,
				EndsAt:    &end,
				Comment:   &comment,
			},
			wantErr: "silence_id is required",
		},
		{
			name: "update without matchers still fails on payload",
			params: ManageSilencesParams{
				Operation: "update",
				SilenceID: strPtr("sil-1"),
				StartsAt:  &start,
				EndsAt:    &end,
				Comment:   &comment,
			},
			wantErr: "matchers is required",
		},
		{
			name: "delete with silence_id is valid",
			params: ManageSilencesParams{
				Operation: "delete",
				SilenceID: strPtr("sil-1"),
			},
		},
		{
			name:    "delete without silence_id",
			params:  ManageSilencesParams{Operation: "delete"},
			wantErr: "silence_id is required",
		},
		{
			name:    "unknown operation",
			params:  ManageSilencesParams{Operation: "expire"},
			wantErr: "unknown operation",
		},
		{
			name:    "empty operation",
			params:  ManageSilencesParams{Operation: ""},
			wantErr: "unknown operation",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.params.validate()
			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestManageSilencesReadParams_Validate(t *testing.T) {
	tests := []struct {
		name    string
		params  ManageSilencesReadParams
		wantErr string
	}{
		{
			name:   "list is valid",
			params: ManageSilencesReadParams{Operation: "list"},
		},
		{
			name: "list with filters is valid",
			params: ManageSilencesReadParams{
				Operation: "list",
				RuleUID:   strPtr("abc123"),
				Matchers:  []SilenceMatcherParam{{Name: "severity", Value: "critical"}},
			},
		},
		{
			name: "get with silence_id is valid",
			params: ManageSilencesReadParams{
				Operation: "get",
				SilenceID: strPtr("sil-1"),
			},
		},
		{
			name:    "get without silence_id",
			params:  ManageSilencesReadParams{Operation: "get"},
			wantErr: "silence_id is required",
		},
		{
			name:    "create is rejected by read-only variant",
			params:  ManageSilencesReadParams{Operation: "create"},
			wantErr: "unknown operation",
		},
		{
			name:    "delete is rejected by read-only variant",
			params:  ManageSilencesReadParams{Operation: "delete"},
			wantErr: "unknown operation",
		},
		{
			name:    "empty operation",
			params:  ManageSilencesReadParams{Operation: ""},
			wantErr: "unknown operation",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.params.validate()
			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestMatcherToFilterString(t *testing.T) {
	tests := []struct {
		name    string
		matcher SilenceMatcherParam
		want    string
	}{
		{
			name:    "equality (default isEqual)",
			matcher: SilenceMatcherParam{Name: "severity", Value: "critical"},
			want:    `severity="critical"`,
		},
		{
			name:    "explicit isEqual true",
			matcher: SilenceMatcherParam{Name: "severity", Value: "critical", IsEqual: boolPtr(true)},
			want:    `severity="critical"`,
		},
		{
			name:    "regex equality",
			matcher: SilenceMatcherParam{Name: "pod", Value: "api-.*", IsRegex: true},
			want:    `pod=~"api-.*"`,
		},
		{
			name:    "negative equality",
			matcher: SilenceMatcherParam{Name: "env", Value: "prod", IsEqual: boolPtr(false)},
			want:    `env!="prod"`,
		},
		{
			name:    "negative regex",
			matcher: SilenceMatcherParam{Name: "pod", Value: "api-.*", IsRegex: true, IsEqual: boolPtr(false)},
			want:    `pod!~"api-.*"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, matcherToFilterString(tc.matcher))
		})
	}
}

func TestBuildSilenceFilters(t *testing.T) {
	t.Run("no filters", func(t *testing.T) {
		require.Empty(t, buildSilenceFilters(nil, nil))
	})

	t.Run("rule_uid produces __alert_rule_uid__ matcher", func(t *testing.T) {
		got := buildSilenceFilters(strPtr("rule-xyz"), nil)
		require.Equal(t, []string{`__alert_rule_uid__="rule-xyz"`}, got)
	})

	t.Run("empty rule_uid is ignored", func(t *testing.T) {
		require.Empty(t, buildSilenceFilters(strPtr(""), nil))
	})

	t.Run("rule_uid and matchers combine in order", func(t *testing.T) {
		got := buildSilenceFilters(strPtr("rule-xyz"), []SilenceMatcherParam{
			{Name: "team", Value: "sre"},
		})
		require.Equal(t, []string{`__alert_rule_uid__="rule-xyz"`, `team="sre"`}, got)
	})
}

func TestToPostableSilence(t *testing.T) {
	start := "2026-07-11T10:00:00Z"
	end := "2026-07-11T12:00:00Z"
	comment := "maintenance"

	t.Run("create applies created_by default and no id", func(t *testing.T) {
		p := ManageSilencesParams{
			Operation: "create",
			Matchers:  []SilenceMatcherParam{{Name: "severity", Value: "critical", IsEqual: boolPtr(false)}},
			StartsAt:  &start,
			EndsAt:    &end,
			Comment:   &comment,
		}
		s := p.toPostableSilence()
		require.Empty(t, s.ID)
		require.Equal(t, defaultSilenceCreatedBy, s.CreatedBy)
		require.Equal(t, start, s.StartsAt)
		require.Equal(t, end, s.EndsAt)
		require.Equal(t, comment, s.Comment)
		require.Len(t, s.Matchers, 1)
		require.Equal(t, "severity", s.Matchers[0].Name)
		require.NotNil(t, s.Matchers[0].IsEqual)
		require.False(t, *s.Matchers[0].IsEqual)

		// created_by must be present in the outgoing JSON, not just the struct.
		raw, err := json.Marshal(s)
		require.NoError(t, err)
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(raw, &decoded))
		require.Equal(t, defaultSilenceCreatedBy, decoded["createdBy"])
		_, hasID := decoded["id"]
		require.False(t, hasID, "id should be omitted for create")
	})

	t.Run("explicit created_by is preserved", func(t *testing.T) {
		p := ManageSilencesParams{
			Operation: "create",
			Matchers:  []SilenceMatcherParam{{Name: "severity", Value: "critical"}},
			StartsAt:  &start,
			EndsAt:    &end,
			Comment:   &comment,
			CreatedBy: strPtr("alice"),
		}
		require.Equal(t, "alice", p.toPostableSilence().CreatedBy)
	})

	t.Run("update carries silence id through", func(t *testing.T) {
		p := ManageSilencesParams{
			Operation: "update",
			SilenceID: strPtr("sil-42"),
			Matchers:  []SilenceMatcherParam{{Name: "severity", Value: "critical"}},
			StartsAt:  &start,
			EndsAt:    &end,
			Comment:   &comment,
		}
		s := p.toPostableSilence()
		require.Equal(t, "sil-42", s.ID)

		raw, err := json.Marshal(s)
		require.NoError(t, err)
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(raw, &decoded))
		require.Equal(t, "sil-42", decoded["id"])
	})
}
