# tmux and terminals

sonarc is built to run in tmux or screen over ssh, where some keys and
features depend on settings you may not have. Nothing below is required:
sonarc works without all of it and says so when something is missing.

## What -doctor checks

Run `sonarc -doctor` inside the tmux session you edit in. It reads the live
tmux settings and prints exactly what to add to `~/.tmux.conf`:

```tmux
set -g  mouse on                        # or no click ever reaches sonarc
set -sg escape-time 10                  # or Alt- keys lag by half a second
set -g  set-clipboard on                # or Ctrl+C never reaches your laptop
set -g  extended-keys on                # enables the Ctrl+Shift accelerators
set -g  default-terminal "tmux-256color"
```

Reload with `tmux source-file ~/.tmux.conf`, or restart tmux.

## The mouse

Inside tmux, clicks reach sonarc only with tmux's `mouse` option on, which is
off by default. sonarc says so at startup when it is off.

In macOS Terminal, View ▸ Allow Mouse Reporting (`⌘R`) turns mouse reporting
off and on. If clicks do nothing there, it is off.

To select text with the terminal's own selection while sonarc has the mouse,
hold `Shift` in most terminals, or `Option` in iTerm2. Terminals that send the
older X10 mouse reports instead of SGR ones work too.

## Copying to your own machine

`Ctrl+C` tries three things in turn, so it always does something:

1. OSC 52, which carries the text through ssh and tmux to the clipboard of the
   machine you are sitting at. tmux needs `set-clipboard on`, and some
   terminals need OSC 52 allowed in their settings.
2. The tmux paste buffer.
3. sonarc's own register, which `Ctrl+V` pastes from.

## Keys tmux does not pass through

- **`Ctrl+Shift` combinations** reach the editor only with tmux's
  `extended-keys` on, and never in screen. No command depends on them; each
  has a `Ctrl+K` chord.
- **`Ctrl+B`** is tmux's default prefix, which is why the file tree is on
  `Ctrl+E`.
- **`Alt` keys** are slow with tmux's default `escape-time`, because tmux waits
  to see whether `Esc` starts a longer sequence.

`F1` lists only the keys your current terminal can send.
