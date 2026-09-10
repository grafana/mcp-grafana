package tools

import (
	"testing"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLabelSelector(t *testing.T) {
	tests := []struct {
		name      string
		selector  string
		want      []siftInput
		wantErr   string
		unordered bool
	}{
		{
			name:     "exact match",
			selector: `{namespace="production"}`,
			want: []siftInput{
				{Type: "label", LabelMatcher: &siftLabelMatcher{Name: "namespace", Value: "production", Type: labels.MatchEqual}},
			},
		},
		{
			name:     "regex match",
			selector: `{namespace=~"prod.*"}`,
			want: []siftInput{
				{Type: "label", LabelMatcher: &siftLabelMatcher{Name: "namespace", Value: "prod.*", Type: labels.MatchRegexp}},
			},
		},
		{
			name:     "negative regex",
			selector: `{namespace!~"test.*"}`,
			want: []siftInput{
				{Type: "label", LabelMatcher: &siftLabelMatcher{Name: "namespace", Value: "test.*", Type: labels.MatchNotRegexp}},
			},
		},
		{
			name:     "not equal",
			selector: `{namespace!="staging"}`,
			want: []siftInput{
				{Type: "label", LabelMatcher: &siftLabelMatcher{Name: "namespace", Value: "staging", Type: labels.MatchNotEqual}},
			},
		},
		{
			name:     "multiple matchers",
			selector: `{namespace=~"prod.*", cluster="us-east-1"}`,
			want: []siftInput{
				{Type: "label", LabelMatcher: &siftLabelMatcher{Name: "namespace", Value: "prod.*", Type: labels.MatchRegexp}},
				{Type: "label", LabelMatcher: &siftLabelMatcher{Name: "cluster", Value: "us-east-1", Type: labels.MatchEqual}},
			},
			unordered: true,
		},
		{
			name:     "ignores __name__ matcher",
			selector: `up{namespace="production"}`,
			want: []siftInput{
				{Type: "label", LabelMatcher: &siftLabelMatcher{Name: "namespace", Value: "production", Type: labels.MatchEqual}},
			},
		},
		{
			name:     "invalid selector",
			selector: `not a selector`,
			wantErr:  "invalid label selector",
		},
		{
			name:     "empty braces",
			selector: `{}`,
			wantErr:  "contains no label matchers",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLabelSelector(tt.selector)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			if tt.unordered {
				assert.ElementsMatch(t, tt.want, got)
			} else {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}
