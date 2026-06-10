package ui

import (
	"regexp"
	"slices"
)

// Search is one saved search: highlighted while enabled, navigable with n/N
// while active.
type Search struct {
	Pattern string
	Enabled bool
	re      *regexp.Regexp
}

func (s *Search) Regexp() *regexp.Regexp { return s.re }

// Searches is owned by the app (not the TUI model) so saved searches survive
// demote/promote cycles, like the filter set does.
type Searches struct {
	List   []*Search
	Active int // index n/N navigates; -1 = none
}

func NewSearches() *Searches {
	return &Searches{Active: -1}
}

// Add compiles pattern (case-insensitive), appends it, and makes it active.
func (s *Searches) Add(pattern string) error {
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return err
	}
	s.List = append(s.List, &Search{Pattern: pattern, Enabled: true, re: re})
	s.Active = len(s.List) - 1
	return nil
}

// Replace recompiles the i-th search in place, keeping its enabled state.
func (s *Searches) Replace(i int, pattern string) error {
	if i < 0 || i >= len(s.List) {
		return s.Add(pattern)
	}
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return err
	}
	s.List[i].Pattern = pattern
	s.List[i].re = re
	return nil
}

func (s *Searches) Remove(i int) {
	if i < 0 || i >= len(s.List) {
		return
	}
	s.List = slices.Delete(s.List, i, i+1)
	switch {
	case s.Active == i:
		s.Active = -1
	case s.Active > i:
		s.Active--
	}
}

func (s *Searches) ActiveSearch() *Search {
	if s.Active < 0 || s.Active >= len(s.List) {
		return nil
	}
	return s.List[s.Active]
}

func (s *Searches) Clear() {
	s.List = nil
	s.Active = -1
}
