package lokiguard

import (
	"testing"
	"time"
)

func TestParseLogQLSelectorsSelectivity(t *testing.T) {
	tests := []struct {
		name          string
		logql         string
		wantCount     int
		wantSelective bool // of the first selector, when present
	}{
		{"empty selector", `{}`, 1, false},
		{"single equality", `{namespace="foo"}`, 1, true},
		{"catch-all regex", `{cluster=~".+"}`, 1, false},
		{"catch-all star", `{cluster=~".*"}`, 1, false},
		{"anchored catch-all", `{cluster=~"^.+$"}`, 1, false},
		{"double star", `{cluster=~".*.*"}`, 1, false},
		{"dotall catch-all", `{cluster=~"(?s).*"}`, 1, false},
		{"char class catch-all", `{cluster=~"[\\s\\S]*"}`, 1, false},
		{"negated newline class", `{cluster=~"[^\\n]*"}`, 1, false},
		{"alternation catch-all", `{cluster=~".*|foo"}`, 1, false},
		{"grouped catch-all", `{cluster=~"(.+)"}`, 1, false},
		{"optional catch-all plus", `{cluster=~"(.+)?"}`, 1, false},
		{"optional catch-all star", `{cluster=~"(.*)?"}`, 1, false},
		{"lazy plus", `{cluster=~".+?"}`, 1, false},
		{"dot-or-newline alternation", `{cluster=~"(.|\\n)*"}`, 1, false},
		{"complementary class alternation", `{cluster=~"(\\s|\\S)+"}`, 1, false},
		{"alternation with any-char branch", `{cluster=~"(foo|.)*"}`, 1, false},
		{"alternation union covers all", `{cluster=~"(a|foo|[^a])*"}`, 1, false},
		{"split complementary classes", `{cluster=~"(\\s|foo|\\S)+"}`, 1, false},
		{"partial alternation union is selective", `{app=~"(a|foo|[b-z])*"}`, 1, true},
		{"empty regex", `{cluster=~""}`, 1, false},
		{"negative only", `{app!="x", job!~"y.*"}`, 1, false},
		{"selective regex", `{app=~"api-.*"}`, 1, true},
		{"selective alternation", `{app=~"a|b"}`, 1, true},
		{"optional literal is selective", `{app=~"(foo)?"}`, 1, true},
		{"alternate empty-only", `{cluster=~"^|^"}`, 1, false},
		{"alternate empty groups", `{cluster=~"()|()"}`, 1, false},
		{"starred empty-only", `{cluster=~"(^$)*"}`, 1, false},
		{"plussed empty-only", `{cluster=~"(^)+"}`, 1, false},
		{"optional empty-only", `{cluster=~"(^$)?"}`, 1, false},
		{"repeated empty-only", `{cluster=~"(^$){2,3}"}`, 1, false},
		{"starred empty-only alternation", `{cluster=~"(^|$)*"}`, 1, false},
		{"starred empty group", `{cluster=~"()*"}`, 1, false},
		{"catch-all plus equality", `{cluster=~".+", namespace="foo"}`, 1, true},
		{"metric query", `sum by (level) (count_over_time({namespace="x", app="y"}[5m]))`, 1, true},
		{"escaped quote in value", `{app="a\"b"}`, 1, true},
		{"backtick value", "{app=`x`}", 1, true},
		{"brace in value", `{app="a}b"}`, 1, true},
		{"quoted label name", `{"service.name"="checkout"}`, 1, true},
		{"spacing", `{ app = "x" , container = "y" }`, 1, true},
		{"binary expression", `sum(rate({app="x"}[5m])) / sum(rate({app="y"}[5m]))`, 2, true},
		{"no selector", `foo`, 0, false},
		{"unterminated", `{app="x"`, 0, false},
		{"garbage matchers", `{app=="x"}`, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selectors := parseLogQLSelectors(tt.logql)
			if len(selectors) != tt.wantCount {
				t.Fatalf("parseLogQLSelectors(%q) found %d selectors, want %d", tt.logql, len(selectors), tt.wantCount)
			}
			if tt.wantCount == 0 {
				return
			}
			if got := hasSelectivePositiveMatcher(selectors[0].matchers); got != tt.wantSelective {
				t.Errorf("hasSelectivePositiveMatcher(%q) = %v, want %v", tt.logql, got, tt.wantSelective)
			}
		})
	}
}

func TestMaxVectorDuration(t *testing.T) {
	tests := []struct {
		name  string
		logql string
		want  time.Duration
	}{
		{"no range vector", `{app="x"} |= "error"`, 0},
		{"minutes", `count_over_time({app="x"}[5m])`, 5 * time.Minute},
		{"days", `sum(count_over_time({app="x"}[30d]))`, 30 * 24 * time.Hour},
		{"weeks", `rate({app="x"}[1w])`, 7 * 24 * time.Hour},
		{"compound", `rate({app="x"}[1h30m])`, 90 * time.Minute},
		{"max of several", `rate({a="x"}[5m]) / rate({a="x"}[2h])`, 2 * time.Hour},
		{"bracket inside string", `{app="x"} |~ "err[0-9]m"`, 0},
		{"overflowing duration ignored", `count_over_time({app="x"}[300y])`, 0},
		{"fractional hours", `rate({app="x"}[1.5h])`, 90 * time.Minute},
		{"fractional long", `sum(count_over_time({app="x"}[720.5h]))`, 720*time.Hour + 30*time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := maxVectorDuration(tt.logql); got != tt.want {
				t.Errorf("maxVectorDuration(%q) = %s, want %s", tt.logql, got, tt.want)
			}
		})
	}
}

func TestParseLogQLDurationRejects(t *testing.T) {
	for _, s := range []string{"", "5", "5x", "m", "-5m", "300y", "9999999999y"} {
		t.Run(s, func(t *testing.T) {
			if _, ok := parseLogQLDuration(s); ok {
				t.Errorf("parseLogQLDuration(%q) must be rejected", s)
			}
		})
	}
}

func TestStripLogQLComments(t *testing.T) {
	// A commented-out catch-all selector must not be scanned.
	selectors := parseLogQLSelectors(stripLogQLComments(`{app="x"} # see {cluster=~".+"} for all`))
	if len(selectors) != 1 || selectors[0].raw != `{app="x"}` {
		t.Errorf("# comment leaked a selector: %+v", selectors)
	}

	// A # inside a string literal is not a comment.
	logql := `{app="x"} |= "#not a comment"`
	if got := stripLogQLComments(logql); got != logql {
		t.Errorf("# inside a string was stripped: %q", got)
	}

	// Loki's lexer also skips Go-style comments; a /* */ inside a selector
	// must not break matcher parsing into an Unknown verdict.
	selectors = parseLogQLSelectors(stripLogQLComments(`{cluster/*x*/=~".+"}`))
	if len(selectors) != 1 || len(selectors[0].matchers) != 1 || selectors[0].matchers[0].Name != "cluster" {
		t.Errorf("block comment broke matcher parsing: %+v", selectors)
	}

	// Comment markers inside string literals are kept.
	logql = `{app="x"} |= "//not" |= "/*neither*/"`
	if got := stripLogQLComments(logql); got != logql {
		t.Errorf("comment markers inside strings were stripped: %q", got)
	}

	// An unterminated /* consumes the rest of the input.
	selectors = parseLogQLSelectors(stripLogQLComments(`{app="x"} /* {cluster=~".+"}`))
	if len(selectors) != 1 || selectors[0].raw != `{app="x"}` {
		t.Errorf("unterminated block comment leaked a selector: %+v", selectors)
	}

	// A lone / (division) is not a comment.
	logql = `rate({app="x"}[5m]) / rate({app="y"}[5m])`
	if got := stripLogQLComments(logql); got != logql {
		t.Errorf("division was treated as a comment: %q", got)
	}
}

func TestHumanizeBytes(t *testing.T) {
	for n, want := range map[int64]string{
		100 << 30:   "100.0 GiB",
		3 << 40 / 2: "1.5 TiB",
		10 << 20:    "10.0 MiB",
		4 << 10:     "4.0 KiB",
		512:         "512 B",
	} {
		if got := humanizeBytes(n); got != want {
			t.Errorf("humanizeBytes(%d) = %q, want %q", n, got, want)
		}
	}
}
