# Ideas / TODO

Brainstorm of possible features. None of these are committed to; ordered
roughly by effort within each section.

## Quick wins (local, no marker-protocol changes)

- [ ] **Replay-and-edit** — `R` in copy mode re-types the selected command into
  the prompt without executing it. `r` currently re-runs because it forwards
  `block.tx` including the trailing newline; strip the trailing `\r`/`\n` in
  `flushReplay` for the new key.
- [ ] **Copy command only** — `Y` copies `string(block.tx)` instead of the
  rendered grid, so the prompt and output are excluded.
- [ ] **Exit-code styling** — color the `[code]` suffix in `sepText` red when
  non-zero.
- [ ] **Failed-only filter** — `?` in copy mode sets `view` to blocks with
  `code != 0`.
- [ ] **Live filter preview** — filter the view as the query is typed, not only
  on Enter (Enter still commits).
- [ ] **Command duration** — record wall time between successive markers, store
  it on `block`, and render it in the separator (e.g. `[0] 1.2s`). Needs no
  shell or marker changes.

## Medium

- [ ] **Per-block collapse** — `c` toggles only the selected block; `C` toggles
  all. `collapsed` is currently a single global bool.
- [ ] **Auto-collapse long blocks** — collapse blocks over N rows to head+tail
  by default, expandable on demand.
- [ ] **Sticky running-command header** — while a command runs and its output
  scrolls the live grid, keep the invoking command line (from `curTx`) pinned
  above it. Closest thing to Warp's "frozen command".
- [ ] **Desktop notification on long commands** — if a command runs longer than
  N seconds, emit OSC 9 (or a bell) when it finishes.
- [ ] **Multi-block selection** — range-select several blocks in copy mode and
  copy them concatenated.
- [ ] **Highlight filter matches** in the rendered block.

## Bigger bets

- [ ] **Rich marker metadata** — extend the marker to carry optional trailing
  fields (`RT;<token>;<code>;<dur_ms>;<cwd>`), parsed leniently. Unlocks
  duration, cwd badges, time-gap grouping, and a path to OSC 133 compatibility.
  Touches every shell hook and the parser.
- [ ] **OSC 133 semantic prompt** — support shells/frameworks that already emit
  semantic prompt marks, as an alternative to per-shell injection.
- [ ] **Pipe a block into a command** — `|` in copy mode sends the block's text
  to a command you type; the output becomes a new block.
- [ ] **Persistent history** — serialize blocks and `tx`, restore across
  sessions. Currently an explicit non-goal; revisit if it proves worth it.
- [ ] **Block diffing** — for repeated commands (`git status`, test runs),
  highlight changed cells between consecutive similar blocks. Extends the
  existing `blocksEqual` hash path.
- [ ] **More REPLs** — node, ruby/irb, psql, sqlite3, lua. The mode abstraction
  makes each one a startup-hook shim.

## Deferred / explicitly rejected

- Mouse support — overkill.
- `Ctrl-F`/`Ctrl-B` paging — not valuable enough.
