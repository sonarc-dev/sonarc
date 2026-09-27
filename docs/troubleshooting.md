# Troubleshooting

Most problems in a terminal editor come from the layers between you and it:
the terminal app, ssh, and tmux. `sonarc -doctor`, run inside the same tmux
session, checks those layers and says what to change.

## Clicks do nothing

- **Inside tmux:** tmux's `mouse` option is off by default, and then no click
  reaches sonarc. sonarc says so at startup. Run `tmux set -g mouse on`, or add
  it to `~/.tmux.conf`.
- **In macOS Terminal:** View ▸ Allow Mouse Reporting (`⌘R`) must be on.
- **Still nothing:** run `sonarc -trace-input /tmp/trace.log`, click once, quit,
  and look at the log. If no mouse report appears in it, the terminal or tmux
  is not sending any.

Every command has a keyboard route, so the mouse is never required.

## A key does nothing

1. Press `F1`. It lists only the keys your terminal can actually send; if the
   key is missing, use the `Ctrl+K` chord shown beside the command.
2. Run `sonarc -trace-input /tmp/trace.log`, press the key once, quit, and read
   the log. It shows the bytes the terminal sent and the key they became.
3. Common causes:
   - `Ctrl+Shift` keys need tmux's `extended-keys` and never work in screen.
   - `Ctrl+B` is taken by tmux as its prefix.
   - Your terminal app may bind the key itself, as many do with `F11` or
     `Ctrl+Tab`.

## Alt keys are slow

tmux waits `escape-time` milliseconds after `Esc` to see whether a longer
sequence follows, and `Alt` keys start with `Esc`. Set
`set -sg escape-time 10`.

## Copy does not reach my laptop

Copying to your own machine uses OSC 52. tmux needs `set -g set-clipboard on`,
and some terminal apps need OSC 52 allowed in their settings. Without it,
`Ctrl+C` still copies into tmux's paste buffer and sonarc's own register.

## Colors look wrong

Set tmux's `default-terminal` to `tmux-256color`, and make sure `TERM` outside
tmux names a 256-color terminal such as `xterm-256color`. With fewer than 256
colors sonarc uses a minimal theme on purpose. Try another theme from the
command palette (`F6`, "choose a color theme").

## Go to definition finds nothing

- `Ctrl+K ?` shows which indexes are answering. With no cscope or ctags index,
  only the built-in indexer answers, and it covers C and C++, Go, and Python.
- Press `F5` to build cscope and ctags indexes in the background. See
  [building the indexes](navigation.md#building-the-indexes).
- On macOS, install `universal-ctags`; the system ctags cannot build a usable
  index.

## A language server gives wrong or no answers

- `Ctrl+K ?` shows each server and its state: running, not started yet,
  failed, or not used and why.
- clangd needs a `compile_commands.json` to know how files are built; without
  one it is not used. Most build systems can write one: `make
  compile_commands.json` in the kernel, `cmake -DCMAKE_EXPORT_COMPILE_COMMANDS=ON`,
  or `bear -- make`.
- A server that failed is retried after a minute. To stop using one, add
  `LANGUAGE off` to `lsp.conf`; see [language servers](navigation.md#language-servers).

## Search covers the wrong directory

Indexes and search cover the project root, found by walking up from what you
opened. If the root is wrong, see
[how the project root is found](navigation.md#the-project-root). Opening the
project's top directory, as in `sonarc ~/src/proj`, always works.

## A big file has no colors

Files over 50 MB are shown without syntax highlighting on purpose, so they
stay fast to scroll and edit.

## sonarc says an update is available but I can't update

`sonarc -update` replaces the binary where it is, so it needs write access to
that directory. If sonarc was installed by root or a package manager, update it
the same way, or install your own copy with the install script into
`~/.local/bin`. Set `SONARC_NO_UPDATE_CHECK=1` to stop the daily check.

## Still stuck

[Open an issue](https://github.com/sonarc-dev/sonarc/issues/new/choose). The
bug form asks for `sonarc -version`, the `-doctor` output and, for keys or
clicks, an input trace, which is usually enough to reproduce the problem.
