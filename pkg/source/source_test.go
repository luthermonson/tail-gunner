package source

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// CRLF-terminated input must never leak \r into emitted lines — neither on
// the initial dump path nor the follow path (nxadm strips only \n).
func TestCRLFStrippedOnBothPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crlf.log")
	if err := os.WriteFile(path, []byte("initial 1\r\ninitial 2\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stream := Open(Config{
		Files:   []string{path},
		Lines:   10,
		OnError: func(name string, err error) { t.Errorf("source error %s: %v", name, err) },
	})

	go func() {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		defer f.Close()
		for i := range 3 {
			fmt.Fprintf(f, "appended %d\r\n", i)
			time.Sleep(150 * time.Millisecond)
		}
	}()

	var got [][]byte
	deadline := time.After(5 * time.Second)
	for len(got) < 5 {
		select {
		case ln := <-stream.C:
			got = append(got, ln.Text)
		case <-deadline:
			t.Fatalf("timed out; received %d/5 lines: %q", len(got), got)
		}
	}
	for _, text := range got {
		if bytes.ContainsRune(text, '\r') {
			t.Errorf("line contains carriage return: %q", text)
		}
		if len(text) == 0 {
			t.Error("empty line emitted")
		}
	}
}
