# sonarc

A modeless terminal code editor for Linux servers, built for `ssh` + `tmux`.

Typing inserts text. `Ctrl+S` saves. `Ctrl+Z` undoes. There are no modes to
escape from and no `:wq`. It ships as a single static binary with no runtime
dependencies, and it treats **cscope** and **ctags** as first-class features
rather than plugins.

```sh
curl -fsSL https://sonarc-dev.github.io/sonarc/install.sh | sh
sonarc path/to/file.c
```

Website: <https://sonarc-dev.github.io/sonarc/>

## Why it exists

Editing code on a headless server usually means vi or emacs. sonarc aims at the
same job with the ergonomics people already have from a GUI editor, while being
honest about what a terminal inside tmux can actually do.

- **One static binary.** `CGO_ENABLED=0` produces a ~4 MB ELF with no libc
  dependency, so the same file runs on Alpine, Debian, and ancient-glibc CentOS.
  `scp` it and go.
- **Built for tmux, not despite it.** tmux does not report `Ctrl+Shift`
  combinations unless you enable `extended-keys`, and screen never does. So no
  command depends on `Ctrl+Shift`; a `Ctrl+K` chord prefix reaches everything.
- **Large codebases.** A kernel-sized `tags` file is over a gigabyte. sonarc
  binary-searches it on disk rather than loading it, and keeps one cscope
  process resident so queries answer in milliseconds instead of seconds.
- **Never dead on arrival.** If neither cscope nor ctags is installed, a
  built-in indexer still provides goto-definition, find-references and symbol
  search.

## Install

On Linux or macOS, amd64 or arm64:

```sh
curl -fsSL https://sonarc-dev.github.io/sonarc/install.sh | sh
```

The script downloads the latest release for your machine, checks it against
the release's `checksums.txt`, and installs it to `~/.local/bin` without root.
`SONARC_INSTALL_DIR` changes where, and `SONARC_VERSION=v0.1.0` picks a
release. Binaries are also on the
[releases page](https://github.com/sonarc-dev/sonarc/releases).

To update later, run `sonarc -update`: it fetches the latest release the same
way and replaces the binary in place. Once a day the editor asks GitHub whether
a newer release exists and says so on the message line; set
`SONARC_NO_UPDATE_CHECK=1` to turn that off, which also stops the request.

With Go 1.24 or later:

```sh
go install github.com/sonarc-dev/sonarc/cmd/sonarc@latest
```

From a checkout:

```sh
make build                     # build for this machine
make linux                     # static binaries for linux/amd64 and arm64
make release                   # linux and macOS, amd64 and arm64, with checksums
make deploy HOST=myserver      # detects the remote arch and scp's the right one
```

Then, on the server:

```sh
sonarc -doctor                # reports terminal capabilities and tmux fixes
sonarc -update                # replaces this binary with the latest release
sonarc file.c                 # edit a file
sonarc file.c:42              # at line 42, as compilers and grep print it
sonarc .                      # open the current directory as a project
sonarc ~/src/proj main.c      # a directory and a file together
```

## Keys

Everything below works on an ordinary `ssh` + `tmux` terminal. `F1` shows the
list filtered to what your current terminal can actually deliver.

### Editing

| Key | Action |
|---|---|
| `Ctrl+S` / `Ctrl+K w` | save / save as |
| `Ctrl+P` / `Ctrl+K o` | open a file by name (fuzzy) |
| `Ctrl+O` | open a file by path |
| `F6` / `Ctrl+K k` | command palette |
| `Ctrl+W` | close this file (prompts if it is unsaved) |
| `Ctrl+K u` | list the open files, to switch between them |
| `Ctrl+K z` | close every other file |
| `Ctrl+Q` | quit (prompts if anything is unsaved) |
| `Ctrl+Z` / `Ctrl+Y` | undo / redo |
| `Ctrl+X` `Ctrl+C` `Ctrl+V` | cut, copy, paste |
| `Ctrl+A` | select all |
| `Ctrl+D` | delete the current line |
| `Ctrl+G` | go to line |
| `Ctrl+K !` | reload this file from disk |

The color theme — dark, light, gruvbox, solarized or high-contrast — is chosen
from the command palette ("choose a color theme") and remembered.

### Files and sessions

Opening a project again with no file named reopens it as you left it: the same
files, cursors and scroll positions, and the same folders open in the tree.
Any file you open again starts where you last left it, in any project. The
session is saved every few seconds as well as on exit, so a dropped ssh
connection loses nothing but unsaved text.

Files changed by another program — `git checkout`, a build, an AI agent — are
noticed within two seconds. One you have not edited is reloaded in place. One
you have edited is left alone with a warning, and saving it asks before
overwriting the other version. New and deleted files appear in the tree the
same way.

### Movement

Arrows move; hold `Shift` to select. `Ctrl+←/→` moves by word, `Ctrl+Home/End`
jumps to the start or end of the file. `Home` pressed twice goes to column 0.
Mouse click, drag and wheel work wherever the terminal reports them; double-
click selects a word and triple-click a line. Clicking a row in the results
panel jumps to it. The mouse is optional: each of these has a keyboard route.

Inside tmux, clicks reach sonarc only with tmux's `mouse` option on, which is
off by default; sonarc says so at startup when it is. Terminals that ignore
the request for SGR mouse reports and send the older X10 form work too. To
select text with the terminal's own selection while sonarc has the mouse,
hold `Shift` in most terminals (`Option` in iTerm2). In macOS Terminal,
View ▸ Allow Mouse Reporting (`⌘R`) turns mouse reporting off and on; if
clicks do nothing there, it is off.

### Project tree

The file tree sits on the left. It is visible by default and hides itself on
terminals narrower than 90 columns.

| Key | Action |
|---|---|
| `Ctrl+E` / `Ctrl+K t` | focus the tree; again to hide it; again to show it |
| `↑` `↓` | move the selection *(tree focused)* |
| `→` or `Enter` | open a file, or expand a directory |
| `←` | collapse a directory |
| `Esc` | return to the text, leaving the tree open |
| `<` `>` | make the tree narrower or wider *(tree focused)* |

Click a file to open it, a directory to expand it; the wheel scrolls the tree.
Drag the `│` line on its right edge to resize it. The width is remembered in
`~/.config/sonarc/state.json`.
The tree follows you: opening a file any other way — go to definition, `Ctrl+P`,
a search result — expands to it and highlights it. Files ignored by
`.gitignore`, VCS metadata and dependency directories are not listed.

Directories are read only when expanded, so opening the tree on a kernel-sized
checkout costs one directory listing, not a walk.

`sonarc <directory>` opens the folder with focus in the tree. The tree shows
the directory you named, while indexes and project search still cover the whole
project found by walking up from it, so `sonarc ~/linux/fs` browses `fs/` but
searches the entire kernel.

`Ctrl+E` rather than the more common `Ctrl+B`: `Ctrl+B` is tmux's default
prefix, and tmux would swallow it before the editor ever saw it.

### Search

| Key | Action |
|---|---|
| `Ctrl+F` / `Ctrl+K /` | incremental find (`Ctrl+R` toggles regex, `Ctrl+W` whole word) |
| `Ctrl+R` / `Ctrl+K e` | find and replace, confirming each |
| `F3` / `Shift+F3` | next / previous match, or next result in the panel |
| `Ctrl+K f` | search text across the project; `/regex/` or `/regex/i` for a pattern |

Search is **smart case**: a lowercase pattern matches any case, a pattern with a
capital in it is taken literally.

### Code navigation

This is what the editor is for.

| Key | Action |
|---|---|
| `Ctrl+]` or `F12` / `Ctrl+K g` | go to definition |
| `Ctrl+T` or `Alt+←` / `Ctrl+K b` | jump back (the tag stack) |
| `Alt+→` / `Ctrl+K j` | jump forward again |
| `Ctrl+K v` | peek: show the definition on the message line without going there |
| `Ctrl+K l` | outline: go to a function, type or macro in this file |
| `F7` / `Ctrl+K r` | find references |
| `Ctrl+K c` | find callers *(cscope)* |
| `Ctrl+K d` | find functions called *(cscope)* |
| `Ctrl+K a` | find assignments to a symbol *(cscope)* |
| `Ctrl+K i` | find files including this one *(cscope)* |
| `F4` / `Ctrl+K s` | search symbols by name |
| `F5` / `Ctrl+K x` | rebuild the tags and cscope databases |
| `Ctrl+K ?` | show which indexes are answering queries |

When a symbol has several definitions — a prototype in a header, the body in a
`.c` file, static variants across the tree — sonarc shows a picker instead of
guessing. Results land in a panel along the bottom that stays open, so you can
walk a call graph rather than re-running the query each time.

Every lookup runs in the background. The editor keeps taking keys, the bottom
line shows what is running and for how long, and `Esc` cancels it. The message
afterwards says which index answered and how long each took, and marks results
that are incomplete as `(partial)` instead of passing them off as everything.
If you keep typing while a lookup runs, its answer is shown but the cursor is
not moved.

Find references uses cscope when it is there: 0.04 s on a kernel tree. The
built-in scan, which reads every file, runs only when no index answered, and
the message points at `Ctrl+K f` for a text search instead. That search uses
`git grep` in a git work tree (about 2 s on a kernel tree with a warm cache),
and falls back to cscope and then the built-in scan elsewhere.

`Ctrl+K x` rebuilds the indexes in the background and says first what it will
run. In a Linux kernel tree that is the kernel's own `make cscope tags`, for the
architecture the existing `cscope.files` covers, so the kernel's macro handling
(`SYSCALL_DEFINE3(read, …)` → `sys_read`) is kept. Elsewhere it runs cscope and
ctags over the project's file list, or its own `cscope.files`. The old index
keeps answering lookups until the new one is complete, and a failed or
cancelled rebuild leaves it exactly as it was. `Ctrl+K x` again cancels; quitting
asks first. On a kernel tree the tags take about half an hour.

Nothing is reachable only through a shortcut you would have to already know:
`F6` opens a command palette listing every command, searchable by description
and annotated with its key binding.

### Git

In a git work tree, sonarc shows what changed since the last commit:

- A bar in the gutter beside added (green) and changed (blue) lines, and a red
  rule where lines were removed. The marks follow your typing, not your saves.
- A letter after each changed file in the tree — `M` modified, `A` added, `D`
  deleted, `R` renamed, `U` conflict, `?` untracked — and a dot on folders
  that contain changes.

Below the tree, a **Changes** section lists every changed file with its
letter. Click its header to fold it away (the choice is remembered); click a
file to see its diff in place of the text: both line numbers, removed lines in
red, added ones in green, three lines of context around each change, and `⋯`
for the unchanged stretches between. An open file is compared as it is in the
editor, unsaved edits included. Double-click a line, or press `Enter` on it,
to open the file there; `Esc` goes back to what you were editing.

| Key | Action |
|---|---|
| `F8` / `Ctrl+K =` | diff this file against the last commit |
| `Ctrl+K m` | go to the Changes list; `↑` `↓` show each file's diff in turn |
| `Tab` | switch between the tree and the Changes list *(sidebar focused)* |
| `Enter` | in the list, move into the diff; in the diff, open the file at that line |
| `n` / `p` | next / previous change *(in a diff)* |
| `Alt+↓` / `Ctrl+K ]` | next changed block in the file |
| `Alt+↑` / `Ctrl+K [` | previous changed block |

Commits, checkouts and resets made in another terminal are picked up
automatically. sonarc only reads the repository: `git status` runs without
taking git's index lock, so it never gets in the way of a `git commit` you
run meanwhile. Staging and committing stay in your shell.

## Syntax highlighting

C/C++, Go, Python, Rust, JavaScript/TypeScript, Java, shell, Make, JSON, YAML
and TOML. Highlighting is a hand-written scanner rather than regular
expressions, which is what makes multi-line constructs — block comments, Go raw
strings, Python triple quotes, nested Rust comments — come out right.

The state each line inherits is cached, so scrolling into the middle of a large
file does not rescan it from the top, and an edit only invalidates the lines
below it. A line costs about 350ns to scan, so a full screen is well under a
millisecond. Files over 50 MB are left uncolored deliberately.

## How navigation works

Three providers answer queries, consulted in this order and merged:

| Provider | Needs | Provides |
|---|---|---|
| **cscope** | `cscope.out` | definitions, references, callers, callees, assignments, includers |
| **ctags** | a `tags` file | definitions, symbol search, completion |
| **built-in** | nothing | definitions, references, text search for C/C++, Go, Python |

Every provider is asked and the union is shown, because ctags and cscope
disagree often enough in real C code that trusting one silently is worse than
offering both. Answers are labelled with their source.

To get the best results:

```sh
cd ~/src/linux
ctags -R --fields=+iaSKmn --extras=+q --sort=yes .   # universal-ctags
cscope -b -q -k -R
```

or just press `F5` inside the editor, which runs both in the background.

> **macOS note:** `/usr/bin/ctags` is BSD ctags and cannot produce a usable tags
> file. sonarc detects it and refuses to generate with it, pointing at
> `universal-ctags`. Reading a tags file works regardless of what wrote it.

## tmux

Five tmux settings silently degrade the editor. `sonarc -doctor` checks them
and prints exactly what to add:

```tmux
set -g  mouse on                        # or no click ever reaches sonarc
set -sg escape-time 10                  # or Alt- keys lag by half a second
set -g  set-clipboard on                # or Ctrl+C never reaches your laptop
set -g  extended-keys on                # enables the Ctrl+Shift accelerators
set -g  default-terminal "tmux-256color"
```

None of these are required. Copy degrades in tiers — OSC 52 to your local
machine, then the tmux paste buffer, then an internal register — so it always
does something.

## Design notes

- **Text storage** is a gap buffer of lines holding raw bytes. Random access by
  line is O(1), which is what the renderer needs every frame, and edits near the
  cursor are O(1) amortized.
- **Nothing is silently rewritten.** Line endings are preserved per line, so a
  file with mixed CRLF and LF stays mixed rather than becoming a whole-file
  diff. A UTF-8 BOM survives. Invalid bytes are shown as `<FF>` and written back
  unchanged. No trailing newline is added that you did not type.
- **Saves are atomic**: write to a temporary file in the same directory, fsync,
  rename. A crash or a full disk leaves the original intact.
- **Rendering is viewport-only**, so a 100k-line file scrolls exactly as fast as
  a 10-line one, and tcell emits only the cells that changed.
- **A kernel-sized tags file is binary-searched on disk.** A lookup is ~20 reads
  and a few kilobytes of memory, not a gigabyte of RAM.
- **Fuzzy matching ranks by structure**, not just by containment: matches at
  path segments and word boundaries and consecutive runs score highest, so "buf"
  finds `lib/buffer.go` before `hugebuffering.go`.

## Development

```sh
make test          # full suite, headless
make lint          # vet + gofmt check
make bench         # benchmarks
make linux         # static cross-compiled binaries
```

The editor is driven in tests against tcell's `SimulationScreen`, so the real
key dispatch, rendering and navigation are exercised end to end with no
terminal. cscope and tmux are tested against protocol-level fakes, so those
paths are covered on machines where neither is installed.

External dependencies: `github.com/gdamore/tcell/v2` for the terminal and
`github.com/rivo/uniseg` for grapheme clusters.

### Layout

```
cmd/sonarc/           the editor: startup, key and mouse dispatch, commands
  main.go             flags, -doctor, -trace-input
  app.go              the app, its state, and the event loop
  prompt.go           the prompt line and the help screen
  launch.go           command-line arguments and finding the project root
  keys.go             the command table, key map and Ctrl+K chords
  edit.go mouse.go    typing and clipboard; clicks, drags and the wheel
  files.go            opening, switching, saving, closing and quitting
  sidebar.go          the file tree's keys and clicks
  nav.go history.go   definitions, references, call graph; jump back/forward
  search.go find.go   project-wide text search; find and replace in a file
  results.go          the results panel
  palette.go          the command palette and pickers
  outline.go peek.go  outline of a file; peek at a definition
  index.go rebuild.go which indexes answer; rebuilding them in the background
  git.go changes.go   following git; the Changes list under the tree
  diff.go             the diff view
  disk.go             noticing files changed on disk
  session.go state.go reopening projects as left; saved preferences
  jobs.go             background queries and how results reach the UI
internal/
  buffer              the text of a file: a gap buffer of lines, undo, saving
  view                cursor, selection and scrolling over a buffer
  ui                  drawing: text, gutter, sidebar, panel, picker, diff view
  term                terminal setup, capabilities, themes, -doctor
  text                bytes, graphemes and display columns
  syntax              highlighting
  search              in-file search and fuzzy matching
  filetree ignore     the sidebar tree; .gitignore rules
  vcs                 git status, committed versions, line diffs
  proc                child processes that die with the editor
  index/provider      the interface every code-intelligence source implements
  index/cscope        cscope, kept running between queries
  index/tags          ctags files, binary-searched on disk
  index/builtin       the fallback indexer, git grep, outlines
  index/rebuild       regenerating cscope and tags safely
site/                 the website, published to GitHub Pages (make site)
  gen/                renders the docs and releases pages from the repo
install.sh            the one-line installer, served beside the website
.github/workflows/    releases on a v* tag; the website on every change
```

Tests sit beside the code they cover, named after it where they can be; the
shared test harness is `cmd/sonarc/harness_test.go`.

## Status

Working: editing, undo/redo, multiple files, search and replace, regex project
search, syntax highlighting, the file tree, mouse, fuzzy file open, the command
palette, outline and peek, tags, cscope, the built-in indexer, background index
rebuilds, git change marks, the Changes list and diff view, session restore,
reloading files changed on disk, themes, and `-doctor`.

Not built yet: split panes, an integrated terminal panel, and a configuration
file for rebinding keys. Bindings are compile-time.

## Verified against a real kernel tree

Run on Linux 7.2.0 source (5.5 GB, 64,525 C/H files) on a 4-core Ubuntu box
with 3.7 GB of RAM, over `ssh` inside tmux 3.4, against real `cscope` 15.9 and
a real 1.1 GB `tags` file -- not fakes.

| | |
|---|---|
| tags file searched | 1.1 GB |
| memory to search it | **8 MB** RSS |
| goto-definition | 4.8 ms per lookup |
| find references, kernel-wide | 6 results in 0.82 s |
| editor RSS, whole tree open | 31 MB |

`-doctor` read the live tmux settings and correctly flagged `escape-time`,
`set-clipboard` and `extended-keys`. Goto-definition returned cscope's
definition and ctags' prototype merged into one picker, deduped where they
agreed.

Three bugs surfaced that no amount of local testing would have found, each now
covered by a regression test:

- **Project root stopped at the first `Makefile`.** Every kernel subdirectory
  has one, so opening `fs/read_write.c` anchored the project at `fs/` -- which
  silently narrowed the built-in index and project-wide search to 4% of the
  tree. Root markers are now tiered: a VCS directory, module file, or existing
  index is decisive; a build file is a last resort.
- **The built-in indexer used 2.5 GB on the kernel tree**, driving the box from
  2.87 GB free to 325 MB, for definitions cscope and ctags were already
  answering. It now builds only the file list when a real index is present:
  2564 MB to 18 MB.
- **Fuzzy open truncated the file list before filtering.** Capped at the
  alphabetically-first 5000 paths, it never reached `fs/`, so `fs/namei.c` was
  unreachable no matter what was typed. Ranking now considers every candidate
  and only the ranked output is capped.

## Not included

LSP, debugging, staging and committing from the editor, AI-agent dispatch,
Markdown and image preview, plugin scripting, and vi keybindings. The
code-intelligence layer is an interface with three implementations, so a
language server can be added later as a fourth without disturbing anything
else.
