// Package tailcompat is the drop-in path: byte-faithful tail behavior for
// pipe mode and for any invocation that never promotes (no -f, --gun-no-promote,
// byte mode). It deliberately imports nothing from the TUI side of the tree.
package tailcompat

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/nxadm/tail"

	"github.com/luthermonson/tail-gunner/pkg/cli"
	"github.com/luthermonson/tail-gunner/pkg/diag"
	"github.com/luthermonson/tail-gunner/pkg/tailio"
)

const stdinName = "standard input"

type printer struct {
	w        *bufio.Writer
	headers  bool
	lastFile int // -1 = nothing printed yet
	names    []string
}

func (p *printer) header(file int) {
	if !p.headers || file == p.lastFile {
		return
	}
	if p.lastFile != -1 {
		p.w.WriteByte('\n')
	}
	fmt.Fprintf(p.w, "==> %s <==\n", p.names[file])
	p.lastFile = file
}

// Run executes tail behavior and returns the process exit code.
func Run(opts cli.Options, stdout, stderr io.Writer) int {
	files := opts.Files
	if len(files) == 0 {
		files = []string{"-"}
	}
	names := make([]string, len(files))
	for i, f := range files {
		if f == "-" {
			names[i] = stdinName
		} else {
			names[i] = f
		}
	}

	p := &printer{
		w:        bufio.NewWriter(stdout),
		headers:  (len(files) > 1 || opts.Verbose) && !opts.Quiet,
		lastFile: -1,
		names:    names,
	}
	defer p.w.Flush()

	exit := 0
	type followTarget struct {
		idx    int
		path   string
		offset int64
	}
	var targets []followTarget

	for i, f := range files {
		if f == "-" {
			p.header(i)
			if err := dumpStdin(opts, p.w); err != nil {
				fmt.Fprintf(stderr, "tail: error reading %s: %v\n", stdinName, err)
				exit = 1
			}
			continue
		}
		off, err := dumpFile(opts, f, i, p)
		if err != nil {
			fmt.Fprintf(stderr, "tail: cannot open '%s' for reading: %v\n", f, unwrapPathErr(err))
			exit = 1
			if !opts.FollowName {
				continue
			}
			off = 0
		}
		if opts.Follow {
			targets = append(targets, followTarget{idx: i, path: f, offset: off})
		}
	}
	p.w.Flush()

	if !opts.Follow {
		return exit
	}

	done := make(chan struct{})
	if opts.PID > 0 {
		go watchPID(opts.PID, done)
	}

	if len(targets) == 0 {
		// stdin-only follow: GNU tail blocks after EOF rather than exiting.
		<-done
		return exit
	}

	type tagged struct {
		idx  int
		text string
	}
	out := make(chan tagged, 256)
	for _, t := range targets {
		cfg := tail.Config{
			Location:  &tail.SeekInfo{Offset: t.offset, Whence: io.SeekStart},
			Follow:    true,
			ReOpen:    opts.FollowName,
			MustExist: !opts.FollowName,
			Poll:      usePolling(),
			Logger:    diag.StdLogger(),
		}
		tf, err := tail.TailFile(t.path, cfg)
		if err != nil {
			fmt.Fprintf(stderr, "tail: cannot follow '%s': %v\n", t.path, err)
			exit = 1
			continue
		}
		go func(idx int, tf *tail.Tail) {
			for line := range tf.Lines {
				if line.Err != nil {
					continue
				}
				out <- tagged{idx: idx, text: line.Text}
			}
		}(t.idx, tf)
	}

	flush := time.NewTicker(100 * time.Millisecond)
	defer flush.Stop()
	for {
		select {
		case ln := <-out:
			p.header(ln.idx)
			p.w.WriteString(ln.text)
			p.w.WriteByte('\n')
		case <-flush.C:
			p.w.Flush()
		case <-done:
			p.w.Flush()
			return exit
		}
	}
}

// dumpFile writes the requested initial portion and returns the offset where
// following should resume.
func dumpFile(opts cli.Options, path string, idx int, p *printer) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	p.header(idx)

	var start int64
	switch {
	case opts.BytesSet && opts.BytesFromStart:
		start = opts.Bytes - 1
		if start < 0 {
			start = 0
		}
	case opts.BytesSet:
		size, err := f.Seek(0, io.SeekEnd)
		if err != nil {
			return 0, err
		}
		start = size - opts.Bytes
		if start < 0 {
			start = 0
		}
	case opts.LinesFromStart:
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return 0, err
		}
		br := bufio.NewReaderSize(f, 32*1024)
		if err := tailio.SkipLines(br, opts.Lines); err != nil {
			return 0, err
		}
		if _, err := io.Copy(p.w, br); err != nil {
			return 0, err
		}
		end, _ := f.Seek(0, io.SeekCurrent)
		return end, nil
	default:
		start, err = tailio.LastNLinesOffset(f, opts.Lines)
		if err != nil {
			return 0, err
		}
	}

	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return 0, err
	}
	written, err := io.Copy(p.w, f)
	if err != nil {
		return start, err
	}
	return start + written, nil
}

func dumpStdin(opts cli.Options, w io.Writer) error {
	in := bufio.NewReaderSize(os.Stdin, 64*1024)
	switch {
	case opts.BytesSet && opts.BytesFromStart:
		if _, err := io.CopyN(io.Discard, in, opts.Bytes-1); err != nil && err != io.EOF {
			return err
		}
		_, err := io.Copy(w, in)
		return err
	case opts.BytesSet:
		b, err := tailio.StreamLastNBytes(in, opts.Bytes)
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	case opts.LinesFromStart:
		if err := tailio.SkipLines(in, opts.Lines); err != nil {
			return err
		}
		_, err := io.Copy(w, in)
		return err
	default:
		lines, err := tailio.StreamLastNLines(in, opts.Lines)
		if err != nil {
			return err
		}
		for _, l := range lines {
			if _, err := w.Write(l); err != nil {
				return err
			}
		}
		return nil
	}
}

func watchPID(pid int, done chan<- struct{}) {
	WaitPID(pid)
	close(done)
}

// WaitPID blocks until the process with the given pid no longer exists.
func WaitPID(pid int) {
	for processAlive(pid) {
		time.Sleep(time.Second)
	}
	diag.L().Info("watched pid died", "pid", pid)
}

func unwrapPathErr(err error) error {
	if pe, ok := err.(*os.PathError); ok {
		return pe.Err
	}
	return err
}
