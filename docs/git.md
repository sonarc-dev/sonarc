# Git

In a git work tree, sonarc shows what has changed since the last commit. It
only reads the repository: staging and committing stay in your shell.

## Change marks

- A bar in the gutter beside added lines (green) and changed lines (blue), and
  a red rule where lines were removed. The marks follow your typing, not your
  saves.
- A letter after each changed file in the tree: `M` modified, `A` added, `D`
  deleted, `R` renamed, `U` conflict, `?` untracked. Folders that contain
  changes get a dot.

`Alt+↓` and `Alt+↑` (or `Ctrl+K ]` and `Ctrl+K [`) jump between changed blocks
in the file you are editing.

## The Changes list

Below the tree, a **Changes** section lists every changed file with its
letter. Click its header to fold it away; the choice is remembered.

`Ctrl+K m` puts you in the list. Moving through it with `↑` `↓` shows each
file's diff in turn, so you can review a change set file by file with the arrow
keys alone. `Tab` switches between the list and the tree.

## Diffs

Clicking a file in the Changes list, or pressing `F8` (`Ctrl+K =`) in any
file, shows its diff in place of the text:

- both line numbers, removed lines in red and added lines in green,
- three lines of context around each change, and `⋯` for the unchanged
  stretches between,
- a file open in the editor is compared as it is there, unsaved edits
  included.

In a diff, `n` and `p` step through the changes. `Enter`, or a double-click,
opens the file at that line. `Esc` goes back to what you were editing.

## Staying in step

Commits, checkouts and resets made in another terminal are picked up
automatically. `git status` runs without taking git's index lock, so it never
gets in the way of a `git commit` you run at the same time.
