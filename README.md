# reverse-terminal (rt)

## Goal

A terminal utility that keeps the shell prompt pinned at the top of the screen.
Each command and its output occupies its own block. As new commands run, old
blocks are pushed downward. This reverses the traditional scrolling direction:
new content appears at the top, history accumulates below.

The utility must work with any terminal emulator and respect the user's existing
shell configuration (aliases, exports, functions, plugins, theme).

---

## Demonstration

The idea is that we start with a new terminal session and it looks like this:

```
+----------------------------+
| > (next command goes here) +
| -------------------------- +
|                            +
|                            +
|                            +
+----------------------------+
```

(the "next command goes here" does not actually appear, it's for your benefit)

So, the prompt is at the top. Let's press `ls<enter>`

```
+----------------------------+
| > (next command goes here) +
| -------------------------- +
| > ls                       +
| < 1.txt 2.txt ....         +
| -------------------------- +
|                            +
+----------------------------+
```

So, `ls`, both the command and its output are on their own block. After ls has finished, it is pushed down and the next prompt is above it. Let's press `pwd<enter>`

```
+----------------------------+
| > (next command goes here) +
| -------------------------- +
| > pwd                      +
| < /path/to/folder          +
| -------------------------- +
| > ls                       +
| < 1.txt 2.txt ....         +
| -------------------------- +
|                            +
+----------------------------+
```

`ls` is pushed further down. So, the prompt is always at the top and previous commands are in reverse order.

## PoC Scope

The proof-of-concept delivers exactly three capabilities. Everything else is
deferred.

### 1. Spawn shell under PTY

Launch a real shell (zsh) as a child process. The utility sits between the user's
terminal and the shell, acting as a transparent proxy for stdin/stdout/stderr.

### 2. Capture command + output as blocks

Detect when a command starts and when it finishes. Capture:

- The command string (what the user typed)
- The full stdout/stderr output
- The exit code

Each command-output pair forms a single block stored in memory.

### 3. Render prompt-at-top layout

Draw the terminal screen so the prompt always lives at row 0. As blocks
accumulate, older blocks shift down. A horizontal separator is drawn between
blocks. The rendering must handle terminal resize events gracefully.

---

## Implementation outline

- Variables:
  - buffer: buffer of bytes
  - vt: vt instance
  - history: slice of blocks, blocks are stripped vt10x grids
  - width, height: dimensions of the terminal
- We set raw+altscreen
- Create log file with timestamp in name
- We add the PS1 stuff in a temporary rc file and load sh with the temp file as its only configuration
- We open a pty with sh
- event handlers/goroutines:
  - read from main's stdin:
    - pprint chunk into log file
    - feed into pty
  - read from pty:
    - pprint chunk into log file
    - feed chunk into buffer
  - renderer, 30FPS:
    - if buffer empty and size unchanged, do nothing
    - if buffer contains separator:
      - feed until separator into vt
      - strip vt's grid and, if not empty, append to history
      - reset vt
      - chunk is now part of buffer after separator
      - buffer is empty
    - else if buffer ends in part of the separator:
      - chunk is part of buffer until separator prefix
      - buffer is separator prefix (will be read at next render)
    - else:
      - chunk is entire buffer
      - buffer is empty
    - feed chunk into vt
    - clear screen, put cursor at 0,0
    - strip vt's grid and print it, counting lines
    - for block in reversed history:
      - print separator, taking current width into account, count one line
      - print block's grid, respecting the current width, counting lines
      - when printed lines reach terminal's height, stop
      - Note: stop mid-block when reach height to avoid scrolling
    - reset cursor position to vt's cursor position
- terminal size change:
  - update width and height variables
  - update pty's size
  - update vt's dimensions
  - (next render will adapt automatically)

Notes:

- Separator is zero-width-private-escape ensuring it's after \r\n

---

## Non-goals (explicitly deferred)

- Scroll through block history
- Copy/paste blocks
- Configuration file
- Mouse support
- Search/filter blocks
- Persistent history across sessions
- Custom keybindings
- bash support (trivial to add after PoC)

## TODOs

- [ ] If consecutive blocks in the history are identical, modify the horizontal separator from `----...` to `- 3x ----...` above the history block and print it once
  - [ ] "identical" = same chars + FG/BG + mode for every glyph; color-only diffs (`git status` clean vs dirty) must NOT collapse
  - [ ] On collapse, keep the most-recent block, prefix separator with the count
- [ ] Make CTRL-l clear the screen in rt (drop history, keep prompt)
  - [ ] Only intercept at prompt-idle: pty buffer empty AND past separator AND child not in alt-screen (track `inAltScreen` flag)
  - [ ] When swallowed: clear `rt.his`, repaint, do NOT forward to child
- [ ] Block invoking rt from rt
  - [ ] Outer rt sets env var (e.g. `RT=1`) on the pty child next to `ENV=`; inner rt checks `os.Environ()` at startup, prints "nested rt unsupported", exits. Do NOT match on command name (aliases/full paths cloak it)
- [ ] separator logic per shell
  - [ ] Shell is `args[1] || $SHELL`
  - [ ] Try to figure out which shell it is (eg '/bin/zsh' -> 'zsh')
  - [ ] User can override with `--mode` flag
  - [ ] Interactive rc path differs: zsh -> `ZDOTDIR`, bash -> `~/.bashrc` (BASH_ENV is non-interactive only), fish -> `XDG_CONFIG_HOME`
  - [ ] Apply separator hook LAST; user rc may overwrite PS1. zsh: precmd hook preferred over PS1 injection
  - [ ] Marker must be prompt-width-safe: zsh `%{...%}`, bash `\[...\]`; bare OSC miscalculates width
  - [ ] Implement zsh first (user shell), keep sh fallback
- [x] fullscreen apps: option 1 ("do nothing") works — vt sized to stdin, alt-screen emulated by vt10x, live grid fills the rows so history stays cropped below. VERIFIED: nvim, bat pager
  - [ ] Partial-height apps (pagers) already let history peek under the live grid — accept as feature
- [ ] fullscreen apps with side-by-side history: intercept alt-screen (`\x1b[?1049h/l`) from pty; if set, render live grid left 70% of stdin width and a strip of recent history blocks on the right. Pane-split renderer, not a width tweak
- [x] Record exit code per block; show in block header (eg `──── [1] ────`). MARKER: PS1 expands `$?` between `sepHead` and the BEL; `boundary()` parses it into `block.code`; final block reaps the shell's own exit status at pty EOF
- [ ] History scroll from prompt: PgUp/PgDn (or C-b/C-f) at idle prompt pans the block list. Gate on `inAltScreen`
- [ ] Repaint perf debt: `\x1b[2J` + full redraw every tick; nvim/htop-class apps tear and burn CPU. Compute cell diff between frames, emit changed rows only
- [ ] Terminal query caps (document, don't fix yet): vt10x ignores DA1/`\x1b[c`, OSC 10/11 dynamic colors, cursor-shape demand; apps fall back to 256-color
