# Keys

Everything here works on an ordinary `ssh` + `tmux` terminal. No command
depends on `Ctrl+Shift`, which tmux and screen do not pass through, and every
command also has a `Ctrl+K` chord: press `Ctrl+K`, let go, then the letter.

`F1` shows these keys filtered to what your current terminal can deliver.
`F6` (or `Ctrl+K k`) opens the command palette, which lists every command with
its key and finds it by description, so nothing is reachable only through a
shortcut you would have to know already.

## Editing

| Key | Action |
|---|---|
| `Ctrl+Z` / `Ctrl+Y` | undo / redo |
| `Ctrl+X` `Ctrl+C` `Ctrl+V` | cut, copy, paste; with nothing selected, copy takes the whole line |
| `Ctrl+A` | select all |
| `Ctrl+D` | delete the current line |
| `Ctrl+G` | go to line |
| `Tab` | insert a tab character |

`Enter` keeps the current line's indentation. Pasting from the terminal
arrives as one insertion, exactly as copied, so there is nothing to switch on
first.

## Files

| Key | Action |
|---|---|
| `Ctrl+S` / `Ctrl+K w` | save / save as |
| `Ctrl+P` / `Ctrl+K o` | open a file by name (fuzzy) |
| `Ctrl+O` | open a file by path |
| `Ctrl+W` | close this file (asks if it is unsaved) |
| `Ctrl+K u` | list the open files, to switch between them |
| `Ctrl+K z` | close every other file |
| `Ctrl+K !` | reload this file from disk |
| `Ctrl+Q` | quit (asks if anything is unsaved) |

Fuzzy open ranks by structure: matches at path separators, word boundaries and
consecutive runs score highest, so `buf` finds `lib/buffer.go` before
`hugebuffering.go`, and `rebuild/rebuild` finds `index/rebuild/rebuild.go`.

## Moving around

Arrows move; hold `Shift` to select. `Ctrl+←` `Ctrl+→` move by word,
`Ctrl+Home` `Ctrl+End` jump to the start or end of the file, and `Home` pressed
twice goes to column 0. `PgUp` `PgDn` page.

The mouse works wherever the terminal reports it: click to place the cursor,
drag to select, double-click for a word, triple-click for a line, and the wheel
scrolls. Clicking a row in the results panel jumps to it. Every one of these
has a keyboard route; the mouse is optional. Inside tmux, clicks need tmux's
`mouse` option on (see [tmux and terminals](tmux.md)).

## File tree

The tree is on the left. It is visible by default and hides itself on
terminals narrower than 90 columns.

| Key | Action |
|---|---|
| `Ctrl+E` / `Ctrl+K t` | focus the tree; again to hide it; again to show it |
| `↑` `↓` | move the selection *(tree focused)* |
| `→` or `Enter` | open a file, or expand a directory |
| `←` | collapse a directory |
| `<` `>` | make the tree narrower or wider *(tree focused)* |
| `Tab` | switch between the tree and the Changes list *(sidebar focused)* |
| `Esc` | back to the text, leaving the tree open |

Click a file to open it or a directory to expand it, and drag the `│` line on
the tree's right edge to resize it. The tree follows you: opening a file any
other way expands the tree to it and highlights it. Files ignored by
`.gitignore`, VCS metadata and dependency directories are not listed, and
directories are read only when you expand them.

The tree is on `Ctrl+E` rather than the more common `Ctrl+B` because `Ctrl+B`
is tmux's default prefix: tmux would take it before the editor saw it.

## Split panes

| Key | Action |
|---|---|
| `Ctrl+K 3` | split side by side, showing this file again |
| `Ctrl+K 2` | split one above the other |
| `F9` / `Ctrl+K ;` | switch to the other pane |
| `Ctrl+K 1` | back to one pane, keeping the one you are in |

The numbers follow emacs's window commands. A split starts with the same file
in both panes and the keyboard in the new one; open another file there with
`Ctrl+P`, go to a definition, or anything else. Each pane has a title line
naming its file, bright for the pane with the keyboard.

Both panes can show one file with separate cursors: an edit in one appears in
the other at once, and the other pane's cursor stays on the text it was on.
Clicking in a pane moves the keyboard there, and the wheel scrolls the pane
under the pointer. Search highlights and diffs belong to the pane with the
keyboard. On a terminal too narrow for two 30-column panes the split stacks
instead, and on one too short to stack, the focused pane fills the screen
until there is room again.

## Search

| Key | Action |
|---|---|
| `Ctrl+F` / `Ctrl+K /` | find in this file, as you type; `Ctrl+R` toggles regex, `Ctrl+W` whole word |
| `Ctrl+R` / `Ctrl+K e` | find and replace, confirming each |
| `F3` / `Shift+F3` | next / previous match, or the next result in the panel |
| `Ctrl+K f` | search text across the project; `/regex/` or `/regex/i` for a pattern |

Search is smart case: a lowercase pattern matches any case, and a pattern with
a capital letter in it is taken literally.

## Code navigation

| Key | Action |
|---|---|
| `Ctrl+]` or `F12` / `Ctrl+K g` | go to definition |
| `Ctrl+T` or `Alt+←` / `Ctrl+K b` | jump back |
| `Alt+→` / `Ctrl+K j` | jump forward again |
| `Ctrl+K v` | peek: show the definition on the message line without going there |
| `Ctrl+K l` | outline: go to a function, type or macro in this file |
| `F7` / `Ctrl+K r` | find references |
| `Ctrl+K c` | find callers *(cscope)* |
| `Ctrl+K d` | find functions called *(cscope)* |
| `Ctrl+K a` | find assignments to a symbol *(cscope)* |
| `Ctrl+K i` | find files that include this one *(cscope)* |
| `F4` / `Ctrl+K s` | search symbols by name |
| `F5` / `Ctrl+K x` | rebuild the tags and cscope databases |
| `Ctrl+K ?` | show which indexes are answering |

Results open in a panel along the bottom that stays open, so you can walk a
call graph without running the query again. `↑` `↓` preview a result, `Enter`
opens it and `Esc` closes the panel. [Code navigation](navigation.md) explains
where the answers come from.

## Git

| Key | Action |
|---|---|
| `F8` / `Ctrl+K =` | diff this file against the last commit |
| `Ctrl+K m` | go to the Changes list; `↑` `↓` show each file's diff in turn |
| `Enter` | in the list, move into the diff; in the diff, open the file at that line |
| `n` / `p` | next / previous change *(in a diff)* |
| `Alt+↓` / `Ctrl+K ]` | next changed block in the file |
| `Alt+↑` / `Ctrl+K [` | previous changed block |
| `Esc` | close the diff |

See [Git](git.md).

## Changing keys

Keys are rebound in `keys.conf`, beside `state.json` (see
[where sonarc keeps things](getting-started.md#where-sonarc-keeps-things)).
The quickest way in is the command palette: `F6`, then "change key bindings".
That opens the file, starting it from a commented template, and saving it
applies it at once.

```
# Comments are whole lines starting with #.
# A direct key:
bind Ctrl+B toggle-sidebar
# A chord: Ctrl+K, then y.
bind Ctrl+K y find-callers
# Take keys away.
unbind F12
unbind Ctrl+K y
```

Keys are written the way `F1` shows them: `Ctrl+B`, `Alt+X`, `Alt+Left`,
`Shift+F3`, `F12`. A later line wins over an earlier one and over the built-in
keys. `sonarc -commands` lists every command name with the keys bound to it
now, and any lines of `keys.conf` it could not use.

Some keys are refused, with the reason, because they would never arrive or
would get in the way:

- `Ctrl+Shift` keys, which tmux and screen do not pass through;
- `Ctrl+H`, `Ctrl+I`, `Ctrl+M` and `Ctrl+[`, which terminals send as
  Backspace, Tab, Enter and Esc;
- `Ctrl+K` itself, which starts every chord;
- a plain character with no modifier, which you need for typing.

A line sonarc cannot use is reported when the editor starts, and every other
line still applies.
