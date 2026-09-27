# For vim users

sonarc is modeless: typing inserts text, and every command is a key
combination or a chord after `Ctrl+K`. It keeps the parts of a vim and cscope
workflow that matter most on a big C tree, including `Ctrl+]` and `Ctrl+T`,
and needs no configuration to get them.

## Your habits, mapped

| In vim | In sonarc |
|---|---|
| `i`, `a`, `o` to insert | just type |
| `:w` | `Ctrl+S` |
| `:q`, `:wq` | `Ctrl+Q`, which asks about unsaved files |
| `u`, `Ctrl+R` | `Ctrl+Z`, `Ctrl+Y` |
| `dd` | `Ctrl+D` |
| `yy`, `p` | `Ctrl+C` with nothing selected copies the line; `Ctrl+V` pastes |
| `v`, `V` | `Shift` + arrows; triple-click selects a line |
| `Ctrl+V` block insert | `Alt+Shift+↓` to add cursors down a column, then type |
| `*` then `cgn` and `.` | `Alt+N` for each occurrence, then type once |
| `/pattern`, `n`, `N` | `Ctrl+F`, `F3`, `Shift+F3` |
| `:%s/old/new/gc` | `Ctrl+R`, confirming each |
| `:42`, `42G` | `Ctrl+G` |
| `gg`, `G` | `Ctrl+Home`, `Ctrl+End` |
| `Ctrl+]`, `gd` | `Ctrl+]` |
| `Ctrl+T`, `Ctrl+O` | `Ctrl+T` or `Alt+←` |
| `Ctrl+I` | `Alt+→` |
| `:tag name`, `:ts` | `F4`, symbol search by name |
| `:cs find s` | `F7`, find references |
| `:cs find c` | `Ctrl+K c`, callers |
| `:cs find d` | `Ctrl+K d`, functions called |
| `:cs find t`, `:grep`, `:vimgrep` | `Ctrl+K f`, project text search |
| `:cs find i` | `Ctrl+K i`, files that include this one |
| `:cs find a` | `Ctrl+K a`, assignments |
| `:copen`, `:cn` | the results panel stays open; `F3` or `↓` for the next |
| preview window, `Ctrl+W }`, `K` | `Ctrl+K v`, peek at the definition or the language server's description |
| `:e file`, `:find` | `Ctrl+P` by fuzzy name, `Ctrl+O` by path |
| `:ls`, `:b` | `Ctrl+K u` |
| `:vsplit`, `:split` | `Ctrl+K 3`, `Ctrl+K 2` |
| `Ctrl+W w` | `F9` or `Ctrl+K ;` |
| `Ctrl+W o`, `:only` | `Ctrl+K 1` |
| `:bd` | `Ctrl+W` |
| `:e!` | `Ctrl+K !` |
| NERDTree, netrw | `Ctrl+E` |
| `:set paste` | not needed: pastes arrive as one insertion |
| `:Gdiff`, gitgutter | `F8`, and the gutter marks are always on |

## What you give up

- **Modal editing.** No operators, text objects, counts, `.` repeat or macros.
  That is the point of sonarc, and it is not coming back as an option.
- **Scripting and plugins.** There is no configuration language, though any
  key can be rebound in [`keys.conf`](keys.md#changing-keys).

## What you get without configuring anything

- cscope and ctags that work the moment a `cscope.out` or `tags` file exists,
  a built-in indexer when neither does, and a language server such as gopls or
  clangd whenever one is installed, with no plugin to configure.
- Every matching definition in a picker, from all indexes at once, instead of
  the first tag match.
- Lookups that run in the background, so a slow query never freezes the
  editor.
- A session that survives a dropped ssh connection.
