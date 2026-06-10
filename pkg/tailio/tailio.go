// Package tailio implements the low-level "where do the last N lines start"
// mechanics shared by pipe mode and interactive mode. GNU semantics: a final
// fragment with no trailing newline counts as a line.
package tailio

import (
	"bufio"
	"io"
)

const blockSize = 32 * 1024

// LastNLinesOffset returns the byte offset where the last n lines of f begin.
// f's read position is left undefined; callers seek before use.
func LastNLinesOffset(f io.ReadSeeker, n int64) (int64, error) {
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, err
	}
	if size == 0 || n <= 0 {
		if n <= 0 {
			return size, nil
		}
		return 0, nil
	}

	scanEnd := size
	last := make([]byte, 1)
	if _, err := f.Seek(size-1, io.SeekStart); err != nil {
		return 0, err
	}
	if _, err := io.ReadFull(f, last); err != nil {
		return 0, err
	}
	if last[0] == '\n' {
		scanEnd = size - 1
	}

	var count int64
	buf := make([]byte, blockSize)
	pos := scanEnd
	for pos > 0 {
		readLen := int64(blockSize)
		if pos < readLen {
			readLen = pos
		}
		pos -= readLen
		if _, err := f.Seek(pos, io.SeekStart); err != nil {
			return 0, err
		}
		if _, err := io.ReadFull(f, buf[:readLen]); err != nil {
			return 0, err
		}
		for i := readLen - 1; i >= 0; i-- {
			if buf[i] == '\n' {
				count++
				if count == n {
					return pos + i + 1, nil
				}
			}
		}
	}
	return 0, nil
}

// SkipLines discards lines from r until n-1 newlines have passed, so the
// next byte read is the start of line n (1-based, GNU `-n +N`).
func SkipLines(r *bufio.Reader, n int64) error {
	for skipped := int64(0); skipped < n-1; skipped++ {
		if _, err := r.ReadBytes('\n'); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
	return nil
}

// StreamLastNLines reads r to EOF keeping only the last n lines, returned
// with their terminators intact (final fragment kept as-is).
func StreamLastNLines(r io.Reader, n int64) ([][]byte, error) {
	if n <= 0 {
		_, err := io.Copy(io.Discard, r)
		return nil, err
	}
	br := bufio.NewReaderSize(r, blockSize)
	ring := make([][]byte, 0, n)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if int64(len(ring)) == n {
				copy(ring, ring[1:])
				ring = ring[:n-1]
			}
			ring = append(ring, line)
		}
		if err == io.EOF {
			return ring, nil
		}
		if err != nil {
			return ring, err
		}
	}
}

// StreamLastNBytes reads r to EOF keeping only the last n bytes.
func StreamLastNBytes(r io.Reader, n int64) ([]byte, error) {
	if n <= 0 {
		_, err := io.Copy(io.Discard, r)
		return nil, err
	}
	keep := make([]byte, 0, n)
	buf := make([]byte, blockSize)
	for {
		m, err := r.Read(buf)
		if m > 0 {
			chunk := buf[:m]
			if int64(m) >= n {
				keep = append(keep[:0], chunk[int64(m)-n:]...)
			} else if int64(len(keep)+m) <= n {
				keep = append(keep, chunk...)
			} else {
				over := int64(len(keep)+m) - n
				keep = keep[:copy(keep, keep[over:])]
				keep = append(keep, chunk...)
			}
		}
		if err == io.EOF {
			return keep, nil
		}
		if err != nil {
			return keep, err
		}
	}
}
