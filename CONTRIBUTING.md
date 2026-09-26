# Contributing to sonarc

Bug reports, fixes and small features are welcome. For anything larger, open
an issue first so we can agree on the shape before you spend time on it.

## Build and test

You need Go 1.24 or later. cscope, ctags and tmux are optional: the tests use
fakes where they are missing.

```sh
make build      # ./sonarc for this machine
make test       # the full suite, no terminal needed
make lint       # go vet and a gofmt check; run before sending a change
```

The editor is tested end to end against tcell's simulation screen, so most
changes can be covered by a test that presses keys and reads the screen. The
shared helpers are in `cmd/sonarc/harness_test.go`.

## Where things live

The README's [Layout](README.md#layout) section maps every file and package.
In short: `cmd/sonarc` is the editor's commands and event handling, and
`internal/` holds the parts it is built from (buffer, view, ui, term, index,
vcs).

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
