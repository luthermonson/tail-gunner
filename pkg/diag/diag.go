// Package diag is tail-gunner's diagnostic logger. It is opt-in
// (--gun-debug) and writes to a side file, never stdout or stderr: stdout is
// the product (byte-faithful in pipe mode) and gunner mode owns the screen,
// so any stray write would corrupt one or the other. User-facing GNU-format
// error messages stay on stderr via fmt — they are compat surface, not logs.
package diag

import (
	"io"
	"log"
	"log/slog"
	"os"
)

var (
	handler slog.Handler = slog.NewTextHandler(io.Discard, nil)
	logger               = slog.New(handler)
)

// Init routes diagnostics to the given file (append, created if missing).
// Call once from main before any goroutines start; not safe to call later.
func Init(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	handler = slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger = slog.New(handler)
	return nil
}

// L returns the diagnostic logger. Discards unless Init was called.
func L() *slog.Logger { return logger }

// StdLogger bridges to a *log.Logger for dependencies that want one
// (nxadm/tail's rotation/reopen events surface through this).
func StdLogger() *log.Logger {
	return slog.NewLogLogger(handler, slog.LevelDebug)
}
