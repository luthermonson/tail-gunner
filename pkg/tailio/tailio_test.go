package tailio

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"
)

func lastN(t *testing.T, content string, n int64) string {
	t.Helper()
	r := bytes.NewReader([]byte(content))
	off, err := LastNLinesOffset(r, n)
	if err != nil {
		t.Fatalf("LastNLinesOffset: %v", err)
	}
	return content[off:]
}

func TestLastNLinesOffset(t *testing.T) {
	cases := []struct {
		content string
		n       int64
		want    string
	}{
		{"a\nb\nc\n", 2, "b\nc\n"},
		{"a\nb\nc", 2, "b\nc"},         // no trailing newline: fragment is a line
		{"a\nb\nc\n", 10, "a\nb\nc\n"}, // n > lines: whole file
		{"a\nb\nc\n", 0, ""},
		{"", 5, ""},
		{"single", 1, "single"},
		{"a\n", 1, "a\n"},
		{"\n\n\n", 2, "\n\n"},
		{strings.Repeat("x", 40000) + "\nend\n", 1, "end\n"}, // spans block boundary
	}
	for i, c := range cases {
		if got := lastN(t, c.content, c.n); got != c.want {
			t.Errorf("case %d: got %q, want %q", i, got, c.want)
		}
	}
}

func TestSkipLines(t *testing.T) {
	br := bufio.NewReader(strings.NewReader("1\n2\n3\n4\n"))
	if err := SkipLines(br, 3); err != nil {
		t.Fatal(err)
	}
	rest, _ := io.ReadAll(br)
	if string(rest) != "3\n4\n" {
		t.Fatalf("got %q", rest)
	}

	br = bufio.NewReader(strings.NewReader("1\n2\n"))
	if err := SkipLines(br, 99); err != nil {
		t.Fatal(err)
	}
	rest, _ = io.ReadAll(br)
	if string(rest) != "" {
		t.Fatalf("skip past EOF: got %q", rest)
	}
}

func TestStreamLastNLines(t *testing.T) {
	lines, err := StreamLastNLines(strings.NewReader("a\nb\nc\nd"), 2)
	if err != nil {
		t.Fatal(err)
	}
	got := string(bytes.Join(lines, nil))
	if got != "c\nd" {
		t.Fatalf("got %q", got)
	}
	lines, _ = StreamLastNLines(strings.NewReader(""), 3)
	if len(lines) != 0 {
		t.Fatalf("empty input: got %v", lines)
	}
}

func TestStreamLastNBytes(t *testing.T) {
	got, err := StreamLastNBytes(strings.NewReader("abcdefghij"), 4)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ghij" {
		t.Fatalf("got %q", got)
	}
	// across multiple reads with partial trim
	var src bytes.Buffer
	src.WriteString(strings.Repeat("ab", 40000))
	got, _ = StreamLastNBytes(&src, 5)
	if string(got) != "babab" {
		t.Fatalf("got %q", got)
	}
}
