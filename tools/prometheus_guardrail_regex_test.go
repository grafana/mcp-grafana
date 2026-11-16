package tools

import (
	"github.com/prometheus/prometheus/model/labels"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestClassifyPromRegex(t *testing.T) {
	for _, pattern := range []string{".*", "^.*$", "^.*", ".*$", "(?:.*)", "(.*)", ".*|.*", "(?s:.*)", ".{0,}", "[\\s\\S]*", "(?:.+)?", "(?:|.+)", ".*.*"} {
		t.Run(pattern, func(t *testing.T) {
			assert.Equal(t, promRegexAll, classifyPromRegex(pattern))
		})
	}
	for _, pattern := range []string{".+", "^.+$", "^.+", ".+$", "(?:.+)", ".+|.+", ".{1,}", ".*.+", "(?s:.+)"} {
		t.Run(pattern, func(t *testing.T) {
			assert.Equal(t, promRegexNonEmpty, classifyPromRegex(pattern))
		})
	}
	for _, pattern := range []string{"api|worker", ".* ", " .*", "api.*", "(?-s:.*)", "(?m:^.*$)", ".+.+", "a^.*", ".*$a", "(?:^.*$).+", "["} {
		t.Run(pattern, func(t *testing.T) {
			assert.Equal(t, promRegexOther, classifyPromRegex(pattern))
		})
	}
}

// A classified language must agree with the actual Prometheus matcher,
// including empty strings and newlines. This checks the recognizer's soundness.
func FuzzPromRegexClassification(f *testing.F) {
	for _, pattern := range []string{".*", ".+", "(?:.*)", ".*|.*", "(?-s:.*)", "^.*$", ".*$a", "(?:^.*$).+"} {
		f.Add(pattern, "\n")
	}
	f.Fuzz(func(t *testing.T, pattern, value string) {
		c := classifyPromRegex(pattern)
		if c != promRegexAll && c != promRegexNonEmpty {
			return
		}
		m, err := labels.NewMatcher(labels.MatchRegexp, "job", pattern)
		require.NoError(t, err)
		for _, v := range []string{"", "a", "\n", value} {
			assert.Equal(t, c == promRegexAll || v != "", m.Matches(v))
		}
	})
}
