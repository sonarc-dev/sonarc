# Design notes

sonarc has two jobs that pull against each other: stay fast and small on a
kernel-sized tree, and never change a byte of a file you did not change. This
page explains how it does both.

## Never damage a file

- **Text is stored as raw bytes**, in a gap buffer of lines. Nothing is decoded
  and re-encoded on the way in or out.
- **Line endings are kept per line**, so a file with mixed CRLF and LF stays
  mixed instead of becoming a whole-file diff. A UTF-8 byte order mark
  survives.
- **Invalid bytes** are shown as `<FF>` and written back unchanged.
- **No trailing newline is added** that you did not type.
- **Saves are atomic**: sonarc writes a temporary file in the same directory,
  syncs it, and renames it over the original. A crash or a full disk leaves the
  original intact. Saving through a symlink writes the file it points to and
  keeps the link.

## Stay fast on a big tree

- **Random access by line is O(1)**, which is what drawing needs every frame,
  and edits near the cursor are O(1) amortized.
- **Only the visible lines are drawn**, so a 100,000-line file scrolls as fast
  as a 10-line one, and only changed cells go to the terminal.
- **A kernel-sized tags file is binary-searched on disk.** A lookup is about
  20 reads and a few kilobytes of memory, not a gigabyte of RAM.
- **One cscope process stays running** between queries, so each answer takes
  milliseconds instead of reloading the database.
- **The file tree reads a directory only when you expand it**, so opening a
  kernel checkout costs one directory listing, not a walk of 64,000 files.
- **Every lookup runs in the background**, cancellable with `Esc`, so a slow
  index never freezes typing.

## Syntax highlighting

Highlighting is a hand-written scanner per language rather than regular
expressions, which is what makes multi-line constructs come out right: block
comments, Go raw strings, Python triple quotes and nested Rust comments. The
state each line inherits is cached, so scrolling into the middle of a large
file does not rescan it from the top, and an edit only invalidates the lines
below it. A line costs about 350 ns to scan, so a full screen takes well under
a millisecond.

## Bugs only a real kernel tree found

Three bugs surfaced on a real 5.5 GB kernel tree that local tests had not
caught. Each now has a regression test.

- **The project root stopped at the first `Makefile`.** Every kernel
  subdirectory has one, so opening `fs/read_write.c` anchored the project at
  `fs/` and quietly narrowed the index and search to 4% of the tree. Root
  markers are now tiered.
- **The built-in indexer used 2.5 GB** for definitions cscope and ctags were
  already answering. It now only lists files when a real index is present:
  2564 MB became 18 MB.
- **Fuzzy open cut the file list before ranking it.** Capped at the first
  5000 paths alphabetically, it never reached `fs/`, so `fs/namei.c` could not
  be opened by name. Every candidate is now ranked, and only the output is
  capped.

## Not included

Debugging, staging and committing from the editor, Markdown and image preview,
plugin scripting, and vi keybindings. The code-intelligence layer is an
interface with several implementations, which is how language servers were
added alongside cscope, ctags and the built-in indexer without changing the
commands that use them.
