# sonarc

A modeless terminal code editor for Linux servers, built for `ssh` + `tmux`.

Typing inserts text. `Ctrl+S` saves. `Ctrl+Z` undoes. There are no modes to
escape from and no `:wq`. It ships as a single static binary with no runtime
dependencies, and it treats **cscope** and **ctags** as first-class features
rather than plugins.

```sh
curl -fsSL https://sonarc-dev.github.io/sonarc/install.sh | sh
sonarc ~/src/linux
```

Website, with a half-minute demo: <https://sonarc-dev.github.io/sonarc/>

## Why it exists

Editing code on a headless server usually means vi or emacs. sonarc does the
same job with the habits people already have from a GUI editor, while being
honest about what a terminal inside tmux can actually do.

- **One static binary.** About 4 MB with no libc dependency, so the same file
  runs on Alpine, Debian and old-glibc CentOS. `scp` it and go, or update it
  in place with `sonarc -update`.
- **Built for tmux, not despite it.** tmux does not pass `Ctrl+Shift` keys on
  unless you enable `extended-keys`, and screen never does. So no command
  depends on them; a `Ctrl+K` chord reaches everything, and `sonarc -doctor`
  says which tmux settings to change.
- **Large codebases.** A kernel-sized `tags` file is over a gigabyte. sonarc
  binary-searches it on disk instead of loading it, and keeps one cscope
  process running so queries answer in milliseconds.
- **Never dead on arrival.** With neither cscope nor ctags installed, a
  built-in indexer still provides goto-definition, find-references and symbol
  search.

On a Linux 7.2 tree (5.5 GB, 64,525 C and header files), goto-definition takes
4.8 ms, searching the 1.1 GB tags file needs 8 MB of memory, and the editor
uses 31 MB with the whole tree open.

## Documentation

- [Getting started](docs/getting-started.md): install, update, open a project, and check your terminal.
- [Keys](docs/keys.md): every key, grouped by what you are doing.
- [Code navigation](docs/navigation.md): definitions, references, and the indexes behind them.
- [Git](docs/git.md): change marks, the Changes list, and diffs.
- [tmux and terminals](docs/tmux.md): the settings that matter and what `-doctor` checks.
- [Troubleshooting](docs/troubleshooting.md): clicks, keys, clipboard, and indexes that misbehave.
- [For vim users](docs/vim.md): your habits, mapped to sonarc.
- [Design notes](docs/design.md): how it stays fast on a kernel tree and never damages a file.

The same pages, with search, are at <https://sonarc-dev.github.io/sonarc/docs/>.

## Contributing

Bug reports, fixes and ideas are welcome. [CONTRIBUTING.md](CONTRIBUTING.md)
explains how to build and test, and where everything lives.

## License

MIT. See [LICENSE](LICENSE).
