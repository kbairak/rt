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

## Design Decisions

| Decision               | Choice                                                 | Rationale                                                  |
| ---------------------- | ------------------------------------------------------ | ---------------------------------------------------------- |
| Architecture           | PTY multiplexer                                        | Only approach that works with any terminal                 |
| Language               | Go                                                     | Single binary, no runtime deps, mature PTY libraries       |
| TUI library            | tcell                                                  | Lightweight cell-buffer abstraction, no framework overhead |
| Shell                  | zsh (PoC)                                              | macOS default, narrow scope for MVP                        |
| Shell integration      | Delimiter injection via $PROMPT hooks                  | Reliable command boundary detection                        |
| Injection method       | Wrapper binary (`rt zsh`)                              | Zero config; no rc file modification required              |
| Full-screen apps       | Auto-detect alternate screen, fall back to normal mode | vim/less/man transparently supported                       |
| Block data model       | Minimal (cmd, output, exit code)                       | Rich fields deferred                                       |
| History persistence    | None for PoC                                           | Blocks lost on exit                                        |
| Navigation/keybindings | None for PoC                                           | User cannot interact with historical blocks                |
| Scrollback             | Custom buffer in memory                                | Terminal-native scrollback not used                        |
| Selection/copy         | Undefined for PoC                                      | Mouse behavior unspecified                                 |

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

