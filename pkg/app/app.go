// Package app orchestrates interactive mode: plain tail-like printing with a
// keystroke listener on the controlling terminal, and the promotion dance
// into and out of the gunner TUI. Ingestion never pauses across transitions.
package app

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luthermonson/tail-gunner/pkg/buffer"
	"github.com/luthermonson/tail-gunner/pkg/cli"
	"github.com/luthermonson/tail-gunner/pkg/diag"
	"github.com/luthermonson/tail-gunner/pkg/filter"
	"github.com/luthermonson/tail-gunner/pkg/source"
	"github.com/luthermonson/tail-gunner/pkg/tailcompat"
	"github.com/luthermonson/tail-gunner/pkg/term"
	"github.com/luthermonson/tail-gunner/pkg/ui"
)

const demoteReplayCap = 10

type printer struct {
	mu       sync.Mutex
	plain    bool // when false (gunner mode), nothing is printed
	headers  bool
	lastFile int
	names    []string
	filters  *filter.Set
}

// printLocked assumes p.mu is held.
func (p *printer) printLocked(file int, text []byte) {
	if !p.filters.Visible(text) {
		return
	}
	if p.headers && file != p.lastFile {
		if p.lastFile != -1 {
			os.Stdout.WriteString("\n")
		}
		fmt.Fprintf(os.Stdout, "==> %s <==\n", p.names[file])
		p.lastFile = file
	}
	os.Stdout.Write(text)
	os.Stdout.WriteString("\n")
}

func (p *printer) print(file int, text []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.plain {
		p.printLocked(file, text)
	}
}

func (p *printer) setPlain(v bool) {
	p.mu.Lock()
	p.plain = v
	p.mu.Unlock()
}

// Run drives interactive mode and returns the process exit code. Falls back
// to pure tail behavior if the controlling terminal can't be opened.
func Run(opts cli.Options) int {
	tty, err := term.OpenTTY()
	if err != nil {
		diag.L().Warn("no controlling terminal; falling back to plain tail", "err", err)
		return tailcompat.Run(opts, os.Stdout, os.Stderr)
	}
	defer tty.Close()

	exit := 0
	ring := buffer.NewRing(opts.BufferCap)
	filters := &filter.Set{}
	searches := ui.NewSearches()

	stream := source.Open(source.Config{
		Files:          opts.Files,
		Lines:          opts.Lines,
		LinesFromStart: opts.LinesFromStart,
		FollowName:     opts.FollowName,
		OnError: func(name string, err error) {
			fmt.Fprintf(os.Stderr, "tail: cannot open '%s' for reading: %v\n", name, err)
			exit = 1
		},
	})

	p := &printer{
		plain:    true,
		headers:  (len(stream.Names) > 1 || opts.Verbose) && !opts.Quiet,
		lastFile: -1,
		names:    stream.Names,
		filters:  filters,
	}

	notify := make(chan struct{}, 1)
	go func() {
		for ln := range stream.C {
			ring.Append(ln.File, ln.Text)
			p.print(ln.File, ln.Text)
			select {
			case notify <- struct{}{}:
			default:
			}
		}
	}()

	promote := make(chan struct{})
	quit := make(chan int, 1)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigs
		quit <- 130
	}()
	if opts.PID > 0 {
		go func() {
			tailcompat.WaitPID(opts.PID)
			quit <- exit
		}()
	}

	// rawSt tracks the terminal state so any exit path can restore it.
	var rawMu sync.Mutex
	var rawSt *term.State
	setRaw := func() bool {
		rawMu.Lock()
		defer rawMu.Unlock()
		st, err := term.MakeRaw(tty)
		if err != nil {
			return false
		}
		rawSt = st
		return true
	}
	restoreRaw := func() {
		rawMu.Lock()
		defer rawMu.Unlock()
		if rawSt != nil {
			term.Restore(tty, rawSt)
			rawSt = nil
		}
	}
	defer restoreRaw()

	listening := make(chan struct{}, 1)
	listening <- struct{}{}
	go func() {
		buf := make([]byte, 1)
		for range listening {
			if !setRaw() {
				diag.L().Warn("raw mode unavailable; promotion disabled")
				continue
			}
			for {
				n, err := tty.Read(buf)
				if err != nil {
					restoreRaw()
					return
				}
				if n == 0 {
					continue
				}
				switch buf[0] {
				case ':':
					restoreRaw()
					promote <- struct{}{}
				case 3: // ctrl-c arrives as a byte in raw mode
					restoreRaw()
					quit <- 130
				default:
					continue
				}
				break
			}
		}
	}()

	for {
		select {
		case code := <-quit:
			return code

		case <-promote:
			p.setPlain(false)
			_, seqAtPromote := ring.Bounds()
			diag.L().Info("promote to gunner mode", "seq", seqAtPromote, "buffered", ring.Len())

			model := ui.New(ring, filters, searches, stream.Names)
			prog := tea.NewProgram(model,
				tea.WithAltScreen(),
				tea.WithInput(tty),
				tea.WithOutput(os.Stdout),
			)
			fwdDone := make(chan struct{})
			go func() {
				for {
					select {
					case <-fwdDone:
						return
					case <-notify:
						prog.Send(ui.IngestMsg{})
					}
				}
			}()

			final, err := prog.Run()
			close(fwdDone)
			if err != nil {
				diag.L().Error("TUI crashed", "err", err)
				fmt.Fprintf(os.Stderr, "tail-gunner: TUI error: %v\n", err)
				return 1
			}
			if m, ok := final.(*ui.Model); ok && m.Action == ui.ActionQuit {
				diag.L().Info("quit from gunner mode")
				return exit
			}

			_, seqNow := ring.Bounds()
			diag.L().Info("demote to plain mode", "missedLines", seqNow-seqAtPromote)
			replayMissed(ring, p, seqAtPromote)
			listening <- struct{}{}
		}
	}
}

// replayMissed reprints the tail of what arrived during gunner mode so the
// scrollback reads continuously, then re-enables plain printing under the
// same lock so no line can fall between replay and resume.
func replayMissed(ring *buffer.Ring, p *printer, fromSeq uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer func() { p.plain = true }()

	_, next := ring.Bounds()
	missed := next - fromSeq
	if missed == 0 {
		return
	}
	start := fromSeq
	if missed > demoteReplayCap {
		fmt.Fprintf(os.Stdout, "--- tail-gunner: %d lines while in TUI, showing last %d ---\n",
			missed, demoteReplayCap)
		start = next - demoteReplayCap
	}
	ring.Range(start, func(l buffer.Line) bool {
		p.printLocked(l.File, l.Text)
		return true
	})
}
