<div align="center">

# reverse-terminal (`rt`)

**A terminal that keeps your prompt pinned to the top and lets history grow downward.**

Your eye stays where you type. Newest output stays next to the prompt. Old commands sink down the screen instead of scrolling away.

</div>

---

## Why

Every terminal in the world pins the input line to the **bottom**. On a tall or full-screen terminal that means your focus is constantly at the lower edge of the display: you look down to type, down to read the result, down again for the next command. When a command produces a lot of output, it pushes your previous work **up** and out of view — the exact content you were most likely to still need.

This is an ergonomics problem before it is a feature problem. [Warp's issue #22](https://github.com/warpdotdev/warp/issues/22) describes it well: treat the input buffer more like a browser's omnibar. The URL bar sits at the top of the window; the page grows **down**. Recent context stays adjacent to where you're working, and your gaze doesn't commute to the bottom of the screen.

`rt` takes that idea all the way: instead of a special input pane bolted onto a normal terminal, it reverses the scrolling direction itself.

- **The prompt is pinned to row 0.** It never moves.
- **Each command and its output form one block.** When the command finishes, the block is pushed downward and a fresh prompt appears above it.
- **History reads newest-first.** The thing you just ran is directly under the prompt; older commands accumulate below it.
- **Content grows down, not up.** Long output fills the space beneath the prompt rather than shoving your recent work off the top.

The result feels like a stack of command "cards" with a permanent input line at the top — a shell that grows in the direction you actually read.

## Demo

A fresh session:

```
+----------------------------------+
| > (next command goes here)       |
| -------------------------------- |
|                                  |
|                                  |
|                                  |
+----------------------------------+
```

Run `ls`:

```
+----------------------------------+
| > (next command goes here)       |
| -------------------------------- |
| > ls                             |
| < 1.txt  2.txt  3.txt            |
| -------------------------------- |
|                                  |
+----------------------------------+
```

Run `pwd`:

```
+----------------------------------+
| > (next command goes here)       |
| -------------------------------- |
| > pwd                            |
| < /path/to/folder                |
| -------------------------------- |
| > ls                             |
| < 1.txt  2.txt  3.txt            |
| -------------------------------- |
|                                  |
+----------------------------------+
```

`ls` is pushed down; `pwd` takes its place right under the prompt. Older blocks keep sinking as you work. The separator carries the block's exit code, e.g. `──── [1] ────` after a command that failed.

## How it works

`rt` is a transparent proxy that sits between your terminal and a real shell. It does not implement a shell, a prompt, or completion — it just re-frames what the shell already does.

1. **Spawn a shell under a PTY.** `rt` opens a pseudo-terminal and launches your shell as a child process, then shuttles bytes in both directions. Your aliases, exports, functions, plugins, and theme all load exactly as they normally would.
2. **Inject a marker, not a prompt.** A small hook added at shell startup prints a zero-width OSC sequence just before each prompt. `rt` watches the output stream for it. The marker is untypable, so it can never be confused with real output, and each shell gets the hook that fits it best (a `precmd` function for zsh, `PROMPT_COMMAND` for bash, `PS1` for POSIX sh, a prompt object for Python, a `post_run_cell` event for IPython).
3. **Freeze each command into a block.** On every marker, `rt` captures the terminal emulator's cell grid — the command line, its stdout/stderr, colors and all — together with the exit code parsed from the marker. That frozen grid becomes a history entry, and the emulator resets for the next command.
4. **Render prompt-at-top.** A renderer clears the screen and draws the live grid first, then walks history newest-to-oldest, drawing a separator and each block until the screen is full. Blocks are cropped at the bottom edge, so the layout never scrolls.
5. **Repaint only what changed.** Each frame is diffed against the previous one; only changed rows are emitted, each update wrapped in a DEC 2026 synchronized-update sequence so capable terminals render it atomically. This keeps `vim`, `htop`, and friends smooth instead of tearing.

Because `rt` emulates the terminal itself (`vt10x`), full-screen apps work: when a program switches to the alternate screen, the live grid fills the visible rows and history simply stays cropped below it. Resizing the window resizes both the PTY and the emulator.

## Supported shells

The shell is taken from an argument, then `$SHELL`, and can always be forced with `--mode`:

| Mode      | Shells                 | Startup injection                                                                                 |
| --------- | ---------------------- | ------------------------------------------------------------------------------------------------- |
| `zsh`     | `zsh`                  | `ZDOTDIR` shim that sources your real `.zshenv`/`.zshrc`, then adds a `precmd` hook               |
| `bash`    | `bash`                 | `--rcfile` shim that sources your real `~/.bashrc`, then prepends a `PROMPT_COMMAND` hook         |
| `sh`      | `sh`, `dash`, `ksh`, … | `ENV` shim that sources your real `$ENV` and installs the marker in `PS1`                         |
| `python`  | `python`, `python3`    | `PYTHONSTARTUP` shim that installs a prompt object emitting the marker                            |
| `ipython` | `ipython`, `ipython3`  | `PYTHONSTARTUP` shim that registers a `post_run_cell` event (real `IPYTHONDIR`/profile preserved) |

Your original configuration file is always sourced first, and the hook is applied last so it wins even if your prompt is set by a plugin. The marker is emitted as zero-width in the prompt, so it never disturbs prompt width or redraws.

## Features

- **Prompt pinned to the top** — never scrolls, never moves.
- **One block per command** — command, output, and exit code captured together.
- **Exit codes** — failures are visible at a glance in the block header.
- **Collapse repeated commands** — consecutive identical blocks (same text and colors) collapse into one with a count, e.g. `- 3x ────`.
- **Copy mode** — browse, filter, copy, and re-run past commands.
- **Clear at the prompt** — `Ctrl-L` drops history and keeps the prompt (only when idle).
- **Full-screen apps** — `vim`, `less`, `htop` and friends run in the live area; history stays tucked below.
- **Resize-aware** — both the PTY and the emulator follow the window.
- **Terminal-agnostic** — works in any emulator; no special support required.
- **Optional logging** — `--log` records a timestamped trace of every byte and event for debugging.

### Copy mode

Open it with **`Ctrl-^`** at an idle prompt (it never triggers while a command or full-screen app is running).

| Key                 | Action                                               |
| ------------------- | ---------------------------------------------------- |
| `j` / `k`, arrows   | Move selection older / newer                         |
| `g` / `G`           | Jump to newest / oldest                              |
| `Ctrl-D` / `Ctrl-U` | Half-page down / up                                  |
| `Enter` / `y`       | Copy the selected block to the system clipboard      |
| `r`                 | Re-run the selected command                          |
| `/`                 | Filter blocks by text; `Enter` applies, `Esc` clears |
| `Ctrl-W`            | In filter mode, delete the last word                 |
| `c`                 | Collapse / expand blocks                             |
| `q` / `Esc`         | Close copy mode                                      |

Clipboard writes use the native tool when available (`pbcopy`, `wl-copy`, `xclip`) and fall back to OSC 52.

## Usage

```sh
go build -o rt .
./rt                 # launch $SHELL
./rt bash            # launch a specific shell
./rt --mode ipython ipython
./rt --log zsh       # write rt-<timestamp>.log
```

`rt` refuses to run inside itself; nesting is unsupported.

## Roadmap

- Scroll history from the idle prompt with `PgUp`/`PgDn`.
- Side-by-side history beside full-screen apps instead of cropping below them.
- Configurable keybindings and a config file.

## Non-goals

- Replacing the shell, prompt, or line editor.
- Persistent history across sessions.

## Development

```sh
make build   # go build -o rt .
make test    # go test ./...
make fmt     # gofumpt + goimports
make lint    # go vet + staticcheck
make check   # format check, build, test, lint
```
