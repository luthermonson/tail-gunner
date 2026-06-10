// tail-gunner: it's tail until you need more.
package main

import (
	"fmt"
	"os"

	"github.com/luthermonson/tail-gunner/pkg/app"
	"github.com/luthermonson/tail-gunner/pkg/cli"
	"github.com/luthermonson/tail-gunner/pkg/diag"
	"github.com/luthermonson/tail-gunner/pkg/tailcompat"
	"github.com/luthermonson/tail-gunner/pkg/term"
)

var version = "dev"

func main() {
	opts, err := cli.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "tail-gunner: %v\nTry 'tail-gunner --help' for more information.\n", err)
		os.Exit(1)
	}
	if opts.ShowHelp {
		fmt.Print(cli.Usage)
		return
	}
	if opts.ShowVersion {
		fmt.Printf("tail-gunner %s\n", version)
		return
	}

	if opts.DebugFile != "" {
		if err := diag.Init(opts.DebugFile); err != nil {
			fmt.Fprintf(os.Stderr, "tail-gunner: cannot open debug log: %v\n", err)
			os.Exit(1)
		}
	}

	// Interactive (promotable) only when: following line-oriented output to
	// a real terminal and not explicitly disabled. Everything else takes the
	// byte-faithful tailcompat path — that is the drop-in contract.
	interactive := opts.Follow &&
		!opts.NoPromote &&
		!opts.BytesSet &&
		term.IsTTY(os.Stdout)

	diag.L().Info("start",
		"version", version,
		"files", opts.Files,
		"follow", opts.Follow,
		"followName", opts.FollowName,
		"interactive", interactive,
	)

	if !interactive {
		os.Exit(tailcompat.Run(opts, os.Stdout, os.Stderr))
	}
	os.Exit(app.Run(opts))
}
