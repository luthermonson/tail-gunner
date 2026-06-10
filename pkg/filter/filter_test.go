package filter

import "testing"

func mustRule(t *testing.T, k Kind, p string, ci bool) *Rule {
	t.Helper()
	r, err := NewRule(k, p, ci)
	if err != nil {
		t.Fatalf("NewRule(%q): %v", p, err)
	}
	return r
}

func TestLiteralFastPath(t *testing.T) {
	r := mustRule(t, In, "ERROR", false)
	if r.literal == nil {
		t.Fatal("plain literal should use fast path")
	}
	re := mustRule(t, In, "ERR.R", false)
	if re.literal != nil {
		t.Fatal("metachar pattern must not use literal path")
	}
	ci := mustRule(t, In, "error", true)
	if ci.literal != nil {
		t.Fatal("case-insensitive must not use literal path")
	}
	if !ci.Matches([]byte("an ERROR here")) {
		t.Fatal("case-insensitive match failed")
	}
}

func TestVisibility(t *testing.T) {
	s := &Set{}
	line := func(s string) []byte { return []byte(s) }

	if !s.Visible(line("anything")) {
		t.Fatal("empty set must show everything")
	}

	s.Add(mustRule(t, Out, "healthcheck", false))
	if s.Visible(line("GET /healthcheck 200")) {
		t.Fatal("OUT rule should drop the line")
	}
	if !s.Visible(line("GET /api 500")) {
		t.Fatal("non-matching line should remain visible")
	}

	s.Add(mustRule(t, In, "ERROR", false))
	s.Add(mustRule(t, In, "WARN", false))
	if !s.Visible(line("WARN disk almost full")) || !s.Visible(line("ERROR boom")) {
		t.Fatal("IN rules should OR together")
	}
	if s.Visible(line("INFO all fine")) {
		t.Fatal("line matching no IN rule should be hidden")
	}
	if s.Visible(line("ERROR healthcheck failed")) {
		t.Fatal("OUT must win over IN")
	}

	s.Rules[1].Enabled = false
	s.Rules[2].Enabled = false
	if !s.Visible(line("INFO all fine")) {
		t.Fatal("disabled IN rules must not constrain visibility")
	}

	s.Add(mustRule(t, Highlight, "fine", false))
	if !s.Visible(line("INFO all fine")) || !s.Visible(line("INFO other")) {
		t.Fatal("HL rules must not affect visibility")
	}
	if len(s.Highlights()) != 1 {
		t.Fatal("expected one highlight rule")
	}

	if !s.Remove(4) || len(s.Rules) != 3 {
		t.Fatal("Remove(4) should drop the HL rule")
	}
	s.Clear()
	if !s.Empty() {
		t.Fatal("Clear should empty the set")
	}
}
