# Code navigation

Navigation is what sonarc is for: go to a definition, list its references and
callers, and walk back. This page explains where the answers come from and how
to make them fast. The keys are on the [keys page](keys.md#code-navigation).

## How a lookup works

When a symbol has several definitions, such as a prototype in a header, the
body in a `.c` file, and static variants across the tree, sonarc shows a
picker instead of guessing. Results land in a panel along the bottom that stays
open, so you can walk a call graph rather than running each query again.

Every lookup runs in the background. The editor keeps taking keys, the bottom
line shows what is running and for how long, and `Esc` cancels it. The message
afterwards says which index answered and how long each took, and marks results
that are incomplete as `(partial)` instead of passing them off as everything.
If you keep typing while a lookup runs, its answer is shown but the cursor is
not moved.

## Where answers come from

Three providers answer queries. Every one is asked and the answers are merged,
because ctags and cscope disagree often enough in real C code that trusting
one silently is worse than offering both. Each answer is labelled with its
source.

| Provider | Needs | Provides |
|---|---|---|
| **cscope** | `cscope.out` | definitions, references, callers, callees, assignments, includers |
| **ctags** | a `tags` file | definitions, symbol search |
| **built-in** | nothing | definitions, references and text search for C and C++, Go, and Python |

`Ctrl+K ?` shows which providers are answering in the current project.

The built-in indexer means sonarc is useful the moment it starts, even on a
machine with neither cscope nor ctags. When a real index exists, the built-in
one only lists files, which keeps memory low: on a kernel tree that is the
difference between 2.5 GB and 18 MB.

## Building the indexes

From a shell:

```sh
cd ~/src/linux
ctags -R --fields=+iaSKmn --extras=+q --sort=yes .   # universal-ctags
cscope -b -q -k -R
```

Or press `F5` (`Ctrl+K x`) inside the editor. It says first what it will run,
then runs it in the background:

- In a Linux kernel tree it runs the kernel's own `make cscope tags`, for the
  architecture your existing `cscope.files` covers, so the kernel's macro
  handling is kept (`SYSCALL_DEFINE3(read, …)` becomes `sys_read`).
- Elsewhere it runs cscope and ctags over the project's files, or over its own
  `cscope.files` if there is one.

The old index keeps answering until the new one is complete, and a failed or
cancelled rebuild leaves it exactly as it was. `Ctrl+K x` again cancels, and
quitting during a rebuild asks first. On a kernel tree the tags take about
half an hour.

> **macOS:** `/usr/bin/ctags` is BSD ctags and cannot produce a usable tags
> file. sonarc detects it and will not generate with it; install
> `universal-ctags` instead. Reading a tags file works whatever wrote it.

## References and text search

Find references (`F7`) uses cscope when it is there: 0.04 s on a kernel tree.
The built-in scan reads every file, so it runs only when no index answered,
and the message then points at `Ctrl+K f` for a text search instead.

Project text search (`Ctrl+K f`) uses `git grep` in a git work tree, about 2 s
across a kernel tree with a warm cache, and falls back to cscope and then the
built-in scan elsewhere. Write `/regex/` for a regular expression, or
`/regex/i` to ignore case.

## The project root

Indexes, search and the Changes list all work on a project root, found by
walking up from the file or directory you opened:

- A `.git`, `.hg` or `.svn`, a `go.mod`, or an existing `cscope.out` or `tags`
  file marks the root.
- A `Makefile` or `CMakeLists.txt` counts only if nothing stronger turns up,
  because nearly every directory of a C project has a Makefile. Treating one
  as decisive once anchored a kernel checkout at `fs/` and quietly narrowed
  search to 4% of the tree.
- A directory named `tags` or `cscope.out` is not an index, and does not mark
  a root.

## Measured on a kernel tree

On Linux 7.2 source (5.5 GB, 64,525 C and header files) on a 4-core Ubuntu
machine with 3.7 GB of RAM, over ssh inside tmux 3.4, with real cscope 15.9
and a real 1.1 GB tags file:

| | |
|---|---|
| tags file searched | 1.1 GB |
| memory to search it | **8 MB** |
| goto-definition | 4.8 ms per lookup |
| find references, kernel-wide | 6 results in 0.82 s |
| the editor, whole tree open | 31 MB |
