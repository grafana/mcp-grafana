package lokiguard

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"testing"
	"time"
)

type goldenCase struct {
	Name          string `json:"name"`
	LogQL         string `json:"logql"`
	Instant       bool   `json:"instant"`
	WindowSeconds int64  `json:"windowSeconds"`
	Policy        struct {
		MaxBytes        int64 `json:"maxBytes"`
		MaxRangeSeconds int64 `json:"maxRangeSeconds"`
		EstimateAlways  bool  `json:"estimateAlways"`
	} `json:"policy"`
	Stats struct {
		Bytes      int64            `json:"bytes"`
		BySelector map[string]int64 `json:"bySelector"`
		Error      bool             `json:"error"`
	} `json:"stats"`
	Expect struct {
		Verdict        string   `json:"verdict"`
		Kinds          []string `json:"kinds"`
		Cause          string   `json:"cause"`
		EstimatedBytes int64    `json:"estimatedBytes"`
		StatsCalls     *int     `json:"statsCalls"`
	} `json:"expect"`
}

// TestGoldenCases runs testdata/cases.json, the cross-language contract for
// the guard. Other implementations load the same file.
func TestGoldenCases(t *testing.T) {
	raw, err := os.ReadFile("testdata/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []goldenCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) == 0 {
		t.Fatal("no golden cases")
	}

	for _, c := range file.Cases {
		t.Run(c.Name, func(t *testing.T) {
			q := Query{LogQL: c.LogQL, Instant: c.Instant}
			if c.WindowSeconds > 0 {
				q.End = testEnd
				q.Start = testEnd.Add(-time.Duration(c.WindowSeconds) * time.Second)
			}
			p := Policy{MaxBytes: c.Policy.MaxBytes, MaxRange: time.Duration(c.Policy.MaxRangeSeconds) * time.Second, EstimateAlways: c.Policy.EstimateAlways}

			// Stats are only wired when a byte budget is set, as in a real
			// caller; statsCalls then verifies the guard's own call pattern.
			var stats StatsFunc
			calls := 0
			if c.Policy.MaxBytes > 0 || c.Policy.EstimateAlways {
				stats = func(_ context.Context, selector string, _, _ time.Time) (int64, error) {
					calls++
					if b, ok := c.Stats.BySelector[selector]; ok {
						return b, nil
					}
					if c.Stats.Error {
						return 0, errors.New("stats unavailable")
					}
					return c.Stats.Bytes, nil
				}
			}

			d := Assess(context.Background(), q, p, stats)

			if got := d.Verdict.String(); got != c.Expect.Verdict {
				t.Fatalf("verdict = %s, want %s (reasons %+v, cause %q)", got, c.Expect.Verdict, d.Reasons, d.Cause)
			}
			var kinds []string
			for _, r := range d.Reasons {
				kinds = append(kinds, string(r.Kind))
			}
			if !slices.Equal(kinds, c.Expect.Kinds) {
				t.Errorf("reason kinds = %v, want %v", kinds, c.Expect.Kinds)
			}
			if string(d.Cause) != c.Expect.Cause {
				t.Errorf("cause = %q, want %q", d.Cause, c.Expect.Cause)
			}
			if d.EstimatedBytes != c.Expect.EstimatedBytes {
				t.Errorf("estimatedBytes = %d, want %d", d.EstimatedBytes, c.Expect.EstimatedBytes)
			}
			if c.Expect.StatsCalls != nil && calls != *c.Expect.StatsCalls {
				t.Errorf("stats calls = %d, want %d", calls, *c.Expect.StatsCalls)
			}
		})
	}
}
