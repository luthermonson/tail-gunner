# tail-gunner

> **It's `tail` until you need more.**

A drop-in replacement for `tail` that can be promoted — live, mid-stream —
into an interactive log viewer with filtering, search, and highlighting.
Stop ctrl-C-ing your `tail -f | grep` pipeline every time you want to change
the pattern.

```
tail -f app.log          you're watching logs fly by
   ⌄ press :             something looks wrong
:in ERROR                now you only see errors — including ones
                         that already scrolled past
   ⌄ press q             back to plain tail, stream never stopped
```

100% Go. Single static binary. MIT licensed.

## Why

The core pain: you run `tail -f app.log`, spot something, and now you want
`grep`. So you ctrl-C, lose your place, rebuild the pipeline, rerun, and wait
for the interesting lines to happen again — for every filter tweak.

tail-gunner keeps the producer running and makes the *view* editable. Every
line lands in an in-memory ring buffer (100k lines by default); filters are
a live view over that buffer. Changing a filter never restarts the stream
and never loses history.

## Install

```sh
go install github.com/luthermonson/tail-gunner/cmd/tail-gunner@latest
```

Or build from source:

```sh
git clone https://github.com/luthermonson/tail-gunner
cd tail-gunner
go build -o tail-gunner ./cmd/tail-gunner
```

Then, if you're bold:

```sh
alias tail=tail-gunner
```

The alias is safe: whenever stdout isn't a terminal (scripts, pipes,
redirects), tail-gunner behaves byte-for-byte like GNU tail — verified by a
golden test suite that diffs its output against the real thing
(`test/golden.sh`).

## Usage

Use it exactly like tail:

```sh
tail-gunner app.log                  # last 10 lines
tail-gunner -n 50 app.log            # last 50 lines
tail-gunner -n +10 app.log           # everything from line 10
tail-gunner -c 1K app.log            # last kilobyte
tail-gunner -f app.log               # follow
tail-gunner -F app.log               # follow by name, survive rotation
tail-gunner -f api.log worker.log    # multiple files with ==> headers
cat app.log | tail-gunner -n 5       # stdin works too
```

### tail-compatible flags

| Flag | Meaning |
|---|---|
| `-n, --lines=[+]NUM` | last NUM lines (default 10); `+NUM` starts at line NUM |
| `-c, --bytes=[+]NUM` | last NUM bytes; `+NUM` starts at byte NUM |
| `-f, --follow` | output appended data as the file grows |
| `-F` | same as `--follow=name --retry` (survives log rotation) |
| `-q, --quiet, --silent` | never print `==> file <==` headers |
| `-v, --verbose` | always print headers |
| `--pid=PID` | with `-f`, exit after process PID dies |
| `--retry` | keep trying to open an inaccessible file |
| `-s, --sleep-interval=N` | accepted for compatibility (polling backend manages its own cadence) |
| `--help`, `--version` | what you'd expect |

`NUM` accepts GNU multiplier suffixes: `b` (512), `K` (1024), `KB` (1000),
`M`, `MB`, `G`, `GB`. getopt forms work as you'd expect: `-n5`, `-fn20`,
`-fq`, flags after filenames, `--` to end flags.

Flags GNU tail has that tail-gunner doesn't implement fail loudly with an
error — never silently misbehave.

### tail-gunner flags

| Flag | Meaning |
|---|---|
| `--gun-buffer=NUM` | ring buffer capacity in lines (default 100000) |
| `--gun-debug[=FILE]` | write diagnostics to FILE (default `tail-gunner.debug.log`) — never to stdout/stderr |
| `--gun-no-promote` | disable the `:` promotion entirely; pure tail behavior |

## The three modes

1. **Pipe mode** — stdout is not a terminal. Byte-for-byte GNU tail.
   Scripts and downstream pipes never know the difference.
2. **Plain mode** — stdout is a terminal and you're following (`-f`).
   Looks and behaves exactly like tail, but a keystroke listener waits on
   the terminal. Press `:` to promote.
3. **Gunner mode** — the full-screen TUI. Filter, search, scroll, pipe the
   view through shell commands. Press `q` to drop back to plain mode; the
   stream never paused, and the lines you missed are replayed into your
   scrollback.

## Gunner mode

### Keys

| Key | Action |
|---|---|
| `:` | open the command palette |
| `/` | add a search (case-insensitive regex) — searches stack, each in its own color |
| `n` / `N` | next / previous hit of the **active** search |
| `f` | toggle follow (pin to bottom) |
| `↑`/`k`, `↓`/`j` | scroll one line (scrolling up unpins follow) |
| `PgUp`/`b`, `PgDn`/`space` | scroll a page |
| `Ctrl+u` / `Ctrl+d` | scroll half a page |
| `g` / `G` | jump to top / jump to bottom and re-follow |
| `q` / `Esc` | demote back to plain tail |
| `Q` | quit tail-gunner entirely |

### Palette commands

Press `:` then type:

| Command | Action |
|---|---|
| `:in <regex>` | keep only matching lines (multiple `:in` rules OR together) |
| `:out <regex>` | drop matching lines — gun down the noise |
| `:hl <regex>` | highlight matches without filtering |
| `:filters` | open the interactive filter panel (below) |
| `:rm <N>` | remove filter number N without opening the panel |
| `:clear` | drop all filters and the search |
| `:/<regex>` | add a search (same as `/`) |
| `:searches` | open the interactive search panel (below) |
| `:f` | toggle follow |
| `:w <file>` | write the current filtered view to a file |
| `:\| <command>` | pipe the current view through a shell command, view the output (`:\| grep -c ERROR`, `:\| jq .msg`) |
| `:q` | demote back to plain tail |
| `:Q` | quit |

### Filter panel (`:filters`)

| Key | Action |
|---|---|
| `↑`/`k`, `↓`/`j` | select a filter |
| `Enter` | edit the selected filter (opens the palette prefilled; submit replaces it) |
| `Del` / `x` | remove the selected filter |
| `Space` | toggle the selected filter on/off without losing it |
| `Esc` / `q` | close the panel |

### Search panel (`:searches`)

Searches stack: every `/pattern` adds a saved search highlighted in its own
color, and the newest becomes the *active* one (marked `●`) that `n`/`N`
navigate.

| Key | Action |
|---|---|
| `↑`/`k`, `↓`/`j` | select a search |
| `Enter` | make the selected search active and jump to its next hit |
| `e` | edit the selected search (opens the palette prefilled; submit replaces it) |
| `Del` / `x` | remove the selected search |
| `Space` | toggle its highlighting on/off |
| `Esc` / `q` | close the panel |

Filters apply to the **entire buffer**, not just new lines — `:in ERROR`
shows errors that scrolled past before you typed it. Filters survive
demotion: back in plain mode, the stream stays filtered until you `:clear`.

Lines are auto-colored by severity (ERROR red, WARN yellow, DEBUG dim), and
the status bar shows `shown/total` line counts, follow state, and active
filter chips.

## Try it

Terminal 1 — generate a noisy log:

```powershell
# PowerShell
$levels = 'INFO','INFO','INFO','DEBUG','WARN','ERROR'
while ($true) {
  Add-Content app.log "$(Get-Date -Format 'HH:mm:ss.fff') $($levels | Get-Random) request id=$(Get-Random -Max 9999)"
  Start-Sleep -Milliseconds (Get-Random -Min 50 -Max 400)
}
```

```sh
# bash
while true; do
  echo "$(date +%T) $(shuf -n1 -e INFO INFO INFO DEBUG WARN ERROR) request id=$RANDOM" >> app.log
  sleep 0.$((RANDOM % 4))
done
```

Terminal 2:

```sh
tail-gunner -f app.log
# press :    then type    in ERROR
# press :    then type    hl id=42
# press q    to go back to plain tail
```

## Using it as a k9s plugin

tail-gunner makes a great log viewer inside [k9s](https://k9scli.io):
select a pod, hit `Ctrl-L`, and you're in tail-gunner on the live stream —
filter out the noise with `:out`, stack colored searches, then `Ctrl-C`
back into k9s. A plugin with `background: false` suspends the k9s UI and
hands tail-gunner the real terminal, so `:` promotion and all the
interactive features just work.

Setup:

1. Have `tail-gunner` and `kubectl` on your PATH.
2. Find your k9s config directory with `k9s info` — `plugins.yaml` lives
   next to `config.yaml` (`~/.config/k9s/` on Linux/macOS,
   `%LOCALAPPDATA%\k9s\` on Windows).
3. Paste the config below into `plugins.yaml` and restart k9s.
4. In the pod or container view you should see `<ctrl-l> tail-gunner logs`
   in the header menu.

> Heads up if you're on an old k9s (≤ v0.26): the file is `plugin.yml` —
> singular — with a top-level `plugin:` key, same entries otherwise. Also
> pick your shortcut carefully: keys like `Ctrl-G` are silently shadowed by
> k9s built-ins; `Ctrl-L` is free.

```yaml
plugins:
  # Ctrl-L in pod view: stream pod logs through tail-gunner
  tail-gunner:
    shortCut: Ctrl-L
    description: tail-gunner logs
    scopes:
      - po
    command: sh        # on Windows use `command: cmd` with `/c`
    background: false
    args:
      - -c
      - kubectl logs -f --tail=500 $NAME -n $NAMESPACE --context $CONTEXT --kubeconfig $KUBECONFIG --all-containers=true 2>&1 | tail-gunner -f

  # same shortcut inside a pod's container view: just that container
  tail-gunner-container:
    shortCut: Ctrl-L
    description: tail-gunner logs
    scopes:
      - containers
    command: sh
    background: false
    args:
      - -c
      - kubectl logs -f --tail=500 $POD -c $NAME -n $NAMESPACE --context $CONTEXT --kubeconfig $KUBECONFIG 2>&1 | tail-gunner -f
```

`--kubeconfig $KUBECONFIG` pins kubectl to the exact config k9s is connected
with (k9s and your shell may disagree), and `2>&1` routes kubectl errors
into tail-gunner where you can see them instead of hanging a blank screen.

Then in k9s: select a pod, hit `Ctrl-L`, and you're tailing — press `:` to
start gunning down noise (`:out healthcheck`), `Ctrl-C` to drop back into
k9s. The shell wrapper (`sh -c` / `powershell -Command`) is required because
k9s execs commands directly and the kubectl→tail-gunner pipe needs a shell.
`kubectl` and `tail-gunner` must both be on your PATH.

## Using it as a library

Everything lives under `pkg/` and is importable — the follow engine
(`pkg/source`), the ring buffer (`pkg/buffer`), the filter stack
(`pkg/filter`), and the last-N-lines mechanics (`pkg/tailio`) are designed
for embedding. Pre-v1.0, APIs may change between minor versions.

```go
import "github.com/luthermonson/tail-gunner/pkg/source"

stream := source.Open(source.Config{
    Files: []string{"app.log"},
    Lines: 10,
    OnError: func(name string, err error) { log.Println(name, err) },
})
for line := range stream.C {
    // line.Text, line.File
}
```

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the full design.

## Development

```sh
go test ./...          # unit tests
bash test/golden.sh    # byte-diff pipe mode against real GNU tail
```

The golden suite is the drop-in contract: 24 scenarios (last-N, +N forms,
byte mode, multi-file headers, missing files, stdin, no-trailing-newline,
follow) must produce output byte-identical to GNU tail, exit codes included.

## Known limitations (v1)

- Following a file, a partial line (no newline yet) isn't shown until the
  newline arrives — GNU tail shows partial data.
- `-s` is accepted but the polling backend manages its own cadence.
- On network filesystems (NFS/SMB), fs-notification can miss remote writes
  on Linux/macOS; Windows always polls and is unaffected.
- Timestamp parsing, format detection, and merge-by-time are deliberately
  out of scope for v1 — that's [lnav](https://lnav.org) territory.

## License

MIT
