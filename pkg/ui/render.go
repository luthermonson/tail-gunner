package ui

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/luthermonson/tail-gunner/pkg/buffer"
)

var (
	styleError  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	styleWarn   = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	styleDebug  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleMatch = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("11"))

	// each saved search gets a stable color from this cycle
	searchPalette = []lipgloss.Style{
		lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("14")),  // cyan
		lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("13")),  // magenta
		lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("10")),  // green
		lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("208")), // orange
		lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("12")), // blue
	}
	styleFile   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleBar    = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("12"))
	styleBarOff = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("11"))
	styleTitle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	styleSel    = lipgloss.NewStyle().Reverse(true)
	styleStatus = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))

	reError = regexp.MustCompile(`(?i)\b(error|err|fatal|panic|fail(ed|ure)?)\b`)
	reWarn  = regexp.MustCompile(`(?i)\b(warn(ing)?)\b`)
	reDebug = regexp.MustCompile(`(?i)\b(debug|trace)\b`)
	reANSI  = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")
)

func (m *Model) View() string {
	if m.width == 0 {
		return ""
	}
	if m.overlay != nil {
		return m.viewOverlay()
	}
	if m.filtersOpen {
		return m.viewFilterPanel()
	}
	if m.searchOpen {
		return m.viewSearchPanel()
	}

	h := m.contentHeight()
	rows := make([]string, 0, m.height)
	for i := m.top; i < m.top+h; i++ {
		if i >= len(m.filtered) {
			rows = append(rows, "")
			continue
		}
		ln, ok := m.ring.Get(m.filtered[i])
		if !ok {
			rows = append(rows, "")
			continue
		}
		rows = append(rows, m.renderLine(ln))
	}

	var bottom string
	if m.paletteOpen {
		bottom = m.palette.View()
	} else {
		bottom = m.statusbar()
	}
	return strings.Join(rows, "\n") + "\n" + bottom
}

func (m *Model) viewOverlay() string {
	h := m.contentHeight()
	o := m.overlay
	rows := make([]string, 0, m.height)
	for i := o.top; i < o.top+h; i++ {
		if i >= len(o.lines) {
			rows = append(rows, "")
			continue
		}
		rows = append(rows, runewidth.Truncate(o.lines[i], m.width, "…"))
	}
	bar := fmt.Sprintf(" %s │ %d lines │ esc/q close ", o.title, len(o.lines))
	return strings.Join(rows, "\n") + "\n" + styleTitle.Render(runewidth.Truncate(bar, m.width, "…"))
}

func (m *Model) viewFilterPanel() string {
	h := m.contentHeight()
	rules := m.filters.Rules
	top := 0
	if m.filterSel >= h {
		top = m.filterSel - h + 1
	}
	rows := make([]string, 0, m.height)
	for i := top; i < top+h; i++ {
		if i >= len(rules) {
			rows = append(rows, "")
			continue
		}
		r := rules[i]
		state := "on "
		if !r.Enabled {
			state = "off"
		}
		line := runewidth.Truncate(
			fmt.Sprintf(" %d. [%s] %-3s %s", i+1, state, r.Kind, r.Pattern),
			m.width, "…")
		switch {
		case i == m.filterSel:
			line = styleSel.Render(line)
		case !r.Enabled:
			line = styleDebug.Render(line)
		}
		rows = append(rows, line)
	}
	bar := fmt.Sprintf(" filters %d/%d │ ↑/↓ select  enter edit  del/x remove  space toggle  esc close ",
		m.filterSel+1, len(rules))
	return strings.Join(rows, "\n") + "\n" + styleTitle.Render(runewidth.Truncate(bar, m.width, "…"))
}

func searchStyle(i int) lipgloss.Style {
	return searchPalette[i%len(searchPalette)]
}

func (m *Model) viewSearchPanel() string {
	h := m.contentHeight()
	list := m.searches.List
	top := 0
	if m.searchSel >= h {
		top = m.searchSel - h + 1
	}
	rows := make([]string, 0, m.height)
	for i := top; i < top+h; i++ {
		if i >= len(list) {
			rows = append(rows, "")
			continue
		}
		s := list[i]
		state := "on "
		if !s.Enabled {
			state = "off"
		}
		active := " "
		if i == m.searches.Active {
			active = "●"
		}
		marker := " "
		if i == m.searchSel {
			marker = ">"
		}
		line := fmt.Sprintf("%s %s %d. [%s] ", marker, active, i+1, state)
		pattern := runewidth.Truncate(s.Pattern, max(0, m.width-runewidth.StringWidth(line)), "…")
		if i == m.searchSel {
			line = styleSel.Render(line+pattern)
		} else if s.Enabled {
			line += searchStyle(i).Render(pattern)
		} else {
			line = styleDebug.Render(line + pattern)
		}
		rows = append(rows, line)
	}
	bar := fmt.Sprintf(" searches %d/%d │ ● active │ ↑/↓ select  enter activate  e edit  del/x remove  space toggle  esc close ",
		m.searchSel+1, len(list))
	return strings.Join(rows, "\n") + "\n" + styleTitle.Render(runewidth.Truncate(bar, m.width, "…"))
}

type span struct{ start, end int }

func mergeSpans(spans []span) []span {
	if len(spans) < 2 {
		return spans
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	out := spans[:1]
	for _, s := range spans[1:] {
		last := &out[len(out)-1]
		if s.start <= last.end {
			if s.end > last.end {
				last.end = s.end
			}
		} else {
			out = append(out, s)
		}
	}
	return out
}

func (m *Model) renderLine(ln buffer.Line) string {
	text := string(ln.Text)
	text = reANSI.ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "\t", "    ")
	// any control chars that survive ingestion (embedded \r from progress
	// bars, etc.) would corrupt row layout
	text = strings.Map(func(r rune) rune {
		if r < 0x20 {
			return -1
		}
		return r
	}, text)

	prefix := ""
	if len(m.names) > 1 && ln.File >= 0 && ln.File < len(m.names) {
		prefix = styleFile.Render(m.names[ln.File] + "▏")
		text = runewidth.Truncate(text, max(0, m.width-runewidth.StringWidth(m.names[ln.File])-1), "…")
	} else {
		text = runewidth.Truncate(text, m.width, "…")
	}

	base := lipgloss.NewStyle()
	switch {
	case reError.MatchString(text):
		base = styleError
	case reWarn.MatchString(text):
		base = styleWarn
	case reDebug.MatchString(text):
		base = styleDebug
	}

	var hlSpans []span
	for _, r := range m.filters.Highlights() {
		for _, loc := range r.Regexp().FindAllStringIndex(text, -1) {
			hlSpans = append(hlSpans, span{loc[0], loc[1]})
		}
	}
	type paint struct {
		span
		style lipgloss.Style
	}
	var paints []paint
	for _, s := range mergeSpans(hlSpans) {
		paints = append(paints, paint{s, styleMatch})
	}
	for i, sr := range m.searches.List {
		if !sr.Enabled {
			continue
		}
		var spans []span
		for _, loc := range sr.Regexp().FindAllStringIndex(text, -1) {
			spans = append(spans, span{loc[0], loc[1]})
		}
		for _, s := range mergeSpans(spans) {
			paints = append(paints, paint{s, searchStyle(i)})
		}
	}
	if len(paints) == 0 {
		return prefix + base.Render(text)
	}
	sort.SliceStable(paints, func(i, j int) bool { return paints[i].start < paints[j].start })

	var b strings.Builder
	pos := 0
	for _, p := range paints {
		if p.start < pos {
			if p.end <= pos {
				continue
			}
			p.start = pos
		}
		if p.start > pos {
			b.WriteString(base.Render(text[pos:p.start]))
		}
		b.WriteString(p.style.Render(text[p.start:p.end]))
		pos = p.end
	}
	if pos < len(text) {
		b.WriteString(base.Render(text[pos:]))
	}
	return prefix + b.String()
}

func (m *Model) statusbar() string {
	name := "stdin"
	if len(m.names) > 0 {
		name = strings.Join(m.names, ",")
	}
	mode := " FOLLOW "
	style := styleBar
	if !m.follow {
		mode = " PAUSED "
		style = styleBarOff
	}

	left := fmt.Sprintf(" tail-gunner │ %s │ %s/%d", name, formatCount(len(m.filtered)), m.ring.Len())
	if !m.filters.Empty() {
		left += " │ " + m.filters.Summary()
	}
	if active := m.searches.ActiveSearch(); active != nil {
		left += fmt.Sprintf(" │ /%s (%d/%d)", active.Pattern, m.searches.Active+1, len(m.searches.List))
	} else if len(m.searches.List) > 0 {
		left += fmt.Sprintf(" │ /(%d saved)", len(m.searches.List))
	}
	if m.status != "" {
		return style.Render(mode) + styleStatus.Render(" "+runewidth.Truncate(m.status, max(0, m.width-9), "…"))
	}
	hints := ":cmd /find f follow q back Q quit "
	gap := m.width - runewidth.StringWidth(mode) - runewidth.StringWidth(left) - runewidth.StringWidth(hints)
	line := left
	if gap > 0 {
		line += strings.Repeat(" ", gap) + hints
	}
	return style.Render(mode) + styleFile.Render(runewidth.Truncate(line, max(0, m.width-runewidth.StringWidth(mode)), "…"))
}

func formatCount(n int) string {
	s := fmt.Sprintf("%d", n)
	if n < 10000 {
		return s
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}
