// Package filter implements the stackable filter rules that define a view
// over the line buffer: IN (keep matching), OUT (drop matching), HL
// (highlight only).
package filter

import (
	"bytes"
	"fmt"
	"regexp"
	"regexp/syntax"
	"slices"
	"strings"
)

type Kind int

const (
	In Kind = iota
	Out
	Highlight
)

func (k Kind) String() string {
	switch k {
	case In:
		return "in"
	case Out:
		return "out"
	default:
		return "hl"
	}
}

// Rule is a single filter entry. If the pattern is a plain literal, matching
// uses bytes.Contains instead of the regexp engine (~10x faster).
type Rule struct {
	Kind    Kind
	Pattern string
	Enabled bool

	re      *regexp.Regexp
	literal []byte // non-nil means literal fast path
}

func NewRule(kind Kind, pattern string, caseInsensitive bool) (*Rule, error) {
	if pattern == "" {
		return nil, fmt.Errorf("empty pattern")
	}
	expr := pattern
	if caseInsensitive {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, err
	}
	r := &Rule{Kind: kind, Pattern: pattern, Enabled: true, re: re}
	if !caseInsensitive && isLiteral(pattern) {
		r.literal = []byte(pattern)
	}
	return r, nil
}

func isLiteral(pattern string) bool {
	parsed, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return false
	}
	return parsed.Op == syntax.OpLiteral
}

func (r *Rule) Matches(line []byte) bool {
	if r.literal != nil {
		return bytes.Contains(line, r.literal)
	}
	return r.re.Match(line)
}

// Regexp exposes the compiled pattern for render-time match positioning.
func (r *Rule) Regexp() *regexp.Regexp { return r.re }

// Set is an ordered stack of rules. It is not goroutine-safe; the UI loop is
// its only writer and reader.
type Set struct {
	Rules []*Rule
}

// Visible reports whether a line passes the IN/OUT rules. Multiple enabled
// IN rules OR together; any enabled OUT match drops the line. HL rules never
// affect visibility.
func (s *Set) Visible(line []byte) bool {
	for _, r := range s.Rules {
		if r.Enabled && r.Kind == Out && r.Matches(line) {
			return false
		}
	}
	anyIn := false
	for _, r := range s.Rules {
		if !r.Enabled || r.Kind != In {
			continue
		}
		anyIn = true
		if r.Matches(line) {
			return true
		}
	}
	return !anyIn
}

// Highlights returns the enabled HL rules.
func (s *Set) Highlights() []*Rule {
	var out []*Rule
	for _, r := range s.Rules {
		if r.Enabled && r.Kind == Highlight {
			out = append(out, r)
		}
	}
	return out
}

func (s *Set) Add(r *Rule) { s.Rules = append(s.Rules, r) }

// Remove deletes the 1-based nth rule.
func (s *Set) Remove(n int) bool {
	i := n - 1
	if i < 0 || i >= len(s.Rules) {
		return false
	}
	s.Rules = slices.Delete(s.Rules, i, i+1)
	return true
}

func (s *Set) Clear()      { s.Rules = nil }
func (s *Set) Empty() bool { return len(s.Rules) == 0 }
func (s *Set) Active() bool {
	for _, r := range s.Rules {
		if r.Enabled && r.Kind != Highlight {
			return true
		}
	}
	return false
}

// Summary renders compact chips like `in:err out:health` for the status bar.
func (s *Set) Summary() string {
	var parts []string
	for _, r := range s.Rules {
		p := r.Pattern
		if len(p) > 18 {
			p = p[:15] + "…"
		}
		chip := r.Kind.String() + ":" + p
		if !r.Enabled {
			chip = "(" + chip + ")"
		}
		parts = append(parts, chip)
	}
	return strings.Join(parts, " ")
}
