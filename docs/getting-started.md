# Getting started

## Install

On Linux or macOS, amd64 or arm64:

```sh
curl -fsSL https://sonarc-dev.github.io/sonarc/install.sh | sh
```

The script downloads the latest release for your machine, checks it against
the release's `checksums.txt`, and installs it to `~/.local/bin` without root.
Two environment variables change what it does:

| Variable | Effect |
|---|---|
| `SONARC_INSTALL_DIR` | where to install, instead of `~/.local/bin` |
| `SONARC_VERSION` | a release tag such as `v0.1.0`, instead of the latest |

The binaries are also on the
[releases page](https://github.com/sonarc-dev/sonarc/releases). Each is one
static file with no dependencies, so copying it to a server with `scp` works
too.

With Go 1.24 or later:

```sh
go install github.com/sonarc-dev/sonarc/cmd/sonarc@latest
```

From a checkout:

```sh
make build                     # ./sonarc for this machine
make linux                     # static binaries for linux/amd64 and arm64
make release                   # linux and macOS, amd64 and arm64, with checksums
make deploy HOST=myserver      # detects the remote arch and copies the right one
```

## Update

```sh
sonarc -update
```

This fetches the latest release the same way the install script does, with
`curl` or `wget`, and replaces the binary in place. A running sonarc keeps running, and nothing
changes if the download or the checksum fails.

Once a day the editor asks GitHub whether a newer release exists and says so
on the message line. Set `SONARC_NO_UPDATE_CHECK=1` to turn that off; it also
stops the request. Builds from a checkout never check.

## Verify a release

Every binary has a GitHub build-provenance attestation, and each release's
`checksums.txt` is signed with a keyless cosign signature from the release
workflow. With the GitHub CLI:

```sh
gh attestation verify sonarc-linux-amd64 -R sonarc-dev/sonarc
```

Or with cosign, then check the binary against the verified checksums:

```sh
cosign verify-blob checksums.txt --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/sonarc-dev/sonarc/\.github/workflows/release\.yml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum --check --ignore-missing checksums.txt
```

## First run

```sh
sonarc -doctor                # reports terminal capabilities and tmux fixes
sonarc -commands              # every command and the keys bound to it
sonarc file.c                 # edit a file
sonarc file.c:42              # at line 42, as compilers and grep print it
sonarc file.c:42:7            # line 42, column 7
sonarc .                      # open the current directory as a project
sonarc ~/src/proj main.c      # a directory and a file together
```

Run `sonarc -doctor` once inside tmux: it reads the live tmux settings and
prints exactly what to add. See [tmux and terminals](tmux.md).

Inside the editor, `F1` lists the keys your terminal can actually send, and
`F6` opens a command palette with every command, searchable by description.
The [keys page](keys.md) has the full list.

## Projects and sessions

`sonarc <directory>` opens a folder as a project, with focus in the file tree.
The tree shows the directory you named, while indexes and project search cover
the whole project found by walking up from it. So `sonarc ~/linux/fs` browses
`fs/` but searches the entire kernel. See
[how the project root is found](navigation.md#the-project-root).

Opening a project again with no file named reopens it as you left it: the same
files, cursors and scroll positions, and the same folders open in the tree.
Any file you open again starts where you last left it, in any project. The
session is saved every few seconds as well as on exit, so a dropped ssh
connection loses nothing but unsaved text.

Files changed by another program, such as `git checkout`, a build, or an AI
agent, are noticed within two seconds. One you have not edited is reloaded in
place. One you have edited is left alone with a warning, and saving it asks
before overwriting the other version. New and deleted files appear in the tree
the same way.

## Where sonarc keeps things

| Path | What |
|---|---|
| `~/.config/sonarc/state.json` | the theme, the sidebar width, whether Changes is folded, and the last update check |
| `~/.config/sonarc/sessions/` | one file per project: open files, cursors and open folders |
| `~/.config/sonarc/positions.json` | where you left each file, for the last 1000 files |
| `~/.config/sonarc/keys.conf` | your key bindings, if you have changed any; see [changing keys](keys.md#changing-keys) |
| `~/.config/sonarc/lsp.conf` | language server choices, if you have changed any; see [language servers](navigation.md#language-servers) |

On macOS the directory is `~/Library/Application Support/sonarc`. Deleting any
of these only resets what it remembers.

## Themes and highlighting

Choose a theme from the command palette (`F6`, then "choose a color theme"):
dark, light, gruvbox, solarized or high-contrast. The choice is remembered.
On terminals with fewer than 256 colors a minimal theme is used.

Syntax highlighting covers C and C++, Go, Python, Rust, JavaScript and
TypeScript, Java, shell, Make, JSON, YAML and TOML. Files over 50 MB are left
uncolored on purpose.
