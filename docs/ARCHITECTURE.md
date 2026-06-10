# tail-gunner — Architecture

> **It's tail until you need more.**
>
> A drop-in replacement for `tail` that can be promoted, live, into an
> interactive log viewer with filtering, search, and highlighting — without
> ctrl-C-ing and rerunning your pipeline.

```
brew install tail-gunner
alias tail=tail-gunner
```

## 1. Vision

The core pain: you run `tail -f app.log`, spot something, and now you want
`| grep`. So you ctrl-C, lose your place, rebuild the pipeline, rerun, wait
for the interesting lines to happen again. Repeat for every filter tweak.

tail-gunner keeps the producer running and makes the *view* editable. All
lines land in an in-memory ring buffer; filters are a live view over that
buffer. Changing the filter never restarts the stream and never loses
history.

### Design pillars

1. **Drop-in safe.** Byte-for-byte tail-compatible when non-interactive.
   Aliasing must never break a script.
2. **Passive until summoned.** In a TTY it looks and behaves like tail —
   plain scrollback, no alternate screen — until the user presses `:`.
3. **The buffer is the product.** Ingestion is decoupled from rendering;
   filters re-project the buffer instantly, including lines that arrived
   before the filter existed.
4. **Polite pipe citizen.** Reads stdin, degrades to a pure filter when
   stdout is not a TTY, composes upstream and downstream.

### Prior art and what we take from each

| Tool | Lesson taken | Why it isn't this tool |
|---|---|---|
| `lnav` | Feature ceiling: filter-in/out, merge, jump-to-error | Full-screen always, not flag-compatible, not a pipe component |
| `less +F` | The interaction model: follow passively, break out on demand | No live filtering, no stream ergonomics |
| `up` (Ultimate Plumber) | Buffer stdin once, re-apply filters live; `:|` shell escape hatch | Batch-minded, shell-subprocess per keystroke, not a drop-in |
| `tailspin` | Automatic highlighting is table stakes; compose politely | Highlighting only, no interactivity |
| `fzf` | Go can feel instant on million-line buffers | Fuzzy finder, not a tail |

The unclaimed niche: **tail-compatible single binary + live re-filterable
follow + command palette.**

### Decisions (settled)

- **Language: Go, 100%.** No cgo, no FFI, no vendored C. Rationale:
  `nxadm/tail` delivers the hard follow-engine correctness as a
  channel-of-lines library; channels → ring buffer → `tea.Msg` is the
  architecture with zero impedance; `CGO_ENABLED=0` static binaries make
  distribution trivial. Rust's regex speed advantage lands on a path that
  is already bounded and debounced (§7) — fzf is the existence proof that
  Go feels instant at this scale.
- **No GNU tail source reuse.** coreutils is GPLv3 and `tail.c` is a
  monolithic binary, not a library. We need tail's *behavior*, not its
  code — the golden test suite (§8) pins the contract byte-for-byte
  against real GNU tail instead.
- **License: MIT.**

## 2. Modes of operation

tail-gunner picks a mode at startup from TTY detection, then can be promoted
at runtime:

```
                 ┌──────────────────────┐
 stdout not TTY  │  PIPE MODE           │  byte-for-byte tail. No TUI code
 ───────────────▶│  (scripts, alias     │  paths touched. Filters from CLI
                 │   safety)            │  flags still apply (grep-like).
                 └──────────────────────┘

                 ┌──────────────────────┐      ┌────────────────────────┐
 stdout is TTY   │  PLAIN MODE          │  `:` │  GUNNER MODE           │
 ───────────────▶│  prints to scroll-   │─────▶│  Bubble Tea full TUI:  │
                 │  back exactly like   │◀─────│  viewport over ring    │
                 │  tail; keyboard      │  `q` │  buffer, palette,      │
                 │  listener on the     │      │  filters, search       │
                 │  terminal device     │      └────────────────────────┘
                 └──────────────────────┘
```

- **Pipe mode** is the compatibility contract. Output must diff clean
  against GNU tail (see §8 Testing).
- **Plain mode** is tail plus an inconspicuous keyboard listener. Optional
  niceties that don't alter the byte stream semantics (severity coloring à
  la tailspin) are allowed here, behind a flag/auto-detection, because
  colors only ever go to TTYs.
- **Gunner mode** owns the alternate screen. `q` returns to plain mode and
  replays the tail of the buffer into scrollback so context is continuous.
  Promotion is lossless in both directions: the ring buffer was filling the
  whole time.

### The stdin/keyboard problem

When data comes from stdin, the keyboard must come from somewhere else:

- Unix: open `/dev/tty` and hand it to Bubble Tea via `tea.WithInput()`.
- Windows: open `CONIN$`.
- If no controlling terminal exists (cron, CI), there is no keyboard;
  tail-gunner is in pipe mode anyway and never tries.

This is the same approach fzf and gum use. It is solved, but it lives in
`pkg/term` behind one interface so platform quirks stay contained.

## 3. Component map

```
┌─────────────────────────────────────────────────────────────────┐
│ cmd/tail-gunner            CLI parsing, mode selection, wiring  │
└──────┬──────────────────────────────────────────────────────────┘
       │
┌──────▼───────┐   lines    ┌─────────────┐   events    ┌──────────────┐
│ pkg/source   │  (chan)    │ pkg/buffer  │  (chan)     │ pkg/ui       │
│              │───────────▶│             │────────────▶│  Bubble Tea  │
│ file follow  │            │ ring buffer │◀────────────│  program     │
│ (nxadm/tail) │            │ + filter    │  queries    │ viewport     │
│ stdin reader │            │   views     │             │ palette      │
│ multi-file   │            │             │             │ statusbar    │
│ merge        │            └─────────────┘             └──────────────┘
└──────────────┘                   ▲
                                   │
                            ┌──────┴──────┐
                            │ pkg/filter  │
                            │ match/      │
                            │ exclude/    │
                            │ highlight   │
                            └─────────────┘

┌──────────────┐  ┌──────────────┐  ┌───────────────────────────┐
│ pkg/term     │  │ pkg/ui render│  │ pkg/tailcompat            │
│ /dev/tty,    │  │ auto-color   │  │ pipe-mode renderer:       │
│ CONIN$,      │  │ severities,  │  │ -n/-c/-q/-v/headers,      │
│ TTY detect   │  │ highlights   │  │ byte-for-byte GNU parity  │
└──────────────┘  └──────────────┘  └───────────────────────────┘
```

All packages are public under `pkg/` — the module is
`github.com/luthermonson/tail-gunner` and the engine (source, buffer,
filter, tailio) is designed for embedding in other programs. Pre-v1.0,
APIs may change between minor versions.

### 3.1 `pkg/source` — ingestion

One interface, three implementations:

```go
type Source interface {
    Lines() <-chan Line   // closed on EOF (when not following)
    Err() error
}

type Line struct {
    Seq    uint64    // monotonic, assigned at ingest
    File   int       // index into the file list; -1 for stdin
    Time   time.Time // arrival time (format-parsed time is a later feature)
    Text   []byte    // raw bytes, no trailing newline
}
```

- **FileSource** wraps `github.com/nxadm/tail` — gives us `-f`/`-F`
  semantics (rotation re-open, truncation detection, inotify with polling
  fallback) as a channel. This is the reuse-tail-wholesale decision: we
  vendor the *follow engine* as a library instead of reimplementing the
  fiddly parts.
- **StdinSource** is a buffered reader loop. EOF closes the channel
  (pipe-mode tail of a finite stream still works).
- **MergeSource** fans in multiple sources, tagging lines with their file
  index for `==> file <==` headers and per-file coloring.

Backpressure: sources never block on the UI. The buffer goroutine is the
single consumer and is fast (append to ring). If the UI is slow, it drops
*render* frames, never lines.

### 3.2 `pkg/buffer` — the ring

Append-only ring buffer of `Line`, capped (default 100k lines, flag-tunable,
with a byte ceiling as a second cap). Single writer (ingest goroutine),
multiple readers (UI queries), guarded coarse-grained — contention is
trivial at log line rates.

The buffer also owns **filter views**:

```go
type View struct {
    Filter   filter.Set // the active filter stack
    Matched  []uint64   // seqs of matching lines, in order
}
```

- New line arrives → tested against the active filter set → appended to the
  view if it passes. O(1) per line on the hot path.
- Filter changes → full re-scan of the ring rebuilds the view. This is the
  only expensive operation, and it's bounded by the cap (100k regex tests —
  tens of ms; see §7 Performance for how we keep it snappy).

Eviction note: when the ring wraps, evicted seqs are pruned from views
lazily.

### 3.3 `pkg/filter` — the filter stack

lnav-style stackable filters, each one of:

- `IN <pattern>`  — keep only matching lines (multiple INs OR together)
- `OUT <pattern>` — drop matching lines (the "gun down noise" primitive)
- `HL <pattern>`  — highlight only, no removal

Patterns are Go `regexp` with a fast path: if the pattern is a literal
string, use `bytes.Contains` (this matches grep's own literal optimization
and is ~10x faster). Filters are individually toggleable and the stack is
visible/editable in the TUI.

Case-insensitivity, whole-word, and invert are per-filter toggles, not
separate syntax.

### 3.4 `pkg/ui` — Bubble Tea program (gunner mode)

Standard Elm architecture. Major model components:

- **viewport** (bubbles/viewport) over the *view*, not the ring — it renders
  matched lines only. Follow-mode pins to bottom; any scroll-up unpins
  (like `less +F` breaking follow); `G` re-pins.
- **palette** — the `:` command line (bubbles/textinput) with completion.
- **statusbar** — file names, line counts (`12,345 / 98,402 shown`), active
  filter chips, follow state, drop indicator.

Messages: `LineBatchMsg` (ingest, batched per frame — never one `tea.Msg`
per line), `ViewRebuiltMsg`, `tea.KeyMsg`, `tea.WindowSizeMsg`.

### 3.5 Palette command set (v1)

```
:in <regex>        add filter-in            :n / :N    next/prev search hit
:out <regex>       add filter-out           :f         toggle follow
:hl <regex>        add highlight            :filters   open filter stack panel
:/<regex>          search (no filtering)    :clear     drop all filters
:| <shell cmd>     pipe current view        :w <file>  write current view
                   through a command        :q         back to plain mode
                   (the `up` steal)         :Q         quit entirely
```

Single-key shortcuts outside the palette: `/` search, `f` follow toggle,
`q` demote to plain, `G`/`g` bottom/top. Keep the surface small; the
palette is the extensibility point.

`:|` semantics: snapshot the current *view* (filtered lines), pipe through
the user's command via `$SHELL -c`, show output in a scratch pane. Never
mutates the buffer. This gives power users jq/awk without us building a
query language.

### 3.6 `pkg/highlight`

Tailspin-inspired automatic colorizing, applied at render time (never stored
in the buffer — the buffer stays raw bytes):

- v1: severity keywords (ERROR/WARN/INFO/DEBUG), timestamps, IPs, quoted
  strings, numbers.
- Applies in gunner mode always; in plain mode only when stdout is a TTY
  and not disabled (`--no-color`, `NO_COLOR`).
- Custom rules deferred to a config file post-v1.

### 3.7 `pkg/tailcompat`

The pipe-mode implementation and the flag surface. This is deliberately a
separate package with **zero imports from ui/buffer/filter** — the
compatibility path must stay small and auditable.

Flag surface (v1 = POSIX + common GNU):

```
-n N / -n +N      last N lines / starting at line N
-c N / -c +N      bytes variant
-f                follow by descriptor
-F                follow by name (retry + rotation)
-q / -v           suppress/force headers
--pid=PID         exit when PID dies
-s / --sleep-interval   poll interval
multiple files    ==> name <== headers, GNU-identical
```

Anything GNU-tail accepts that we don't implement → fail loudly with a
clear message, never silently misbehave. (`--retry`, `--max-unchanged-stats`
etc. get implemented or rejected, never ignored.)

tail-gunner's own flags live in a separate namespace to avoid future GNU
collisions: `--gun-buffer=200000`, `--gun-no-promote`, `--gun-theme=...`.

## 4. Data flow walkthrough

`tail -f app.log` (aliased), user presses `:out healthcheck` later:

1. CLI parses flags → file source via nxadm/tail → ingest goroutine appends
   to ring. stdout is a TTY → plain mode: lines also print to scrollback as
   they arrive. A raw-mode listener on `/dev/tty` waits for `:`.
2. User presses `:` → plain printer stops, Bubble Tea program starts on the
   alternate screen, viewport renders the tail of the ring, palette is open
   and focused. Ingestion never paused.
3. User types `out healthcheck` ⏎ → filter set updated → ring re-scanned
   into a new view (background, debounced if they keep typing) → viewport
   swaps to the new view. The healthcheck spam vanishes, *including
   historical lines*, position pinned to bottom.
4. New lines keep arriving → tested against the filter → appended to view →
   viewport repaints (batched per frame).
5. `q` → TUI exits alternate screen, last N visible lines re-printed to
   scrollback, plain mode resumes with the filter *still active* (status
   line notes it; `:clear` from palette removes).

## 5. Concurrency model

Three goroutine domains, channel-connected, no shared mutable state outside
the buffer:

1. **Ingest** — source goroutines → one buffer-writer goroutine.
2. **Filter rebuild** — spawned on filter change; works over an immutable
   snapshot of the ring (seq high-water mark); result swapped in atomically;
   superseded rebuilds cancelled via context.
3. **UI** — the Bubble Tea loop. Receives batched line events; queries the
   buffer read-locked.

Rule: the ingest path never waits on the UI, and the UI never blocks the
ingest path. Loss is not acceptable in the buffer (lines are dropped only by
ring eviction); loss is acceptable in rendering (frame skipping).

## 6. Promotion mechanics (the risky bit)

The plain→gunner→plain transition is the most novel/fragile part:

- Plain mode keeps the terminal in normal (cooked) mode for output but the
  `/dev/tty` listener needs raw mode for single-keystroke detection. We set
  raw on the tty device only, not stdin (which may be a pipe carrying data).
- On promotion, the plain printer is stopped *between whole lines* (never
  mid-line), then Bubble Tea takes the alternate screen.
- On demotion, Bubble Tea releases the alternate screen and we reprint the
  last screenful from the buffer so the scrollback reads continuously.
- Resize, SIGTSTP/SIGCONT (ctrl-Z), and SIGINT must be handled in both
  modes. Ctrl-C in plain mode = exit like tail. Ctrl-C in gunner mode =
  copy-friendly no-op (use `q`/`:Q`), matching common TUI convention.

This deserves a spike before anything else is built (see §9).

## 7. Performance strategy

Honest constraint: Go's `regexp` is slower than ripgrep's engine. Mitigations,
in order of leverage:

1. **Literal fast path** — `bytes.Contains` when the pattern has no
   metacharacters; prefix/suffix literal extraction otherwise.
2. **Incremental by default** — new lines are tested once on arrival; a full
   re-scan happens only on filter *change*.
3. **Debounce + cancel** — re-scans triggered by palette typing are debounced
   (~80ms) and cancellable; only the final pattern pays full cost.
4. **Bounded corpus** — the ring cap (100k lines default) bounds worst-case
   re-scan. fzf proves this scale feels instant in Go.
5. **Batched rendering** — ingest events coalesce per frame; the viewport
   renders only the visible window.

Budget targets (M2 exit criteria): full re-scan of 100k avg-length lines
< 150ms; steady-state follow at 50k lines/sec without dropped input; UI
frame budget 16ms.

## 8. Testing strategy

- **Golden compatibility tests** — the drop-in insurance policy. A corpus of
  scenarios (`-n`, `-c +N`, multi-file headers, `-F` through a rotation,
  empty files, no trailing newline, binary garbage) run against both real
  GNU tail and tail-gunner in pipe mode; outputs must be byte-identical.
  Run in CI on Linux and macOS (BSD tail differences documented and pinned
  to GNU behavior).
- **Filter/buffer unit tests** — property-based where cheap (eviction
  invariants, view consistency under concurrent append + rebuild).
- **TUI tests** — Bubble Tea's `teatest` for golden-frame snapshots of the
  viewport/palette/statusbar.
- **The promotion dance** — scripted PTY tests (creack/pty) covering
  promote/demote/resize/ctrl-Z. This is where the bugs will live.

## 9. Milestones

| | Scope | Proves |
|---|---|---|
| **M0 — Spike** | stdin→ring→Bubble Tea with `/dev/tty` input; plain↔gunner promotion on Unix + Windows | The risky mechanics work at all |
| **M1 — tail** | `tailcompat` complete, golden tests green, brew-installable | Safe to alias |
| **M2 — gunner** | Ring + filter stack + palette (`:in/:out/:hl/:/`) + follow/unpin + statusbar | The actual product |
| **M3 — polish** | Auto-highlighting, `:|` shell escape, `:w`, multi-file merge view, config file | Daily-driver quality |

Deliberately **out of scope for v1**: format auto-detection, timestamp
parsing/merge-by-time, SQL queries, remote files, sessions/bookmarks.
That's lnav's territory; we earn the right to it later. The cut line is:
*v1 ships nothing that compromises the drop-in story.*

## 10. Distribution

- Single static binary; goreleaser → GitHub releases → Homebrew tap
  (`brew install <owner>/tap/tail-gunner`), core formula once traction
  justifies it. Scoop/winget for Windows later.
- Binary name `tail-gunner`; docs recommend the alias rather than shipping
  a `tail` symlink (PATH-shadowing coreutils uninvited is hostile).
- Zero runtime dependencies, no config required to be useful.
- MIT licensed. All dependencies (§11) are MIT/BSD-compatible — no GPL
  anywhere in the tree, keeping embedding and redistribution unencumbered.

## 11. Key dependencies

| Dep | Role | Risk note |
|---|---|---|
| `github.com/nxadm/tail` | follow engine (`-f`/`-F`, rotation, polling fallback) | Maintained fork of hpcloud/tail; if it stalls, vendoring is viable — it's small |
| `github.com/charmbracelet/bubbletea` + `bubbles` + `lipgloss` | TUI | Healthy ecosystem |
| `github.com/urfave/cli/v3` | flag parsing | getopt forms GNU tail accepts (`-n5`, `-fn20`, flags after operands, optional `--follow` value) are pre-normalized by a small argv rewriter in `pkg/cli` before urfave parses |
| `github.com/creack/pty` | PTY tests only | test-only dep |
| stdlib `regexp`, `bytes` | filtering | perf mitigations in §7 |
