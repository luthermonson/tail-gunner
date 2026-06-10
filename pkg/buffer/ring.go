// Package buffer holds the append-only ring of ingested lines. Single
// writer (the ingest goroutine), multiple readers (UI, demote replay).
// Lines are addressed by monotonic sequence number, which stays valid
// across ring eviction.
package buffer

import (
	"sync"
	"time"
)

type Line struct {
	Seq  uint64
	File int // index into the file list; -1 for stdin
	Time time.Time
	Text []byte
}

type Ring struct {
	mu    sync.RWMutex
	buf   []Line
	start int // position of the oldest line
	count int
	first uint64 // seq of the oldest retained line
	next  uint64 // seq the next appended line will get
}

func NewRing(capacity int) *Ring {
	if capacity < 1 {
		capacity = 1
	}
	return &Ring{buf: make([]Line, capacity)}
}

// Append stores text (copied) and returns its assigned seq.
func (r *Ring) Append(file int, text []byte) uint64 {
	cp := make([]byte, len(text))
	copy(cp, text)
	r.mu.Lock()
	seq := r.next
	r.next++
	ln := Line{Seq: seq, File: file, Time: time.Now(), Text: cp}
	if r.count < len(r.buf) {
		r.buf[(r.start+r.count)%len(r.buf)] = ln
		r.count++
	} else {
		r.buf[r.start] = ln
		r.start = (r.start + 1) % len(r.buf)
		r.first++
	}
	r.mu.Unlock()
	return seq
}

// Bounds returns the seq range currently retained: [first, next).
func (r *Ring) Bounds() (first, next uint64) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.first, r.next
}

// Get returns the line with the given seq, if still retained.
func (r *Ring) Get(seq uint64) (Line, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if seq < r.first || seq >= r.next {
		return Line{}, false
	}
	return r.buf[(r.start+int(seq-r.first))%len(r.buf)], true
}

// Range calls fn for each retained line with seq >= from, in order, until fn
// returns false. fn must not retain the Line's Text slice across calls.
func (r *Ring) Range(from uint64, fn func(Line) bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if from < r.first {
		from = r.first
	}
	for seq := from; seq < r.next; seq++ {
		if !fn(r.buf[(r.start+int(seq-r.first))%len(r.buf)]) {
			return
		}
	}
}

func (r *Ring) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.count
}
