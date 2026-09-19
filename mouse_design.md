# Mouse support

Status: design only. Not scheduled. Deliberately not vibe-coded — see
"Required refactor" below. Implement the refactor first, then the features.

## Desired features

- scroll wheel scrolls history blocks
- left click on a block copies its contents to the clipboard
- right click on a block replays whatever was fed into stdin when the block was
  active to the current pty

## Implications

- We need to enable mouse support by sending control sequences to main's stdout
  first. We also need to make sure we revert everything on exit
- Currently, we mostly forward main's stdin to the pty. We need to intercept
  mouse events
  - If the pty has gotten into alt-screen mode, we should not capture mouse
    events and just forward them to the pty
  - If the pty has requested mouse support and the click's coordinates are
    within the bounds of the active block, we forward the click to the pty
  - Else, if the click's coordinates are on a history block, we copy or replay
- We currently don't record whatever was fed to the pty for each block, we need
  this in order for the "replay" feature to work
- We currently have no model of what is on screen: `repaint` recomputes layout
  from scratch every tick and throws it away. Hit-testing, scrolling and
  click-to-block resolution all need that layout to persist for one frame.

## Locked decisions

| Question | Decision |
|---|---|
| Mouse activation | Always on; document Shift-select (mouse mode kills native click-drag selection) |
| Scroll step | 3 lines per wheel notch |
| Scroll reset | snap to newest when a new block is appended (boundary / drain) and on Ctrl-L clear |
| Replay gesture | Right-click |
| Replay bytes | Exact raw stdin bytes for that block, including terminating Enter (re-executes) |
| Clipboard | Native tool first (`pbcopy` / `wl-copy` / `xclip`), OSC 52 fallback |
| Copy content | Command + output, plain text (ANSI absent by construction) |

## Architecture problems this exposes

These exist today and block a clean implementation:

1. **stdin forwarding is inline and single-purpose.** `main.go:200-218` mixes
   `swallowCtrlL`, logging and `master.Write` in one goroutine body. There is no
   seam to parse and route escape sequences.
2. **stdout writes are unsynchronized and scattered.** `repaint`
   (`emu.go:273-275`), `restore` (`main.go:112`) and the exit message all write
   directly. Copy (OSC 52) and status feedback must write from the stdin
   goroutine while the render loop is mid-frame.
3. **`rtState` is a god object.** It owns the byte buffer, the emulator, history,
   the logger, the child process, layout flags and render methods. No separation
   between model, input handling, rendering and terminal control.
4. **No persistent frame model.** Layout is implicit in the render pass. Nothing
   maps a screen row back to a block.
5. **Full clear + full redraw every tick.** Already known perf debt
   (`README.md`); scroll, hit-test and status make a real frame model worth
   having.
6. **Input recording is not first-class.** tx is written to the log file only
   (`rec.go`), never retained per block.
7. **Locking discipline is fuzzy.** `tick` unlocks before calling `repaint`, so
   `repaint` reads `his` / `width` / `height` without the lock while the pty and
   stdin goroutines can mutate them.

## Required refactor before implementation

Do these first, as standalone changes with tests, before any mouse code.

### R1. Screen / frame model (`screen.go`, or a `frame` type in `emu.go`)

Make layout an explicit value produced once per paint and reused for painting
and hit-testing.

```go
type rowKind int

const (
    rowLive rowKind = iota
    rowSep
    rowBlock
)

// rowRef records what a screen row belongs to. block is an index into history,
// or -1 for rows with no block (live grid).
type rowRef struct {
    kind  rowKind
    block int
}

// frame is the computed layout for one paint: one rowRef per terminal row,
// plus the resolved paint rows. Immutable once built.
type frame struct {
    rows   int
    cols   int
    layout []rowRef
    // paint rows / source rows, enough for repaint and for status overlay
}
```

- Builder: `buildFrame(live grid, h *history, scroll, rows, cols) frame`.
- `repaint` becomes a pure consumer of `frame`; no layout logic inside it.
- Hit-test: `frame.blockAt(y) (int, bool)`.
- This is the single change that unlocks scroll, copy and replay cleanly.

### R2. History store (`history.go`)

Extract `[]block` plus its operations out of `rtState`.

```go
type history struct {
    blocks []block
}

func (h *history) append(b block)
func (h *history) clear()
func (h *history) len() int
// layout returns the rowRefs for the history region given a scroll offset,
// applying duplicate-run collapse exactly as repaint does today.
func (h *history) layout(scroll, rows int) []rowRef
func (h *history) text(i int) string
```

- `block` gains the input record: `tx []byte` (see R3/feature 4).
- `text(i)` is the existing "serialize grid to plain text" logic, moved here
  (trim trailing blanks per row, join with `\n`).
- Collapse logic moves out of `repaint` (`emu.go:252-256`) into `layout`.

### R3. Input router (`input.go`)

Replace the inline stdin body with a parser/router that owns a pending buffer.

```go
type actionKind int

const (
    actForward actionKind = iota // deliver bytes to the pty
    actCtrlL                     // clear history at idle
    actMouse                     // mouse event for rt to handle
)

type mouseEvent struct {
    btn   int  // 0 left, 1 middle, 2 right, 0x40 wheel-up, 0x41 wheel-down
    x, y  int  // 1-based cell coordinates
    press bool // 'M' press vs 'm' release
    shift, meta, ctrl bool
}

type inputRouter struct {
    pend []byte // bytes held because they may be a split escape sequence
}

// feed consumes a stdin read and returns bytes to forward plus actions rt must
// handle. Ordering of forwarded bytes is preserved.
func (r *inputRouter) feed(b []byte) (forward []byte, actions []action)
```

- The stdin goroutine becomes: read → `router.feed` → forward the bytes via the
  shared `forward()` helper → apply actions.
- `swallowCtrlL` becomes `actCtrlL` produced by the router (same idle gating).
- Keyboard escape sequences (arrows, function keys) are not touched; only
  `ESC [ < ... (M|m)` is recognized as mouse.

### R4. Terminal writer / control layer (`screen.go`)

All writes to the real terminal go through one serialized object.

```go
type screen struct {
    mu  sync.Mutex
    out io.Writer
}

func (s *screen) write(b []byte)
func (s *screen) paint(f frame)
func (s *screen) setMouse(on bool)      // ESC[?1000h/l ESC[?1006h/l
func (s *screen) clipboard(text string) // native tool, OSC 52 fallback
func (s *screen) status(text string)    // bottom-row overlay
```

- Owns `restore`: cursor show, reset, mouse off.
- This is what makes concurrent copy/status/repaint safe.
- Alt-screen tracking (`trackAltScreen`) stays, but becomes state read by the
  router, not a repaint concern.

### R5. Locking discipline

- One model mutex around `rtState`'s mutable fields (`history`, `buffer`,
  `scroll`, `curTx`, `inAltScreen`, `status`, `frame`).
- Render loop: lock → snapshot (`frame`, status, dims) → unlock → `screen.paint`.
  Never hold the model lock across terminal I/O.
- `screen.mu` is separate and only serializes stdout.
- Document this in a short comment block at the top of `rtState`.

## Feature design (after refactor)

### 1. Mouse input layer

- Enable `ESC[?1000h ESC[?1006h` at startup, only when `term.IsTerminal`.
  Disable in `screen.restore`.
- SGR only (`1006`). We enable `1000` (press/release), not `1002`/`1003`: no
  drag, no hover — fewer events to filter.
- Decode: `btn = Cb & 0x03`; wheel if `Cb & 0x40` (bit 0 = up/down); modifiers
  at bits 4/8/16; motion bit 32 ignored.
- Split-sequence handling: hold a trailing tail only when it is a proper prefix
  of `ESC[<` with length ≥ 2, or a partial mouse body. A lone trailing `ESC`
  forwards immediately so zsh-vi / vim ESC is never delayed.
  - Micro-risk: a mouse sequence split exactly between `ESC` and `[<` is
    dropped. Terminals write these atomically and the sequence is <20 bytes, so
    this is accepted and documented.
- Dispatch:
  - `inAltScreen` → forward raw to pty.
  - idle prompt → handle (scroll / copy / replay).
  - otherwise → ignore.

### 2. Layout + hit-testing

- `buildFrame` produces `frame.layout` (R1). Click resolves
  `frame.blockAt(y-1)`.
- Separator row belongs to the block it heads; a collapsed run resolves to the
  newest kept block. Live-grid region → no-op (proposed default).

### 3. Scroll

- `rt.scroll int` = lines scrolled back from newest.
- Wheel up `+= 3`, down `-= 3`, clamp `[0, totalHistoryLines]`.
- Paint: live grid pinned at rows `[0, len(grid))`; history viewport is rows
  `[len(grid), height)`, sliced from the laid-out history at `scroll`.
- Reset to 0 on `append` (boundary / drain) and on Ctrl-L clear.
- If `len(grid) >= height`, no history visible → scroll is a no-op.
- While `scroll > 0`, the status line shows the offset (see 6).

### 4. Replay

- Per-block input record: `rt.curTx []byte` appended on every forwarded stdin
  byte. At `boundary()` / `drain`, move into `block.tx`, reset.
- One `forward(b []byte)` helper used by both the stdin path and replay: logs
  tx, appends to `curTx`, `master.Write`.
- Right-click press on a block:
  - gate: `!inAltScreen && len(buffer) == 0 && !first && len(block.tx) > 0`.
    Otherwise ignore + status message.
  - `forward(block.tx)` — exact bytes incl. terminating Enter → re-executes.
- Replayed bytes become the new block's tx, so the result is replayable too.
  Replaying an older block runs it *now*, appending a new newest block.
- Cap `curTx` / `block.tx` at 256 KB (truncate + flag) to bound memory from
  huge pastes.

### 5. Copy

- `history.text(i)` (R2): plain text, trailing blanks trimmed, `\n`-joined.
- `screen.clipboard(text)`:
  - native: darwin `pbcopy`; linux `wl-copy`, then `xclip -selection clipboard`.
    Pipe via stdin.
  - fallback: OSC 52 `ESC]52;c;<base64>BEL`.
  - Some terminals cap OSC 52 (~100 KB) or need it enabled; native path avoids
    that where available.

### 6. Status / feedback

- `rt.status string` + `rt.statusUntil time.Time`. Paint overlays the bottom row
  (reverse video) when active, or when `scroll > 0`.
- Messages: "copied block N", "replayed block N", "cannot replay now".
- Expiry: `time.AfterFunc` wakes the render loop to clear.

## Tradeoffs and risks

- Enabling mouse mode disables native click-drag text selection. Shift-select
  becomes required. Inherent to the feature; document prominently.
- Right-click can be intercepted by some terminals (iTerm2 right-click
  preference, macOS ctrl-click mapping). If it proves unreliable, fall back to
  Alt+click or a configurable binding.
- OSC 52 may be disabled by the terminal or capped in size; native tool path is
  preferred but not universal (headless/SSH).
- Replay is destructive by design: it re-executes. It must be gated to the idle
  prompt and clearly surfaced in the status line.
- Full-screen apps: rt owns terminal mouse mode (vt10x swallows app-issued mode
  sequences), so forwarding in alt-screen is mandatory for nvim etc. This is a
  new responsibility, not currently exercised.
- `curTx`/`block.tx` memory: capped, but large sessions still grow. Consider
  spilling to the log or dropping tx for old blocks later.

## Proposed defaults (adjust before implementing)

- Click on the live grid = no-op.
- Separator click = the block it heads.
- No drag (`1002`) / hover (`1003`) — wheel + clicks only.
- 256 KB per-block tx cap.
- Status line occupies the bottom row while active.

## Test plan

- Router: whole SGR sequence; split across reads; multiple events interleaved
  with keystrokes; lone `ESC` forwarded; non-mouse CSI (arrows, F-keys)
  untouched; wheel and modifier decoding; ctrl-l action gating.
- `history.text`: trailing trim, empty block, interior spaces, wide runes.
- `buildFrame` / hit-test: block / separator / collapsed-run mapping, live
  region, scroll slicing, clamp at both ends.
- Scroll: reset on append and Ctrl-L, no-op when live grid fills the screen.
- Replay: idle gate, alt-screen gate, exact byte round-trip, tx cap.
- Clipboard: command selection per GOOS (unit test the chooser, not the tools).

## Open questions

- Should the active (live) block be copyable / replayable at all?
- Do we want a visible scrollbar, or is the status-line offset enough?
- Should tx be persisted across sessions (ties into the deferred persistent
  history non-goal)?
- Is a configurable binding for copy/replay wanted, or fixed gestures?
