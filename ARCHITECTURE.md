# rt — Architecture

## The Problem

A traditional terminal is a scrollback stream: new output is appended at the
bottom, older output drifts upward and eventually out of view. `rt` inverts
that. The prompt is pinned to the top of the screen. Every command you run
becomes a discrete block (command, rendered output) placed immediately below
the prompt. As new blocks are added, older ones move down. The screen reads
most-recent-first, and nothing scrolls out of view because the entire frame
is re-drawn every time.

## Background: The Primitive Facts This Relies On

### 1. Interactive programs do not "print" — they draw

A program like `vim` or a shell with a rich prompt does not emit a linear text
stream. It emits a sequence of cursor moves, partial rewrites, and region
clears. The output is only meaningful when interpreted against a screen
state. You cannot capture `vim`'s behavior by reading its stdout the way you
would `cat file`.

This rules out a tape-recorder capture ("read stdout until exit"). rt must
know what was *drawn*, not merely what bytes passed by.

### 2. A PTY is the interface that produces screen state

A pseudo-terminal (PTY) is a kernel device pair; one end is held by rt, the
other is the child's controlling terminal. The child believes it is talking to
a real terminal: it queries size, sets raw mode, emits escape sequences. The
master end observes the child's complete, unmodified draw stream.

This is what makes rt terminal-agnostic: it never asks the user's terminal to
understand anything new. It sits between the terminal and the shell and
re-emits its own drawing.

### 3. ANSI escape sequences are the rendering protocol

Control sequences instruct the terminal to mutate display state (set scroll
region `ESC[n;mr`, clear `ESC[2J`, move cursor `ESC[r;cH`, SGR attributes
`ESC[m`). Nothing interprets them except the renderer. Because rt controls
everything written to the user's screen, it can produce an arbitrary layout
and let the terminal execute it.

### 4. A screen is a grid of cells; escape sequences are state transitions

Consequence of the above: any valid terminal stream can be reduced — by a
terminal emulator — to an absolute `cols×rows` grid of glyphs
(character + attributes). Cursor motion, clears, overwrites, and scroll all
collapse into "what cell holds what character." This grid is the lossless,
rendering-independent form of the output.

This is the key architectural idea. A *real terminal state machine* (vt10x)
resolves the draw stream into a grid once, instead of rt trying to reverse a
drawing into text. The failure mode of the earlier "strip ANSI and splice
lines" approach — overwrites, progress bars, `\r` replays, prompt echoes
corrupting reconstructed text — disappears, because the grid stores what was
actually painted, not a linear guess.

### 5. A live terminal grid has bounded height

A grid is exactly as tall as the terminal. When a program outputs more lines
than the grid holds, old rows scroll out. So a snapshot of the live grid
captures only the *last screenful*. Any design that wants "the whole command
output" as a block must handle this separately.

## Design

One live emulator for display; one *tall* emulator, replayed per finished
command, for the archive.

```
PTY master ──► blockParser
                 │  every chunk
                 ├─► live vt10x (terminal-height grid)    ──► Render: top region
                 │
                 ├─► marker scan (RTMRK prompt sentinel)
                 │      └─► on marker: replay accumulated bytes
                 │           through a fresh tall vt10x
                 │           (cols × 16384) → snapshot grid   ──► BlockStore
                 │
                 └─► BlockStore ──► Render: separators + frozen grids below
```

### shell.go — Prompt-marker injection

A hook script in a temp dir (via `ZDOTDIR` for zsh) sets the prompt to a
fixed literal containing the sentinel `RTMRK` and disables zsh's prompt
redraw options. Every time the shell renders a prompt, `RTMRK` appears in the
stream. rt never parses the user's real prompt or rc config; it searches for
the sentinel it injected. The user's config is untouched and the temp dir is
deleted on exit.

The sentinel is also blanked out of the rendered grids so the visible prompt
reads naturally (`RTMRK%# ` → `%# `).

### capture.go — Emulation and block formation

`blockParser.Feed` does two things per chunk:

1. **Live feed**: the chunk is written to the session-wide live vt10x
   (terminal-height). Marker bytes are included so the prompt keeps its
   screen-relative alignment. At every marker an `ESC[2J` (erase display,
   cursor preserved) is injected, so each command starts from a cleared
   screen — the live region only ever shows the *current* command.
2. **Marker scan**: the same chunk is scanned for `RTMRK`. Bytes between
   markers accumulate in a per-command buffer. On a marker:
   - first marker: bootstrap noise is discarded;
   - subsequent marker: the accumulated buffer for the just-finished command
     is replayed through a fresh tall emulator (`cols × archiveRows=16384`).
     Because the emulator is taller than a screen, content never scrolls out;
     `snapshotGrid` trailing-trims empty rows and saves the frozen grid to
     the `BlockStore`. No re-parse ever happens again after this point.

The clear-at-boundary is what pins the prompt: after a command ends, the
live emulator's screen is wiped, the new prompt draws at the preserved cursor
position, and `Render` trims leading blank rows — so the frame's live region
collapses to just the prompt while the finished command lives on as a frozen
block below.

The tall-replay is the answer to fact 5: the live grid can only ever hold the
last screenful, but the archive grid holds the full output. Commands
producing more than 16384 lines truncate (lower bound, not observed in
practice).

Concurrency: `vt10x.Write` locks internally (vt_posix.go), and reads
(snapshotGrid) take the same lock under `Lock()/Unlock()`. A dedicated
`emuMu` additionally protects the emulator field against the panic-rebuild
path and `Resize`. `RecordWidth` is atomic because the SIGWINCH handler
(main goroutine) writes it while the reader goroutine reads it.

Defensive note: vt10x is unmaintained and can panic on malformed sequences
(observed: a zero-sized `WithSize` panics on first write with an out-of-range
index). `Feed` recovers by rebuilding the live emulator, so a stray escape
sequence degrades to a stale screen rather than killing the session.
`main` also sanitizes PTY size reads: a `0×0` size (e.g., under `script(1)`)
would otherwise produce a zero-sized emulator.

### render.go — Frame composition

`Render` is the only writer to the user's screen. Strategy: clear, rebuild
entire frame, write once; no incremental diffing.

```
row 1..k     live grid (both-ends trimmed, marker-blanked); k = current-command
             content only, because the emulator screen is cleared at every
             command boundary
───          separator
block N-1    newest saved grid
───
block N      older... bottom-cropped if the frame overflows the terminal
```

- Rows are emitted at absolute positions (`ESC[y;1H`), truncating each block
  to the current terminal width. SGR codes are emitted only when a cell's
  attributes change; `vt10x.Color` is mapped to ANSI (30-37/90-97), xterm-256
  (38;5;n), or truecolor (38;2;r;g;b).
- **Frozen blocks are never re-replayed.** If the terminal is narrower than a
  saved grid, the block is cropped at render time; the archive itself keeps
  its capture width. This is the deliberate trade-off that lets resize be
  trivial: repaint, crop, re-drop overcrowded oldest blocks.
- The real cursor is replanted at the live emulator's cursor position
  (captured under the same lock as the grid snapshot). Live-echo keystrokes
  are therefore right where they belong without rt ever tracking input for
  display purposes.

### main.go — Event loop and I/O

A `select` loop over: PTY-reader repaint signals, SIGWINCH, SIGINT, SIGTERM,
stdin EOF. Traces:

- **Reader goroutine**: `master.Read` → `parser.Feed` → non-blocking signal on
  `renderCh`. The render path coalesces bursts by draining `renderCh` before
  each repaint, so high-bandwidth output does not repaint per chunk.
- **Stdin**: `forwardInput` forwards every byte to the master and, on Enter
  (`\r`), pushes the tracked line onto `CommandQueue` for `Block.Command`.
  It no longer builds display state — the shell's own echo, resolved by the
  live emulator, is what gets displayed.
- **SIGWINCH**: resize the child PTY, resize the live emulator (`Resize`
  truncates), repaint — which re-lays-out all blocks under the new geometry.
- **SIGINT** is forwarded to the child's process group so Ctrl-C interrupts
  running commands. SIGTERM/stdin-EOF tear the loop down; on exit the
  alternate screen is restored and rt exits with the shell's code.

### types.go — Data model

A block is a frozen grid: `{Command, Cells [][]vt10x.Glyph, Width int}` plus
the archive height bound. The output is stored as painted cells — character
plus colors — so rendering needs no fidelity decision.

## Data Flow, End to End

```
   your keystrokes
        │
        ▼
 forwardInput ──► tracks command line ──► CommandQueue.Push (on Enter)
        │              │
        │ master.Write ▼
        │            shell (zsh, under PTY)
        │              │
        ▼              ▼
   master.Read ──► parser.Feed ──► live vt10x ────────┐
                    │                   │             │
                    │ marker?           │ snapshotGrid│
                    │   │               └────► Render(top region)
                    │   ▼                             │
                    │ replayGrid(tall) ─► BlockStore ─┘
                    └───────────★────────────────────► Render(separators
                                                            + blocks)
```

## Resize Semantics

1. SIGWINCH → read new size (guarded against 0×0) → resize child PTY + live
   emulator → repaint.
2. Blocks keep their capture width. A too-wide grid is cropped from the
   right; a shrinking terminal permanently shows less of old blocks (records
   are not re-wrapped, by design).
3. If the frame is taller than the terminal, the newest content (live region +
   newest blocks) wins; the oldest blocks are dropped from the bottom.

## Intentional Omissions

- **Full-screen apps** (vim/less/man): undefined behavior for this build.
  Whether the alternate screen passes through or freezes last-frame is not
  specified.
- No scrollable history; blocks live in memory and die with the session.
- No selection, mouse, copy/paste of blocks; no config file.
- zsh-only prompt injection (bash hook exists, untested).
- Wide characters: stored and emitted; geometric alignment for double-width
  glyphs is not specially handled.
- Exit codes per command not captured (only session exit).

## Invocation

```
make
./rt              # wraps /bin/zsh
./rt /bin/bash    # explicit shell
exit              # restores the terminal, exits with the shell's code
```