package lokiguard

import (
	"regexp/syntax"
	"sort"
	"unicode"
)

// hasSelectivePositiveMatcher reports whether at least one matcher positively
// narrows the stream set: an equality with a non-empty value, or a regex
// match that is not a catch-all.
func hasSelectivePositiveMatcher(ms []labelMatcher) bool {
	for _, m := range ms {
		switch m.Op {
		case "=":
			if m.Value != "" {
				return true
			}
		case "=~":
			if !regexIsCatchAll(m.Value) {
				return true
			}
		}
	}
	return false
}

// regexIsCatchAll reports whether a matcher regex fails to narrow the stream
// set: it matches every value (.*, .+, (?s).*, .*.*, [\s\S]*, ...) or only
// the empty value (which selects every stream missing the label). Matcher
// regexes are fully anchored by Loki, so "matches every value" means the
// pattern can consume any whole string. Unparseable regexes count as
// selective — Loki rejects them with a proper error anyway.
func regexIsCatchAll(pattern string) bool {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return false
	}
	re = re.Simplify()
	return matchesAnyString(re) || matchesOnlyEmpty(re)
}

// matchesAnyString reports whether re matches every string (or every
// non-empty string: .+-style patterns are equally unselective for label
// values). Sufficient conditions only; a false negative merely lets a query
// through to the byte-budget check.
func matchesAnyString(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpStar, syntax.OpPlus:
		return matchesAnyChar(re.Sub[0]) || matchesAnyString(re.Sub[0])
	case syntax.OpQuest:
		// x? matches every string iff x does (the empty branch only adds
		// the empty string), e.g. (.+)?.
		return matchesAnyString(re.Sub[0])
	case syntax.OpCapture:
		return matchesAnyString(re.Sub[0])
	case syntax.OpConcat:
		// Any-string as a whole: one element matches any string and every
		// other element can match empty (e.g. ^.*$, .*(foo)?).
		found := false
		for _, sub := range re.Sub {
			if matchesAnyString(sub) {
				found = true
			} else if !nullable(sub) {
				return false
			}
		}
		return found
	case syntax.OpAlternate:
		for _, sub := range re.Sub {
			if matchesAnyString(sub) {
				return true
			}
		}
	}
	return false
}

// matchesOnlyEmpty reports whether re can only ever match the empty string
// (empty pattern, ^$, anchors alone). Word boundaries are deliberately not
// included: \b never matches within the empty string, so ^\b$ matches
// nothing at all — treating it as empty-only would misclassify patterns
// like .*\b as catch-alls.
func matchesOnlyEmpty(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpEmptyMatch, syntax.OpBeginLine, syntax.OpEndLine,
		syntax.OpBeginText, syntax.OpEndText:
		return true
	case syntax.OpCapture:
		return matchesOnlyEmpty(re.Sub[0])
	case syntax.OpStar, syntax.OpPlus, syntax.OpQuest, syntax.OpRepeat:
		// Repeating something that only matches empty still only matches
		// empty, e.g. (^$)*, (^)+, (^$){2,3}. Simplify does not fold these.
		return matchesOnlyEmpty(re.Sub[0])
	case syntax.OpConcat:
		for _, sub := range re.Sub {
			if !matchesOnlyEmpty(sub) {
				return false
			}
		}
		return true
	case syntax.OpAlternate:
		// ^|^ or ()|(): every branch matches only the empty string, so the
		// matcher selects every stream missing the label.
		for _, sub := range re.Sub {
			if !matchesOnlyEmpty(sub) {
				return false
			}
		}
		return true
	}
	return false
}

// nullable reports whether re can match the empty string. Word boundaries
// are conservatively treated as non-nullable (\b does not match within the
// empty string), erring toward classifying patterns as selective.
func nullable(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpEmptyMatch, syntax.OpStar, syntax.OpQuest,
		syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText:
		return true
	case syntax.OpCapture, syntax.OpPlus:
		return nullable(re.Sub[0])
	case syntax.OpConcat:
		for _, sub := range re.Sub {
			if !nullable(sub) {
				return false
			}
		}
		return true
	case syntax.OpAlternate:
		for _, sub := range re.Sub {
			if nullable(sub) {
				return true
			}
		}
	case syntax.OpRepeat:
		return re.Min == 0 || nullable(re.Sub[0])
	}
	return false
}

// matchesAnyChar reports whether re matches any single character: the dot
// metachar, a character class covering every rune, or an alternation whose
// branches jointly cover every rune.
func matchesAnyChar(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return true
	case syntax.OpCharClass:
		return charClassCoversAll(re.Rune)
	case syntax.OpCapture:
		return matchesAnyChar(re.Sub[0])
	case syntax.OpAlternate:
		return alternationCoversAllChars(re.Sub)
	}
	return false
}

// alternationCoversAllChars reports whether an alternation matches any single
// character: one branch does on its own (e.g. (foo|.)), or the union of its
// single-character branches covers every rune modulo newline (e.g.
// (a|foo|[^a])). The parser merges adjacent single-char branches itself
// (\s|\S parses as .), so this only fires when other branches keep them
// apart. Branches matching longer strings only add coverage, so skipping
// them in the union errs toward classifying the pattern as selective.
func alternationCoversAllChars(subs []*syntax.Regexp) bool {
	var pairs []rune
	for _, sub := range subs {
		for sub.Op == syntax.OpCapture {
			sub = sub.Sub[0]
		}
		switch sub.Op {
		case syntax.OpAnyChar:
			return true
		case syntax.OpAnyCharNotNL:
			pairs = append(pairs, 0, '\n'-1, '\n'+1, unicode.MaxRune)
		case syntax.OpCharClass:
			pairs = append(pairs, sub.Rune...)
		case syntax.OpLiteral:
			if len(sub.Rune) == 1 {
				pairs = append(pairs, sub.Rune[0], sub.Rune[0])
			}
		}
	}
	// charClassCoversAll expects ranges sorted by low bound; branches can
	// contribute them in any order.
	ranges := make([][2]rune, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		ranges = append(ranges, [2]rune{pairs[i], pairs[i+1]})
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i][0] < ranges[j][0] })
	sorted := pairs[:0]
	for _, r := range ranges {
		sorted = append(sorted, r[0], r[1])
	}
	return charClassCoversAll(sorted)
}

// charClassCoversAll reports whether the class matches every rune, modulo
// newline (so [^\n] counts, matching dot's default semantics).
func charClassCoversAll(pairs []rune) bool {
	var next rune
	for i := 0; i+1 < len(pairs); i += 2 {
		lo, hi := pairs[i], pairs[i+1]
		if lo > next && (next != '\n' || lo != '\n'+1) {
			return false
		}
		if hi >= next {
			next = hi + 1
		}
	}
	return next > unicode.MaxRune
}
