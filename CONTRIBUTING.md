# Contributing to sonarc

Bug reports, fixes and small features are welcome. For anything larger, open
an issue first so we can agree on the shape before you spend time on it.

## Build and test

You need Go 1.24 or later. cscope, ctags and tmux are optional: the tests use
protocol-level fakes where they are missing.

```sh
make build      # ./sonarc for this machine
make test       # the full suite, no terminal needed
make race       # the suite under the race detector
make lint       # go vet and a gofmt check; run before sending a change
make bench      # benchmarks
make site       # the website, as the Pages workflow builds it, in build/site
```

The editor is tested end to end against tcell's simulation screen: tests press
keys and read the screen through the same key dispatch, rendering and
navigation code a real terminal uses. Most changes can be covered that way.
The shared helpers are in `cmd/sonarc/harness_test.go`, and tests sit beside
the code they cover.

The editor's only dependencies are `github.com/gdamore/tcell/v2` for the
terminal and `github.com/rivo/uniseg` for grapheme clusters.

## Where things live

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
docs/                 the documentation, one page per topic
site/                 the website, published to GitHub Pages (make site)
  gen/                renders the docs and releases pages from the repo
install.sh            the one-line installer, served beside the website
.github/workflows/    releases on a v* tag; the website on every change
```

The documentation is in `docs/`, one Markdown page per topic, listed in order
in `docs/README.md`. The website's docs are generated from those files, so a
change to them is a change to the site.

## Principles

- Every command must be reachable over plain ssh inside tmux. No binding may
  depend on `Ctrl+Shift`, and a `Ctrl+K` chord should exist for anything bound
  to a key tmux or screen might not deliver.
- It must stay fast and small on a kernel-sized tree. Anything that walks the
  whole project runs in the background and never loads a large index into
  memory.
- No new runtime dependencies. The release is one static binary.

## Reporting a bug

Include `sonarc -version`, the output of `sonarc -doctor` run inside the same
terminal and tmux session, and the steps that reproduce it. For keys or clicks
that do nothing, `sonarc -trace-input /tmp/trace.log` records what the
terminal actually sends.
