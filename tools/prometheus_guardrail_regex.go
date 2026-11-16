package tools

import (
	"unicode"

	"github.com/grafana/regexp/syntax"
)

// These classes describe languages, not estimated query costs. Other includes
// patterns this deliberately conservative recognizer cannot prove universal.
type promRegexClass uint8

const (
	promRegexOther promRegexClass = iota
	promRegexAll
	promRegexNonEmpty
	promRegexEmpty // Internal: the language containing only the empty string.
)

func classifyPromRegex(value string) promRegexClass {
	// Common forms avoid an additional regex parse.
	switch value {
	case ".*", "^.*$", "^.*", ".*$":
		return promRegexAll
	case ".+", "^.+$", "^.+", ".+$":
		return promRegexNonEmpty
	}
	// Match Prometheus' parser flags, including dot matching newlines.
	re, err := syntax.Parse(value, syntax.Perl|syntax.DotNL)
	if err != nil {
		return promRegexOther
	}
	re = unwrapPromRegex(re.Simplify())
	// Only outer text anchors are redundant under Prometheus' full anchoring.
	// Never strip interior anchors or line anchors from multiline expressions.
	if re.Op == syntax.OpConcat {
		subs := re.Sub
		if len(subs) > 0 && subs[0].Op == syntax.OpBeginText {
			subs = subs[1:]
		}
		if len(subs) > 0 && subs[len(subs)-1].Op == syntax.OpEndText {
			subs = subs[:len(subs)-1]
		}
		return classifyPromRegexConcat(subs)
	}
	return classifyPromRegexNode(re)
}

func unwrapPromRegex(re *syntax.Regexp) *syntax.Regexp {
	for re.Op == syntax.OpCapture {
		re = re.Sub[0]
	}
	return re
}

func classifyPromRegexNode(re *syntax.Regexp) promRegexClass {
	re = unwrapPromRegex(re)
	switch re.Op {
	case syntax.OpEmptyMatch:
		return promRegexEmpty
	case syntax.OpStar, syntax.OpPlus:
		child := unwrapPromRegex(re.Sub[0])
		c := classifyPromRegexNode(child)
		anyChar := child.Op == syntax.OpAnyChar || (child.Op == syntax.OpCharClass && len(child.Rune) == 2 && child.Rune[0] == 0 && child.Rune[1] == unicode.MaxRune)
		if anyChar || c == promRegexNonEmpty || c == promRegexAll {
			if re.Op == syntax.OpStar || c == promRegexAll {
				return promRegexAll
			}
			return promRegexNonEmpty
		}
	case syntax.OpQuest:
		c := classifyPromRegexNode(re.Sub[0])
		if c == promRegexAll || c == promRegexNonEmpty {
			return promRegexAll
		}
	case syntax.OpAlternate:
		var hasEmpty, hasNonEmpty, hasOther bool
		for _, sub := range re.Sub {
			switch classifyPromRegexNode(sub) {
			case promRegexAll:
				return promRegexAll
			case promRegexEmpty:
				hasEmpty = true
			case promRegexNonEmpty:
				hasNonEmpty = true
			default:
				hasOther = true
			}
		}
		if hasEmpty && hasNonEmpty {
			return promRegexAll
		}
		if hasOther {
			return promRegexOther
		}
		if hasNonEmpty {
			return promRegexNonEmpty
		}
		return promRegexEmpty
	case syntax.OpConcat:
		return classifyPromRegexConcat(re.Sub)
	}
	return promRegexOther
}

func classifyPromRegexConcat(subs []*syntax.Regexp) promRegexClass {
	result := promRegexEmpty
	for _, sub := range subs {
		c := classifyPromRegexNode(sub)
		switch {
		case c == promRegexOther:
			return promRegexOther
		case c == promRegexEmpty:
			continue
		case result == promRegexNonEmpty && c == promRegexNonEmpty:
			// At least two characters: neither of our universal classes.
			return promRegexOther
		case result == promRegexNonEmpty || c == promRegexNonEmpty:
			result = promRegexNonEmpty
		default:
			result = promRegexAll
		}
	}
	return result
}
