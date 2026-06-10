package buffer

import (
	"fmt"
	"testing"
)

func TestRingEviction(t *testing.T) {
	r := NewRing(3)
	for i := 0; i < 5; i++ {
		r.Append(-1, fmt.Appendf(nil, "line%d", i))
	}
	first, next := r.Bounds()
	if first != 2 || next != 5 {
		t.Fatalf("bounds = [%d,%d), want [2,5)", first, next)
	}
	if _, ok := r.Get(1); ok {
		t.Fatal("evicted seq should be gone")
	}
	ln, ok := r.Get(2)
	if !ok || string(ln.Text) != "line2" {
		t.Fatalf("Get(2) = %q,%v", ln.Text, ok)
	}

	var got []string
	r.Range(0, func(l Line) bool {
		got = append(got, string(l.Text))
		return true
	})
	want := []string{"line2", "line3", "line4"}
	if len(got) != len(want) {
		t.Fatalf("Range returned %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Range[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAppendCopiesText(t *testing.T) {
	r := NewRing(4)
	src := []byte("mutable")
	r.Append(0, src)
	src[0] = 'X'
	ln, _ := r.Get(0)
	if string(ln.Text) != "mutable" {
		t.Fatal("Append must copy the text")
	}
}

func TestRangeEarlyStop(t *testing.T) {
	r := NewRing(10)
	for i := 0; i < 5; i++ {
		r.Append(-1, []byte{byte('a' + i)})
	}
	n := 0
	r.Range(0, func(Line) bool { n++; return n < 2 })
	if n != 2 {
		t.Fatalf("early stop visited %d lines", n)
	}
}
