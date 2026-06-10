package cli

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		args  []string
		check func(o Options) bool
		err   bool
	}{
		{[]string{}, func(o Options) bool { return o.Lines == 10 && o.Stdin() }, false},
		{[]string{"-n", "5", "app.log"}, func(o Options) bool { return o.Lines == 5 && len(o.Files) == 1 }, false},
		{[]string{"-n5", "app.log"}, func(o Options) bool { return o.Lines == 5 }, false},
		{[]string{"-n", "+3"}, func(o Options) bool { return o.Lines == 3 && o.LinesFromStart }, false},
		{[]string{"--lines=20"}, func(o Options) bool { return o.Lines == 20 }, false},
		{[]string{"-c", "1K"}, func(o Options) bool { return o.BytesSet && o.Bytes == 1024 }, false},
		{[]string{"-c", "2b"}, func(o Options) bool { return o.Bytes == 1024 }, false},
		{[]string{"-fq", "a", "b"}, func(o Options) bool { return o.Follow && o.Quiet && len(o.Files) == 2 }, false},
		{[]string{"-F", "a"}, func(o Options) bool { return o.Follow && o.FollowName && o.Retry }, false},
		{[]string{"--follow=name", "--retry", "a"}, func(o Options) bool { return o.FollowName }, false},
		{[]string{"-fn20", "a"}, func(o Options) bool { return o.Follow && o.Lines == 20 }, false},
		{[]string{"--pid=123", "-f"}, func(o Options) bool { return o.PID == 123 }, false},
		{[]string{"--gun-buffer=5000"}, func(o Options) bool { return o.BufferCap == 5000 }, false},
		{[]string{"--gun-no-promote"}, func(o Options) bool { return o.NoPromote }, false},
		{[]string{"--", "-n"}, func(o Options) bool { return len(o.Files) == 1 && o.Files[0] == "-n" }, false},
		{[]string{"a.log", "-n", "2"}, func(o Options) bool { return o.Lines == 2 && len(o.Files) == 1 && o.Files[0] == "a.log" }, false}, // GNU permutes
		{[]string{"--follow", "a"}, func(o Options) bool { return o.Follow && !o.FollowName && len(o.Files) == 1 }, false},
		{[]string{"-", "x"}, func(o Options) bool { return o.Stdin() && len(o.Files) == 2 }, false},
		{[]string{"--zero-terminated"}, nil, true},
		{[]string{"-x"}, nil, true},
		{[]string{"-n", "abc"}, nil, true},
		{[]string{"-n"}, nil, true},
		{[]string{"-c", "5X"}, nil, true},
	}
	for i, c := range cases {
		o, err := Parse(c.args)
		if c.err {
			if err == nil {
				t.Errorf("case %d %v: expected error", i, c.args)
			}
			continue
		}
		if err != nil {
			t.Errorf("case %d %v: %v", i, c.args, err)
			continue
		}
		if !c.check(o) {
			t.Errorf("case %d %v: check failed: %+v", i, c.args, o)
		}
	}
}
