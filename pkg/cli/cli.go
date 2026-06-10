// Package cli parses the tail-compatible flag surface plus tail-gunner's
// own --gun-* namespace, built on urfave/cli. Anything GNU tail accepts that
// we don't implement fails loudly rather than silently misbehaving.
//
// urfave (like Go flag libraries generally) doesn't speak getopt: GNU tail
// accepts attached short values (-n5, -fn20), flags after file operands
// (tail f.log -n 2), and an optional value on --follow. normalize() rewrites
// argv into the canonical form urfave parses; it classifies tokens only —
// all flag semantics live in the urfave declarations below.
package cli

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	ucli "github.com/urfave/cli/v3"
)

type Options struct {
	Files []string

	Lines          int64 // -n
	LinesFromStart bool  // -n +N
	Bytes          int64 // -c
	BytesFromStart bool  // -c +N
	BytesSet       bool

	Follow     bool // -f
	FollowName bool // -F / --follow=name --retry
	Retry      bool

	Quiet   bool // -q
	Verbose bool // -v

	PID           int
	SleepInterval time.Duration

	// tail-gunner extensions
	BufferCap int    // --gun-buffer
	NoPromote bool   // --gun-no-promote
	DebugFile string // --gun-debug

	ShowHelp    bool
	ShowVersion bool
}

const Usage = `Usage: tail-gunner [OPTION]... [FILE]...
Print the last 10 lines of each FILE to standard output. It's tail until you
need more: when following in a terminal, press ':' to open the gunner TUI
with live filtering and search.

tail-compatible options:
  -c, --bytes=[+]NUM       output the last NUM bytes; +NUM outputs starting
                           with byte NUM
  -f, --follow             output appended data as the file grows
  -F                       same as --follow=name --retry
  -n, --lines=[+]NUM       output the last NUM lines (default 10); +NUM
                           outputs starting with line NUM
      --pid=PID            with -f, terminate after process PID dies
  -q, --quiet, --silent    never output headers giving file names
      --retry              keep trying to open a file if it is inaccessible
  -s, --sleep-interval=N   with -f, poll interval (accepted; polling backend
                           manages its own cadence)
  -v, --verbose            always output headers giving file names
      --help               display this help and exit
      --version            output version information and exit

tail-gunner options:
      --gun-buffer=NUM     ring buffer capacity in lines (default 100000)
      --gun-debug[=FILE]   write diagnostics to FILE (default
                           tail-gunner.debug.log); never stdout/stderr
      --gun-no-promote     disable the ':' TUI promotion; pure tail behavior

NUM may have a multiplier suffix: b=512, K=1024, KB=1000, M, MB, G, GB.
With no FILE, or when FILE is -, read standard input.
`

var multipliers = map[string]int64{
	"":   1,
	"b":  512,
	"K":  1024,
	"KB": 1000,
	"k":  1024,
	"M":  1024 * 1024,
	"MB": 1000 * 1000,
	"G":  1024 * 1024 * 1024,
	"GB": 1000 * 1000 * 1000,
}

func parseNum(s string) (n int64, fromStart bool, err error) {
	if strings.HasPrefix(s, "+") {
		fromStart = true
		s = s[1:]
	}
	digits := s
	suffix := ""
	for i, c := range s {
		if c < '0' || c > '9' {
			digits, suffix = s[:i], s[i:]
			break
		}
	}
	if digits == "" {
		return 0, false, fmt.Errorf("invalid number of lines/bytes: %q", s)
	}
	mult, ok := multipliers[suffix]
	if !ok {
		return 0, false, fmt.Errorf("invalid multiplier suffix %q in %q", suffix, s)
	}
	v, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("invalid number: %q", s)
	}
	return v * mult, fromStart, nil
}

// shortTakesValue lists short flags that consume a value, getopt-style.
const shortTakesValue = "ncs"

// longTakesValue lists long flags that consume the following argument when
// the '=' form isn't used.
var longTakesValue = []string{
	"--lines", "--bytes", "--pid", "--sleep-interval", "--gun-buffer",
}

// normalize rewrites GNU getopt argv forms into canonical "--flag value"
// tokens with all flags ahead of operands (GNU permutes; urfave doesn't).
func normalize(args []string) ([]string, error) {
	var flags, operands []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			operands = append(operands, args[i+1:]...)
			i = len(args)

		case arg == "-" || !strings.HasPrefix(arg, "-"):
			operands = append(operands, arg)

		case strings.HasPrefix(arg, "--"):
			if arg == "--follow" {
				flags = append(flags, "--follow=descriptor")
				continue
			}
			if arg == "--gun-debug" {
				flags = append(flags, "--gun-debug=tail-gunner.debug.log")
				continue
			}
			flags = append(flags, arg)
			if !strings.Contains(arg, "=") && slices.Contains(longTakesValue, arg) {
				i++
				if i >= len(args) {
					return nil, fmt.Errorf("option %s requires an argument", arg)
				}
				flags = append(flags, args[i])
			}

		default: // short cluster: split -fn20 into -f -n 20
			rest := arg[1:]
			for len(rest) > 0 {
				c := rest[0]
				rest = rest[1:]
				if c == 'f' {
					// -f must not be an alias of the --follow string flag:
					// urfave would eat the next token as its value
					flags = append(flags, "--follow=descriptor")
					continue
				}
				flags = append(flags, "-"+string(c))
				if !strings.ContainsRune(shortTakesValue, rune(c)) {
					continue
				}
				val := rest
				rest = ""
				if val == "" {
					i++
					if i >= len(args) {
						return nil, fmt.Errorf("option -%c requires an argument", c)
					}
					val = args[i]
				}
				flags = append(flags, val)
			}
		}
	}
	if len(operands) > 0 {
		// explicit terminator so urfave never re-parses operands like "-n"
		flags = append(flags, "--")
		flags = append(flags, operands...)
	}
	return flags, nil
}

// Parse processes args (without the program name).
func Parse(args []string) (Options, error) {
	o := Options{Lines: 10, BufferCap: 100000, SleepInterval: time.Second}

	norm, err := normalize(args)
	if err != nil {
		return o, err
	}

	cmd := &ucli.Command{
		Name:        "tail-gunner",
		HideHelp:    true,
		HideVersion: true,
		Flags: []ucli.Flag{
			&ucli.StringFlag{Name: "lines", Aliases: []string{"n"}, Value: "10"},
			&ucli.StringFlag{Name: "bytes", Aliases: []string{"c"}},
			&ucli.StringFlag{Name: "follow"},
			&ucli.BoolFlag{Name: "F"},
			&ucli.BoolFlag{Name: "retry"},
			&ucli.BoolFlag{Name: "quiet", Aliases: []string{"q", "silent"}},
			&ucli.BoolFlag{Name: "verbose", Aliases: []string{"v"}},
			&ucli.IntFlag{Name: "pid"},
			&ucli.Float64Flag{Name: "sleep-interval", Aliases: []string{"s"}, Value: 1},
			&ucli.StringFlag{Name: "gun-buffer"},
			&ucli.StringFlag{Name: "gun-debug"},
			&ucli.BoolFlag{Name: "gun-no-promote"},
			&ucli.BoolFlag{Name: "help"},
			&ucli.BoolFlag{Name: "version"},
		},
		Action: func(_ context.Context, cmd *ucli.Command) error {
			o.Files = cmd.Args().Slice()

			if o.ShowHelp = cmd.Bool("help"); o.ShowHelp {
				return nil
			}
			if o.ShowVersion = cmd.Bool("version"); o.ShowVersion {
				return nil
			}

			if o.Lines, o.LinesFromStart, err = parseNum(cmd.String("lines")); err != nil {
				return err
			}
			if b := cmd.String("bytes"); b != "" {
				if o.Bytes, o.BytesFromStart, err = parseNum(b); err != nil {
					return err
				}
				o.BytesSet = true
			}
			switch f := cmd.String("follow"); f {
			case "":
			case "descriptor":
				o.Follow = true
			case "name":
				o.Follow, o.FollowName = true, true
			default:
				return fmt.Errorf("invalid argument %q for --follow", f)
			}
			if cmd.Bool("F") {
				o.Follow, o.FollowName, o.Retry = true, true, true
			}
			o.Retry = o.Retry || cmd.Bool("retry")
			if o.Retry && o.Follow {
				o.FollowName = true
			}
			o.Quiet = cmd.Bool("quiet")
			o.Verbose = cmd.Bool("verbose")
			o.PID = int(cmd.Int("pid"))
			o.SleepInterval = time.Duration(cmd.Float64("sleep-interval") * float64(time.Second))
			if gb := cmd.String("gun-buffer"); gb != "" {
				n, _, err := parseNum(gb)
				if err != nil || n < 1 {
					return fmt.Errorf("invalid --gun-buffer: %q", gb)
				}
				o.BufferCap = int(n)
			}
			o.NoPromote = cmd.Bool("gun-no-promote")
			o.DebugFile = cmd.String("gun-debug")
			return nil
		},
	}

	if err := cmd.Run(context.Background(), append([]string{"tail-gunner"}, norm...)); err != nil {
		return o, err
	}
	return o, nil
}

// Stdin reports whether input comes from standard input.
func (o Options) Stdin() bool {
	return len(o.Files) == 0 || slices.Contains(o.Files, "-")
}
