// Package source feeds the interactive path: it emits each file's initial
// tail portion in GNU order (file by file), then follows all inputs
// concurrently, fanning everything into one channel tagged by file index.
package source

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"runtime"

	"github.com/nxadm/tail"

	"github.com/luthermonson/tail-gunner/pkg/diag"
	"github.com/luthermonson/tail-gunner/pkg/tailio"
)

type Line struct {
	File int // index into Names; identifies origin
	Text []byte
}

type Config struct {
	Files          []string // "-" means stdin
	Lines          int64
	LinesFromStart bool
	FollowName     bool
	OnError        func(name string, err error)
}

type Stream struct {
	Names []string
	C     <-chan Line
}

const stdinName = "standard input"

// Open starts ingestion. Initial portions are emitted sequentially per file;
// follow output then interleaves as it arrives. The channel never closes
// while following (mirrors tail -f).
func Open(cfg Config) *Stream {
	files := cfg.Files
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
	out := make(chan Line, 512)
	s := &Stream{Names: names, C: out}

	go func() {
		offsets := make([]int64, len(files))
		for i, path := range files {
			if path == "-" {
				continue
			}
			off, err := emitInitial(out, i, path, cfg)
			diag.L().Debug("initial dump", "file", path, "resumeOffset", off, "err", err)
			if err != nil {
				cfg.OnError(names[i], err)
				offsets[i] = -1
				if !cfg.FollowName {
					continue
				}
				off = 0
			}
			offsets[i] = off
		}
		for i, path := range files {
			if path == "-" {
				go followStdin(out, i)
				continue
			}
			if offsets[i] < 0 && !cfg.FollowName {
				continue
			}
			go followFile(out, i, path, offsets[i], cfg)
		}
	}()
	return s
}

// emitInitial sends the requested starting portion of one file and returns
// the offset where following should resume.
func emitInitial(out chan<- Line, idx int, path string, cfg Config) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	var start int64
	if cfg.LinesFromStart {
		start = 0
	} else {
		start, err = tailio.LastNLinesOffset(f, cfg.Lines)
		if err != nil {
			return 0, err
		}
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return 0, err
	}
	br := bufio.NewReaderSize(f, 64*1024)
	if cfg.LinesFromStart {
		if err := tailio.SkipLines(br, cfg.Lines); err != nil {
			return 0, err
		}
	}
	read := int64(0)
	for {
		line, err := br.ReadBytes('\n')
		read += int64(len(line))
		if len(line) > 0 {
			out <- Line{File: idx, Text: chomp(line)}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return start + read, err
		}
	}
	pos, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return start + read, nil
	}
	return pos - int64(br.Buffered()), nil
}

func followFile(out chan<- Line, idx int, path string, offset int64, cfg Config) {
	tf, err := tail.TailFile(path, tail.Config{
		Location:  &tail.SeekInfo{Offset: offset, Whence: io.SeekStart},
		Follow:    true,
		ReOpen:    cfg.FollowName,
		MustExist: !cfg.FollowName,
		Poll:      runtime.GOOS == "windows",
		Logger:    diag.StdLogger(),
	})
	if err != nil {
		diag.L().Error("follow failed", "file", path, "err", err)
		cfg.OnError(path, err)
		return
	}
	diag.L().Debug("following", "file", path, "offset", offset, "reopen", cfg.FollowName)
	for ln := range tf.Lines {
		if ln.Err != nil {
			continue
		}
		// nxadm strips only \n; CRLF input leaves a trailing \r that
		// corrupts TUI row rendering
		out <- Line{File: idx, Text: bytes.TrimSuffix([]byte(ln.Text), []byte("\r"))}
	}
}

func followStdin(out chan<- Line, idx int) {
	br := bufio.NewReaderSize(os.Stdin, 64*1024)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			out <- Line{File: idx, Text: chomp(line)}
		}
		if err != nil {
			return
		}
	}
}

// chomp strips the line terminator (\n or \r\n) and returns a copy safe to
// retain.
func chomp(line []byte) []byte {
	line = bytes.TrimSuffix(line, []byte("\n"))
	line = bytes.TrimSuffix(line, []byte("\r"))
	cp := make([]byte, len(line))
	copy(cp, line)
	return cp
}
