// Package ui is gunner mode: the Bubble Tea program that renders a
// re-filterable view over the ring buffer with a ':' command palette.
package ui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/luthermonson/tail-gunner/pkg/buffer"
	"github.com/luthermonson/tail-gunner/pkg/diag"
	"github.com/luthermonson/tail-gunner/pkg/filter"
)

// Action is what the app should do after the program exits.
type Action int

const (
	ActionDemote Action = iota // back to plain mode
	ActionQuit                 // exit tail-gunner entirely
)

// IngestMsg tells the model new lines are in the ring. Sent (coalesced) by
// the app's forwarder goroutine.
type IngestMsg struct{}

type shellDoneMsg struct {
	cmd    string
	output []string
	err    error
}

type overlay struct {
	title string
	lines []string
	top   int
}

type Model struct {
	ring    *buffer.Ring
	filters *filter.Set
	names   []string

	width, height int
	filtered      []uint64 // seqs visible under current filters, ascending
	nextSeq       uint64   // first seq not yet examined
	top           int      // index into filtered of first visible row
	follow        bool

	palette     textinput.Model
	paletteOpen bool
	searches    *Searches
	status      string
	overlay     *overlay

	filtersOpen bool
	filterSel   int
	editRule    int // index of the rule being edited via the palette; -1 = none

	searchOpen bool
	searchSel  int
	editSearch int // index of the search being edited via the palette; -1 = none

	Action Action
}

func New(ring *buffer.Ring, filters *filter.Set, searches *Searches, names []string) *Model {
	ti := textinput.New()
	ti.Prompt = ":"
	ti.CharLimit = 512
	return &Model{
		ring:       ring,
		filters:    filters,
		searches:   searches,
		names:      names,
		follow:     true,
		palette:    ti,
		editRule:   -1,
		editSearch: -1,
	}
}

func (m *Model) Init() tea.Cmd {
	m.ingest()
	return nil
}

func (m *Model) contentHeight() int {
	h := m.height - 1
	if h < 1 {
		h = 1
	}
	return h
}

// ingest examines ring lines not yet seen and appends matches to the view.
func (m *Model) ingest() {
	first, _ := m.ring.Bounds()
	// prune seqs evicted from the ring
	if len(m.filtered) > 0 && m.filtered[0] < first {
		i := sort.Search(len(m.filtered), func(i int) bool { return m.filtered[i] >= first })
		m.filtered = m.filtered[i:]
		m.top -= i
		if m.top < 0 {
			m.top = 0
		}
	}
	if m.nextSeq < first {
		m.nextSeq = first
	}
	m.ring.Range(m.nextSeq, func(l buffer.Line) bool {
		if m.filters.Visible(l.Text) {
			m.filtered = append(m.filtered, l.Seq)
		}
		m.nextSeq = l.Seq + 1
		return true
	})
	m.clampTop()
}

// rebuild rescans the whole ring against the current filter set.
func (m *Model) rebuild() {
	start := time.Now()
	m.filtered = m.filtered[:0]
	m.nextSeq, _ = m.ring.Bounds()
	m.ingest()
	if !m.follow {
		m.clampTop()
	}
	diag.L().Debug("filter rebuild",
		"rules", len(m.filters.Rules),
		"scanned", m.ring.Len(),
		"matched", len(m.filtered),
		"took", time.Since(start),
	)
}

func (m *Model) clampTop() {
	maxTop := len(m.filtered) - m.contentHeight()
	if maxTop < 0 {
		maxTop = 0
	}
	if m.follow || m.top > maxTop {
		m.top = maxTop
	}
	if m.top < 0 {
		m.top = 0
	}
}

func (m *Model) scroll(delta int) {
	if delta < 0 {
		m.follow = false
	}
	m.top += delta
	if m.top >= len(m.filtered)-m.contentHeight() {
		m.clampTop()
	}
	if m.top < 0 {
		m.top = 0
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampTop()
		return m, nil

	case IngestMsg:
		m.ingest()
		return m, nil

	case shellDoneMsg:
		if msg.err != nil && len(msg.output) == 0 {
			m.status = fmt.Sprintf("| %s: %v", msg.cmd, msg.err)
			return m, nil
		}
		m.overlay = &overlay{title: "| " + msg.cmd, lines: msg.output}
		return m, nil

	case tea.KeyMsg:
		m.status = ""
		if m.overlay != nil {
			return m.updateOverlay(msg)
		}
		if m.filtersOpen {
			return m.updateFilterPanel(msg)
		}
		if m.searchOpen {
			return m.updateSearchPanel(msg)
		}
		if m.paletteOpen {
			return m.updatePalette(msg)
		}
		return m.updateKeys(msg)
	}
	return m, nil
}

func (m *Model) updateKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case ":":
		m.paletteOpen = true
		m.palette.Prompt = ":"
		m.palette.SetValue("")
		m.palette.Focus()
	case "/":
		m.paletteOpen = true
		m.palette.Prompt = "/"
		m.palette.SetValue("")
		m.palette.Focus()
	case "q", "esc":
		m.Action = ActionDemote
		return m, tea.Quit
	case "Q":
		m.Action = ActionQuit
		return m, tea.Quit
	case "f":
		m.follow = !m.follow
		m.clampTop()
	case "up", "k":
		m.scroll(-1)
	case "down", "j":
		m.scroll(1)
	case "pgup", "b":
		m.scroll(-m.contentHeight())
	case "pgdown", " ":
		m.scroll(m.contentHeight())
	case "ctrl+u":
		m.scroll(-m.contentHeight() / 2)
	case "ctrl+d":
		m.scroll(m.contentHeight() / 2)
	case "g", "home":
		m.follow = false
		m.top = 0
	case "G", "end":
		m.follow = true
		m.clampTop()
	case "n":
		m.searchJump(1)
	case "N":
		m.searchJump(-1)
	}
	return m, nil
}

func (m *Model) updateOverlay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	o := m.overlay
	page := m.contentHeight()
	switch msg.String() {
	case "q", "esc", "enter":
		m.overlay = nil
	case "up", "k":
		o.top--
	case "down", "j":
		o.top++
	case "pgup", "b":
		o.top -= page
	case "pgdown", " ":
		o.top += page
	case "g", "home":
		o.top = 0
	case "G", "end":
		o.top = len(o.lines) - page
	}
	if o.top > len(o.lines)-page {
		o.top = len(o.lines) - page
	}
	if o.top < 0 {
		o.top = 0
	}
	return m, nil
}

func (m *Model) updateFilterPanel(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(m.filters.Rules)
	switch msg.String() {
	case "esc", "q":
		m.filtersOpen = false
	case "up", "k":
		if m.filterSel > 0 {
			m.filterSel--
		}
	case "down", "j":
		if m.filterSel < n-1 {
			m.filterSel++
		}
	case "delete", "x":
		if n == 0 {
			break
		}
		m.filters.Remove(m.filterSel + 1)
		m.rebuild()
		if m.filterSel >= len(m.filters.Rules) {
			m.filterSel = len(m.filters.Rules) - 1
		}
		if m.filters.Empty() {
			m.filtersOpen = false
			m.status = "all filters removed"
		}
	case " ":
		if n == 0 {
			break
		}
		r := m.filters.Rules[m.filterSel]
		r.Enabled = !r.Enabled
		m.rebuild()
	case "enter":
		if n == 0 {
			break
		}
		r := m.filters.Rules[m.filterSel]
		m.filtersOpen = false
		m.editRule = m.filterSel
		m.paletteOpen = true
		m.palette.Prompt = ":"
		m.palette.SetValue(r.Kind.String() + " " + r.Pattern)
		m.palette.CursorEnd()
		m.palette.Focus()
	}
	return m, nil
}

func (m *Model) updateSearchPanel(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(m.searches.List)
	switch msg.String() {
	case "esc", "q":
		m.searchOpen = false
	case "up", "k":
		if m.searchSel > 0 {
			m.searchSel--
		}
	case "down", "j":
		if m.searchSel < n-1 {
			m.searchSel++
		}
	case "delete", "x":
		if n == 0 {
			break
		}
		m.searches.Remove(m.searchSel)
		if m.searchSel >= len(m.searches.List) {
			m.searchSel = len(m.searches.List) - 1
		}
		if len(m.searches.List) == 0 {
			m.searchOpen = false
			m.status = "all searches removed"
		}
	case " ":
		if n == 0 {
			break
		}
		s := m.searches.List[m.searchSel]
		s.Enabled = !s.Enabled
	case "enter":
		if n == 0 {
			break
		}
		m.searches.Active = m.searchSel
		m.searchOpen = false
		m.searchJump(1)
	case "e":
		if n == 0 {
			break
		}
		s := m.searches.List[m.searchSel]
		m.searchOpen = false
		m.editSearch = m.searchSel
		m.paletteOpen = true
		m.palette.Prompt = "/"
		m.palette.SetValue(s.Pattern)
		m.palette.CursorEnd()
		m.palette.Focus()
	}
	return m, nil
}

func (m *Model) updatePalette(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.paletteOpen = false
		m.editRule = -1
		m.editSearch = -1
		m.palette.Blur()
		return m, nil
	case "enter":
		input := m.palette.Value()
		m.paletteOpen = false
		m.palette.Blur()
		if m.palette.Prompt == "/" {
			m.execSearch(input)
			return m, nil
		}
		return m, m.execCommand(input)
	}
	var cmd tea.Cmd
	m.palette, cmd = m.palette.Update(msg)
	return m, cmd
}

func (m *Model) execSearch(pattern string) {
	// an edit started from the search panel applies to exactly one submission
	edit := m.editSearch
	m.editSearch = -1

	if pattern == "" {
		m.status = "empty search — manage saved searches with :searches"
		return
	}
	var err error
	if edit >= 0 {
		err = m.searches.Replace(edit, pattern)
	} else {
		err = m.searches.Add(pattern)
	}
	if err != nil {
		m.status = "bad pattern: " + err.Error()
		return
	}
	m.searchJump(1)
}

func (m *Model) searchJump(dir int) {
	active := m.searches.ActiveSearch()
	if active == nil {
		m.status = "no active search — use / or :searches"
		return
	}
	n := len(m.filtered)
	if n == 0 {
		return
	}
	start := m.top
	if dir > 0 {
		start = m.top + 1
	} else {
		start = m.top - 1
	}
	for i := start; i >= 0 && i < n; i += dir {
		ln, ok := m.ring.Get(m.filtered[i])
		if ok && active.Regexp().Match(ln.Text) {
			m.follow = false
			m.top = i
			m.clampTop()
			if m.top > i {
				m.top = i
			}
			return
		}
	}
	m.status = "no more matches for /" + active.Pattern
}

func (m *Model) execCommand(input string) tea.Cmd {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}
	if strings.HasPrefix(input, "/") {
		m.execSearch(strings.TrimPrefix(input, "/"))
		return nil
	}
	if strings.HasPrefix(input, "|") {
		return m.execShell(strings.TrimSpace(strings.TrimPrefix(input, "|")))
	}
	cmd, arg, _ := strings.Cut(input, " ")
	arg = strings.TrimSpace(arg)

	// an edit started from the filter panel applies to exactly one palette
	// submission, whatever the command turns out to be
	edit := m.editRule
	m.editRule = -1

	addRule := func(k filter.Kind) {
		if arg == "" {
			m.status = "usage: :" + cmd + " <pattern>"
			return
		}
		r, err := filter.NewRule(k, arg, false)
		if err != nil {
			m.status = "bad pattern: " + err.Error()
			return
		}
		if edit >= 0 && edit < len(m.filters.Rules) {
			m.filters.Rules[edit] = r
		} else {
			m.filters.Add(r)
		}
		m.rebuild()
	}

	switch cmd {
	case "in":
		addRule(filter.In)
	case "out":
		addRule(filter.Out)
	case "hl":
		addRule(filter.Highlight)
	case "rm":
		n, err := strconv.Atoi(arg)
		if err != nil || !m.filters.Remove(n) {
			m.status = "usage: :rm <filter number> (see :filters)"
		} else {
			m.rebuild()
		}
	case "clear":
		m.filters.Clear()
		m.searches.Clear()
		m.rebuild()
	case "filters":
		if m.filters.Empty() {
			m.status = "no filters — add one with :in/:out/:hl"
		} else {
			m.filtersOpen = true
			m.filterSel = 0
		}
	case "searches":
		if len(m.searches.List) == 0 {
			m.status = "no searches — add one with /"
		} else {
			m.searchOpen = true
			m.searchSel = max(m.searches.Active, 0)
		}
	case "f", "follow":
		m.follow = !m.follow
		m.clampTop()
	case "w", "write":
		if arg == "" {
			m.status = "usage: :w <file>"
		} else {
			m.writeView(arg)
		}
	case "q":
		m.Action = ActionDemote
		return tea.Quit
	case "Q", "quit":
		m.Action = ActionQuit
		return tea.Quit
	default:
		m.status = "unknown command: " + cmd
	}
	return nil
}

// viewBytes snapshots the current filtered view as raw text.
func (m *Model) viewBytes() []byte {
	var b bytes.Buffer
	for _, seq := range m.filtered {
		if ln, ok := m.ring.Get(seq); ok {
			b.Write(ln.Text)
			b.WriteByte('\n')
		}
	}
	return b.Bytes()
}

func (m *Model) writeView(path string) {
	if err := os.WriteFile(path, m.viewBytes(), 0o644); err != nil {
		m.status = "write failed: " + err.Error()
		return
	}
	m.status = fmt.Sprintf("wrote %d lines to %s", len(m.filtered), path)
}

const shellOutputCap = 10000

func (m *Model) execShell(cmdStr string) tea.Cmd {
	if cmdStr == "" {
		m.status = "usage: :| <command>"
		return nil
	}
	input := m.viewBytes()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var c *exec.Cmd
		if runtime.GOOS == "windows" {
			c = exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", cmdStr)
		} else {
			sh := os.Getenv("SHELL")
			if sh == "" {
				sh = "/bin/sh"
			}
			c = exec.CommandContext(ctx, sh, "-c", cmdStr)
		}
		c.Stdin = bytes.NewReader(input)
		out, err := c.CombinedOutput()
		lines := strings.Split(strings.TrimRight(string(out), "\r\n"), "\n")
		for i, l := range lines {
			lines[i] = strings.TrimRight(l, "\r")
		}
		if len(lines) > shellOutputCap {
			lines = append(lines[:shellOutputCap], fmt.Sprintf("… output truncated at %d lines", shellOutputCap))
		}
		if len(lines) == 1 && lines[0] == "" {
			lines = []string{"(no output)"}
		}
		return shellDoneMsg{cmd: cmdStr, output: lines, err: err}
	}
}
